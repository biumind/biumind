// Authz service entry point.
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

	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	bmetrics "github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/services/authz/internal/api"
	"github.com/biumind/biumind/services/authz/internal/cache"
	"github.com/biumind/biumind/services/authz/internal/engine"
	"github.com/biumind/biumind/services/authz/internal/policies"
)

const (
	serviceName    = "authz"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string        `env:"LISTEN_ADDR" default:":7009"`
	Environment  string        `env:"BIUMIND_ENV" default:"dev"`
	LogLevel     string        `env:"BIUMIND_LOG_LEVEL" default:"info"`
	OtlpEndpoint string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT" default:""`
	PoliciesPath string        `env:"POLICIES_PATH" default:"/etc/biumind/authz/policies"`
	CacheSize    int           `env:"AUTHZ_CACHE_SIZE" default:"10000"`
	CacheTTL     time.Duration `env:"DECISION_CACHE_TTL_SEC" default:"30s"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "authz: %v\n", err)
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
	slog.SetDefault(slog.New(botel.SlogJSONHandler(level)).With("service", serviceName, "version", serviceVersion))

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

	// Engine + initial policy load
	eng := engine.New()
	raw, files, err := policies.LoadDir(cfg.PoliciesPath)
	if err != nil {
		return fmt.Errorf("policies: %w", err)
	}
	if err := eng.LoadPolicies(raw); err != nil {
		return fmt.Errorf("policies parse: %w", err)
	}
	slog.Info("policies loaded", "files", files, "count", eng.PolicyCount())

	dc, err := cache.New(cfg.CacheSize, cfg.CacheTTL)
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	apiSrv := &api.Server{
		Engine:    eng,
		Cache:     dc,
		PolicyDir: cfg.PoliciesPath,
		Logger:    slog.Default(),
	}

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
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
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	slog.Info("authz listening", "addr", cfg.ListenAddr, "policy_dir", cfg.PoliciesPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
