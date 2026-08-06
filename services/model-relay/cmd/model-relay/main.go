// model-relay service entry point.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	bcors "github.com/biumind/biumind/packages/go-sdk/biu/cors"
	"github.com/biumind/biumind/packages/go-sdk/biu/dbmigrate"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/packages/go-sdk/biu/quota"
	"github.com/biumind/biumind/services/model-relay/internal/adminapi"
	"github.com/biumind/biumind/services/model-relay/internal/api"
	"github.com/biumind/biumind/services/model-relay/internal/billing"
	billinglocal "github.com/biumind/biumind/services/model-relay/internal/billing/local"
	mrbyok "github.com/biumind/biumind/services/model-relay/internal/byok"
	"github.com/biumind/biumind/services/model-relay/internal/fxsync"
	"github.com/biumind/biumind/services/model-relay/internal/health"
	"github.com/biumind/biumind/services/model-relay/internal/internalapi"
	"github.com/biumind/biumind/services/model-relay/internal/keys"
	"github.com/biumind/biumind/services/model-relay/internal/plan"
	mrregistry "github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/files"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/anthropic"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/dashscope"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/volcengine"
	"github.com/biumind/biumind/services/model-relay/internal/router"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	serviceName    = "model-relay"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string `env:"LISTEN_ADDR" default:":7001"`
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
	MasterKeyB64    string `env:"BIUMIND_MASTER_KEY" required:"true"`

	// Phase 1.A env-based platform pool (ANTHROPIC_API_KEY / OPENAI_API_KEY
	// / *_BASE_URL) was retired in M5 切流 — all upstream selection now
	// goes through the admin-managed channels table. BYOK header support
	// (X-Biumind-LLM-Key / X-Biumind-LLM-Base-Url) lives in the request
	// path, not in startup config.

	// Brain reverse-proxy upstream. When set, /v1/code/ is forwarded
	// to Brain's REST handlers (multi-device task sync). Brain does its
	// own JWT verification — model-relay passes Authorization through unchanged.
	BrainURL string `env:"MODEL_RELAY_BRAIN_URL" default:""`

	// Per-virtual-key rate limits. 0 disables the gate.
	// Used as the *Free* tier ceiling when plan resolution is enabled
	// (PlanResolverURL set); otherwise applies to every user.
	DefaultRPM int64 `env:"MODEL_RELAY_DEFAULT_RPM" default:"600"`
	DefaultTPM int64 `env:"MODEL_RELAY_DEFAULT_TPM" default:"500000"`

	// Optional Postgres-backed quota. When set, multiple model-relay replicas
	// share one budget; unset → in-memory (per-replica) limiter.
	QuotaDatabaseURL string `env:"QUOTA_DATABASE_URL" default:""`

	// Admin / model-config database. Hosts model_relay.* tables (providers,
	// credentials, models, channels, pricing, fx_rates, ...). When unset
	// the /v1/admin/* endpoints are NOT mounted — production-style relay-only
	// deployments stay env-driven exactly like Phase 1.A. When set, model-relay
	// also runs the model_relay schema migrations on startup, brings up the
	// admin handler stack (registry / cache / vault / probe / supervisor),
	// and serves /v1/admin/*.
	AdminDatabaseURL string `env:"MODEL_RELAY_ADMIN_DATABASE_URL" default:""`
	// Where the migrations live. Container deployments mount the
	// services/model-relay/migrations directory here.
	AdminMigrationsDir string `env:"MODEL_RELAY_ADMIN_MIGRATIONS_DIR" default:"services/model-relay/migrations"`
	// Upstream model metadata source for /v1/admin/models/sync-upstream.
	// Empty → adminapi falls back to basellm.github.io/llm-metadata.
	AdminSyncUpstreamURL string `env:"MODEL_RELAY_SYNC_UPSTREAM_URL" default:""`

	// Daily fx-rate cron pulls USD↔CNY from this URL. Empty → fxsync
	// default (open.er-api.com). Set MODEL_RELAY_FX_SYNC_DISABLED=1 to
	// disable the cron entirely (admin keeps manual control via UI).
	FxSyncURL      string `env:"MODEL_RELAY_FX_SYNC_URL"      default:""`
	FxSyncDisabled bool   `env:"MODEL_RELAY_FX_SYNC_DISABLED" default:"false"`

	// Identity URL exposing the per-user plan lookup endpoint
	// (`GET {url}/v1/internal/users/{id}/plan` → {"plan":"pro"}). Empty
	// disables plan resolution and every user gets DefaultRPM/TPM.
	PlanResolverURL string `env:"MODEL_RELAY_PLAN_RESOLVER_URL" default:""`
	// Static plan override. Useful for self-hosted deployments where
	// every user is on the same tier. Overridden by PlanResolverURL.
	StaticPlan string `env:"MODEL_RELAY_STATIC_PLAN" default:""`

	// Identity service URL + internal bearer for billing (Hold/Settle/Release)
	// 与 BYOK (Get/IncrementFailure/TouchUsed). 空 IdentityBaseURL → 两者皆禁用,
	// chat 免费, BYOK 不可用. 与 W0 行为一致.
	IdentityBaseURL       string `env:"MODEL_RELAY_IDENTITY_URL" default:""`
	IdentityInternalToken string `env:"IDENTITY_INTERNAL_TOKEN" default:""`

	// InternalToken — shared secret gating THIS service's /v1/internal/*
	// endpoints (currently /v1/internal/embeddings, the platform-infra
	// embedding lane for background workers like brain's embedder). Same
	// value deployed to callers (brain reads MODEL_RELAY_INTERNAL_TOKEN).
	// Empty → those endpoints disabled. Mirrors identity's
	// IDENTITY_INTERNAL_TOKEN convention.
	InternalToken string `env:"MODEL_RELAY_INTERNAL_TOKEN" default:""`

	// P4.S3.1: NATS bus 用于 /v1/jobs publish 异步任务. 空 → JobsHandler.Bus
	// 为 NoopBus, /v1/jobs publish 静默丢弃 (dev / 早期阶段).
	NATSURL string `env:"NATS_URL" default:""`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Relay: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg Config
	if err := bconfig.Load(&cfg); err != nil {
		return err
	}

	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	slog.SetDefault(slog.New(botel.SlogJSONHandler(logLevel)).With("service", serviceName, "version", serviceVersion))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
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

	// Envelope (BYOK encryption). Held for adminapi.CredentialVault below.
	envelope, err := keys.NewEnvelopeFromBase64(cfg.MasterKeyB64)
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}

	// JWT verifier
	verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)

	// Provider registry — both Anthropic and OpenAI-compatible (covers
	// OpenAI, Azure OpenAI, LiteLLM, vLLM, OpenRouter, Together, etc.
	// via Credentials.BaseURL).
	//
	// File resolver: 把消息 parts 里的 source.type=file (BiuMind file_id 引用)
	// 解成上游 LLM 能消费的 https URL / data URL — 通过调 brain 的
	// /v1/files/{id}/presign-get 端点。BrainURL 没配时退化成无 resolver,
	// file 引用块会被丢弃 (本地开发若不发图就没影响)。
	registry := provider.NewRegistry()
	if cfg.BrainURL != "" {
		fileResolver := files.NewHTTPResolver(cfg.BrainURL)
		registry.Register(anthropic.NewWithResolver(fileResolver))
		registry.Register(openai.NewWithResolver(fileResolver))
	} else {
		registry.Register(anthropic.New())
		registry.Register(openai.New())
	}
	// dashscope.Adaptor — v0.3 M1: 阿里云 cosyvoice TTS HTTP. 后续 M3 加
	// image/video TaskAdaptor. dashscope chat 模型继续走 openai-compat
	// (registry.ProtocolOpenAICompat → openai adaptor), 这里不冲突.
	registry.Register(dashscope.New())
	// volcengine.Adaptor — 段3.6: 火山豆包 Seedream 文生图 (同步) + Seedance
	// 文生视频 (异步). Doubao chat 仍走 openai_compat.
	registry.Register(volcengine.New())

	// /v1/messages 的 resolver 由 startAdminStack 返回（M5 切流后）。
	// 不再支持 env-driven 模型/Key 路径 — 所有上游通过 admin 后台配置的
	// channel + credential 来选路。BYOK header 仍然支持作为单次 override:
	//   X-Biumind-LLM-Key       覆盖 plaintext API key
	//   X-Biumind-LLM-Base-Url  覆盖 endpoint
	// Brain 在用户配置 BYOK provider with fetch_mode='server' 时设置这两个头。
	const overrideKeyHeader = "X-Biumind-LLM-Key"
	const overrideBaseHeader = "X-Biumind-LLM-Base-Url"
	_ = overrideKeyHeader
	_ = overrideBaseHeader

	// Health server + Prometheus
	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.SetReady(true)
	metrics.SetService(serviceName)

	// Build the quota limiter — one Spec per (metric, plan) so
	// CheckAndReserve can pick the right ceiling per request based on
	// the resolved plan. We always register hub.rpm.<plan> /
	// hub.tpm.<plan> for every plan in DefaultLimits, plus the legacy
	// "hub.tpm" / "hub.tpm" buckets sized by MODEL_RELAY_DEFAULT_* for
	// deployments without plan resolution wired up.
	specs := plan.SpecsFor(plan.DefaultLimits)
	if cfg.DefaultRPM > 0 {
		specs["hub.rpm"] = quota.Spec{
			Window: time.Minute, Limit: cfg.DefaultRPM, Unit: "requests",
		}
	}
	if cfg.DefaultTPM > 0 {
		specs["hub.tpm"] = quota.Spec{
			Window: time.Minute, Limit: cfg.DefaultTPM, Unit: "tokens",
		}
	}

	// Plan resolver — order of precedence: explicit URL → static env
	// override → nil (everyone gets the legacy "hub.rpm" bucket).
	var planResolver plan.Resolver
	switch {
	case cfg.PlanResolverURL != "":
		planResolver = newHTTPPlanResolver(cfg.PlanResolverURL)
		slog.Default().Info("model-relay plan resolver: identity HTTP",
			"url", cfg.PlanResolverURL)
	case cfg.StaticPlan != "":
		planResolver = plan.StaticResolver(plan.Plan(cfg.StaticPlan))
		slog.Default().Info("model-relay plan resolver: static",
			"plan", cfg.StaticPlan)
	default:
		slog.Default().Info("model-relay plan resolver: disabled (legacy buckets)")
	}
	var limiter quota.Limiter
	if cfg.QuotaDatabaseURL != "" {
		pool, err := pgxpool.New(ctx, cfg.QuotaDatabaseURL)
		if err != nil {
			return fmt.Errorf("Relay: open quota pool: %w", err)
		}
		defer pool.Close()
		limiter = quota.NewPGLimiter(pool, specs)
		slog.Default().Info("model-relay quota gates configured (postgres)",
			"rpm", cfg.DefaultRPM, "tpm", cfg.DefaultTPM)
	} else {
		limiter = quota.NewInMemoryLimiter(specs)
		slog.Default().Info("model-relay quota gates configured (in-memory)",
			"rpm", cfg.DefaultRPM, "tpm", cfg.DefaultTPM)
	}

	mux := http.NewServeMux()
	hz.Mount(mux)
	mux.Handle("/metrics", metrics.Handler())

	// Admin stack is REQUIRED — env-driven /v1/messages routing was
	// retired in M5 切流. Without admin DB the service refuses to start;
	// keeps the failure visible up front instead of "all requests 5xx".
	if cfg.AdminDatabaseURL == "" {
		return fmt.Errorf(
			"MODEL_RELAY_ADMIN_DATABASE_URL is required " +
				"(env-driven /v1/messages routing was removed in M5; " +
				"configure providers/credentials/models/channels via admin instead)",
		)
	}
	credsResolver, onRequestComplete, adminStack, shutdownAdmin, err := startAdminStack(
		ctx, mux, &cfg, envelope, verifier, registry,
	)
	if err != nil {
		return fmt.Errorf("admin stack: %w", err)
	}
	defer shutdownAdmin()

	// W1: Billing + BYOK 客户端 (HTTP → identity internalapi).
	// IdentityBaseURL 空时两者皆 nil, chat 不计费, BYOK 不查 (与 W0 一致).
	var billingClient *billing.Client
	var byokClient *mrbyok.Client
	if cfg.IdentityBaseURL != "" {
		billingClient = billing.NewClient(cfg.IdentityBaseURL, cfg.IdentityInternalToken)
		byokClient = mrbyok.NewClient(cfg.IdentityBaseURL, cfg.IdentityInternalToken)
		slog.Default().Info("billing + byok wired", "identity_base", cfg.IdentityBaseURL)
	} else {
		slog.Default().Info("billing + byok disabled (MODEL_RELAY_IDENTITY_URL empty)")
	}

	// W4 SoT: pricing 改本地查 model_relay.pricing (经 cache + PricingRepo).
	// 之前 LookupPrice 走 HTTP 调 identity 的 billing.pricing_book,跟 admin
	// 后台改的 model_relay.pricing 数据无同步 (实测 glm-5.1 配了价但不扣积
	// 分). 现 model_relay.pricing 单 SoT.
	// fxRate 0 → lookuper 用默认 7.2; TODO 后续接 fx_rates 表动态取.
	if billingClient != nil && adminStack != nil {
		billingClient.WithPricing(billinglocal.NewLookuper(
			adminStack.Cache, adminStack.Store.Pricing, 0))
		slog.Default().Info("billing pricing source: model_relay.pricing (local SoT)",
			"fx_rate_default", 7.2)
	}

	// v0.3 M5 — 多模态共享 Billing 句柄. 给 5 modality handler (embed /
	// rerank / speech / image / video) 注入. nil 时 handler 跳过计费,
	// 跟 chat 同灰度策略.
	var modalityBilling *api.ModalityBilling
	if billingClient != nil {
		modalityBilling = &api.ModalityBilling{
			Billing: billingClient,
			BYOK:    byokClient,
			Logger:  slog.Default(),
		}
	} else {
		_ = modalityBilling // 防 unused import 时的兜底
	}

	relayHandler := &api.MessagesHandler{
		Registry:          registry,
		HTTPClient:        streamingHTTPClient(), // streaming: Timeout 0 + ResponseHeaderTimeout 防黑洞挂死
		CredsResolver:     credsResolver,
		Limiter:           limiter, // post-flight TPM accounting
		OnRequestComplete: onRequestComplete,
		Billing:           billingClient,
		BYOK:              byokClient,
		Logger:            slog.Default(),
	}
	mux.Handle("/v1/messages",
		authMiddleware(verifier,
			quotaMiddleware(limiter, planResolver, relayHandler),
		),
	)

	// W1-8: POST /v1/chat/estimate — 客户端 composer 发送前查 (估算积分区间).
	// estimate 不消费 quota / 不限流, 只挂 authMiddleware.
	estimateHandler := &api.EstimateHandler{
		CredsResolver: credsResolver,
		Billing:       relayHandler,
	}
	mux.Handle("/v1/chat/estimate", authMiddleware(verifier, estimateHandler))

	// 公开模型列表 — 普通用户 JWT 即可访问。P6: 端点改名 /v1/me/models
	// (site nginx /v1/models 已被 aigc 占用, 同名撞)。返 markup 后实际计费
	// 单价 + min_plan + max_output, 供 client/miniapp picker 直读官方模型。
	// brain P6 删 relay_client.go 后不再批量同步 official, 此端点主服务 client。
	publicModelsHandler := &api.PublicModelsHandler{
		Models: adminStack.Store.Models,
		Pricer: adminStack.Store.Pricing, // BatchLatest; nil→pricing chip 缺失
		Logger: slog.Default(),
	}
	mux.Handle("GET /v1/me/models",
		authMiddleware(verifier, publicModelsHandler))

	// GET /v1/me/usage — 账户级用量 (积分 + token + 逐调用), 数据统计·用量页.
	usageHandler := &api.UsageHandler{Usage: adminStack.Store.UsageLog}
	mux.Handle("GET /v1/me/usage", authMiddleware(verifier, usageHandler))

	// P4.S3.1: /v1/jobs 异步任务入口 (image / video / digital_human / hotparse).
	// admin stack 必须在线 (上面已经 require), 因为依赖 vault 解密 + cache
	// 路由模型. NATS 空时 publish 退化 (dev), 但 INSERT aigc.tasks 仍正常.
	jobsBus := bus.Bus(bus.NewNoopBus())
	if cfg.NATSURL != "" {
		nb, err := bus.Connect(cfg.NATSURL, "model-relay", cfg.Environment)
		if err != nil {
			slog.Default().Warn("model-relay NATS connect failed — /v1/jobs publish will be noop",
				"err", err)
		} else {
			jobsBus = nb
			defer func() { _ = nb.Close() }()
			slog.Default().Info("model-relay NATS connected", "url", cfg.NATSURL)
		}
	}
	jobsHandler := &api.JobsHandler{
		Pool:    adminStack.Pool,
		Cache:   adminStack.Cache,
		Vault:   adminStack.Vault,
		Billing: billingClient,
		Bus:     jobsBus,
		Logger:  slog.Default(),
	}
	mux.Handle("POST /v1/jobs", authMiddleware(verifier, http.HandlerFunc(jobsHandler.SubmitJob)))
	mux.Handle("GET /v1/jobs/{id}", authMiddleware(verifier, http.HandlerFunc(jobsHandler.GetJob)))
	slog.Default().Info("model-relay /v1/jobs mounted",
		"nats", cfg.NATSURL != "")

	// v0.3 M1: POST /v1/audio/speech (OpenAI 兼容 TTS).
	// ModeRouter 强制 model.mode == 'audio_speech'; 实现 SpeechAdaptor 的
	// adaptor 才能命中 (M1: dashscope.Adaptor for cosyvoice). M1 暂无 quota
	// / billing / retry — 单 attempt 直通; M2 加.
	modeRouter := router.NewModeRouter(adminStack.Resolver, registry)
	speechHandler := &api.SpeechHandler{
		ModeRouter: modeRouter,
		HTTPClient: streamingHTTPClient(), // streaming: Timeout 0 + ResponseHeaderTimeout 防黑洞挂死
		Logger:     slog.Default(),
		PlanFromClaims: func(r *http.Request) mrregistry.Plan {
			if planResolver == nil {
				return mrregistry.PlanFree
			}
			return mrregistry.Plan(plan.PlanFromRequest(r, planResolver))
		},
		Billing: modalityBilling,
	}
	mux.Handle("POST /v1/audio/speech",
		authMiddleware(verifier, quotaMiddleware(limiter, planResolver, speechHandler)))
	slog.Default().Info("model-relay /v1/audio/speech mounted (M1 cosyvoice TTS)")

	// v0.3 M2: POST /v1/embeddings (OpenAI 兼容). model.mode 必须 ==
	// 'embedding', adaptor 必须实现 EmbedAdaptor (M2: openai.Adaptor 已扩,
	// 覆盖 OpenAI / SiliconFlow / 智谱 / DeepSeek / TEI / Ollama / bge-m3 等).
	// 同步路径无 streaming, 单 attempt 无 quota/billing — M2 后续 patch.
	embeddingsHandler := &api.EmbeddingsHandler{
		ModeRouter: modeRouter,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Logger:     slog.Default(),
		PlanFromClaims: func(r *http.Request) mrregistry.Plan {
			if planResolver == nil {
				return mrregistry.PlanFree
			}
			return mrregistry.Plan(plan.PlanFromRequest(r, planResolver))
		},
		Billing: modalityBilling,
	}
	mux.Handle("POST /v1/embeddings",
		authMiddleware(verifier, quotaMiddleware(limiter, planResolver, embeddingsHandler)))
	slog.Default().Info("model-relay /v1/embeddings mounted (M2 OpenAI compat)")

	// /v1/internal/embeddings — platform-infra embedding lane for
	// background workers with no user request context (brain embedder
	// indexing wiki/memory/graph vectors). Same EmbeddingsHandler, but:
	//   - gated by MODEL_RELAY_INTERNAL_TOKEN (not a user JWT)
	//   - Billing nil (platform cost, not per-user)
	//   - no claims → resolver sees uuid.Nil user → platform pool
	//     resolves the configured embedding model (bge-m3).
	if cfg.InternalToken != "" {
		internalEmbeddings := &api.EmbeddingsHandler{
			ModeRouter: modeRouter,
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
			Logger:     slog.Default(),
			PlanFromClaims: func(r *http.Request) mrregistry.Plan {
				return mrregistry.PlanFree
			},
			Billing: nil,
		}
		mux.Handle("POST /v1/internal/embeddings",
			api.InternalTokenMiddleware(cfg.InternalToken, internalEmbeddings))
		slog.Default().Info("model-relay /v1/internal/embeddings mounted (platform infra; token-gated)")
	} else {
		slog.Default().Info("model-relay /v1/internal/embeddings disabled (MODEL_RELAY_INTERNAL_TOKEN unset)")
	}

	// v0.3 M2.5: POST /v1/rerank (Cohere shape). model.mode == 'rerank',
	// adaptor 实现 RerankAdaptor (M2.5: openai.Adaptor 走 Cohere /v1/rerank
	// 标准, 覆盖 SiliconFlow / Jina / Voyage / 新-API / TEI 等).
	rerankHandler := &api.RerankHandler{
		ModeRouter: modeRouter,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Logger:     slog.Default(),
		PlanFromClaims: func(r *http.Request) mrregistry.Plan {
			if planResolver == nil {
				return mrregistry.PlanFree
			}
			return mrregistry.Plan(plan.PlanFromRequest(r, planResolver))
		},
		Billing: modalityBilling,
	}
	mux.Handle("POST /v1/rerank",
		authMiddleware(verifier, quotaMiddleware(limiter, planResolver, rerankHandler)))
	slog.Default().Info("model-relay /v1/rerank mounted (M2.5 Cohere compat)")

	// /v1/internal/rerank — platform-infra rerank lane for server-side
	// callers with no user JWT (brain search handler re-ranking fused
	// results). Mirrors /v1/internal/embeddings: token-gated, Billing
	// nil (platform cost), no claims → platform pool resolves the
	// configured rerank model (bge-reranker-v2-m3). P1-2.
	if cfg.InternalToken != "" {
		internalRerank := &api.RerankHandler{
			ModeRouter: modeRouter,
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
			Logger:     slog.Default(),
			PlanFromClaims: func(r *http.Request) mrregistry.Plan {
				return mrregistry.PlanFree
			},
			Billing: nil,
		}
		mux.Handle("POST /v1/internal/rerank",
			api.InternalTokenMiddleware(cfg.InternalToken, internalRerank))
		slog.Default().Info("model-relay /v1/internal/rerank mounted (platform infra; token-gated)")
	}

	// v0.3 M3: POST /v1/images/generations (OpenAI 兼容). model.mode 必须
	// == 'image_generation'. AsyncImageAdaptor (dashscope wanx) 走 submit
	// + poll 内部循环 (5s 间隔, 5min 超时); 同步 ImageAdaptor (DALL-E /
	// 自部署 SD) 走单次 HTTP. 两路客户端透明感, 始终 sync 响应 {data:[{url}]}.
	imagesHandler := &api.ImagesHandler{
		ModeRouter: modeRouter,
		// HTTPClient 30s timeout 仅用于 submit / 单次 poll 调用;
		// 长任务靠 PollTimeout (默认 5min) 控制.
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Logger:     slog.Default(),
		PlanFromClaims: func(r *http.Request) mrregistry.Plan {
			if planResolver == nil {
				return mrregistry.PlanFree
			}
			return mrregistry.Plan(plan.PlanFromRequest(r, planResolver))
		},
		Billing: modalityBilling,
	}
	mux.Handle("POST /v1/images/generations",
		authMiddleware(verifier, quotaMiddleware(limiter, planResolver, imagesHandler)))
	slog.Default().Info("model-relay /v1/images/generations mounted (M3 wanx async facade)")

	// v0.3 M4: POST /v1/videos/generations (BiuMind 自定 OpenAI-style 端点).
	// model.mode=='video_generation', adaptor 实现 VideoAdaptor (M4: dashscope
	// wanx-video / wanx2.1-i2v-turbo). Async submit+poll 默认 8s 间隔 / 10min
	// 超时 — 视频任务 30s-3min 不等, image facade 那个 5s/5min 不够.
	videosHandler := &api.VideosHandler{
		ModeRouter: modeRouter,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Logger:     slog.Default(),
		PlanFromClaims: func(r *http.Request) mrregistry.Plan {
			if planResolver == nil {
				return mrregistry.PlanFree
			}
			return mrregistry.Plan(plan.PlanFromRequest(r, planResolver))
		},
		Billing: modalityBilling,
	}
	mux.Handle("POST /v1/videos/generations",
		authMiddleware(verifier, quotaMiddleware(limiter, planResolver, videosHandler)))
	slog.Default().Info("model-relay /v1/videos/generations mounted (M4 wanx-video async facade)")

	// 段 3.6: POST /v1/internal/generations — aigc worker 把生成流量导回
	// model-relay(I6 单一 egress)。内部 bearer token 鉴权,复用上面的
	// image/video handler 执行真上游调用 + 同步 Hold/Settle。
	internalGenSrv := &internalapi.Server{
		Token:    cfg.IdentityInternalToken,
		Images:   imagesHandler,
		Videos:   videosHandler,
		Messages: relayHandler, // 爆款解析 /v1/internal/chat 复用 /v1/messages handler
	}
	internalGenSrv.MountGenerations(mux)
	slog.Default().Info("model-relay /v1/internal/generations mounted (段3.6 单一 egress)")
	internalGenSrv.MountChat(mux)
	slog.Default().Info("model-relay /v1/internal/chat mounted (爆款解析 LLM 拆解)")

	// v0.3 M6: POST /v1/audio/transcriptions (OpenAI 兼容 ASR).
	// model.mode=='audio_transcription', adaptor 实现 TranscribeAdaptor
	// (M6: openai.Adaptor 走 multipart Whisper / GPT-4o-transcribe. dashscope
	// paraformer 是 file_url 异步, 留 M6.5 AsyncTranscribeAdaptor).
	transcriptionsHandler := &api.TranscriptionsHandler{
		ModeRouter: modeRouter,
		// 长音频转写可能 5+ min, 给宽松超时.
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
		Logger:     slog.Default(),
		PlanFromClaims: func(r *http.Request) mrregistry.Plan {
			if planResolver == nil {
				return mrregistry.PlanFree
			}
			return mrregistry.Plan(plan.PlanFromRequest(r, planResolver))
		},
		Billing: modalityBilling,
	}
	mux.Handle("POST /v1/audio/transcriptions",
		authMiddleware(verifier, quotaMiddleware(limiter, planResolver, transcriptionsHandler)))
	slog.Default().Info("model-relay /v1/audio/transcriptions mounted (M6 Whisper)")

	// 爆款解析 /v1/internal/transcribe 复用上面的 transcriptionsHandler
	// (在此 wire,因 handler 此处才构造完;内部 bearer token 鉴权)。
	internalGenSrv.Transcriptions = transcriptionsHandler
	internalGenSrv.MountTranscribe(mux)
	slog.Default().Info("model-relay /v1/internal/transcribe mounted (爆款解析 STT)")

	// /v1/code/ → Brain reverse proxy. Brain enforces its own JWT auth +
	// per-user data scoping; model-relay just forwards the bearer header. Used by
	// the Flutter client's CodeTasksClient (multi-device task sync) so the
	// app only ever talks to a single endpoint (model-relay :7001).
	if cfg.BrainURL != "" {
		brainURL, perr := url.Parse(cfg.BrainURL)
		if perr != nil {
			return fmt.Errorf("invalid MODEL_RELAY_BRAIN_URL %q: %w", cfg.BrainURL, perr)
		}
		brainProxy := httputil.NewSingleHostReverseProxy(brainURL)
		mux.Handle("/v1/code/", brainProxy)
		// /v1/files/ → 同一个 brain (artifacts L3 上传 / 下载). multipart
		// upload 需要更长 read timeout, 但 model-relay Server 的 ReadHeaderTimeout
		// 已经够 (10s 只看 headers); 整体 body 由 idle timeout 兜底。
		mux.Handle("/v1/files/", brainProxy)
		// sidebar webview 图标按 sha256 拉文件走 brain /v1/brain/files-by-sha/{sha}
		// (独立命名空间, 不能挂 /v1/files/ 下 —— Go 1.22 mux 与 {id}/meta 冲突)。
		// 单 origin 后客户端经 site nginx 直达 brain, 这条 model-relay 反代仅为
		// 兼容历史直连 model-relay 的调用方。
		mux.Handle("/v1/brain/", brainProxy)
		slog.Info("model-relay /v1/code/ + /v1/files/ + /v1/brain/ → brain reverse proxy enabled",
			"upstream", cfg.BrainURL)
	}

	// admin stack 启动 + /v1/messages handler 已在上面用 startAdminStack
	// 接入完毕。这里直接进 HTTP server 收尾。

	// Wrap the entire mux in otelhttp so every request gets a span.
	// otelhttp reads context propagation headers automatically; future
	// changes to call inner services with the same context will join
	// the same trace.
	// HTTP metrics middleware: 自动记录每个请求的 method/status/duration.
	// /metrics 自身被 middleware 跳过, 不进自循环.
	metrics.SetServiceInfo(serviceVersion)
	metricsHandler := metrics.HTTPMiddleware(mux)

	corsCfg := bcors.Default()
	corsHandler := corsCfg.Wrap(metricsHandler)
	tracedHandler := otelhttp.NewHandler(corsHandler, "model-relay")
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           tracedHandler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       5 * time.Minute,
		// WriteTimeout intentionally 0: SSE long streams
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutdown signaled")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("model-relay listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	slog.Info("model-relay stopped")
	return nil
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// CredsResolverFn matches api.MessagesHandler.CredsResolver.
type CredsResolverFn = func(r *http.Request, modelName string) (string, *provider.Credentials, *http.Request, error)

