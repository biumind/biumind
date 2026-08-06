// Runtime service entry point.
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

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/email"
	rssApp "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
	tasksApp "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/tasks"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/webclip"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	bdb "github.com/biumind/biumind/packages/go-sdk/biu/db"
	"github.com/biumind/biumind/packages/go-sdk/biu/dbmigrate"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	bmetrics "github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/services/runtime/internal/agent"
	rtagentplane "github.com/biumind/biumind/services/runtime/internal/agentplane"
	"github.com/biumind/biumind/services/runtime/internal/api"
	"github.com/biumind/biumind/services/runtime/internal/apptools"
	"github.com/biumind/biumind/services/runtime/internal/authz"
	"github.com/biumind/biumind/services/runtime/internal/memclient"
	rsandbox "github.com/biumind/biumind/services/runtime/internal/sandbox"
	"github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/biumind/biumind/services/runtime/internal/skills/installer"
	"github.com/biumind/biumind/services/runtime/internal/store"
	"github.com/google/uuid"
)

const (
	serviceName    = "runtime"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string `env:"LISTEN_ADDR" default:":7002"`
	Environment  string `env:"BIUMIND_ENV" default:"dev"`
	LogLevel     string `env:"BIUMIND_LOG_LEVEL" default:"info"`
	OtlpEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" default:""`
	DatabaseURL  string `env:"DATABASE_URL" required:"true"`
	// MigrationsDir 启动自动跑 goose up. 容器里默认 /etc/biumind/migrations/runtime.
	MigrationsDir string `env:"BIUMIND_MIGRATIONS_DIR" default:"/etc/biumind/migrations/runtime"`
	JWTSecret     string `env:"JWT_SECRET" required:"true"`
	JWTIssuer     string `env:"JWT_ISSUER" default:"https://identity.biumind.local"`
	JWTAudience   string `env:"JWT_AUDIENCE" default:"biumind-api"`
	// IdentityJWKSURL — Identity's public JWKS endpoint. When set, this
	// service verifies RS256 tokens against it instead of the shared HS256
	// secret. JWT_SECRET is still required as the dev/test fallback.
	IdentityJWKSURL string `env:"IDENTITY_JWKS_URL" default:""`
	// 三个 env 字段在 S11-4 后已不被 runtime 直接消费（biumindkit
	// 走 brain agent_plane 间接调 LLM；publisher / realtime fanout 已删）。
	// 字段保留是因为现有部署 env 已经带，删字段会让 bconfig 报 unknown env。
	// 后续 deploy 配置整理后可彻底移除。
	RelayURL    string `env:"MODEL_RELAY_URL" default:""`
	RelayToken  string `env:"HUB_TOKEN" default:""`
	RealtimeURL string `env:"REALTIME_INTERNAL_URL" default:""`
	// BrainURL — when set, Runtime registers the memory.recall /
	// memory.store tools so the Agent can read/write the user's
	// long-term memories via Brain.Memory. The bearer token from
	// the inbound /v1/agents/run request is forwarded to Brain so
	// memory access is correctly attributed.
	BrainURL string `env:"BRAIN_URL" default:""`

	// AuthzURL — when set, Runtime checks Authz before invoking
	// high-risk Skills tools (skill.exec_script / fetch_network /
	// read_wiki / recall_memory) and before approve / reject /
	// share-org state transitions. Empty falls back to AlwaysAllow
	// — fine for CLI-only / dev mode but logged on startup.
	AuthzURL string `env:"AUTHZ_URL" default:""`

	// SandboxURL — when set, skill.exec_script can drive sandbox
	// execution. Empty falls back to the soft-error path
	// (PS3.6-cont). Bundle mounting (/skill/<id>/) lands separately
	// once sandbox + Files internal endpoints are wired.
	SandboxURL string `env:"SANDBOX_URL" default:""`

	// SkillsStdlibDir — directory of bundled SKILL.md packages
	// upserted into runtime.skills as source='bundled' on every
	// startup. Default points at the path the deploy compose mounts
	// packages/skills-stdlib into; empty disables the loader.
	SkillsStdlibDir string `env:"SKILLS_STDLIB_DIR" default:"/etc/biumind/skills-stdlib"`

	// NatsURL — connect to NATS to subscribe to inbound channel messages.
	// Empty disables the channels subscriber; the API path still works.
	NatsURL string `env:"NATS_URL" default:""`

	// BusUseJetStream —— 当前 runtime 没自己的 JetStream 用法（channelsbus
	// 已删；agent_plane 通过 brain HTTP 间接走）。保留 env 字段防部署
	// 配置已带，删字段会让 bconfig 报 unknown env。后续 cleanup 可移除。
	BusUseJetStream bool `env:"BUS_USE_JETSTREAM" default:"false"`

	// AgentPlaneAdminUserID — runtime 注册 brain Agent Plane environment
	// 时用的 admin / 系统账号 uuid。runtime 自签 JWT 把这个 uuid 当 user_id
	// claim；brain 端 mustUserID 拿它去 DB 落 row。空时跳过 agentplane 注册
	// （v1 cutover 期间允许 runtime 不参与 task mode pool）。
	AgentPlaneAdminUserID string `env:"AGENT_PLANE_ADMIN_USER_ID" default:""`
	// AgentPlanePoolTag — runtime 池子标签，让 task mode brain.PickRuntimeEnvironment
	// 按 pool_tag 过滤。空时进默认池。
	AgentPlanePoolTag string `env:"AGENT_PLANE_POOL_TAG" default:""`
	// AgentPlaneCapabilities —— 自陈能力清单，写到 environment row 让运维 / pool
	// 选择参考。当前只是记录用，brain pool 选择 v1 不读它。
	AgentPlaneCapabilities []string `env:"AGENT_PLANE_CAPABILITIES" default:"sandbox,skills,apps"`

	// AgentPlaneAnthropicAPIKey / Endpoint —— task mode runtime worker 调
	// LLM 用。biumindkit 直连 Anthropic，跟 chat 模式（brain ChatRunner）
	// 同款。空时 worker poll loop 不启（runtime 仍注册 environment 但不
	// 接 work，方便灰度 / dev 部署不带 Anthropic key）。
	AgentPlaneAnthropicAPIKey   string `env:"AGENT_PLANE_ANTHROPIC_API_KEY"  default:""`
	AgentPlaneAnthropicEndpoint string `env:"AGENT_PLANE_ANTHROPIC_ENDPOINT" default:""`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %v\n", err)
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

	pool, err := bdb.New(ctx, bdb.Defaults(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	// Schema 自动 migrate. runtime.tasks 是 00001 的探针表.
	// baselineMax=0 = 老行为(把所有现有文件 mark applied). runtime 当前
	// 没有"先有表后切 dbmigrate"的历史包袱, 0 不会有副作用. 后续如果
	// 真碰到老库迁过来场景, 把这里改成首次切包时的最高版本号.
	if cfg.MigrationsDir != "" {
		if err := dbmigrate.Run(ctx, pool, serviceName, cfg.MigrationsDir, "runtime.tasks", 0); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	st := store.New(pool)
	skillReg := skills.New(pool)
	if n, err := skillReg.LoadBundled(ctx, cfg.SkillsStdlibDir); err != nil {
		logger.Warn("skills-stdlib load failed", "dir", cfg.SkillsStdlibDir, "err", err)
	} else if n > 0 {
		logger.Info("skills-stdlib loaded", "count", n, "dir", cfg.SkillsStdlibDir)
	}
	var skillsAuthz authz.Decider
	if cfg.AuthzURL != "" {
		skillsAuthz = authz.NewHTTP(cfg.AuthzURL)
		logger.Info("skills authz enabled", "url", cfg.AuthzURL)
	} else {
		skillsAuthz = authz.AlwaysAllow{}
		logger.Warn("AUTHZ_URL unset; skills tools fall back to AlwaysAllow (dev / CLI mode)")
	}
	// agent.Agent struct 现在只是 type holder（S11-4 删了 agent.Run）；
	// 实际 LLM 驱动走 BuildBiumindkitAgent + agentplane.Worker。Tools
	// registry 被 BuildBiumindkitAgentInput 复用。
	ag := &agent.Agent{
		Store: st,
		Tools: agent.DefaultRegistry(),
	}

	// biuapp.Registry — same 5 bundled apps the App Center service
	// hosts. Required for apptools to resolve manifest + Invoke each
	// installed App's actions during agent runs. Translate isn't
	// registered here because it needs an llm.Provider injected; the
	// runtime path doesn't need translate's Invoke (its actions are
	// only callable via app_center HTTP for now).
	biuReg := biuapp.NewRegistry(biuapp.Deps{Logger: biuapp.DiscardLogger{}})
	for _, app := range []biuapp.App{rssApp.New(), email.New(), tasksApp.New(), webclip.New()} {
		if err := biuReg.Register(ctx, app); err != nil {
			return fmt.Errorf("biuapp register %s: %w", app.Manifest().Name, err)
		}
	}
	logger.Info("biuapp registry loaded", "count", len(biuReg.List()))

	// Construct the apptools loader once. Per-run AppToolDeps is built
	// from this in api.handleRun by capturing (UserID, AgentID, OrgID).
	apptoolsLoader := &apptools.Loader{Pool: pool, Registry: biuReg}
	apptoolsRecorder := apptools.NewPgxRecorder(pool)
	// P2-#10 — load ed25519 publisher trust store from env. Empty is
	// permissive (legacy / dev); non-empty flips zip installs to
	// strict (signature_b64 required). Loading errors abort startup
	// so a typo in the trust dir doesn't silently downgrade to
	// permissive mode.
	trustStore, err := installer.LoadTrustStoreFromEnv()
	if err != nil {
		return fmt.Errorf("trust store: %w", err)
	}
	if trustStore.IsEmpty() {
		logger.Warn("skills install trust store empty — zip installs accept any signature " +
			"(set BIUMIND_SKILL_TRUSTED_PUBKEY_DIR or BIUMIND_SKILL_TRUSTED_PUBKEY_PEM " +
			"for strict mode)")
	} else {
		logger.Info("skills install in strict mode — every zip install must verify against trust store")
	}

	// AppTools wiring (M3.5). Per-run AppToolDeps captures the
	// (agentID, orgID) tuple from the request and produces the
	// Load/Register/Prompt fns the agent loop calls. The closure
	// keeps the loader/recorder/biuReg references alive without
	// leaking them into RunInput.
	apptoolsDeps := apptools.ToolDeps{
		Registry: biuReg,
		Recorder: apptoolsRecorder,
		// Authz for app:invoke is wired separately once the central
		// authz client integration lands per-app (M3.6 follow-up).
	}
	appDepsFor := func(agentID uuid.UUID, orgID string) *agent.AppToolDeps {
		return apptools.MakeAgentDeps(apptoolsLoader, apptoolsDeps, agentID, orgID)
	}

	apiSrv := api.NewServer(ag, st, bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience), logger).
		WithSkills(skillReg).
		WithSkillsAuthz(skillsAuthz).
		WithSkillsTrustStore(trustStore).
		WithAppTools(appDepsFor)
	if cfg.SandboxURL != "" {
		// Token forwarded from inbound /v1/agents/run requests is what
		// the sandbox client uses; the per-request wiring lands in
		// api.handleRun. Here we just attach the addressable client.
		apiSrv = apiSrv.WithSkillsSandbox(rsandbox.New(cfg.SandboxURL, ""))
		logger.Info("skills sandbox enabled", "url", cfg.SandboxURL)
	} else {
		logger.Info("SANDBOX_URL unset; skill.exec_script will soft-error")
	}
	if cfg.BrainURL != "" {
		brainURL := cfg.BrainURL
		apiSrv = apiSrv.WithMemory(func(token string) agent.MemoryClient {
			return memclient.New(brainURL, token)
		})
		logger.Info("memory tools enabled", "brain_url", cfg.BrainURL)
	} else {
		logger.Info("memory tools disabled (BRAIN_URL unset)")
	}

	// NATS bus —— S11-4 后只剩 agent_plane work poller / heartbeat 用。
	// 老 channelsbus subscriber 已删（channels 现在走 brain Agent Plane）。
	natsBus, err := bus.Connect(cfg.NatsURL, "runtime", cfg.Environment)
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer natsBus.Close()
	if cfg.NatsURL != "" {
		logger.Info("nats connected", "url", cfg.NatsURL, "env", cfg.Environment)
	}
	_ = natsBus // 保留连接以兼容 brain agent_plane worker / 未来 bus 用途

	// S11-1: 注册成 brain Agent Plane environment（worker_kind=runtime）。
	// AGENT_PLANE_ADMIN_USER_ID + BRAIN_URL 都有 + JWT_SECRET 才启动；缺
	// 任一就 skip（dev / 测试 / S11 灰度期友好）。
	if cfg.AgentPlaneAdminUserID != "" && cfg.BrainURL != "" {
		adminUID, err := uuid.Parse(cfg.AgentPlaneAdminUserID)
		if err != nil {
			return fmt.Errorf("parse AGENT_PLANE_ADMIN_USER_ID: %w", err)
		}
		// 长效 admin JWT —— TTL 1 小时，到期前 Registrar 应该自然 deregister
		// 在 graceful shutdown；运行中 token 过期会被 brain 401 拒绝心跳，
		// 走 re-register 失败 / 重试，下一轮拿新 token。这里 TTL 设 24h
		// 让单实例 24h 内不会过期（Registrar 不轮换 token）；如果跑超 24h
		// 实例需要 SIGHUP 重启。后续可加 token 自动刷新但 v1 不做。
		signer := bauth.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 24*time.Hour)
		adminTok, err := signer.Sign(&bauth.Claims{
			UserID: adminUID.String(),
		})
		if err != nil {
			return fmt.Errorf("sign agentplane admin token: %w", err)
		}
		registrar, err := rtagentplane.NewRegistrar(ctx, rtagentplane.Config{
			BrainURL:     cfg.BrainURL,
			Token:        adminTok,
			PoolTag:      cfg.AgentPlanePoolTag,
			Capabilities: cfg.AgentPlaneCapabilities,
		}, logger)
		if err != nil {
			// 注册失败不阻止 runtime 启动 —— 老 AG-UI publisher 路径仍能跑，
			// task mode 仅是少了一个 runtime 池实例。
			logger.Warn("agentplane register failed; runtime won't be in task pool",
				"err", err)
		} else {
			defer registrar.Stop(context.Background())

			// S11-2：work poller —— 长轮询 brain work queue，每条 work
			// per-task 起 biumindkit.Agent。runtime 暂时没把 cloud tools
			// （time/web/wiki/memory）注册成 biumindkit.Tool —— task mode
			// v1 跑纯 LLM；S11-3 内核替换时把 brain.tools 同款 cloud 工具
			// 适配过来（runtime 有自己的 file/Bash/skill/app 工具，那些
			// 也走 biumindkit.Tool 接口收编）。
			if cfg.AgentPlaneAnthropicAPIKey != "" {
				// S11-3: builder 调 agent.BuildBiumindkitAgent —— 把
				// runtime 现有的 file/Bash 工具 + 系统提示组装好的
				// biumindkit.Agent 返回给 worker。task mode 当前不传
				// Skills / Apps / Memory（这些跟 user 凭证强相关 ——
				// task mode WorkPayload 里只有 user_id 但没 BYOK token，
				// 等 v2 brain 把 BYOK 透传过来再启用）。
				baseTools := agent.DefaultRegistry()
				builder := func(ctx context.Context, work rtagentplane.WorkPayload) (*biumindkit.Agent, error) {
					return agent.BuildBiumindkitAgent(ctx, logger, agent.BuildBiumindkitAgentInput{
						AnthropicAPIKey:   cfg.AgentPlaneAnthropicAPIKey,
						AnthropicEndpoint: cfg.AgentPlaneAnthropicEndpoint,
						Model:             work.Model,
						System:            work.SystemPrompt,
						UserID:            work.UserID,
						PermissionMode:    agent.PermSafe, // task mode 默认中等（允许 read + 受控写）
						Tools:             baseTools,
					})
				}
				worker := rtagentplane.NewWorker(registrar, builder, rtagentplane.WorkerConfig{}, logger)
				go func() {
					if err := worker.Run(ctx); err != nil {
						logger.Error("agentplane worker exited", "err", err)
					}
				}()
				logger.Info("agentplane worker: poll loop started")
			} else {
				logger.Info("agentplane worker disabled (AGENT_PLANE_ANTHROPIC_API_KEY unset)")
			}
		}
	} else {
		logger.Info("agentplane register skipped",
			"reason", "AGENT_PLANE_ADMIN_USER_ID or BRAIN_URL unset")
	}

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.AddProbe("postgres", bdb.HealthProbe(pool))
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
		// Run-stream sync mode may take long; no WriteTimeout
	}
	go func() {
		<-ctx.Done()
		logger.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	logger.Info("runtime listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
