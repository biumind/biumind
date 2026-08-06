// aigc service entry point.
//
// 提供文生图 / 文生视频 / 数字人 / 爆款解析的 API 接入层。
// 任务实际执行由 workers/aigc (Python) 通过 NATS 消费完成。
//
// 设计：docs/BiuMind-AIGC-Migration-Plan.md
//
//	docs/BiuMind-AIGC-Storage-Design.md
//	docs/BiuMind-AIGC-Client-Progress-Design.md
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	"github.com/biumind/biumind/packages/go-sdk/biu/dbmigrate"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/services/aigc/internal/api"
	"github.com/biumind/biumind/services/aigc/internal/authz"
	"github.com/biumind/biumind/services/aigc/internal/billing"
	"github.com/biumind/biumind/services/aigc/internal/blob"
	"github.com/biumind/biumind/services/aigc/internal/config"
	"github.com/biumind/biumind/services/aigc/internal/orchestrator"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 通过 ldflags 注入：-X main.serviceVersion=$(git describe --always)
var (
	serviceName    = "aigc"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aigc: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg config.Config
	if err := bconfig.Load(&cfg); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// 日志
	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	slog.SetDefault(
		slog.New(botel.SlogJSONHandler(logLevel)).
			With("service", serviceName, "version", serviceVersion),
	)

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM,
	)
	defer cancel()

	// OTel
	_, shutdownOtel, err := botel.Init(ctx, botel.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		Environment:    cfg.Environment,
		OtlpEndpoint:   cfg.OtlpEndpoint,
	})
	if err != nil {
		return fmt.Errorf("otel: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownOtel(shutdownCtx)
	}()

	// 健康 / 就绪
	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	metrics.SetService(serviceName)

	// PG（可选，便于 e2e / dev 起裸服务）
	var pool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		pool, err = pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("pgxpool: %w", err)
		}
		defer pool.Close()

		// 跑 migrations（aigc.tasks 是 00001 创建的，故 checkTable 用它；
		// baselineMax=0 因为 aigc schema 全新，无历史 goose 状态）。
		if cfg.MigrationsDir != "" {
			if err := dbmigrate.Run(ctx, pool, "aigc",
				cfg.MigrationsDir, "aigc.tasks", 0); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
		}

		hz.AddProbe("postgres", func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			return pool.Ping(ctx)
		})
	} else {
		slog.Warn("AIGC_DATABASE_URL empty — running without DB (skeleton mode)")
	}

	// HTTP
	mux := http.NewServeMux()
	hz.Mount(mux)
	mux.Handle("/metrics", metrics.Handler())

	// ── 业务 endpoint 装配 (P2) ───────────────────
	if pool == nil {
		slog.Warn("aigc business endpoints disabled (DB not configured)")
	} else {
		// JWT verifier — 优先 JWKS (RS256 from identity), 缺省 fallback HS256.
		verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)

		// authz — AUTHZ_URL 空时退化到 AlwaysAllow (dev), 同时 log warning.
		var dec authz.Decider
		if cfg.AuthzURL != "" {
			dec = authz.NewHTTP(cfg.AuthzURL)
		} else {
			slog.Warn("AUTHZ_URL empty — using AlwaysAllow stub (DO NOT USE IN PROD)")
			dec = authz.AlwaysAllow{}
		}

		// billing — IDENTITY_URL 空时积分相关 endpoint 返 503.
		var bill *billing.Client
		if cfg.IdentityURL != "" && cfg.InternalToken != "" {
			bill = billing.NewClient(cfg.IdentityURL, cfg.InternalToken)
		} else {
			slog.Warn("AIGC_IDENTITY_URL or IDENTITY_INTERNAL_TOKEN empty — billing disabled (POST /v1/generations 返 503)")
		}

		// NATS bus — NATS_URL 空时走 NoopBus (publish 静默丢弃).
		var b bus.Bus
		if cfg.NATSURL != "" {
			nb, err := bus.Connect(cfg.NATSURL, serviceName, cfg.Environment)
			if err != nil {
				slog.Warn("NATS connect failed — falling back to NoopBus", "err", err)
				b = bus.NewNoopBus()
			} else {
				b = nb
				defer func() { _ = b.Close() }()
			}
		} else {
			slog.Warn("NATS_URL empty — using NoopBus (worker won't receive task.submit/cancel)")
			b = bus.NewNoopBus()
		}

		st := store.New(pool)

		// 段3.6: aigc 不再 resolve 凭证。生成经 model-relay 单一 egress,
		// 凭证由 model-relay 自己从 vault 解密(worker 调 /v1/internal/generations)。

		// MinIO blob client (CAS 产物下载 /v1/aigc/files-by-sha). nil 时 endpoint
		// 返 503 (dev 无 MinIO 优雅降级).
		blobClient, err := blob.New(blob.Config{
			Endpoint:          cfg.S3Endpoint,
			AccessKey:         cfg.S3AccessKey,
			SecretKey:         cfg.S3SecretKey,
			UseSSL:            cfg.S3UseSSL,
			Region:            cfg.S3Region,
			BucketOutputs:     cfg.BucketOutputs,
			BucketDerivatives: cfg.BucketDerivatives,
			BucketUploads:     cfg.BucketUploads,
			BucketPublic:      cfg.BucketPublic,
			BucketTemp:        cfg.BucketTemp,
		})
		apiSrv := &api.Server{
			Store:    st,
			Authz:    dec,
			Billing:  bill,
			Verifier: verifier,
		}
		// typed-nil 陷阱: 不能直接把可能为 nil 的 *blob.Client 赋给 interface
		// 字段 (会变成非 nil interface). 仅在真有 client 时赋值.
		if err != nil {
			slog.Warn("aigc blob client init failed — /v1/aigc/files-by-sha disabled", "err", err)
		} else if blobClient == nil {
			slog.Warn("AIGC_S3_ENDPOINT/keys empty — /v1/aigc/files-by-sha disabled (产物图无法在客户端显示)")
		} else {
			apiSrv.Blob = blobClient
			slog.Info("aigc blob client wired", "endpoint", cfg.S3Endpoint)
		}
		apiSrv.SetSubmitDeps(api.SubmitDeps{
			Bus:    b,
			Logger: slog.Default(),
		})
		apiSrv.Mount(mux)

		// P4.S3.6: aigc 临时 adminapi 已删. 运营走 model-relay 的
		// /v1/admin/models?mode=image_generation 等单源入口.

		// orchestrator: 订阅 NATS aigc.task.update (worker 发出),
		// 写库 + fan-out 到 services/realtime 让 SSE 推给客户端.
		if b.Connected() {
			orch := &orchestrator.Orchestrator{
				Store:  st,
				Bus:    b,
				Env:    cfg.Environment,
				Logger: slog.Default(),
			}
			if err := orch.Start(ctx); err != nil {
				slog.Warn("orchestrator start failed", "err", err)
			}
		} else {
			slog.Warn("orchestrator disabled (NoopBus — worker events not delivered)")
		}
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
	}

	// 启动
	hz.SetReady(true)
	slog.Info("aigc starting",
		"listen", cfg.ListenAddr,
		"env", cfg.Environment,
		"db", cfg.DatabaseURL != "",
		"nats", cfg.NATSURL != "",
		"s3", cfg.S3Endpoint != "",
	)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
	}

	hz.SetReady(false)
	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