// OnRequestCompleteFn matches api.MessagesHandler.OnRequestComplete.
type OnRequestCompleteFn = func(
	r *http.Request,
	model, providerName string,
	usage provider.Usage,
	latency time.Duration,
	success bool,
	errCode string,
	creditsCharged int64,
)

// startAdminStack wires up the model-config admin layer + the
// /v1/messages routing path:
//
//	migrate → store → vault → cache → probe → supervisor →
//	  + adminapi (mounted on mux)
//	  + credsResolver  (router.Resolver wrapper)
//	  + onRequestComplete (supervisor + usage_log writer)
//
// Both callbacks share the same store / vault / cache / supervisor
// closures so the request path benefits from the in-memory cache and
// stamps fresh stats back onto the channel row.
//
// Fails loud on startup error — caller refuses to listen.
// AdminStack 暴露 startAdminStack 内部已构造的依赖, 让 main.go 可以
// 把 pool / cache / vault 给到其它 handler (P4.S3.1 /v1/jobs 等).
type AdminStack struct {
	Pool  *pgxpool.Pool
	Store *mrregistry.Store
	Cache *mrregistry.Cache
	Vault *mrregistry.CredentialVault
	// Resolver — 给新 modality handler (audio_speech / embedding / 等) 用.
	// chat 路径继续走 credsResolver (含 BYOK / quota / retry); 新路径暂时
	// 单 attempt + 无 quota, M2 再统一.
	Resolver *router.Resolver
}

