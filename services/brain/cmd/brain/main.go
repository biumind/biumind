// Brain service entry point.
//
// MVP scope: Wiki sub-module (pages/blocks/sources/events).
// Graph / Memory / Search land in Phase 2.3+.
package main

import (
	"context"
	"crypto/rand"
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
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	bcors "github.com/biumind/biumind/packages/go-sdk/biu/cors"
	bdb "github.com/biumind/biumind/packages/go-sdk/biu/db"
	"github.com/biumind/biumind/packages/go-sdk/biu/dbmigrate"
	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/packages/go-sdk/biu/rerank"
	agentplanepkg "github.com/biumind/biumind/services/brain/internal/agentplane"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	providerspkg "github.com/biumind/biumind/services/brain/internal/chat/providers"
	"github.com/biumind/biumind/services/brain/internal/events"
	filespkg "github.com/biumind/biumind/services/brain/internal/files"
	graphapi "github.com/biumind/biumind/services/brain/internal/graph/api"
	graphstore "github.com/biumind/biumind/services/brain/internal/graph/store"
	graphsub "github.com/biumind/biumind/services/brain/internal/graph/subscriber"
	memoryapi "github.com/biumind/biumind/services/brain/internal/memory/api"
	memconsolidator "github.com/biumind/biumind/services/brain/internal/memory/consolidator"
	memorymcp "github.com/biumind/biumind/services/brain/internal/memory/mcp"
	memorystore "github.com/biumind/biumind/services/brain/internal/memory/store"
	memworker "github.com/biumind/biumind/services/brain/internal/memory/worker"
	noteapi "github.com/biumind/biumind/services/brain/internal/note/api"
	notestore "github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/biumind/biumind/services/brain/internal/publisher"
	searchapi "github.com/biumind/biumind/services/brain/internal/search/api"
	"github.com/biumind/biumind/services/brain/internal/search/bm25"
	"github.com/biumind/biumind/services/brain/internal/search/decay"
	"github.com/biumind/biumind/services/brain/internal/search/searxng"
	searchvector "github.com/biumind/biumind/services/brain/internal/search/vector"
	toolspkg "github.com/biumind/biumind/services/brain/internal/tools"
	toolsapi "github.com/biumind/biumind/services/brain/internal/tools/api"
	toolsbuiltin "github.com/biumind/biumind/services/brain/internal/tools/builtin"
	wikiactivity "github.com/biumind/biumind/services/brain/internal/wiki/activity"
	"github.com/biumind/biumind/services/brain/internal/wiki/api"
	wikichunks "github.com/biumind/biumind/services/brain/internal/wiki/chunks"
	wikiembed "github.com/biumind/biumind/services/brain/internal/wiki/embedworker"
	wikienrich "github.com/biumind/biumind/services/brain/internal/wiki/enrich"
	wikigraphproj "github.com/biumind/biumind/services/brain/internal/wiki/graph"
	wikiingest "github.com/biumind/biumind/services/brain/internal/wiki/ingest"
	wikillmsettings "github.com/biumind/biumind/services/brain/internal/wiki/llmsettings"
	wikirelevance "github.com/biumind/biumind/services/brain/internal/wiki/relevance"
	wikiresearch "github.com/biumind/biumind/services/brain/internal/wiki/research"
	wikireviews "github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikisearchproj "github.com/biumind/biumind/services/brain/internal/wiki/search"
	wikisources "github.com/biumind/biumind/services/brain/internal/wiki/sources"
	"github.com/biumind/biumind/services/brain/internal/wiki/store"
	wikisuggestions "github.com/biumind/biumind/services/brain/internal/wiki/suggestions"
	wikisynccp "github.com/biumind/biumind/services/brain/internal/wiki/synccheckpoint"
	wikisyncws "github.com/biumind/biumind/services/brain/internal/wiki/syncws"
	wikivision "github.com/biumind/biumind/services/brain/internal/wiki/vision"
)

