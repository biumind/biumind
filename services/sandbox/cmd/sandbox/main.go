// Sandbox service entry point.
//
// Boots one of the registered drivers based on SANDBOX_DRIVER and serves
// the HTTP surface defined in internal/api. Stateless — each instance is
// the source of truth for the sandboxes it created and forgets them on
// restart (durable bookkeeping is the Runtime/Brain job).
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
	"github.com/biumind/biumind/packages/go-sdk/biu/quota"
	"github.com/biumind/biumind/services/sandbox/internal/api"
	"github.com/biumind/biumind/services/sandbox/internal/driver"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	serviceName    = "sandbox"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string `env:"LISTEN_ADDR" default:":7005"`
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

	// docker | k8s | stub. Stub MUST NOT be used outside of dev/test —
	// it executes commands directly on the host with no isolation.
	Driver string `env:"SANDBOX_DRIVER" default:"docker"`

	// Per-owner quota gates. 0 disables the corresponding gate.
	MaxConcurrentPerOwner int    `env:"SANDBOX_MAX_CONCURRENT_PER_OWNER" default:"5"`
	DailyCreatesPerOwner  int64  `env:"SANDBOX_DAILY_CREATES_PER_OWNER"  default:"100"`
	DockerBin             string `env:"SANDBOX_DOCKER_BIN" default:"docker"`

	// R5 沙箱加固策略（docker + k8s 共用）。
	// ImageAllowlist：逗号分隔的额外允许镜像（默认镜像 alpine:3.20 总允许）。
	// 空 → 只允许默认镜像。
	ImageAllowlist string `env:"SANDBOX_IMAGE_ALLOWLIST" default:""`
	// EgressEnforced=false → 请求 selective egress 时 fail-closed 到 network=none
	// （不连未受控 bridge）。host iptables 就位后才设 true。
	EgressEnforced bool `env:"SANDBOX_EGRESS_ENFORCED" default:"false"`
	// WorkspaceTmpfsMB：/workspace 可写 tmpfs 大小（rootfs 只读）。
	WorkspaceTmpfsMB int `env:"SANDBOX_WORKSPACE_TMPFS_MB" default:"512"`
	// RunAsUser："uid:gid"，容器非 root 运行。空 = image 默认（root）逃生门。
	RunAsUser string `env:"SANDBOX_RUN_AS_USER" default:"65532:65532"`

	// k8s driver. KubeconfigPath empty + KUBE_NAMESPACE set → in-cluster
	// config (Sandbox itself runs as a Pod). With kubeconfig path it's
	// an out-of-cluster client (dev / k3s in compose).
	KubeconfigPath   string `env:"KUBECONFIG"            default:""`
	KubeNamespace    string `env:"KUBE_NAMESPACE"        default:"biumind-sandbox"`
	KubeRuntimeClass string `env:"SANDBOX_K8S_RUNTIMECLASS" default:""`
	KubeImage        string `env:"SANDBOX_K8S_IMAGE"     default:"alpine:3.20"`

	// Optional Postgres-backed quota. When set, multiple Sandbox
	// replicas share the daily-create budget per owner.
	QuotaDatabaseURL string `env:"QUOTA_DATABASE_URL" default:""`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: %v\n", err)
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

	// R5 加固策略（docker + k8s 共用）。默认镜像取各 driver 约定（docker
	// alpine:3.20 / k8s cfg.KubeImage）；ImageAllowlist 逗号拆分。
	var allowlist []string
	for _, s := range strings.Split(cfg.ImageAllowlist, ",") {
		if t := strings.TrimSpace(s); t != "" {
			allowlist = append(allowlist, t)
		}
	}
	dockerPolicy := driver.Policy{
		DefaultImage:     "alpine:3.20",
		ImageAllowlist:   allowlist,
		EgressEnforced:   cfg.EgressEnforced,
		WorkspaceTmpfsMB: cfg.WorkspaceTmpfsMB,
		RunAsUser:        cfg.RunAsUser,
	}
	k8sPolicy := dockerPolicy
	k8sPolicy.DefaultImage = cfg.KubeImage

	var d driver.Driver
	switch cfg.Driver {
	case "docker":
		logger.Info("using docker driver", "bin", cfg.DockerBin,
			"image_allowlist", allowlist, "egress_enforced", cfg.EgressEnforced,
			"run_as_user", cfg.RunAsUser)
		d = driver.NewDocker(cfg.DockerBin, logger, dockerPolicy)
	case "k8s":
		k, err := driver.NewK8s(cfg.KubeconfigPath, cfg.KubeNamespace,
			cfg.KubeRuntimeClass, cfg.KubeImage, logger, k8sPolicy)
		if err != nil {
			return fmt.Errorf("k8s driver: %w", err)
		}
		logger.Info("using k8s driver",
			"namespace", cfg.KubeNamespace,
			"runtime_class", cfg.KubeRuntimeClass,
			"kubeconfig", cfg.KubeconfigPath)
		d = k
	case "stub":
		// LOUD warning — stub is host-execution. Production deployment
		// must never accidentally land here.
		logger.Warn("⚠ using STUB driver — no isolation; for tests/dev only")
		d = driver.NewStub()
	default:
		return fmt.Errorf("unknown driver %q (use docker | k8s | stub)", cfg.Driver)
	}

	verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)

	// Per-owner quota gates. The Limiter handles the daily-create cap;
	// concurrent cap is enforced inline against the driver's owner-scoped
	// List.
	specs := map[string]quota.Spec{}
	if cfg.DailyCreatesPerOwner > 0 {
		specs["sandbox.daily"] = quota.Spec{
			Window: 24 * time.Hour,
			Limit:  cfg.DailyCreatesPerOwner,
			Unit:   "creates",
		}
	}
	var limiter quota.Limiter
	if cfg.QuotaDatabaseURL != "" {
		pool, err := pgxpool.New(ctx, cfg.QuotaDatabaseURL)
		if err != nil {
			return fmt.Errorf("sandbox: open quota pool: %w", err)
		}
		defer pool.Close()
		limiter = quota.NewPGLimiter(pool, specs)
		logger.Info("sandbox quota gates (postgres)",
			"max_concurrent", cfg.MaxConcurrentPerOwner,
			"daily_creates", cfg.DailyCreatesPerOwner)
	} else {
		limiter = quota.NewInMemoryLimiter(specs)
		logger.Info("sandbox quota gates (in-memory)",
			"max_concurrent", cfg.MaxConcurrentPerOwner,
			"daily_creates", cfg.DailyCreatesPerOwner)
	}

	apiSrv := api.NewServer(d, verifier, logger).
		WithQuota(limiter, cfg.MaxConcurrentPerOwner)

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.SetReady(true)

	mux := http.NewServeMux()
	hz.Mount(mux)
	apiSrv.Mount(mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// Exec endpoints stream — no global write deadline.
	}
	go func() {
		<-ctx.Done()
		logger.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	logger.Info("sandbox listening", "addr", cfg.ListenAddr, "driver", cfg.Driver)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