func startAdminStack(
	ctx context.Context,
	mux *http.ServeMux,
	cfg *Config,
	envelope *keys.Envelope,
	verifier *bauth.Verifier,
	providerRegistry *provider.Registry,
) (CredsResolverFn, OnRequestCompleteFn, *AdminStack, func(), error) {
	logger := slog.Default()

	pool, err := pgxpool.New(ctx, cfg.AdminDatabaseURL)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open admin pool: %w", err)
	}
	closers := []func(){pool.Close}
	bail := func(err error) (CredsResolverFn, OnRequestCompleteFn, *AdminStack, func(), error) {
		runClosers(closers)
		return nil, nil, nil, nil, err
	}

	// Auto-migrate. Probe table model_relay.providers is created by 00001;
	// baselineMax=0 because model_relay schema is brand-new — no historical
	// goose state to honour.
	if cfg.AdminMigrationsDir != "" {
		if err := dbmigrate.Run(ctx, pool, "model_relay",
			cfg.AdminMigrationsDir, "model_relay.providers", 0); err != nil {
			return bail(fmt.Errorf("migrate: %w", err))
		}
	}

	store := mrregistry.NewStore(pool)
	vault := mrregistry.NewCredentialVault(store.Credentials, envelope)

	cache := mrregistry.NewCache(store, mrregistry.CacheConfig{Logger: logger})
	if err := cache.Start(ctx); err != nil {
		return bail(fmt.Errorf("cache start: %w", err))
	}
	closers = append(closers, cache.Close)

	probe := health.New(health.Config{
		Store:    store,
		Vault:    vault,
		Adaptors: providerRegistry,
		Logger:   logger,
	})
	supervisor := health.NewSupervisor(probe, store, health.SupervisorConfig{Logger: logger})
	supervisor.Start(ctx)
	closers = append(closers, supervisor.Close)

	// Channel health gauge — periodic SELECT count(*) GROUP BY status.
	// Cheap (3-row aggregate over an indexed column) and gives Prometheus
	// a real-time picture of how many channels are healthy / disabled
	// without each request hitting a counter-update path.
	healthCancel := startChannelHealthPoller(ctx, pool, logger)
	closers = append(closers, healthCancel)

	// Inflight counter shared between LeastBusy strategy (reads) and
	// the resolver wrapper / OnRequestComplete (writes). Process-local
	// in-memory state — multi-replica deployments see drift but each
	// replica picks correctly for its own stream of requests.
	inflight := router.NewInflightCounter()

	stratReg := router.NewRegistry()
	stratReg.Register(router.NewWeighted())
	stratReg.Register(router.NewLowestLatency())
	stratReg.Register(router.NewLeastBusy(inflight))

	resolver := router.NewResolver(cache, vault, stratReg, router.ResolverConfig{Logger: logger})

	// Channel quota — per-channel RPM/TPM gate. Resolver retry kicks in
	// when a channel's budget is exhausted before the upstream is even
	// hit. State sourced from the channels table; refreshed every 30s
	// so admin edits to rpm_limit / tpm_limit propagate without a
	// restart.
	chQuota := router.NewChannelQuota()
	closers = append(closers, startChannelQuotaPoller(ctx, store, chQuota, logger))

	// Fx-rate cron — keeps fx_rates table fresh against open.er-api.com
	// (or whatever cfg.FxSyncURL points to). 24h interval, first run
	// 90s after boot to avoid restart storms hitting the upstream.
	if !cfg.FxSyncDisabled {
		closers = append(closers, startFxSync(ctx, store, cfg.FxSyncURL, logger))
	} else {
		logger.Info("model-relay fx-rate cron: disabled by env")
	}

	roleCache := bauth.NewRoleCache(pool)
	if err := roleCache.Reload(ctx); err != nil {
		return bail(fmt.Errorf("role cache reload: %w", err))
	}

	srv := &adminapi.Server{
		Store:           store,
		Vault:           vault,
		Cache:           cache,
		Probe:           probe,
		Supervisor:      supervisor,
		Strategies:      stratReg,
		RoleCache:       roleCache,
		JWTVerifier:     verifier,
		Logger:          logger,
		SyncUpstreamURL: cfg.AdminSyncUpstreamURL,
	}
	srv.Mount(mux)

	// P4.S1.1: 内部凭证解密 endpoint, 供 aigc 等姐妹服务在 dispatch 前
	// 拿明文 key. Bearer = IDENTITY_INTERNAL_TOKEN (与 identity 共享).
	// Token 空时仅 dev 自测用 (单测覆盖); 生产 NetworkPolicy 限定 in-cluster.
	internalSrv := &internalapi.Server{
		Token: cfg.IdentityInternalToken,
		Vault: vault,
	}
	internalSrv.Mount(mux)
	if cfg.IdentityInternalToken == "" {
		logger.Warn("model-relay internalapi: IDENTITY_INTERNAL_TOKEN empty — auth disabled (dev only)")
	} else {
		logger.Info("model-relay internalapi mounted", "endpoints", "/v1/internal/credentials/{id}/get-decrypted")
	}

	credsResolver := buildCredsResolver(resolver, chQuota, inflight, logger)
	onComplete := buildOnRequestComplete(store, supervisor, cache, chQuota, inflight, logger)

	logger.Info("model-relay admin stack ready",
		"db", redactDSN(cfg.AdminDatabaseURL),
		"sync_upstream", srv.SyncUpstreamURL,
	)
	stack := &AdminStack{
		Pool:     pool,
		Store:    store,
		Cache:    cache,
		Vault:    vault,
		Resolver: resolver,
	}
	return credsResolver, onComplete, stack, func() { runClosers(closers) }, nil
}

