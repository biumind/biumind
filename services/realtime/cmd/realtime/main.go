// Realtime service entry point.
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
	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	bmetrics "github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/services/realtime/internal/api"
	"github.com/biumind/biumind/services/realtime/internal/authz"
	"github.com/biumind/biumind/services/realtime/internal/hub"
	"github.com/biumind/biumind/services/realtime/internal/ledger"
	"github.com/biumind/biumind/services/realtime/internal/natsbus"
)

const (
	serviceName    = "realtime"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string `env:"LISTEN_ADDR" default:":7008"`
	Environment  string `env:"BIUMIND_ENV" default:"dev"`
	LogLevel     string `env:"BIUMIND_LOG_LEVEL" default:"info"`
	OtlpEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" default:""`
	JWTSecret    string `env:"JWT_SECRET" required:"true"`
	JWTIssuer    string `env:"JWT_ISSUER" default:"https://identity.biumind.local"`
	JWTAudience  string `env:"JWT_AUDIENCE" default:"biumind-api"`
	// IdentityJWKSURL — Identity's public JWKS endpoint. When set, this
	// service verifies RS256 tokens against it instead of the shared HS256
	// secret. JWT_SECRET is still required as the dev/test fallback.
	IdentityJWKSURL string        `env:"IDENTITY_JWKS_URL" default:""`
	NATSURL         string        `env:"NATS_URL" default:""`
	AuthzURL        string        `env:"AUTHZ_URL" default:""`
	HeartbeatPeriod time.Duration `env:"HEARTBEAT_SEC" default:"15s"`
	LedgerRetention time.Duration `env:"LEDGER_RETENTION" default:"1h"`
	MaxConnBuffer   int           `env:"MAX_CONN_BUFFER" default:"1024"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "realtime: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg Config
	if err := bconfig.Load(&cfg); err != nil {
		return err
	}
	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	logger := slog.New(botel.SlogJSONHandler(level)).With("service", serviceName, "version", serviceVersion)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	_, shutdownOtel, err := botel.Init(ctx, botel.Config{
		ServiceName: serviceName, ServiceVersion: serviceVersion,
		Environment: cfg.Environment, OtlpEndpoint: cfg.OtlpEndpoint,
	})
	if err != nil {
		return fmt.Errorf("otel: %w", err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownOtel(c)
	}()

	h := hub.NewHub(cfg.MaxConnBuffer)
	l := ledger.New(cfg.LedgerRetention, 256)

	// Periodic GC
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if removed := l.GC(); removed > 0 {
					logger.Debug("ledger gc", "removed", removed)
				}
			}
		}
	}()

	// NATS bridge (optional — service works standalone with /v1/internal/publish)
	var bus *natsbus.Bus
	if cfg.NATSURL != "" {
		bus = &natsbus.Bus{
			NATSURL: cfg.NATSURL,
			Subject: fmt.Sprintf("biumind.%s.*.*.realtime", cfg.Environment),
			Hub:     h, Ledger: l, Logger: logger,
		}
		if err := bus.Connect(ctx); err != nil {
			logger.Warn("nats unavailable; running degraded", "err", err)
		}
	}

	var authzClient api.AuthzClient = authz.AlwaysAllow{}
	if cfg.AuthzURL != "" {
		authzClient = authz.New(cfg.AuthzURL)
	}

	apiSrv := &api.Server{
		Hub:             h,
		Ledger:          l,
		Authz:           authzClient,
		Verifier:        bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience),
		HeartbeatPeriod: cfg.HeartbeatPeriod,
		Logger:          logger,
	}

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	if bus != nil {
		hz.AddProbe("nats", func(ctx context.Context) error {
			if !bus.IsConnected() {
				return errors.New("nats disconnected")
			}
			return nil
		})
	}
	hz.SetReady(true)

	bmetrics.SetService(serviceName)
	bmetrics.SetServiceInfo(serviceVersion)

	mux := http.NewServeMux()
	hz.Mount(mux)
	apiSrv.Mount(mux)
	mux.Handle("/metrics", bmetrics.Handler())

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           bmetrics.HTTPMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		// SSE: WriteTimeout = 0; idle handled via heartbeat
	}

	go func() {
		<-ctx.Done()
		logger.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	logger.Info("realtime listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