const (
	serviceName    = "brain"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string `env:"LISTEN_ADDR" default:":7003"`
	Environment  string `env:"BIUMIND_ENV" default:"dev"`
	LogLevel     string `env:"BIUMIND_LOG_LEVEL" default:"info"`
	OtlpEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" default:""`
	DatabaseURL  string `env:"DATABASE_URL" required:"true"`
	// MigrationsDir 启动自动跑 goose up. 容器里默认 /etc/biumind/migrations/brain.
	// 留空跳过 (单测).
	MigrationsDir string `env:"BIUMIND_MIGRATIONS_DIR" default:"/etc/biumind/migrations/brain"`
	JWTSecret     string `env:"JWT_SECRET" required:"true"`
	JWTIssuer     string `env:"JWT_ISSUER" default:"https://identity.biumind.local"`
	JWTAudience   string `env:"JWT_AUDIENCE" default:"biumind-api"`
	// IdentityJWKSURL — Identity's public JWKS endpoint. When set, this
	// service verifies RS256 tokens against it instead of the shared HS256
	// secret. JWT_SECRET is still required as the dev/test fallback.
	IdentityJWKSURL    string `env:"IDENTITY_JWKS_URL" default:""`
	RealtimeURL        string `env:"REALTIME_INTERNAL_URL" default:""`
	SearxNGURL         string `env:"SEARXNG_URL" default:""`
	SearchHalfLifeDays int    `env:"SEARCH_HALF_LIFE_DAYS" default:"30"`

	// ShareSigningKey — 笔记分享访问 JWT 的 HS256 密钥（§7.6）。为空则
	// 启动时随机生成 + warn（单实例 dev 可用）；多实例部署必须显式配置，
	// 否则各实例签发的访客 token 互不认。
	ShareSigningKey string `env:"BRAIN_SHARE_SIGNING_KEY" default:""`

	// NatsURL — when set, Wiki block events fan out to NATS in addition
	// to Realtime. Empty disables NATS publishing (Realtime path stays).
	NatsURL string `env:"NATS_URL" default:""`

	// BusUseJetStream — when true (and NATS_URL set), the publisher
	// writes block events to JetStream (durable, at-least-once) and
	// the graph subscriber binds a durable consumer. Production
	// default; pair with channels + runtime running with the same flag.
	BusUseJetStream bool `env:"BUS_USE_JETSTREAM" default:"false"`

	// OutboxPoller — when true, runs a transactional outbox poller
	// that scans brain.events for unpublished rows and publishes them
	// alongside the LISTEN/NOTIFY listener. Belt-and-suspenders: the
	// listener is fast-path, the poller is the durability floor (it
	// catches rows the listener missed during restart, broker outage,
	// or replica fanout). Production default true.
	OutboxPoller         bool          `env:"BUS_OUTBOX_POLLER" default:"true"`
	OutboxPollerInterval time.Duration `env:"BUS_OUTBOX_POLLER_INTERVAL" default:"5s"`
	OutboxPollerBatch    int           `env:"BUS_OUTBOX_POLLER_BATCH" default:"100"`

	// ─── Files / MinIO blob storage (artifacts L3 + future generic file ref) ──
	// 空 endpoint 时 files 模块不挂载 — 上传 / 下载 endpoint 整个不存在。
	// 部署时给 MINIO_ENDPOINT='minio:9000' / ACCESS_KEY / SECRET_KEY 即开。
	MinioEndpoint       string `env:"MINIO_ENDPOINT"          default:""`
	MinioAccessKey      string `env:"MINIO_ACCESS_KEY"        default:""`
	MinioSecretKey      string `env:"MINIO_SECRET_KEY"        default:""`
	MinioUseSSL         bool   `env:"MINIO_USE_SSL"           default:"false"`
	MinioBucket         string `env:"MINIO_BUCKET"            default:"biumind-files"`
	FilesMaxUploadBytes int64  `env:"FILES_MAX_UPLOAD_BYTES"  default:"104857600"` // 100MB

	// GraphAutoExtract — when true (and NATS is connected), Brain runs
	// the heuristic graph extractor against every block.created/updated
	// event. Default off so dev environments don't grow a graph noise
	// floor; production should set this.
	GraphAutoExtract bool `env:"GRAPH_AUTO_EXTRACT" default:"false"`

	// ─── Memory embedding worker ─────────────────────────────────
	// EMBED_PROVIDER controls how Brain.Memory recall ranks results.
	//   "stub"   — deterministic hash-based vectors, no network. Good
	//              for dev / tests / air-gapped environments.
	//   "openai" — OpenAI-compatible /embeddings. Default egress is
	//              model-relay /v1/internal/embeddings (platform pool,
	//              bge-m3); set EMBED_BASE_URL to bypass only when you
	//              know why (breaks I6 central-egress).
	//   ""       — disables the worker; recall stays lexical-only.
	EmbedProvider    string `env:"EMBED_PROVIDER"      default:""`
	EmbedAPIKey      string `env:"EMBED_API_KEY"       default:""`
	EmbedBaseURL     string `env:"EMBED_BASE_URL"      default:""`
	EmbedModel       string `env:"EMBED_MODEL"         default:"bge-m3"`
	EmbedDims        int    `env:"EMBED_DIMS"          default:"1024"`
	EmbedWorkerEvery int    `env:"EMBED_WORKER_EVERY_SEC" default:"5"`
	EmbedWorkerBatch int    `env:"EMBED_WORKER_BATCH"  default:"32"`

	// ─── Cross-encoder reranker (search P1-2) ─────────────────────
	// RERANK_PROVIDER unset → no rerank; the fused list keeps RRF order.
	//   "cohere" — Cohere-shape /rerank. Default egress is model-relay
	//              /v1/internal/rerank (platform pool, bge-reranker-v2-m3);
	//              set RERANK_BASE_URL to bypass (breaks I6 central-egress).
	//   "stub"   — deterministic token-overlap (tests/dev, no network).
	//   ""       — disables rerank.
	RerankProvider string `env:"RERANK_PROVIDER" default:""`
	RerankAPIKey   string `env:"RERANK_API_KEY"   default:""`
	RerankBaseURL  string `env:"RERANK_BASE_URL"  default:""`
	RerankModel    string `env:"RERANK_MODEL"     default:"BAAI/bge-reranker-v2-m3"`

	// ─── Wiki chunk embedding worker ─────────────────────────────
	// Drives the third RRF retrieval path (vector). Shares the embedder
	// configured via EMBED_PROVIDER above; when EMBED_PROVIDER is unset
	// the worker is disabled and search/api falls back to BM25 + web
	// fusion only. Tunables live separately so wiki rechunking can run
	// at a different cadence than memory backfill.
	WikiEmbedEvery        int `env:"WIKI_EMBED_EVERY_SEC"      default:"10"`
	WikiEmbedRechunkBatch int `env:"WIKI_EMBED_RECHUNK_BATCH"  default:"16"`
	WikiEmbedBatch        int `env:"WIKI_EMBED_BATCH"          default:"32"`
	WikiChunkTargetChars  int `env:"WIKI_CHUNK_TARGET_CHARS"   default:"1000"`
	WikiChunkMaxChars     int `env:"WIKI_CHUNK_MAX_CHARS"      default:"1500"`
	WikiChunkMinChars     int `env:"WIKI_CHUNK_MIN_CHARS"      default:"200"`

	// ─── Memory consolidator ─────────────────────────────────────
	// Set CONSOLIDATE_INTERVAL_HOURS=0 to disable. Default 1h is fine
	// for projects with thousands of memories; bump up for quieter
	// workloads.
	ConsolidateIntervalHours int     `env:"CONSOLIDATE_INTERVAL_HOURS" default:"1"`
	ConsolidateCosineMax     float64 `env:"CONSOLIDATE_COSINE_THRESHOLD" default:"0.05"`
	SalienceDecayPerDay      float64 `env:"SALIENCE_DECAY_PER_DAY" default:"0.01"`
	SalienceIdleDays         float64 `env:"SALIENCE_IDLE_DAYS" default:"7"`

	// ─── Chat (multi-thread + history persistence, design doc) ──
	// RelayURL: when set, the chat send endpoint streams via model-relay. Empty
	// disables /v1/threads/{id}/send (clients fall back to direct mode
	// where the LLM call goes from the client and only the resulting
	// message is PATCH'd back).
	RelayURL string `env:"MODEL_RELAY_URL" default:""`

	// AgentPlaneDefaultChatModel — client 没传 thread.model(显示
	// "BiuMind 默认")时 chat-mode runner 的 env 覆盖。兜底链:
	// relay default-chat (models.is_default_chat) > 本 env >
	// claude-sonnet-4-6 硬兜底。运维用它强制切到 model-relay 上
	// active 的 model id,避免 admin 关掉默认模型后 chat 全废。
	AgentPlaneDefaultChatModel string `env:"AGENT_PLANE_DEFAULT_CHAT_MODEL" default:""`
	// Stale window for cleanup of orphan streaming messages (server
	// crash mid-stream). Default 5 min.
	ChatOrphanCleanupMinutes int `env:"CHAT_ORPHAN_CLEANUP_MINUTES" default:"5"`

	// IdentityURL — identity service base URL. P3: brain no longer stores
	// user LLM keys (key_vaults_encrypted removed); it fetches them on
	// demand from identity's service-to-service internal API
	// /v1/internal/byok/* (same pattern model-relay uses). Empty in dev →
	// BYOK lookups return not-found (model refresh skips, daemon agent/task
	// BYOK falls back to platform pool).
	IdentityURL string `env:"IDENTITY_URL" default:""`
	// IdentityInternalToken — shared service bearer for identity /v1/internal/*
	// (same value model-relay uses; identity/cmd/main.go reads the
	// IDENTITY_INTERNAL_TOKEN env). Required when IdentityURL is set.
	IdentityInternalToken string `env:"IDENTITY_INTERNAL_TOKEN" default:""`

	// CORS_EXTRA_ORIGINS — comma-separated list of additional origins
	// allowed to call this service from a browser. The defaults
	// (app.biumind.com / biumind.com / localhost) cover most setups;
	// add staging hosts here.
	CorsExtraOrigins string `env:"CORS_EXTRA_ORIGINS" default:""`

	// BIUMIND_INTERNAL_TOKEN — shared secret guarding the /v1/internal/*
	// endpoints workers reach for service-to-service calls. Empty
	// disables those endpoints (worker source-id ingest path stops
	// working — workers fall back to inline raw_text when present).
	// Production: 32+ random chars, rotated on leak.
	InternalToken string `env:"BIUMIND_INTERNAL_TOKEN" default:""`

	// ModelRelayInternalToken — shared secret for model-relay's
	// /v1/internal/* endpoints (platform-infra lane). Same value relay
	// validates (relay reads MODEL_RELAY_INTERNAL_TOKEN). The embedder
	// uses it to call /v1/internal/embeddings without a user JWT —
	// embeddings are background indexing infra, billed to the platform.
	ModelRelayInternalToken string `env:"MODEL_RELAY_INTERNAL_TOKEN" default:""`

	// ─── Wiki dedup worker ───────────────────────────────────────
	// Periodic scan of brain.wiki_chunks ANN distance to surface near-
	// duplicate pages as review_items. 0h disables the worker; the
	// REST + MCP surface still works for manually-injected reviews.
	DedupIntervalHours   int     `env:"DEDUP_INTERVAL_HOURS" default:"6"`
	DedupMaxDistance     float64 `env:"DEDUP_MAX_DISTANCE"   default:"0.15"`
	DedupMaxPairsPerProj int     `env:"DEDUP_MAX_PAIRS_PER_PROJECT" default:"50"`
	DedupMaxOpenPerProj  int     `env:"DEDUP_MAX_OPEN_PER_PROJECT"  default:"100"`

	// LLM 二次过滤. true 时 worker 在 cosine 候选基础上再调 model-relay 让模型判
	// duplicate vs related, 拦掉假阳性. 需要 MODEL_RELAY_URL 已配 (本来就要给
	// chat 用) — 缺则降级到无过滤. DEDUP_LLM_MODEL 默认 haiku 是 cost/
	// speed 折中, ops 可改 sonnet 提质量.
	DedupLLMFilter bool   `env:"DEDUP_LLM_FILTER" default:"true"`
	DedupLLMModel  string `env:"DEDUP_LLM_MODEL"  default:"claude-haiku-4-5-20251001"`

	// ─── Wiki lint worker ────────────────────────────────────────
	// Periodic rules-based audit (untitled / empty / stub / dead
	// wikilinks). 0h disables; rules + REST/MCP surface still work
	// for direct integration tests.
	LintIntervalHours  int  `env:"LINT_INTERVAL_HOURS"        default:"12"`
	LintMaxOpenPerProj int  `env:"LINT_MAX_OPEN_PER_PROJECT"  default:"200"`
	LintLLMFilter      bool `env:"LINT_LLM_FILTER"            default:"true"`

	// ─── Wiki relevance worker ───────────────────────────────────
	// Periodic page-pair scoring (direct wikilink + Adamic-Adar +
	// type affinity) feeding wiki.related_pages / GET /v1/wiki/pages/{id}/related.
	// 0h disables.
	RelevanceIntervalHours int     `env:"RELEVANCE_INTERVAL_HOURS"     default:"6"`
	RelevanceMinScore      float64 `env:"RELEVANCE_MIN_SCORE"         default:"0.5"`
	RelevanceMaxNeighbours int     `env:"RELEVANCE_MAX_NEIGHBOURS"    default:"30"`

	// ─── Wiki sweep worker ───────────────────────────────────────
	// Daily audit for stale (long un-updated) and orphaned (no
	// incoming wikilinks + stale) pages. 0h disables.
	SweepIntervalHours   int  `env:"SWEEP_INTERVAL_HOURS"        default:"24"`
	SweepStaleAfterDays  int  `env:"SWEEP_STALE_AFTER_DAYS"      default:"90"`
	SweepOrphanAfterDays int  `env:"SWEEP_ORPHAN_AFTER_DAYS"    default:"60"`
	SweepMaxOpenPerProj  int  `env:"SWEEP_MAX_OPEN_PER_PROJECT"  default:"200"`
	SweepLLMFilter       bool `env:"SWEEP_LLM_FILTER"            default:"true"`

	// ─── Wiki enrich worker (LLM-driven [[wikilink]]) ───────────
	// Polls pages where enriched_at < updated_at and asks the model
	// for a list of {term, target} substitutions; deterministic
	// applier inserts brackets. 0 disables. Default OFF — costs an
	// LLM call per page edit.
	EnrichIntervalSec int    `env:"ENRICH_INTERVAL_SEC"   default:"0"`
	EnrichBatch       int    `env:"ENRICH_BATCH"          default:"8"`
	EnrichModel       string `env:"ENRICH_LLM_MODEL"      default:"claude-haiku-4-5-20251001"`

	// ─── Wiki vision-caption worker ─────────────────────────────
	// Captions image refs `![](url)` whose alt is empty/placeholder
	// using a vision LLM. URL-keyed cache dedupes across pages.
	// 0 disables. Default OFF — vision calls are slow + expensive.
	VisionIntervalSec int    `env:"VISION_INTERVAL_SEC"  default:"0"`
	VisionBatch       int    `env:"VISION_BATCH"         default:"8"`
	VisionModel       string `env:"VISION_LLM_MODEL"     default:"claude-sonnet-4-6"`
	VisionMaxImageMB  int    `env:"VISION_MAX_IMAGE_MB"  default:"5"`

	// ─── Deep Research ──────────────────────────────────────────
	// On-demand pipeline: topic → web search → LLM synthesis →
	// new wiki page. Always available when SearxNG + model-relay are up.
	// Knob is the synthesis model — defaults to sonnet (research
	// quality matters more than cost here vs enrich/wikilinks).
	ResearchModel string `env:"RESEARCH_LLM_MODEL" default:"claude-sonnet-4-6"`

	// ─── Semantic Lint ────────────────────────────────────────────
	// On-demand LLM 语义审查（POST /v1/wiki/projects/{pid}/lint/semantic）：
	// 全项目页摘要一次调用，判 contradiction/stale/missing-page/suggestion，
	// 写 review_items kind=lint（payload.rule_family=semantic）。
	// sonnet —— 跨页矛盾判定需中等推理，haiku 精度不够。
	SemanticModel string `env:"SEMANTIC_LLM_MODEL" default:"claude-sonnet-4-6"`
	// SelectionModel powers inline selection edit/ask (S3 P1-6).
	SelectionModel string `env:"SELECTION_LLM_MODEL" default:"claude-sonnet-4-6"`

	// ─── Wiki ingest reaper ─────────────────────────────────────────
	// 回收卡死的 ingest 任务（publish 失败的 pending / worker 死亡的
	// running/partial）并重发给 wiki-llm；progress.requeue_count 超
	// MaxRequeue 标 failed 防毒丸。0s 禁用。
	IngestReaperIntervalSec int `env:"INGEST_REAPER_INTERVAL_SEC" default:"60"`
	IngestStalePendingSec   int `env:"INGEST_STALE_PENDING_SEC"   default:"120"`
	IngestStaleActiveSec    int `env:"INGEST_STALE_ACTIVE_SEC"    default:"600"`
	IngestStaleClientSec    int `env:"INGEST_STALE_CLIENT_SEC"    default:"600"`
	IngestMaxRequeue        int `env:"INGEST_MAX_REQUEUE"         default:"5"`

	// ─── Wiki 云端解析计费（client-docproc W4）──────────────────────
	// 云端 wiki-parse 完成后按页扣费，价格挂 model_relay.pricing 的
	// pseudo-model（经 relay /v1/internal/usage/charge 代理，brain 不直读
	// pricing 表）。空 = 禁用计费（解析照常，等同免费兜底）。
	ParseBillingModel string `env:"PARSE_BILLING_MODEL" default:""`
	// B1 OCR：parser=mineru 的解析走独立计费档位（wiki-ocr pseudo-model）。
	// 空 = OCR 免费兜底（解析照常不扣费），与 PARSE_BILLING_MODEL 同哲学。
	OCRBillingModel string `env:"OCR_BILLING_MODEL" default:""`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "brain: %v\n", err)
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

	// Schema 自动 migrate. brain.projects 是 00001 的探针表.
	// baselineMax=2: 若遇到"有表但无 brain_goose_db_version"的存量库,
	// 只把 00001-00002 mark applied, 00003 (notebook_hierarchy) 起的新
	// migration 仍会真正执行; 传 0 会把它们静默标成已应用 (dbmigrate
	// 包文档明确警告的场景).
	if cfg.MigrationsDir != "" {
		if err := dbmigrate.Run(ctx, pool, serviceName, cfg.MigrationsDir, "brain.projects", 2); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	st := store.New(pool)
	// §⑤ 迁移 00066 后回填 body_md='' 的页（BlocksToMarkdown 重算）。幂等，重复启动无副作用。
	if n, berr := st.BackfillBodyMd(ctx); berr != nil {
		logger.Warn("wiki: backfill body_md failed", "err", berr)
	} else if n > 0 {
		logger.Info("wiki: backfilled body_md", "pages", n)
	}
	// 2026-09-04 串味事故回填：剥离历史 body_md 开头误存的 YAML
	// frontmatter（入 jsonb 列 + 重投影 blocks）。幂等，剥离后 body_md
	// 不再以 --- 开头，重复启动无副作用。
	if n, berr := st.BackfillFrontmatter(ctx); berr != nil {
		logger.Warn("wiki: backfill frontmatter failed", "err", berr)
	} else if n > 0 {
		logger.Info("wiki: backfilled frontmatter", "pages", n)
	}
	sourcesStore := wikisources.New(pool)
	// Notes 域 store 提前建：note api（含 promote → wiki store）与
	// search 统一检索的 notes 一路（WithNotes）共用同一个实例。
	noteStore := notestore.New(pool)

	// Realtime path — UI fanout via SSE.
	var rtPub publisher.Publisher = publisher.NewRealtime(cfg.RealtimeURL, logger)
	if cfg.RealtimeURL == "" {
		rtPub = publisher.Noop{}
		logger.Warn("realtime URL empty; events will not fanout to UI")
	}

	// NATS path — cross-service async fanout. Empty NATS_URL ⇒ NoopBus.
	natsBus, err := bus.Connect(cfg.NatsURL, "brain", cfg.Environment)
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer natsBus.Close()

	// Agent-plane readiness reconciler —— 后台驱动
	// disconnected → connected → streams_ready 状态机（EnsureWork /
	// Control / SessionStream 幂等重试，带退避）。路由无条件挂载、惰性
	// 消费 readiness,根治「启动时刻 NATS 未就绪 → WS / worker poll 路由
	// 终身不挂载 → daemon 无限重注册 / 客户端无限转圈」的启动竞态（实测
	// 踩过，见 readiness.go 头注释）。queue 先以 nil JS 建好,reconciler
	// 就绪后经 SetJS 灌入句柄;消费方经 readiness.Queue()/JetStream()
	// 取当前句柄。
	agentPlaneQueue := agentplanepkg.NewQueue(nil)
	agentPlaneReadiness := agentplanepkg.NewReadiness(natsBus, cfg.NatsURL != "", agentPlaneQueue, logger)
	go agentPlaneReadiness.Run(ctx)
	if cfg.NatsURL != "" {
		// bus.Connect 用 RetryOnFailedConnect(true) —— NATS 尚未就绪时立即
		// 返回「未连接」的 bus（后台异步重连），不报错。这里给初始连接一个
		// 有界等待让正常路径快速就绪、日志好看；5s 后仍未就绪也不阻塞启动
		// —— reconciler 在后台继续，就绪后无需重启自愈，/readyz 报 pending。
		for i := 0; i < 50 && !agentPlaneReadiness.Ready(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if agentPlaneReadiness.Ready() {
			logger.Info("nats connected + agent-plane streams ready",
				"url", cfg.NatsURL, "env", cfg.Environment)
		} else {
			logger.Warn("nats/agent-plane streams not ready after 5s wait; "+
				"WS + worker-poll routes stay mounted (503 until ready) and self-heal in background; see /readyz",
				"url", cfg.NatsURL, "state", agentPlaneReadiness.State())
		}
	}
	busPub := publisher.NewBus(natsBus, cfg.Environment, logger)

	// JetStream — when wired, the publisher writes durably and the
	// graph subscriber (below) binds a durable consumer. EnsureStream
	// is idempotent so both publish + consume sides are safe.
	var jsHandle bus.JetStream
	if cfg.BusUseJetStream && natsBus.Connected() {
		js, err := natsBus.JetStream()
		if err != nil {
			logger.Warn("JetStream init failed; staying on core pub-sub", "err", err)
		} else {
			streamName := "BIUMIND_BRAIN"
			streamSubj := bus.Subject(cfg.Environment, "brain") + ".>"
			if err := js.EnsureStream(ctx, bus.StreamSpec{
				Name:     streamName,
				Subjects: []string{streamSubj},
				MaxAge:   7 * 24 * time.Hour,
			}); err != nil {
				logger.Warn("JetStream EnsureStream failed; staying on core pub-sub",
					"err", err, "stream", streamName)
			} else {
				busPub = busPub.WithJetStream(js)
				jsHandle = js
				logger.Info("JetStream stream ensured (brain)",
					"stream", streamName, "subjects", streamSubj)
			}
		}
	}

	// Tee fans events into both paths so existing Realtime UI behaviour
	// is preserved while NATS workers can subscribe independently.
	pub := publisher.NewTee(rtPub, busPub)

	// Listener bridges Postgres LISTEN/NOTIFY → Realtime + NATS.
	// Realtime fast-path: sub-second delivery while everything is healthy.
	listener := &events.Listener{
		Pool: pool, Channel: "brain_events", Publisher: pub, Logger: logger,
	}
	go listener.Run(ctx)

	// Outbox poller — durability floor for the brain.events table.
	// Catches rows the listener missed during restart / broker outage /
	// multi-replica fanout, marking each row published_at on success so
	// the listener and poller don't double-deliver in steady state.
	if cfg.OutboxPoller {
		poller := &events.Poller{
			Pool:      pool,
			Publisher: pub,
			Logger:    logger,
			Interval:  cfg.OutboxPollerInterval,
			Batch:     cfg.OutboxPollerBatch,
		}
		go func() {
			if err := poller.Run(ctx); err != nil &&
				!errors.Is(err, context.Canceled) {
				logger.Warn("events.Poller exited", "err", err)
			}
		}()
		logger.Info("events outbox poller: ON",
			"interval", cfg.OutboxPollerInterval,
			"batch", cfg.OutboxPollerBatch)
	}

	// Graph auto-extractor — opt-in, requires a real bus. Connectivity 判
	// 断消费 agent-plane readiness 的同一份快照（不另起 Connected() 逻辑）。
	if cfg.GraphAutoExtract && agentPlaneReadiness.NATSConnected() {
		gs := &graphsub.Subscriber{
			Bus: natsBus, Env: cfg.Environment,
			Store: graphstore.New(pool), Logger: logger,
		}
		if jsHandle != nil {
			gs.JS = jsHandle
		}
		if err := gs.Run(ctx); err != nil {
			return fmt.Errorf("graph subscriber: %w", err)
		}
		logger.Info("graph auto-extractor: ON", "jetstream", jsHandle != nil)
	} else if cfg.GraphAutoExtract {
		logger.Warn("GRAPH_AUTO_EXTRACT=true but NATS not connected — extractor disabled")
	}

	verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	apiSrv := api.NewServer(st, sourcesStore, verifier, logger)

	// Search subsystem
	searcher := bm25.New(pool)
	// S3 P1-6 phase B: wiki api Ask-mode KB retrieval (top5 same-project pages).
	apiSrv = apiSrv.WithBM25(searcher)
	var sxClient *searxng.Client
	if cfg.SearxNGURL != "" {
		sxClient = searxng.New(cfg.SearxNGURL)
	}
	searchSrv := searchapi.NewServer(searcher, sxClient, decay.New(float64(cfg.SearchHalfLifeDays)), verifier, logger)
	// 统一搜索 notes 一路（N3）：请求 include_notes=true 时才生效，
	// 隐私默认关。nil 不会传入 —— noteStore 总是非 nil。
	searchSrv = searchSrv.WithNotes(noteStore)

	// Wiki chunks store — backs the vector retrieval path. The store is
	// always wired (cheap, just a pool ref); the *worker* and the
	// search/api integration only activate when an embedder is built
	// below.
	chunksStore := wikichunks.New(pool)

	// Cloud-side tool registry. Always created so the chat agent loop
	// + /v1/tools/invoke proxy have a single canonical catalog. Tool
	// registrations follow as their dependencies become available
	// (websearch needs SearxNG, wiki.search needs embedder, etc.).
	// 设计文档: docs/BiuMind-Chat-Optimization-Design.md §4.6.
	toolReg := toolspkg.New()
	toolReg.MustRegister(toolsbuiltin.TimeNow())
	if sxClient != nil {
		toolReg.MustRegister(toolsbuiltin.WebSearch(sxClient))
	}

	// S3 P0-1 — wiki write tools (autonomous-maintenance agent loop).
	// Unconditional: they wrap the wiki store (st), not the embedder, so
	// they are available whether or not EMBED_PROVIDER is set. Only
	// advertised to the LLM under WikiAgentToolAllowlist; plain chat
	// (DefaultChatToolAllowlist) never sees them. Write safety rests on
	// version乐观锁 + page_revisions rollback, not this registration.
	// reviewsStore 提前构造（下面 §reviews 段复用同一实例）——
	// WikiMergePages 合并成功后要自动 resolve 对应 dedup review。
	reviewsStore := wikireviews.New(pool)
	toolReg.MustRegister(toolsbuiltin.WikiCreatePage(st))
	toolReg.MustRegister(toolsbuiltin.WikiUpdatePage(st))
	toolReg.MustRegister(toolsbuiltin.WikiMergePages(st, reviewsStore))

	// Graph subsystem
	graphSrv := graphapi.NewServer(graphstore.New(pool), verifier, logger)

	// Memory subsystem — multi-layer durable memory keyed by project.
	memStore := memorystore.New(pool)
	memorySrv := memoryapi.NewServer(memStore, st, verifier, logger)
	memoryMCP := memorymcp.New(memStore, st, verifier, logger).
		WithSearch(searcher, searchvector.New(chunksStore))

	// Optional embedder + backfill worker. Enables hybrid (semantic +
	// lexical) recall ranking AND the wiki vector retrieval path.
	// EMBED_PROVIDER unset → both fall back: memory recall is lexical
	// only, search drops to BM25 + web fusion.
	embedder, err := buildEmbedder(cfg)
	if err != nil {
		return fmt.Errorf("brain: build embedder: %w", err)
	}
	reranker, err := buildReranker(cfg)
	if err != nil {
		return fmt.Errorf("brain: build reranker: %w", err)
	}
	if embedder != nil {
		memorySrv = memorySrv.WithEmbedder(embedder)
		memoryMCP = memoryMCP.WithEmbedder(embedder)
		w := memworker.New(memStore, embedder, memworker.Config{
			Interval: time.Duration(cfg.EmbedWorkerEvery) * time.Second,
			Batch:    cfg.EmbedWorkerBatch,
			Logger:   logger,
		})
		go w.Run(ctx)
		logger.Info("memory embed worker enabled",
			"provider", cfg.EmbedProvider, "model", embedder.Model(),
			"dim", embedder.Dim())

		// Wiki vector path — third RRF retriever over brain.wiki_chunks.
		// Reuses the same embedder so query embeddings come from the
		// same provider that filled the index, ensuring the vectors
		// live in the same space.
		searchSrv = searchSrv.WithVector(searchvector.New(chunksStore), embedder)
		ww := wikiembed.New(pool, st, chunksStore, embedder, wikiembed.Config{
			Interval:     time.Duration(cfg.WikiEmbedEvery) * time.Second,
			RechunkBatch: cfg.WikiEmbedRechunkBatch,
			EmbedBatch:   cfg.WikiEmbedBatch,
			ChunkOpts: wikichunks.Options{
				TargetChars: cfg.WikiChunkTargetChars,
				MaxChars:    cfg.WikiChunkMaxChars,
				MinChars:    cfg.WikiChunkMinChars,
			},
			Logger: logger,
		})
		go ww.Run(ctx)
		logger.Info("wiki embed worker enabled",
			"provider", cfg.EmbedProvider,
			"target_chars", cfg.WikiChunkTargetChars,
			"max_chars", cfg.WikiChunkMaxChars)

		// Wiki RAG tool — same embedder as the indexer so query +
		// stored vectors share a space.
		toolReg.MustRegister(toolsbuiltin.WikiSearch(chunksStore, embedder))
		// Memory recall tool — hybrid (semantic + lexical + salience +
		// recency) ranking on brain.memories, project-scoped.
		toolReg.MustRegister(toolsbuiltin.MemoryRecall(memStore, embedder))
	} else {
		logger.Info("memory + wiki embed workers disabled (EMBED_PROVIDER unset);" +
			" wiki.search tool also unavailable")
		// memory.recall still works lexically without an embedder.
		toolReg.MustRegister(toolsbuiltin.MemoryRecall(memStore, nil))
	}
	logger.Info("chat tool registry seeded",
		"cloud_tools", toolReg.AvailableNames(toolspkg.ExecutionCloud))

	// Consolidator — dedupes semantic duplicates and decays salience
	// on idle memories. Independent of the embed worker; runs even
	// without an embedder (decay is content-agnostic).
	if cfg.ConsolidateIntervalHours > 0 {
		cons := memconsolidator.New(pool, memconsolidator.Config{
			Interval:        time.Duration(cfg.ConsolidateIntervalHours) * time.Hour,
			CosineThreshold: cfg.ConsolidateCosineMax,
			DecayPerDay:     cfg.SalienceDecayPerDay,
			IdleAfterDays:   cfg.SalienceIdleDays,
			Logger:          logger,
		})
		go cons.Run(ctx)
	} else {
		logger.Info("memory consolidator disabled (CONSOLIDATE_INTERVAL_HOURS=0)")
	}

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.AddProbe("postgres", bdb.HealthProbe(pool))
	hz.SetReady(true)
	metrics.SetService(serviceName)

	// Provider config (per-user provider metadata + model catalog). P3:
	// brain no longer stores user LLM keys — credentials live in identity
	// and are fetched on demand via identityBYOK (refresh upstream /models
	// + daemon agent/task BYOK). Built before chat sender + agentplane so
	// both can be plumbed with the identity client.
	providersStore := providerspkg.New(pool)
	providersSrv := providerspkg.NewServer(providersStore, verifier, logger)
	identityBYOK := providerspkg.NewIdentityBYOKClient(cfg.IdentityURL, cfg.IdentityInternalToken)
	providersSrv.IdentityBYOK = identityBYOK
	if cfg.IdentityURL == "" {
		logger.Warn("IDENTITY_URL unset — BYOK lookups disabled (model refresh skips, daemon agent/task BYOK falls back to platform pool)")
	}
	// Chat threads + messages (design doc: docs/Chat-Threads-Design.md)
	chatStore := chatpkg.New(pool)
	chatSrv := chatpkg.NewServer(chatStore, verifier, logger)
	// S3 P0-1: sender hoisted out of the if-block so wiki/api can reuse it
	// for the agent loop (apiSrv.WithRelay below). nil when MODEL_RELAY_URL
	// is unset — the wiki agent handler degrades to 503 in that case.
	var sender *chatpkg.HTTPSender
	// Default chat model resolver, shared by the agentplane chat runner
	// (S4-5) and MCP wiki.chat (P2 #22) — relay is the SoT for "BiuMind
	// 默认" (models.is_default_chat). nil when MODEL_RELAY_URL is unset;
	// both consumers treat a nil/empty resolution as "no default".
	var chatDefaultModels *agentplanepkg.DefaultModelResolver
	if cfg.RelayURL != "" {
		// Chat send via model-relay. BYOK no longer flows through Brain:
		// model-relay resolves the user's key itself by querying identity
		// (/v1/internal/byok/*) using the forwarded user JWT, so Brain
		// just posts /v1/messages with the bearer. See
		// docs/BiuMind-BYOK-Unification-Design.md P2.
		sender = chatpkg.NewHTTPSender(chatStore, cfg.RelayURL).
			WithTools(toolReg)
		chatSrv = chatSrv.WithSender(sender)
		// P2 #22: MCP wiki.chat drives the same sender in-process
		// (RunAgentLoopBuffered), so external AI clients can ask the
		// project knowledge base with identical auth/billing semantics
		// as the SSE agent run. Resolver warm is async, boot unaffected.
		chatDefaultModels = agentplanepkg.NewDefaultModelResolver(
			cfg.RelayURL, cfg.IdentityInternalToken, logger)
		go chatDefaultModels.Warm(ctx)
		memoryMCP = memoryMCP.WithAgent(sender, chatDefaultModels)
		logger.Info("chat send via model-relay enabled", "hub_url", cfg.RelayURL)
	} else {
		logger.Info("chat send disabled (MODEL_RELAY_URL unset);" +
			" clients use direct mode + PATCH for persistence")
	}
	// S3 P0-1 — wire the same model-relay sender into wiki/api so the
	// autonomous-maintenance agent loop (POST /v1/wiki/projects/{pid}/agent/run)
	// can reach model-relay. Shares the tool registry (sender.WithTools above),
	// which already holds the wiki write tools registered earlier.
	apiSrv = apiSrv.WithRelay(sender)
	// Cleanup orphan streaming messages from prior crashes — covered
	// at startup + every cleanup window.
	cleanupEvery := time.Duration(cfg.ChatOrphanCleanupMinutes) * time.Minute
	if cleanupEvery > 0 {
		runCleanup := func() {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			n, err := chatStore.CleanupOrphanStreaming(c, cleanupEvery)
			if err != nil {
				logger.Warn("chat orphan cleanup failed", "err", err)
				return
			}
			if n > 0 {
				logger.Info("chat orphans marked errored", "count", n)
			}
		}
		runCleanup() // boot scan
		go func() {
			t := time.NewTicker(cleanupEvery)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					runCleanup()
				}
			}
		}()
	}

	// Wiki ingest task service — durable LLM-driven ingest pipeline
	// (P1: brain.ingest_tasks + workers/wiki-llm). Publishes to the
	// `wiki.ingest.requested` topic via the same NATS publisher as
	// chat/code/wiki events so env prefixing (biumind.<env>.brain.…)
	// stays uniform.
	ingestStore := wikiingest.New(pool)
	ingestSrv := wikiingest.NewServer(ingestStore, st, busPub, verifier, logger)
	// Hand the same store + publisher to the MCP server so wiki.ingest
	// works for AI clients (Claude Desktop / Cursor / …).
	memoryMCP = memoryMCP.WithIngest(ingestStore, busPub)
	// Internal endpoint for the wiki-llm worker to reach back when the
	// task carries a source_id-only payload. Mount is a no-op when
	// BIUMIND_INTERNAL_TOKEN is empty; the worker falls back to inline
	// raw_text in that case (see workers/wiki-llm/wiki_llm/runner.py).
	ingestInternal := wikiingest.NewInternalServer(sourcesStore, cfg.InternalToken, logger)
	// P2 #17：两阶段 ingest 的上下文端点（purpose/schema + 页面索引）。
	ingestInternal.Wiki = st
	// W4：云端解析按页计费（经 model-relay 代理）。PARSE_BILLING_MODEL 为空
	// 则禁用（charger=nil，解析照常免费）。
	if cfg.ParseBillingModel != "" {
		ingestInternal.Charger = wikiingest.NewUsageCharger(
			cfg.RelayURL, cfg.ModelRelayInternalToken, cfg.ParseBillingModel, logger)
		if ingestInternal.Charger != nil {
			logger.Info("wiki parse billing enabled", "model", cfg.ParseBillingModel)
		}
	}
	// B1 OCR：mineru 档按 OCR_BILLING_MODEL 计费。为空则禁用
	// （OCRCharger=nil，OCR 解析免费兜底）。
	if cfg.OCRBillingModel != "" {
		ingestInternal.OCRCharger = wikiingest.NewUsageCharger(
			cfg.RelayURL, cfg.ModelRelayInternalToken, cfg.OCRBillingModel, logger)
		if ingestInternal.OCRCharger != nil {
			logger.Info("wiki OCR billing enabled", "model", cfg.OCRBillingModel)
		}
	}

	// Ingest reaper：回收 publish 失败的 pending 任务 + worker 死亡的
	// running/partial 任务并重发（api.go 注释里承诺的 "operator-level
	// reaper"）。0s 禁用。
	if cfg.IngestReaperIntervalSec > 0 {
		reaper := wikiingest.NewReaper(ingestStore, busPub, wikiingest.ReaperConfig{
			Interval:     time.Duration(cfg.IngestReaperIntervalSec) * time.Second,
			PendingStale: time.Duration(cfg.IngestStalePendingSec) * time.Second,
			ActiveStale:  time.Duration(cfg.IngestStaleActiveSec) * time.Second,
			ClientStale:  time.Duration(cfg.IngestStaleClientSec) * time.Second,
			MaxRequeue:   cfg.IngestMaxRequeue,
			Logger:       logger,
		}).WithSources(sourcesStore)
		go reaper.Run(ctx)
	}

	// Wiki ingest worker → brain update subscriber. Listens on
	// brain.wiki.ingest.update and applies running/page/done/failed/
	// cancelled deltas. Only wired when NATS is connected（同 readiness
	// 快照） —— without a bus the worker can't reach us anyway, and the
	// subscribe call would error.
	if agentPlaneReadiness.NATSConnected() {
		ingestSub := &wikiingest.Subscriber{
			Bus:    natsBus,
			Env:    cfg.Environment,
			Tasks:  ingestStore,
			Wiki:   st,
			Logger: logger,
		}
		if jsHandle != nil {
			ingestSub.JS = jsHandle
		}
		if err := ingestSub.Run(ctx); err != nil {
			return fmt.Errorf("wiki ingest subscriber: %w", err)
		}
	} else {
		logger.Info("wiki ingest subscriber: NATS not connected; subscriber off")
	}

	mux := http.NewServeMux()
	hz.Mount(mux)
	// /readyz —— agent-plane readiness 快照（nats / jetstream_streams /
	// queue / ingress），未全就绪 503。healthz 包也挂了无方法限定的
	// /readyz（非 GET 仍走它）；这里用 GET 限定 pattern 覆盖 GET ——
	// ServeMux 方法限定 pattern 更具体，注册不冲突。
	mux.HandleFunc("GET /readyz", agentPlaneReadiness.HandleReadyz)
	mux.Handle("/metrics", metrics.Handler())
	apiSrv.Mount(mux)
	// Notes 域（N0 骨架，docs/BiuMind-Notes-Design-Draft.md §6.1）：
	// 与 wiki 同库不同表、不同路由前缀（/v1/notes、/v1/notebooks、/v1/note-tags）。
	// WithWiki 注入 wiki store —— N3「转入知识库」promote 在同进程内直调建页。
	noteSrv := noteapi.NewServer(noteStore, verifier, logger).WithWiki(st)
	// 笔记分享（§7.6）：管理端随 Mount（requireAuth），公开端 /v1/shares/n/*
	// 走 MountPublic —— 无鉴权，brain 首批公开业务路由。访问 JWT 为 HS256
	// 服务端密钥；未配置时随机生成（重启/多实例即全失效，warn 提示）。
	if cfg.ShareSigningKey != "" {
		noteSrv.ShareSigningKey = []byte(cfg.ShareSigningKey)
	} else {
		shareKey := make([]byte, 32)
		if _, err := rand.Read(shareKey); err != nil {
			return fmt.Errorf("generate share signing key: %w", err)
		}
		noteSrv.ShareSigningKey = shareKey
		logger.Warn("BRAIN_SHARE_SIGNING_KEY unset — generated random share signing key; " +
			"share access tokens invalidate on restart and across replicas " +
			"(multi-instance deployments MUST set BRAIN_SHARE_SIGNING_KEY explicitly)")
	}
	noteSrv.Mount(mux)
	noteSrv.MountPublic(mux)

	// 笔记历史版本周期清理 —— N3 只交付了 store.PruneRevisions 函数，
	// 这里接周期：只删 change_type='edit' 且超出 keepRecent/keepDays
	// 双门槛的版本，restore 版本永留。启动先跑一轮，之后每天一次。
	runNotePrune := func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		n, err := noteStore.PruneRevisions(c,
			notestore.PruneDefaultKeepRecent, notestore.PruneDefaultKeepDays)
		if err != nil {
			logger.Warn("note revisions prune failed", "err", err)
			return
		}
		if n > 0 {
			logger.Info("note revisions pruned", "deleted", n)
		}
	}
	runNotePrune() // boot scan
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runNotePrune()
			}
		}
	}()
	logger.Info("notes: revision prune worker started", "interval", "24h",
		"keep_recent", notestore.PruneDefaultKeepRecent,
		"keep_days", notestore.PruneDefaultKeepDays)

	// Wiki 页历史版本周期清理 —— 照上面 runNotePrune 的既有模式
	// （store.PrunePageRevisions 交付时只给了函数）：boot scan + 每日 tick，
	// 只删 change_type='edit' 且超出 keepRecent/keepDays 双门槛的版本。
	runWikiPrune := func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		n, err := st.PrunePageRevisions(c,
			store.PruneDefaultKeepRecent, store.PruneDefaultKeepDays)
		if err != nil {
			logger.Warn("wiki page revisions prune failed", "err", err)
			return
		}
		if n > 0 {
			logger.Info("wiki page revisions pruned", "deleted", n)
		}
	}
	runWikiPrune() // boot scan
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWikiPrune()
			}
		}
	}()
	logger.Info("wiki: page revision prune worker started", "interval", "24h",
		"keep_recent", store.PruneDefaultKeepRecent,
		"keep_days", store.PruneDefaultKeepDays)

	// 分享会话去重记录 TTL 清理（S2，迁移 00006）—— 照上面
	// runNotePrune 的既有模式：boot scan + 每日 tick，单实例进程内
	// 周期任务（brain 无 task_states 这类跨实例 job 治理基建）。
	runShareViewSessionPrune := func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		n, err := noteStore.PruneShareViewSessions(c, notestore.ShareViewSessionsDefaultKeepDays)
		if err != nil {
			logger.Warn("note share view sessions prune failed", "err", err)
			return
		}
		if n > 0 {
			logger.Info("note share view sessions pruned", "deleted", n)
		}
	}
	runShareViewSessionPrune() // boot scan
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runShareViewSessionPrune()
			}
		}
	}()
	logger.Info("notes: share view sessions prune worker started",
		"interval", "24h", "keep_days", notestore.ShareViewSessionsDefaultKeepDays)
	searchSrv.Mount(mux)
	graphSrv.Mount(mux)
	memorySrv.Mount(mux)
	memoryMCP.Mount(mux)
	chatSrv.Mount(mux)
	providersSrv.Mount(mux)
	ingestSrv.Mount(mux)
	ingestInternal.Mount(mux)

	// ─── Wiki B0.5 stub modules ─────────────────────────────────
	// 给后续按批次（B2/B3/B4/B5/B6）迁前端代码用的路由骨架。每个模块
	// 当前是 stub：handler 校验 401/ownership 通过后返回空数据或 501。
	// 完整业务实现跟随每个 batch 激活。详见各 package 头注释。
	sourcesSrv := wikisources.NewServer(sourcesStore, st, busPub, verifier, logger)
	sourcesSrv.Mount(mux)
	wikiactivity.NewServer(pool, st, verifier, logger).Mount(mux)
	wikisynccp.NewServer(pool, verifier, logger).Mount(mux)
	wikisearchproj.NewServer(verifier, logger).Mount(mux)
	wikigraphSrv := wikigraphproj.NewServer(wikigraphproj.NewStore(pool), st, verifier, logger)
	wikigraphSrv.Mount(mux)
	wikillmsettings.NewServer(verifier, logger).Mount(mux)
	wikisuggestions.NewServer(wikisuggestions.New(pool), verifier, logger).Mount(mux)
	wikisyncws.NewServer(st, verifier, logger).Mount(mux)

	// Tool catalog + invocation proxy. W7's Flutter ToolHost calls
	// these from client-mode threads to reach cloud-only tools (wiki
	// search, memory recall) the same way the cloud agent loop does.
	toolsapi.New(toolReg, verifier, logger).Register(mux)

	// Wiki review queue (P2-D dedup is the first producer; lint /
	// sweep / merge / suggestion follow). The store + REST stay always
	// mounted so manually-injected reviews (e.g. via MCP) work even
	// when no detector worker is running. reviewsStore 在上方 wiki
	// write tools 注册处已构造（同一实例）。
	reviewsSrv := wikireviews.NewServer(reviewsStore, st, verifier, logger)
	reviewsSrv.Mount(mux)
	memoryMCP = memoryMCP.WithReviews(reviewsStore)

	// Phase 3: wiki-parse worker parse done 后经 internal_api 查项目内 source dedup。
	ingestInternal.Reviews = reviewsStore
	// client-docproc: 客户端本机解析随 source 提交 extracted_text 时同查 dedup。
	sourcesSrv.Reviews = reviewsStore

	// Deep Research — always-mounted endpoint. Orchestrator only
	// runs when SearxNG + model-relay LLM are both configured; without
	// that the handler returns 503 'research_disabled'.
	researchStore := wikiresearch.New(pool)
	var researchOrch *wikiresearch.Orchestrator
	if sxClient != nil && cfg.RelayURL != "" && cfg.JWTSecret != "" {
		signer := bauth.NewSigner(
			cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 5*time.Minute,
		)
		// Reuse the enrich model-relay LLM caller — same shape (system + user
		// → text). Different model is the only knob.
		caller := wikienrich.NewRelayLLMCaller(
			cfg.RelayURL, cfg.ResearchModel, signer, logger,
		)
		researchOrch = wikiresearch.NewOrchestrator(
			pool, researchStore, st, sxClient, caller,
			wikiresearch.Config{Logger: logger},
		).WithReviews(reviewsStore)
		logger.Info("deep research enabled",
			"model", cfg.ResearchModel,
			"searx", cfg.SearxNGURL)
	} else {
		logger.Info("deep research disabled",
			"searx_set", sxClient != nil,
			"hub_set", cfg.RelayURL != "",
			"jwt_set", cfg.JWTSecret != "")
	}
	researchSrv := wikiresearch.NewServer(
		researchStore, st, researchOrch, verifier, logger,
	)
	researchSrv.Mount(mux)

	// Boot recover: re-adopt research tasks orphaned by a previous crash
	// (still marked searching/synthesizing/saving across the restart).
	// Resume logic + savePage dup guard keep re-runs idempotent; the
	// orchestrator's sem bounds boot-time concurrency. One-shot on boot.
	if researchOrch != nil {
		go func() {
			if err := researchOrch.Recover(ctx); err != nil {
				logger.Warn("research boot recover", "err", err)
			}
		}()
	}

	// Page relatedness store + REST + MCP. Worker (below) populates
	// the table on a schedule; the surface is always-on so a manually
	// triggered scan or a backfill script's writes are immediately
	// queryable.
	relevanceStore := wikirelevance.New(pool)
	relevanceSrv := wikirelevance.NewServer(relevanceStore, st, verifier, logger)
	relevanceSrv.Mount(mux)
	memoryMCP = memoryMCP.WithRelevance(relevanceStore)
	// Plumb relevance into search/api so the BM25 → graph propagation
	// path lights up automatically (P2-C-2). Off when the worker
	// hasn't populated rows yet; the search degrades to bm25+vector+web.
	searchSrv = searchSrv.WithRelevance(relevanceStore)
	if reranker != nil {
		searchSrv = searchSrv.WithReranker(reranker)
		memoryMCP = memoryMCP.WithReranker(reranker)
		logger.Info("search reranker enabled",
			"provider", cfg.RerankProvider, "model", reranker.Model())
	}

	// Shared LLM precision filter across dedup / lint / sweep workers
	// (P2-D-LLM + P2-tail-3). One HubLLMFilter instance is safe for
	// concurrent use; we build it once when model-relay + jwt are configured
	// and inject into each worker that opted in via its env flag.
	// Per-rule filtering decisions live inside HubLLMFilter (it skips
	// deterministic rules like empty_page itself).
	var sharedLLMFilter wikireviews.LLMFilter
	if cfg.RelayURL != "" && cfg.JWTSecret != "" {
		signer := bauth.NewSigner(
			cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience,
			5*time.Minute,
		)
		sharedLLMFilter = wikireviews.NewHubLLMFilter(
			cfg.RelayURL, cfg.DedupLLMModel, signer, logger,
		)
		logger.Info("shared llm precision filter built",
			"model", cfg.DedupLLMModel)

		// Semantic lint runner 搬入 reviews 包（wiki/lint 收敛 B-10 后），
		// 经 reviewsSrv.SetSemantic 注入；/reviews/scan family=semantic 触发。
		// handleScan 检测 Semantic==nil 返 503 —— 所以仅在此分支注入。
		// 同一实例也注入 wiki apiSrv：agent run 成功后服务端自动触发扫描
		// （S3 P1，不再依赖客户端 deep 跑完后调 /reviews/scan）。runner 的
		// per-project inflight 防重入，两个入口并发安全。
		semanticCaller := wikireviews.NewRelaySemanticCaller(
			cfg.RelayURL, cfg.SemanticModel, signer, logger,
		)
		semanticRunner := wikireviews.NewSemanticRunner(
			pool, reviewsStore, semanticCaller, logger,
		)
		reviewsSrv.SetSemantic(semanticRunner)
		apiSrv = apiSrv.WithSemantic(semanticRunner)
		logger.Info("semantic lint enabled", "model", cfg.SemanticModel)

		// S3 P1-6 inline selection edit/ask — same RelayLLMCaller shape as
		// deep research (main.go:827). handleSelectionEdit returns 503 if nil.
		selectionCaller := wikienrich.NewRelayLLMCaller(
			cfg.RelayURL, cfg.SelectionModel, signer, logger,
		)
		apiSrv = apiSrv.WithSelection(selectionCaller)
		logger.Info("selection edit enabled", "model", cfg.SelectionModel)
	} else {
		logger.Info("llm precision filter unavailable (MODEL_RELAY_URL or JWT_SECRET unset); workers run rule-only; semantic lint disabled")
	}

	// Periodic dedup worker. Runs only when the interval is positive
	// and at least 5 minutes; 0 disables (the var clamp in NewWorker
	// also enforces the floor to keep ops from accidentally hot-looping
	// the cosine query against a giant project).
	if cfg.DedupIntervalHours > 0 {
		var filter wikireviews.LLMFilter
		if cfg.DedupLLMFilter {
			filter = sharedLLMFilter
		}

		dw := wikireviews.NewWorker(pool, reviewsStore, wikireviews.WorkerConfig{
			Interval: time.Duration(cfg.DedupIntervalHours) * time.Hour,
			DedupOpts: wikireviews.DedupOptions{
				MaxDistance:        cfg.DedupMaxDistance,
				MaxPairsPerProject: cfg.DedupMaxPairsPerProj,
			},
			MaxOpenPerProject: cfg.DedupMaxOpenPerProj,
			Filter:            filter,
			Logger:            logger,
		})
		go dw.Run(ctx)
		logger.Info("dedup worker enabled",
			"interval_h", cfg.DedupIntervalHours,
			"max_distance", cfg.DedupMaxDistance,
			"max_pairs_per_project", cfg.DedupMaxPairsPerProj)
	} else {
		logger.Info("dedup worker disabled (DEDUP_INTERVAL_HOURS=0)")
	}

	// Lint worker — structural lint findings → review_items kind=lint.
	// Always constructed so /reviews/scan family=structural can trigger
	// an on-demand re-scan even with LINT_INTERVAL_HOURS=0; the periodic
	// loop only starts when the interval is positive.
	var lintFilter wikireviews.LLMFilter
	if cfg.LintLLMFilter {
		lintFilter = sharedLLMFilter
	}
	lw := wikireviews.NewLintWorker(pool, reviewsStore, wikireviews.LintWorkerConfig{
		Interval:          time.Duration(cfg.LintIntervalHours) * time.Hour,
		MaxOpenPerProject: cfg.LintMaxOpenPerProj,
		Filter:            lintFilter,
		Logger:            logger,
	})
	reviewsSrv.SetLint(lw)
	if cfg.LintIntervalHours > 0 {
		go lw.Run(ctx)
		logger.Info("lint worker enabled",
			"interval_h", cfg.LintIntervalHours,
			"max_open_per_project", cfg.LintMaxOpenPerProj,
			"llm_filter", lintFilter != nil)
	} else {
		logger.Info("lint worker disabled (LINT_INTERVAL_HOURS=0); /reviews/scan still active")
	}

	// Periodic sweep worker — stale + orphan detection. Daily by
	// default; piggy-backs on the same review_items queue + MCP
	// surface as dedup and lint.
	if cfg.SweepIntervalHours > 0 {
		var sweepFilter wikireviews.LLMFilter
		if cfg.SweepLLMFilter {
			sweepFilter = sharedLLMFilter
		}
		sw := wikireviews.NewSweepWorker(pool, reviewsStore, wikireviews.SweepWorkerConfig{
			Interval:          time.Duration(cfg.SweepIntervalHours) * time.Hour,
			StaleAfterDays:    cfg.SweepStaleAfterDays,
			OrphanAfterDays:   cfg.SweepOrphanAfterDays,
			MaxOpenPerProject: cfg.SweepMaxOpenPerProj,
			Filter:            sweepFilter,
			Logger:            logger,
		})
		go sw.Run(ctx)
		logger.Info("sweep worker enabled",
			"interval_h", cfg.SweepIntervalHours,
			"stale_after_days", cfg.SweepStaleAfterDays,
			"orphan_after_days", cfg.SweepOrphanAfterDays,
			"llm_filter", sweepFilter != nil)
	} else {
		logger.Info("sweep worker disabled (SWEEP_INTERVAL_HOURS=0)")
	}

	// Relevance worker — page-pair scoring。无论周期 tick 是否启用都创建：
	// 手动「重建关系」（POST /graph/recompute）依赖它做按需单项目重算。
	rw := wikirelevance.NewWorker(pool, relevanceStore, wikirelevance.WorkerConfig{
		Interval: time.Duration(cfg.RelevanceIntervalHours) * time.Hour,
		ScoreOpts: wikirelevance.ScoreOptions{
			MinScore:            float32(cfg.RelevanceMinScore),
			MaxNeighborsPerPage: cfg.RelevanceMaxNeighbours,
		},
		Logger: logger,
	})
	wikigraphSrv.WithRecompute(rw.RecomputeProject)
	if cfg.RelevanceIntervalHours > 0 {
		go rw.Run(ctx)
		logger.Info("relevance worker enabled",
			"interval_h", cfg.RelevanceIntervalHours,
			"min_score", cfg.RelevanceMinScore,
			"max_neighbours_per_page", cfg.RelevanceMaxNeighbours)
	} else {
		logger.Info("relevance worker periodic tick disabled (RELEVANCE_INTERVAL_HOURS=0); manual recompute still available")
	}

	// Periodic enrich worker — LLM-driven [[wikilink]] insertion. Runs
	// only when both the interval is positive AND a model-relay-backed LLM
	// caller is configured (MODEL_RELAY_URL + JWT_SECRET). Disabled by default
	// because it costs one LLM call per page edit.
	if cfg.EnrichIntervalSec > 0 && cfg.RelayURL != "" && cfg.JWTSecret != "" {
		signer := bauth.NewSigner(
			cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 5*time.Minute,
		)
		caller := wikienrich.NewRelayLLMCaller(
			cfg.RelayURL, cfg.EnrichModel, signer, logger,
		)
		ew := wikienrich.New(pool, st, caller, wikienrich.Config{
			Interval:  time.Duration(cfg.EnrichIntervalSec) * time.Second,
			BatchSize: cfg.EnrichBatch,
			Logger:    logger,
		})
		go ew.Run(ctx)
		logger.Info("enrich worker enabled",
			"interval_s", cfg.EnrichIntervalSec,
			"batch", cfg.EnrichBatch,
			"model", cfg.EnrichModel)
	} else {
		logger.Info("enrich worker disabled",
			"interval_s", cfg.EnrichIntervalSec,
			"hub_set", cfg.RelayURL != "",
			"jwt_set", cfg.JWTSecret != "")
	}

	// Periodic vision-caption worker — same gating shape as enrich.
	// Costs more per call than text — keep batch small.
	if cfg.VisionIntervalSec > 0 && cfg.RelayURL != "" && cfg.JWTSecret != "" {
		signer := bauth.NewSigner(
			cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 5*time.Minute,
		)
		caller := wikivision.NewRelayVisionCaller(
			cfg.RelayURL, cfg.VisionModel, signer, logger,
		)
		vw := wikivision.New(pool, st, caller, wikivision.Config{
			Interval:   time.Duration(cfg.VisionIntervalSec) * time.Second,
			BatchSize:  cfg.VisionBatch,
			MaxImageMB: cfg.VisionMaxImageMB,
			Logger:     logger,
		})
		go vw.Run(ctx)
		logger.Info("vision caption worker enabled",
			"interval_s", cfg.VisionIntervalSec,
			"batch", cfg.VisionBatch,
			"model", cfg.VisionModel,
			"max_image_mb", cfg.VisionMaxImageMB)
	} else {
		logger.Info("vision caption worker disabled",
			"interval_s", cfg.VisionIntervalSec)
	}

	// 编码任务 100% 本地(D4 / Code-I4)—— brain 不再持有编码任务表 / endpoint;
	// 旧 codeSync 已废弃移除(Code-I6)。远控将走 Runtime v3 agent-plane(M6)。

	// Agent Plane environments CRUD (S3-2) + session 路由 (S3-6) +
	// token refresh (S3-9) + WS ingress (S3-5)。
	// 设计: docs/BiuMind-Agent-Plane-Design.md
	agentPlaneStore := agentplanepkg.NewStore(pool)
	agentPlaneSigner := bauth.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, agentplanepkg.SessionTokenTTL)
	// session_token 是 brain 自签的 HS256，需要专门的 HS256 verifier 来对
	// 上 Signer 的密钥；不能复用全局 `verifier`（IDENTITY_JWKS_URL 配上后
	// 它走 RS256/JWKS，对 HS256 直接 "no shared secret configured" 401）。
	agentPlaneSessionVerifier := bauth.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	// queue / ingress 都**无条件**建：queue 在 NATS 段已建好（nil JS），
	// readiness reconciler 就绪后经 SetJS 灌入句柄；ingress 每次请求经
	// jsFn 取当前句柄，nil → 503 no_jetstream。NATS_URL 为空时句柄永远
	// nil → 恒定 503（路由恒挂，语义正确，不再出现 404 无限转圈）。
	// EnsureWork/Control/SessionStream 已全部下沉到 reconciler（幂等重试）。
	agentPlaneIngress := agentplanepkg.NewIngress(nil, agentPlaneStore, agentPlaneSessionVerifier, logger)
	agentPlaneIngress.SetJSFunc(agentPlaneReadiness.JetStream)
	// Wire control queue + chat-mode interrupt 回调,让 cancel 帧能反向投
	// 到 daemon 或进程内 chat session。
	agentPlaneIngress.SetQueue(agentPlaneQueue)
	agentPlaneSrv := agentplanepkg.NewServer(agentPlaneStore, verifier,
		agentPlaneSigner, agentPlaneQueue, agentPlaneIngress, logger)
	agentPlaneSrv.Readiness = agentPlaneReadiness
	// §8.2 翻案:brain 作为对话真相源 —— WS chat / agent 路径把对话轮落库到
	// chat.messages 并服务端组装多轮历史。chatStore 复用上面 v1 chat 同一实例
	// (同一 pool)。TranscriptRecorder 挂到 Queue observer,统一累积 assistant
	// 轮(chat+agent 帧都过 PublishSessionFrame)。queue 恒非 nil,observer
	// 在 publish 成功后才触发,JS 未就绪时无副作用。
	agentPlaneSrv.ChatStore = chatStore
	agentPlaneTranscript := agentplanepkg.NewTranscriptRecorder(chatStore, logger)
	agentPlaneSrv.Transcript = agentPlaneTranscript
	agentPlaneQueue.SetObserver(agentPlaneTranscript)

	// S4-5/6/7: chat-mode in-process runner —— 经 model-relay PassThrough
	// 打 LLM + 推 SDK Protocol 帧到 .out subject。NATS_URL 为空时 runner
	// 不挂（无 NATS 无法推帧，保持旧 dev 行为）；配上 NATS 时 queue 的
	// JS 由 reconciler 后补，readiness 就绪前创建的 chat session 推帧会
	// 失败 — 跟 broker 短暂抖动同语义。生产需配 MODEL_RELAY_URL,
	// 否则 chat session 创建后立即 finalize failed (missing_bearer)。
	if cfg.NatsURL != "" {
		// chat.AgentLoop 是 chat 模式的真正内核 —— RunSingleTurn 跑
		// biumindkit + tool catalog。HTTPSender 传 nil —— 那是 legacy
		// /v1/threads/:id/send SSE 路径的依赖,WS 路径不用。
		chatLoop := chatpkg.NewAgentLoop(nil, toolReg)
		// Q1: chat-mode tool whitelist (default-deny). See tools/chatmode.go.
		chatLoop.ChatToolAllowlist = toolspkg.DefaultChatToolAllowlist
		// P2 #19（agent-42 遗留）：WS chat 路径（RunV2/biumindkit 内核）接
		// 检索预算。WS 侧无 mode 概念，取 standard 档 4 次 —— 对齐 wiki agent
		// run 的 wikiAgentRetrievalBudget 默认档（standard=4，未知 mode 也
		// 落 standard）。v1 SSE 普通 chat 不接线，保持 0=关 的老行为。
		chatLoop.RetrievalBudget = 4
		// 默认模型真相源在 relay (models.is_default_chat) —— resolver 已在
		// sender 构建处创建并异步预热（RelayURL 为空时为 nil, defaultChatModel
		// 自然落到 env 覆盖 > 内置兜底链）；relay 不可达时按负缓存退避逐
		// turn 重试。
		chatRunner := agentplanepkg.NewChatRunner(
			agentPlaneQueue, agentPlaneStore, chatLoop,
			cfg.AgentPlaneDefaultChatModel,
			cfg.RelayURL, // PassThrough 目标 — 跟 chat send 同源
			chatDefaultModels,
			logger,
		)
		agentPlaneSrv.ChatRunner = chatRunner
		// 同款 BYOK pre-resolve 让 createAgentSession / createTaskSession
		// enqueue 前把用户 provider key 塞 WorkPayload(daemon 拿到再
		// 当 X-Biumind-LLM-Key 透传给 model-relay)。P3: key 现从 identity 现取
		agentPlaneSrv.KeyResolver = identityBYOK
		// 让 ingress 在收到 control_cancel_request 时优先尝试进程内 chat
		// 打断;命中即结束,不再走 environment control 队列。
		agentPlaneIngress.SetChatInterrupt(chatRunner.InterruptSession)
		// 凭证路径(chat 去 env 化):一律 model-relay PassThrough ——
		// 透传 user JWT,relay 做 channel 路由 + 用户级配额 + BYOK 统一。
		if cfg.RelayURL != "" {
			logger.Info("agentplane chat runner: PassThrough enabled",
				"relay_url", cfg.RelayURL,
				"cloud_tools", toolReg.AvailableNames(toolspkg.ExecutionCloud))
		} else {
			logger.Warn("agentplane chat runner: MODEL_RELAY_URL unset; " +
				"chat sessions will finalize failed (missing_bearer)")
		}
	}
	agentPlaneSrv.Mount(mux)

	// Agent Plane janitor (S3-7) —— 把 last_seen_at 过老的 online environment
	// 标 offline。Run 阻塞，放 goroutine；顶层 ctx 取消时自动停。
	// R8：注入 agentPlaneQueue —— janitor 把孤儿/过期 session 标 failed 时向
	// biu.session.<id>.out 推 SDKResultError 帧,客户端 spinner 才会停（queue
	// 可空,无 NATS 的 dev 环境退化为只改 DB 状态）。
	go agentplanepkg.NewJanitor(pool, logger, agentPlaneQueue).Run(ctx)

	// Files (artifacts L3) — 仅当 MINIO_ENDPOINT 配置时挂。生产部署给
	// MinIO/S3 endpoint, 开发缺这个就不开 files endpoint, 客户端 L3 上传
	// 自然 404, L1+L2 路径不受影响。
	if cfg.MinioEndpoint != "" {
		blob, berr := filespkg.NewBlob(ctx, filespkg.BlobConfig{
			Endpoint:     cfg.MinioEndpoint,
			AccessKey:    cfg.MinioAccessKey,
			SecretKey:    cfg.MinioSecretKey,
			UseSSL:       cfg.MinioUseSSL,
			Bucket:       cfg.MinioBucket,
			EnsureBucket: true,
		})
		if berr != nil {
			logger.Error("files: blob init failed, files endpoints disabled",
				"err", berr, "endpoint", cfg.MinioEndpoint)
		} else {
			filesSrv := &filespkg.Server{
				Store:          filespkg.NewStore(pool),
				Blob:           blob,
				Verifier:       verifier,
				Logger:         logger,
				MaxUploadBytes: cfg.FilesMaxUploadBytes,
			}
			filesSrv.Mount(mux)
			// 笔记分享附件代理复用同一 Blob（/v1/shares/n/{token}/files/{file_id}
			// 302 → presign）；noteSrv 早已挂载，这里回填字段即可——
			// handler 按请求时读字段，此时 http server 尚未起监听，无竞态。
			noteSrv.ShareBlob = blob
			logger.Info("files: mounted /v1/files/* (MinIO blob backend)",
				"bucket", cfg.MinioBucket, "max_upload", cfg.FilesMaxUploadBytes)

			// Pending-row cleanup worker — sweeps abandoned uploads
			// (presign-upload that never finalized) every 5 min,
			// removes rows + MinIO objects older than 1h.
			cleaner := filespkg.NewCleanupWorker(pool, blob, filespkg.CleanupConfig{
				Interval:   5 * time.Minute,
				PendingTTL: time.Hour,
				Logger:     logger,
			})
			go cleaner.Run(ctx)
			logger.Info("files: cleanup worker started",
				"interval", "5m", "pending_ttl", "1h")

			// Phase 3: wiki-parse worker 经 internal_api 拉 blob presigned URL 下载。
			ingestInternal.Blob = blob
		}
	} else {
		logger.Info("files: MINIO_ENDPOINT unset, /v1/files/* not mounted")
	}

	// Wrap mux with CORS so the Flutter Web app at app.biumind.com can
	// call /v1/* directly without a proxy.
	extraOrigins := []string{}
	for _, o := range strings.Split(cfg.CorsExtraOrigins, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			extraOrigins = append(extraOrigins, o)
		}
	}
	corsCfg := bcors.Default(extraOrigins...)
	logger.Info("cors enabled",
		"allowed_origins", corsCfg.AllowedOrigins)
	// metrics middleware 内部, CORS 外部
	metrics.SetServiceInfo(serviceVersion)
	handler := corsCfg.Wrap(metrics.HTTPMiddleware(mux))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 必须 > agentplane.pollWaitMax (=30s) 否则
		// long-poll 在 wait 上限处会跟 WriteTimeout 同时到期，给
		// daemon 端表现成 EOF / connection reset by peer。多给 30s
		// 余量覆盖 handler 序列化 + 网络写入。
		WriteTimeout: 60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		logger.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	logger.Info("brain listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// buildEmbedder constructs the configured embedder. Returns nil
// (no error) when EMBED_PROVIDER is empty so callers can treat the
// worker as opt-in.
func buildEmbedder(cfg Config) (embed.Embedder, error) {
	switch cfg.EmbedProvider {
	case "":
		return nil, nil
	case "stub":
		return embed.NewStub(cfg.EmbedDims), nil
	case "openai":
		base := cfg.EmbedBaseURL
		key := cfg.EmbedAPIKey
		// I6: default egress through model-relay's internal embeddings
		// endpoint so background indexing never bypasses the central
		// LLM gateway or egresses straight to api.openai.com. The relay
		// resolves the platform embedding pool (bge-m3); brain
		// authenticates with the shared internal token, not a user JWT
		// and not an OpenAI API key. Explicit EMBED_BASE_URL still wins
		// (operator override).
		if base == "" && cfg.RelayURL != "" {
			base = strings.TrimRight(cfg.RelayURL, "/") + "/v1/internal"
		}
		if key == "" {
			key = cfg.ModelRelayInternalToken
		}
		return embed.NewOpenAI(embed.OpenAIConfig{
			BaseURL: base,
			APIKey:  key,
			Model:   cfg.EmbedModel,
			Dims:    cfg.EmbedDims,
		})
	default:
		return nil, fmt.Errorf("unknown EMBED_PROVIDER %q (use stub | openai | empty)",
			cfg.EmbedProvider)
	}
}

// buildReranker constructs the configured cross-encoder reranker.
// Returns nil (no error) when RERANK_PROVIDER is empty so callers can
// treat rerank as opt-in.
func buildReranker(cfg Config) (rerank.Reranker, error) {
	switch cfg.RerankProvider {
	case "":
		return nil, nil
	case "stub":
		return rerank.NewStub(), nil
	case "cohere":
		base := cfg.RerankBaseURL
		key := cfg.RerankAPIKey
		// I6: default egress through model-relay's internal rerank
		// endpoint (mirrors /v1/internal/embeddings) so search never
		// bypasses the central LLM gateway. brain authenticates with
		// the shared internal token, not a user JWT; Billing is nil on
		// the relay side so this is platform cost (same as background
		// embedding). Explicit RERANK_BASE_URL wins.
		if base == "" && cfg.RelayURL != "" {
			base = strings.TrimRight(cfg.RelayURL, "/") + "/v1/internal"
		}
		if key == "" {
			key = cfg.ModelRelayInternalToken
		}
		return rerank.NewCohere(rerank.CohereConfig{
			BaseURL: base,
			APIKey:  key,
			Model:   cfg.RerankModel,
		})
	default:
		return nil, fmt.Errorf("unknown RERANK_PROVIDER %q (use stub | cohere | empty)",
			cfg.RerankProvider)
	}
}