// startFxSync launches the daily fx-rate sync goroutine. Returns a
// cancel func bundled into the shutdown chain so process shutdown stops
// it cleanly.
func startFxSync(parent context.Context, store *mrregistry.Store, urlOverride string, logger *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	syncer := &fxsync.Syncer{
		Store:  store,
		URL:    urlOverride, // empty → fxsync.DefaultURL
		Logger: logger,
	}
	// 6h 间隔 — 上游 open.er-api.com 自身按小时刷新，6h 一次 = 4 次/天，
	// admin 重启服务后最多半天追上市场（vs 24h 间隔的整天漂移）；上游
	// 无配额，4 次/天的成本可忽略。
	go syncer.RunCron(ctx, 6*time.Hour, 90*time.Second)
	logger.Info("model-relay fx-rate cron started",
		"url", firstNonEmpty(urlOverride, fxsync.DefaultURL),
		"interval", "6h", "first_run_after", "90s")
	return cancel
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// streamingHTTPClient 给流式上游(chat / TTS)用的 client。
//
// 流式请求的 Client.Timeout 必须为 0 —— 非零会把整条 SSE 流(可达数分钟)
// 当超时掐断。但 Timeout:0 + 默认 Transport 有个致命盲区:DefaultTransport
// 有 30s dial 超时,却没有 ResponseHeaderTimeout。一旦上游 TCP 连上却迟迟不
// 回响应头(实测:your-llm-gateway 这类内网网关在 DNS 间歇解析成功后连到黑洞),
// h.HTTPClient.Do() 永不返回 → daemon 的 model 调用阻塞 → 客户端无限转圈
// (现象见 agent 模式 #11 卡死,与 #9 的 DNS no-such-host 快速失败成对照)。
//
// ResponseHeaderTimeout 只约束"请求写完 → 收到首个响应头"这一段,不限制后续
// 流式 body 读取,因此长 SSE 流不受影响,而黑洞上游会快速退化成 upstream_error
// (复用既有错误帧链路结束 spinner)。默认 120s(容忍推理模型较长 TTFT),可经
// MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT(如 "90s")覆盖。
func streamingHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = upstreamHeaderTimeout()
	return &http.Client{Timeout: 0, Transport: tr}
}

