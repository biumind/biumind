// Deploy service entry point.
//
// Routes:
//
//	/healthz, /readyz       — health probes
//	/v1/deploys/...         — JWT-gated deploy API (driver-backed)
//	/static/<id>/...        — public file serve when DEPLOY_DRIVER=static
//
// The /static path lives on the same listener so a single hostname covers
// both the API and the served files. Production swaps that for a real
// static host (CloudFront / Cloudflare / nginx in front of an S3 driver).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/services/deploy/internal/api"
	"github.com/biumind/biumind/services/deploy/internal/driver"
)

const (
	serviceName    = "deploy"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string `env:"LISTEN_ADDR" default:":7006"`
	Environment  string `env:"BIUMIND_ENV" default:"dev"`
	LogLevel     string `env:"BIUMIND_LOG_LEVEL" default:"info"`
	OtlpEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" default:""`
	JWTSecret    string `env:"JWT_SECRET" required:"true"`
	JWTIssuer    string `env:"JWT_ISSUER" default:"https://identity.biumind.local"`
	JWTAudience  string `env:"JWT_AUDIENCE" default:"biumind-api"`
	// IdentityJWKSURL — Identity's public JWKS endpoint. When set, this
	// service verifies RS256 tokens against it instead of the shared HS256
	// secret. JWT_SECRET is still required as the dev/test fallback.
	IdentityJWKSURL string `env:"IDENTITY_JWKS_URL" default:""`

	// static | container | s3 | stub
	Driver     string `env:"DEPLOY_DRIVER" default:"static"`
	PublicURL  string `env:"DEPLOY_PUBLIC_URL" default:"http://localhost:7006"`
	StaticRoot string `env:"DEPLOY_STATIC_ROOT" default:"/tmp/biu-deploy-static"`
	DockerBin  string `env:"DEPLOY_DOCKER_BIN" default:"docker"`
	StagingDir string `env:"DEPLOY_STAGING_DIR" default:"/tmp/biu-deploy-stage"`

	// S3 driver — works against AWS S3, MinIO, Cloudflare R2, etc.
	S3Endpoint  string `env:"DEPLOY_S3_ENDPOINT" default:""`
	S3Region    string `env:"DEPLOY_S3_REGION" default:"us-east-1"`
	S3Bucket    string `env:"DEPLOY_S3_BUCKET" default:""`
	S3AccessKey string `env:"DEPLOY_S3_ACCESS_KEY" default:""`
	S3SecretKey string `env:"DEPLOY_S3_SECRET_KEY" default:""`
	S3PublicURL string `env:"DEPLOY_S3_PUBLIC_URL" default:""`
	S3Vhost     bool   `env:"DEPLOY_S3_VHOST" default:"false"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "deploy: %v\n", err)
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
	logger := slog.New(botel.SlogJSONHandler(level)).With(
		"service", serviceName, "version", serviceVersion,
	)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownOtel(c)
	}()

	var d driver.Driver
	var staticRoot string
	switch cfg.Driver {
	case "static":
		if err := os.MkdirAll(cfg.StaticRoot, 0o755); err != nil {
			return fmt.Errorf("mkdir static root: %w", err)
		}
		staticBaseURL := strings.TrimRight(cfg.PublicURL, "/") + "/static"
		st := driver.NewStatic(cfg.StaticRoot, staticBaseURL)
		d = st
		staticRoot = st.Root()
		logger.Info("using static driver", "root", cfg.StaticRoot, "base", staticBaseURL)
	case "container":
		c := driver.NewContainer(cfg.DockerBin, cfg.PublicURL, cfg.StagingDir)
		d = c
		logger.Info("using container driver", "stage", cfg.StagingDir, "base", cfg.PublicURL)
	case "s3":
		if cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" {
			return fmt.Errorf("s3 driver requires DEPLOY_S3_ENDPOINT + BUCKET + ACCESS_KEY")
		}
		s := driver.NewS3(
			cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket,
			cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3PublicURL, cfg.S3Vhost,
		)
		d = s
		logger.Info("using s3 driver",
			"endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket,
			"region", cfg.S3Region, "vhost", cfg.S3Vhost)
	case "stub":
		logger.Warn("⚠ using STUB driver — no real deployment work; for tests/dev only")
		d = driver.NewStub()
	default:
		return fmt.Errorf("unknown DEPLOY_DRIVER %q (use static | container | stub)", cfg.Driver)
	}

	verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	apiSrv := api.NewServer(d, verifier, logger)

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.SetReady(true)

	mux := http.NewServeMux()
	hz.Mount(mux)
	apiSrv.Mount(mux)

	if staticRoot != "" {
		// Public file server for static deployments. Path layout:
		//   GET /static/<id>/index.html  → <staticRoot>/<id>/index.html
		fs := http.FileServer(http.Dir(staticRoot))
		mux.Handle("GET /static/", http.StripPrefix("/static/", fs))
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// Multipart uploads + log streams — no global write deadline.
	}
	go func() {
		<-ctx.Done()
		logger.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	logger.Info("deploy listening", "addr", cfg.ListenAddr, "driver", cfg.Driver)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