// upstreamHeaderTimeout 解析 MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT(Go duration
// 格式),非法 / 缺省回退 120s。
func upstreamHeaderTimeout() time.Duration {
	const def = 120 * time.Second
	if v := os.Getenv("MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		slog.Default().Warn("model-relay: 非法 MODEL_RELAY_UPSTREAM_HEADER_TIMEOUT,回退默认",
			"value", v, "default", def.String())
	}
	return def
}

// startChannelQuotaPoller refreshes ChannelQuota's spec map from the
// channels table every 30s. Returns a cancel func bundled into the
// shutdown chain.
func startChannelQuotaPoller(parent context.Context, store *mrregistry.Store, q *router.ChannelQuota, logger *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		// Prime the limiter immediately so the first request after
		// boot already sees current limits.
		reloadChannelQuota(ctx, store, q, logger)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reloadChannelQuota(ctx, store, q, logger)
			}
		}
	}()
	return cancel
}

func reloadChannelQuota(ctx context.Context, store *mrregistry.Store, q *router.ChannelQuota, logger *slog.Logger) {
	chans, err := store.Channels.List(ctx, mrregistry.ChannelFilter{Status: mrregistry.StatusActive})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.Warn("channel quota reload failed", "err", err.Error())
		}
		return
	}
	q.ReloadFromList(chans)
}

// buildCredsResolver constructs the per-request resolver fn that the
// /v1/messages handler calls. (P1: BYOK 不在此处 —— X-Biumind-LLM-Key header
// fast-path 已删, BYOK 统一走 messages.go 的 identity match fallback, 不靠
// 模型名前缀猜 adaptor.)
//
//  1. calls router.Resolver in a retry loop, gating each
//     pick through ChannelQuota: if the picked channel is RPM/TPM
//     exhausted, exclude it and let Strategy pick the next. Up to
//     3 attempts (covers fallback to a 3rd-priority tier).
//  2. stamps the resolved channel onto r.Context() so onComplete can
//     pull it back for supervisor + usage_log + ChannelQuota refund.
func buildCredsResolver(resolver *router.Resolver, chQuota *router.ChannelQuota, inflight *router.InflightCounter, logger *slog.Logger) CredsResolverFn {
	return func(r *http.Request, modelName string) (string, *provider.Credentials, *http.Request, error) {
		// Standard path — registry-backed resolver. (P1: BYOK 统一走 messages.go
		// 的 identity match fallback —— 删了 X-Biumind-LLM-Key header fast-path
		// + guessAdaptorByModelPrefix, 不再靠模型名前缀猜 adaptor.)
		claims, _ := bauth.ClaimsFrom(r.Context())
		var userID uuid.UUID
		if claims != nil && claims.UserID != "" {
			if id, err := uuid.Parse(claims.UserID); err == nil {
				userID = id
			}
		}
		userPlan := mrregistry.Plan(planFromClaims(r))

		// Resolve + ChannelQuota retry loop. Each iteration: Strategy
		// picks a channel, then we gate through chQuota; on quota miss
		// the channel is added to Exclude and we re-Pick. 3 attempts
		// max — covers a 3-tier priority fallback before giving up.
		var (
			out          *router.ResolveOutput
			err          error
			exclude      = map[uuid.UUID]error{}
			resolveStart = time.Now()
		)
		const maxAttempts = 3
		for attempt := 0; attempt < maxAttempts; attempt++ {
			out, err = resolver.Resolve(r.Context(), router.ResolveInput{
				ModelCode: modelName,
				UserID:    userID,
				UserPlan:  userPlan,
				RequestID: r.Header.Get("X-Request-ID"),
				Exclude:   exclude,
				Attempt:   attempt,
			})
			if err != nil {
				break
			}
			if qErr := chQuota.AcquireRPM(out.Channel.ID); qErr != nil {
				// This channel is over budget — exclude and try the
				// next priority. Don't burn supervisor failure_count;
				// it's not the channel's fault.
				// reason 当前只有 rpm_exhausted (TPM peek 也走同一 errcode)
				// — future "cooldown" / "circuit_break" 上线时再分。
				metrics.RecordChannelFallback("rpm_exhausted")
				exclude[out.Channel.ID] = qErr
				out = nil
				err = router.ErrAllChannelsFailed // tentative; reset by next iter
				continue
			}
			// channel selected — track in-flight so least_busy strategy
			// sees this request count toward the channel's load. Release
			// happens in OnRequestComplete; defer-style here would be
			// awkward because we hand control back to MessagesHandler.
			inflight.Acquire(out.Channel.ID)
			err = nil
			break
		}
		metrics.RecordModelRelayResolve(classifyResolveError(err), time.Since(resolveStart))
		if err != nil {
			return "", nil, nil, err
		}

		// Stamp the resolved channel onto the request ctx so the
		// post-call hook can find it.
		scoped := r.WithContext(router.WithResolveOutput(r.Context(), out))

		adaptorName := adaptorNameFromProtocol(out.ProviderProtocol)
		creds := &provider.Credentials{
			APIKey:  string(out.Plaintext),
			BaseURL: out.BaseURL,
		}
		// header_override goes through Credentials.Extra — adaptors
		// that recognise vendor-specific keys (Azure api-version etc.)
		// can pick them up; vanilla OpenAI/Anthropic ignore.
		if len(out.Header) > 0 {
			creds.Extra = make(map[string]string, len(out.Header))
			for k, v := range out.Header {
				creds.Extra[k] = v
			}
		}
		return adaptorName, creds, scoped, nil
	}
}

// buildOnRequestComplete constructs the post-call hook that records
// success / failure on the resolved channel and writes the dual-currency
// usage_log row.
//
// Failure handling:
//   - errCode "" success: RecordSuccess (latency EWMA, clear failure_count)
//   - ChannelQuota.RecordTokens (TPM accounting)
//   - errCode upstream_status / upstream_error / stream_error:
//     RecordFailure → may flip channel to auto_disabled at threshold
//   - errCode translate_request_failed: refund RPM (we acquired but
//     didn't reach upstream) — keeps the gate accurate.
//   - other errCodes (parse / unknown_provider): just write usage_log;
//     not the channel's fault, RPM stays consumed (we did make the
//     upstream call, just couldn't parse the answer).
func buildOnRequestComplete(
	store *mrregistry.Store,
	supervisor *health.Supervisor,
	cache *mrregistry.Cache,
	chQuota *router.ChannelQuota,
	inflight *router.InflightCounter,
	logger *slog.Logger,
) OnRequestCompleteFn {
	return func(
		r *http.Request,
		model, providerName string,
		usage provider.Usage,
		latency time.Duration,
		success bool,
		errCode string,
		creditsCharged int64,
	) {
		ctx := r.Context()
		out, ok := router.ResolveOutputFrom(ctx)
		if !ok {
			// BYOK or resolver-level failure (no channel selected).
			// Nothing to stamp on a channel row; usage_log requires
			// channel_id NOT NULL so we skip it too. Prometheus metrics
			// already captured the request via reportUsage.
			return
		}
		// Always release the in-flight slot first — even on failure
		// paths, the request is no longer in flight. Defensive:
		// floor-at-0 means an extra Release on a never-Acquired channel
		// is a no-op (BYOK path, etc).
		defer inflight.Release(out.Channel.ID)

		latencyMs := int(latency / time.Millisecond)
		if latencyMs <= 0 {
			latencyMs = 1
		}

		// Channel health bookkeeping + channel quota TPM accounting.
		totalTokens := int64(usage.PromptTokens) + int64(usage.CompletionTokens) +
			int64(usage.CacheReadTokens) + int64(usage.CacheWriteTokens)
		switch {
		case success:
			if err := store.Channels.RecordSuccess(ctx, out.Channel.ID, latencyMs); err != nil {
				logger.Warn("model_relay: record_success failed",
					"channel", out.Channel.ID, "err", err.Error())
			}
			chQuota.RecordTokens(out.Channel.ID, totalTokens)
		case isUpstreamFailure(errCode):
			// R4-B：按上游 status 分类失败（429/401/402/5xx → 不同 disable 阈值
			// + cooldown）。UpstreamFailure 由 handler 在 >=400 分支经 ctx 带来；
			// 无（网络/读取错误，无 HTTP status）→ 退化为瞬态。
			kind := health.FailureTransient
			var retryAfter time.Duration
			upStatus := 0
			if uf, ok := router.UpstreamFailureFrom(ctx); ok {
				kind = health.ClassifyUpstreamStatus(uf.StatusCode)
				retryAfter = uf.RetryAfter
				upStatus = uf.StatusCode
			}
			if _, _, err := supervisor.RecordFailureKind(ctx, out.Channel.ID,
				fmt.Errorf("relay: %s (status=%d)", errCode, upStatus), kind, retryAfter); err != nil {
				logger.Warn("model_relay: record_failure failed",
					"channel", out.Channel.ID, "err", err.Error())
			}
			// Even on upstream failure, tokens may have been billed by
			// the upstream provider before the error response — count
			// them so quota stays honest.
			if totalTokens > 0 {
				chQuota.RecordTokens(out.Channel.ID, totalTokens)
			}
		case errCode == "translate_request_failed":
			// Adaptor never sent the request; RPM was acquired during
			// resolve but no upstream call → refund.
			chQuota.RefundRPM(out.Channel.ID)
		}

		// Usage log — fire-and-forget on a worker goroutine so a slow
		// log write can't slow down the response. Capture by value.
		go writeUsageLogAsync(
			r, out, store, cache, logger,
			model, usage, latencyMs, success, errCode, creditsCharged,
		)
	}
}

// writeUsageLogAsync converts the request's tokens into both the
// origin currency (whatever pricing.currency was) and the user's
// settlement currency, then appends a row.
func writeUsageLogAsync(
	r *http.Request,
	out *router.ResolveOutput,
	store *mrregistry.Store,
	cache *mrregistry.Cache,
	logger *slog.Logger,
	model string,
	usage provider.Usage,
	latencyMs int,
	success bool,
	errCode string,
	creditsCharged int64,
) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Warn("model_relay: usage_log writer panic",
				"recovered", fmt.Sprintf("%v", rec))
		}
	}()
	// Use a fresh context with timeout — caller may have already
	// returned its response and r.Context() could be canceled.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	claims, _ := bauth.ClaimsFrom(r.Context())
	var userID uuid.UUID
	if claims != nil && claims.UserID != "" {
		if id, err := uuid.Parse(claims.UserID); err == nil {
			userID = id
		}
	}

	// Get current pricing — append-only table; resolver of "now()" is
	// what we charge. Missing pricing → zero cost (still logs the row
	// for token accounting; admin will see "$0" and notice).
	var (
		originCurrency = mrregistry.CurrencyUSD
		originAmount   float64
	)
	if pr, err := store.Pricing.GetCurrent(ctx, out.Model.ID); err == nil {
		originCurrency = pr.Currency
		originAmount = computeCost(usage, pr)
	}

	// Settlement currency: per-user policy (placeholder — MVP just
	// settles in CNY for everyone; Phase 2 lookup user preference).
	settleCurrency := mrregistry.CurrencyCNY
	fxRate := 1.0
	if originCurrency != settleCurrency {
		if r, err := cache.FxRate(ctx, originCurrency, settleCurrency); err == nil {
			fxRate = r
		}
	}
	settleAmount := originAmount * fxRate

	statusVal := mrregistry.UsageOK
	if !success {
		switch errCode {
		case "rate_limited", "upstream_status":
			statusVal = mrregistry.UsageRateLimited
		default:
			statusVal = mrregistry.UsageError
		}
	}

	if err := store.UsageLog.Append(ctx, mrregistry.UsageLogInput{
		UserID:             userID,
		ModelID:            out.Model.ID,
		ChannelID:          out.Channel.ID,
		ModelCode:          model,
		UpstreamModel:      out.UpstreamModel,
		UserPlan:           mrregistry.Plan(planFromClaims(r)),
		InputTokens:        int64(usage.PromptTokens),
		OutputTokens:       int64(usage.CompletionTokens),
		CacheReadTokens:    int64(usage.CacheReadTokens),
		CacheWriteTokens:   int64(usage.CacheWriteTokens),
		CostOriginCurrency: originCurrency,
		CostOriginAmount:   originAmount,
		CostSettleCurrency: settleCurrency,
		CostSettleAmount:   settleAmount,
		FxRate:             fxRate,
		LatencyMs:          latencyMs,
		CreditsCharged:     creditsCharged,
		Status:             statusVal,
		ErrorCode:          errCode,
		RequestID:          r.Header.Get("X-Request-ID"),
	}); err != nil {
		logger.Warn("model_relay: usage_log append failed", "err", err.Error())
	}
}

// computeCost calculates the request's cost in pricing.currency units.
// Tokens × per-Mtok rate. cache split pulled out separately when
// the upstream reports it (Anthropic prompt caching).
func computeCost(u provider.Usage, p *mrregistry.Pricing) float64 {
	const mtok = 1_000_000.0
	return (float64(u.PromptTokens)*p.InputPerMTok +
		float64(u.CompletionTokens)*p.OutputPerMTok +
		float64(u.CacheWriteTokens)*p.CacheWritePerMTok +
		float64(u.CacheReadTokens)*p.CacheReadPerMTok) / mtok
}

func isUpstreamFailure(errCode string) bool {
	switch errCode {
	case "upstream_error", "upstream_status", "stream_error", "upstream_read":
		return true
	default:
		return false
	}
}

// adaptorNameFromProtocol maps registry.ProviderProtocol to the name
// the provider.Registry knows the adaptor by. registry-side values are
// "openai_compat" / "anthropic"; adaptor-side names are "openai" /
// "anthropic".
func adaptorNameFromProtocol(p mrregistry.ProviderProtocol) string {
	switch p {
	case mrregistry.ProtocolAnthropic:
		return "anthropic"
	default: // openai_compat
		return "openai"
	}
}

// (P1) guessAdaptorByModelPrefix 已删 —— BYOK adaptor 改由 identity 返回的
// protocol 经 adaptorNameFromProtocol 选择, 不再靠模型名前缀猜.

// startChannelHealthPoller runs a 30s ticker that snapshots
// model_relay.channels into the channel_health gauge. Cancel by
// invoking the returned function. Failures are logged and skipped —
// missing one tick doesn't matter.
func startChannelHealthPoller(parent context.Context, pool *pgxpool.Pool, logger *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		// Run once on start so the gauge is populated quickly.
		pollChannelHealth(ctx, pool, logger)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollChannelHealth(ctx, pool, logger)
			}
		}
	}()
	return cancel
}

func pollChannelHealth(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	rows, err := pool.Query(ctx,
		`SELECT status, count(*) FROM model_relay.channels GROUP BY status`)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			logger.Warn("channel health poll failed", "err", err.Error())
		}
		return
	}
	defer rows.Close()
	// Reset all known statuses to 0 first — channels that move OUT of a
	// status need their old gauge cleared, otherwise the value sticks.
	seen := map[string]int{
		"active":        0,
		"disabled":      0,
		"auto_disabled": 0,
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		seen[status] = count
	}
	for status, count := range seen {
		metrics.SetModelRelayChannelHealth(status, count)
	}
}

// classifyResolveError maps router sentinel errors to Prometheus label
// values so dashboards can branch by failure mode without parsing
// human-readable messages.
func classifyResolveError(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, router.ErrModelDisabled):
		return "disabled"
	case errors.Is(err, router.ErrModelNotFound):
		return "not_found"
	case errors.Is(err, router.ErrModelHidden):
		return "hidden"
	case errors.Is(err, router.ErrNoActiveChannel):
		return "no_channel"
	case errors.Is(err, router.ErrAllChannelsFailed):
		return "exhausted"
	case errors.Is(err, router.ErrCredentialUnavailable):
		return "cred_unavailable"
	default:
		return "internal"
	}
}

// planFromClaims is duplicated from internal/api/messages.go because it
// is also needed in main.go for usage_log + cred resolver. Keeping the
// 5 lines duplicated is cheaper than exporting an internal helper.
func planFromClaims(r *http.Request) string {
	c, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		return "free"
	}
	if c.Plan != "" {
		return c.Plan
	}
	if len(c.Roles) > 0 {
		return "admin"
	}
	return "free"
}

// runClosers fires close fns in reverse insertion order so dependencies
// shut down before their dependents.
func runClosers(fns []func()) {
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}

// redactDSN strips the password from a postgres DSN for safe logging.
func redactDSN(dsn string) string {
	// Cheap heuristic — works for postgres://user:pass@host/db format.
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return dsn
	}
	colon := strings.Index(dsn[:at], "://")
	if colon < 0 {
		return dsn
	}
	creds := dsn[colon+3 : at]
	if idx := strings.Index(creds, ":"); idx >= 0 {
		return dsn[:colon+3] + creds[:idx] + ":***" + dsn[at:]
	}
	return dsn
}

// authMiddleware validates Bearer JWT and attaches claims to context.
// Bypasses /healthz, /readyz, /api/version.
func authMiddleware(v *bauth.Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		claims, err := v.Verify(auth[7:])
		if err != nil {
			http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
			return
		}
		r = r.WithContext(bauth.WithClaims(r.Context(), claims))
		next.ServeHTTP(w, r)
	})
}

// quotaMiddleware enforces per-virtual-key rate limits.
//
// Two gates per relay request:
//  1. RPM (hub.rpm): hard pre-flight. Reserves 1 unit. Refused → 429.
//  2. TPM (hub.tpm): soft peek. If the user is already over budget
//     based on prior accounted usage, refuse before we burn an
//     upstream call. Reserve happens post-flight in the handler
//     (see MessagesHandler.reportUsage) because we don't know the
//     real token count until the SSE stream finishes.
//
// Caller is identified by the JWT `sub` claim (user id) for now;
// migration to the dedicated virtual-key id when Identity exposes it
// is a one-line change here.
// v0.3 M5: 5 个 modality 端点跟 chat 共用 hub.rpm/hub.tpm 限流 bucket.
// TPM 维度对 image/video 不严格 (单 request token 数小, 真烧的是 image
// 张数 / video 秒数; 真正的成本闸门走 billing.Hold). RPM 普适 — 防一个
// 用户秒级灌爆所有 modality.
var quotaPaths = map[string]bool{
	"/v1/messages":             true,
	"/v1/chat/completions":     true,
	"/v1/embeddings":           true, // M2
	"/v1/rerank":               true, // M2.5
	"/v1/audio/speech":         true, // M1
	"/v1/audio/transcriptions": true, // M6
	"/v1/images/generations":   true, // M3
	"/v1/videos/generations":   true, // M4
}

func quotaMiddleware(l quota.Limiter, resolver plan.Resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip non-relay paths so /api/version and friends always work.
		if !quotaPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		c, ok := bauth.ClaimsFrom(r.Context())
		if !ok {
			// auth ran first; if we're here something is misconfigured.
			http.Error(w, "no claims in context", http.StatusInternalServerError)
			return
		}
		key := c.UserID

		// Resolve plan → bucket. With no resolver wired up we fall back
		// to the legacy bucket names so existing deployments keep their
		// MODEL_RELAY_DEFAULT_* sizing behaviour.
		rpmBucket, tpmBucket := "hub.tpm", "hub.tpm"
		if resolver != nil {
			p := plan.PlanFromRequest(r, resolver)
			rpmBucket = plan.BucketFor("hub.rpm", p)
			tpmBucket = plan.BucketFor("hub.tpm", p)
			w.Header().Set("X-RateLimit-Plan", string(p))
		}

		// Gate 1: RPM hard pre-flight.
		d := l.CheckAndReserve(rpmBucket, key, 1)
		metrics.RecordQuota(rpmBucket, d.Allow, d.Remaining)
		for k, v := range d.Headers() {
			w.Header().Set(k, v)
		}
		if !d.Allow {
			w.Header().Set("Retry-After",
				fmt.Sprintf("%d", int(time.Until(d.Reset).Seconds())+1))
			metrics.RecordHubRequest(r.URL.Path, http.StatusTooManyRequests)
			http.Error(w, "rate limit exceeded (rpm)", http.StatusTooManyRequests)
			return
		}

		// Gate 2: TPM soft peek. Snapshot is read-only; the real
		// debit happens after the upstream response in reportUsage.
		// If the bucket is unconfigured, Snapshot returns Allow=true
		// with Limit=0 and we no-op.
		if t := l.Snapshot(tpmBucket, key); t.Limit > 0 && !t.Allow {
			metrics.RecordQuota(tpmBucket, false, 0)
			w.Header().Set("X-RateLimit-TPM-Limit", fmt.Sprintf("%d", t.Limit))
			w.Header().Set("X-RateLimit-TPM-Remaining", "0")
			w.Header().Set("X-RateLimit-TPM-Reset", fmt.Sprintf("%d", t.Reset.Unix()))
			w.Header().Set("Retry-After",
				fmt.Sprintf("%d", int(time.Until(t.Reset).Seconds())+1))
			metrics.RecordHubRequest(r.URL.Path, http.StatusTooManyRequests)
			http.Error(w, "rate limit exceeded (tpm)", http.StatusTooManyRequests)
			return
		}

		// Wrap response writer so we can record final status for the
		// downstream handler (200 / 4xx / 5xx).
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		metrics.RecordHubRequest(r.URL.Path, ww.status)
	})
}

// httpPlanResolver queries Identity for a user's plan. Cached for
// 60 s in-process so the relay path is not bottlenecked on Identity
// latency. The cache is best-effort: a stale read at most causes a
// short window of incorrect bucketing after a plan change.
type httpPlanResolver struct {
	baseURL string
	client  *http.Client
	cache   sync.Map // userID → cachedPlan
}

type cachedPlan struct {
	plan      plan.Plan
	expiresAt time.Time
}

func newHTTPPlanResolver(baseURL string) *httpPlanResolver {
	return &httpPlanResolver{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 2 * time.Second},
	}
}

func (h *httpPlanResolver) Resolve(ctx context.Context, userID string) (plan.Plan, error) {
	if userID == "" {
		return plan.PlanFree, nil
	}
	now := time.Now()
	if v, ok := h.cache.Load(userID); ok {
		c := v.(cachedPlan)
		if c.expiresAt.After(now) {
			return c.plan, nil
		}
	}
	url := strings.TrimRight(h.baseURL, "/") +
		"/v1/internal/users/" + userID + "/plan"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return plan.PlanFree, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return plan.PlanFree, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return plan.PlanFree, fmt.Errorf("plan lookup status %d", resp.StatusCode)
	}
	var body struct {
		Plan string `json:"plan"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return plan.PlanFree, err
	}
	p := plan.Plan(body.Plan)
	h.cache.Store(userID, cachedPlan{
		plan: p, expiresAt: now.Add(60 * time.Second),
	})
	return p, nil
}

// statusRecorder is a tiny ResponseWriter shim that captures the final
// HTTP status code for metrics. It does NOT buffer the body — streaming
// SSE responses pass through untouched.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// Flush — required for SSE streaming to keep flushing through us.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
