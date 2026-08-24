// App Center service entry point.
//
// Hosts the BiuApp registry and dispatches /v1/apps/* requests to the
// installed apps. The 3 reference apps (rss / translate / tasks) are
// always registered; future apps land via additional Register() calls
// here or via an external registration RPC.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/email"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/tasks"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/webclip"
	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	bdb "github.com/biumind/biumind/packages/go-sdk/biu/db"
	"github.com/biumind/biumind/packages/go-sdk/biu/dbmigrate"
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	bmetrics "github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/services/app_center/internal/api"
	acauthz "github.com/biumind/biumind/services/app_center/internal/authz"
	acevents "github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/biumind/biumind/services/app_center/internal/installs"
	"github.com/biumind/biumind/services/app_center/internal/outbox"
	"github.com/biumind/biumind/services/app_center/internal/radar"
	"github.com/biumind/biumind/services/app_center/internal/radar/actions"
	"github.com/biumind/biumind/services/app_center/internal/rankings"
	"github.com/biumind/biumind/services/app_center/internal/repoanalyze"
	rssbriefing "github.com/biumind/biumind/services/app_center/internal/rss/briefing"
	rsscopilot "github.com/biumind/biumind/services/app_center/internal/rss/copilot"
	"github.com/biumind/biumind/services/app_center/internal/rss/digest"
	rssembed "github.com/biumind/biumind/services/app_center/internal/rss/embed"
	rssinterest "github.com/biumind/biumind/services/app_center/internal/rss/interest"
	rsstoday "github.com/biumind/biumind/services/app_center/internal/rss/today"
	rsstranscribe "github.com/biumind/biumind/services/app_center/internal/rss/transcribe"
	rssweekly "github.com/biumind/biumind/services/app_center/internal/rss/weekly"
	rsswiki "github.com/biumind/biumind/services/app_center/internal/rss/wiki"
	"github.com/biumind/biumind/services/app_center/internal/triggers"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	serviceName    = "app_center"
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
	IdentityJWKSURL string `env:"IDENTITY_JWKS_URL" default:""`

	// TasksStorePath — when set, tasks app persists to this file
	// (atomic write-and-rename). Empty = in-memory only.
	TasksStorePath string `env:"TASKS_STORE_PATH" default:""`

	// DatabaseURL — Postgres connection URL. v1.5+ install / catalogue
	// state lives here; the catalogue HTTP endpoints (List / Get /
	// Invoke) work without a DB so this stays optional in dev / CI.
	// When empty, the service skips pool init and migrations and runs
	// in stateless v1.0 mode (the existing /v1/apps/{name}/invoke
	// path goes through the in-memory Registry only).
	DatabaseURL string `env:"DATABASE_URL" default:""`

	// MigrationsDir — goose migrations directory. Auto-runs on startup
	// when DatabaseURL is also set. Container default points at the
	// path the deploy compose mounts services/app_center/migrations
	// into; absolute repo path works for local dev.
	MigrationsDir string `env:"BIUMIND_MIGRATIONS_DIR" default:"/etc/biumind/migrations/app_center"`

	// RealtimeURL — services/realtime base URL the outbox poller posts
	// to (e.g. http://realtime:7005). Empty → poller still runs but
	// publishes go to the noop sink (events row gets stamped, no
	// downstream fanout). Production ALWAYS sets this.
	RealtimeURL string `env:"REALTIME_URL" default:""`

	// NewsNowBaseURL — origin of the newsnow API used by the rankings
	// scheduler (P1). Empty falls back to the public default in the
	// rankings.NewClient constructor.
	NewsNowBaseURL string `env:"NEWSNOW_BASE_URL" default:"https://newsnow.example.com"`

	// ModelRelayURL — base URL of the model-relay service. P3 radar
	// (LLM nl→rule) uses this; empty disables the rules_from_nl
	// action gracefully.
	ModelRelayURL string `env:"MODEL_RELAY_URL" default:""`

	// AuthzURL — base URL of the central Authz service. M11.2 org-scope
	// reads/writes consult it (rss:org_read / rss:org_write). Empty ⇒
	// AlwaysAllow stub + startup WARN (dev only; org writes ungated).
	AuthzURL string `env:"AUTHZ_URL" default:""`

	// BrainURL — base URL of the brain service. M3 wiki sink uses
	// this to create wiki pages on the user's behalf. Empty disables
	// entries_to_wiki gracefully.
	BrainURL string `env:"BRAIN_URL" default:""`

	// RuntimeURL — runtime service base URL, used by M9 skill action
	// to invoke skills via /v1/tools/{id}/invoke. Empty disables skill
	// action gracefully (regs runner without skill type).
	RuntimeURL string `env:"RUNTIME_URL" default:""`

	// SelfBaseURL — app-center 自身 URL, M9 task action 通过它 loop-back
	// 调 tasks app /v1/apps/tasks/invoke. 默认填 "http://localhost:7011"
	// 兼容默认 listen 端口.
	SelfBaseURL string `env:"APP_CENTER_BASE_URL" default:"http://localhost:7011"`

	// RSSDigestModel — model code in model-relay catalog. Empty falls
	// back to digest.defaultModel ("glm-5.1") which is what dev compose
	// has active. Set in prod once Anthropic Haiku is provisioned.
	RSSDigestModel string `env:"RSS_DIGEST_MODEL" default:""`

	// RSSEmbedModel — model code for /v1/embeddings. Empty defaults to
	// "bge-m3" (1024d). Schema 00014 locks dim to 1024; switching to
	// a different-dim model requires a follow-up migration.
	RSSEmbedModel string `env:"RSS_EMBED_MODEL" default:""`

	// RSSTranscribeModel — model code for /v1/audio/transcriptions (M13.5
	// podcast). Empty defaults to "paraformer-v2" (dashscope async ASR).
	// Must be an audio_transcription-mode model in the model-relay catalog;
	// if unprovisioned, transcription degrades to a per-entry ai_error.
	RSSTranscribeModel string `env:"RSS_TRANSCRIBE_MODEL" default:""`

	// GitHubToken — platform read-only token for the repo-app analyzer
	// (M1.4). Empty falls back to anonymous GitHub API access (60 req/h
	// per IP); a startup WARN flags the degraded rate limit.
	GitHubToken string `env:"GITHUB_TOKEN" default:""`

	// RepoPollInterval — base cadence of the repo-app release poller
	// (M2.1). Per-row backoff derives from it; tests / dev set small
	// values (e.g. 30s) to watch polls live.
	RepoPollInterval time.Duration `env:"REPO_POLL_INTERVAL" default:"6h"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "app_center: %v\n", err)
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

	// DB pool is optional in v1.5: the v1.0 invoke surface works
	// without it; install / catalogue persistence requires it.
	var pool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		p, err := bdb.New(ctx, bdb.Defaults(cfg.DatabaseURL))
		if err != nil {
			return fmt.Errorf("db: %w", err)
		}
		defer p.Close()
		pool = p

		// Schema auto-migrate. app_center.apps is the 00001 probe table.
		// baselineMaxVersion=0 — the schema lands fresh in v1.5; there
		// is no pre-existing app_center DB to baseline against.
		if cfg.MigrationsDir != "" {
			if err := dbmigrate.Run(ctx, pool, serviceName, cfg.MigrationsDir, "app_center.apps", 0); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
		}
		logger.Info("postgres connected", "migrations_dir", cfg.MigrationsDir)
	} else {
		logger.Warn("DATABASE_URL unset; running stateless (v1.0 invoke path only — no installs / catalogue persistence)")
	}

	// Events SDK bridge — when the DB is wired, in-process Apps can
	// PublishViewDataChanged via Deps.Events. The bridge keeps an
	// installID → identifier resolver so payloads can be denormalised
	// at write time. Without DB we install a noop publisher.
	var sdkEvents biuapp.EventPublisher = biuapp.NoopEventPublisher{}
	if pool != nil {
		bridge := &acevents.SDKBridge{
			Pub: acevents.NewPgxPublisher(pool),
			IdentifierFor: func(installID string) string {
				var ident string
				_ = pool.QueryRow(ctx,
					`SELECT identifier FROM app_center.installations WHERE id = $1`,
					installID,
				).Scan(&ident)
				return ident
			},
		}
		sdkEvents = bridge
	}
	reg := biuapp.NewRegistry(biuapp.Deps{
		Logger: biuapp.DiscardLogger{},
		Events: sdkEvents,
	})

	// Reference apps that don't need external secrets register
	// unconditionally. Translate is omitted here because it needs an
	// llm.Provider; wire it in from a parent service that holds the
	// model-relay/virtual-key relationship (see app_center docs).
	var rssApp biuapp.App
	var rankingsStore *rankings.Store
	var radarStore *radar.Store
	var rssAppRef *rss.App
	if pool != nil {
		rssAppRef = rss.NewWithPool(pool)
		rankingsStore = rankings.NewStore(pool)
		radarStore = radar.NewStore(pool)
		rssAppRef.WithBoards(&rankings.SDKAdapter{Store: rankingsStore})
		rssAppRef.WithRadar(&radar.SDKAdapter{Store: radarStore})

		// M2: Today picker (in-process, 30min cache).
		todayPicker := rsstoday.New(pool)
		rssAppRef.WithTodayPicker(&rsstoday.SDKAdapter{Picker: todayPicker})

		// M3: Wiki sink (calls brain on caller's behalf).
		if cfg.BrainURL != "" {
			rssAppRef.WithWikiSink(&rsswiki.SDKAdapter{
				Client: rsswiki.NewClient(cfg.BrainURL),
			})
			logger.Info("rss app: wiki sink wired", "brain", cfg.BrainURL)
		}

		if cfg.ModelRelayURL != "" {
			rssAppRef.WithLLM(&radar.LLMSDKAdapter{
				Client: radar.NewLLMClient(cfg.ModelRelayURL),
			})
			logger.Info("rss app: LLM advisor wired", "model_relay", cfg.ModelRelayURL)
		}

		// M11.2: org-scope authorizer. Real Authz when AUTHZ_URL is set;
		// AlwaysAllow stub + WARN otherwise (org writes ungated in dev).
		var orgDecider acauthz.Decider
		if cfg.AuthzURL != "" {
			orgDecider = acauthz.NewHTTP(cfg.AuthzURL)
			logger.Info("rss app: org-scope authz wired", "authz", cfg.AuthzURL)
		} else {
			orgDecider = acauthz.AlwaysAllow{}
			logger.Warn("rss app: AUTHZ_URL unset — org-scope writes UNGATED (dev only)")
		}
		rssAppRef.WithAuthz(acauthz.RSSOrgChecker{D: orgDecider})

		// M11.3: base URL for public share links.
		rssAppRef.WithShareBaseURL(cfg.SelfBaseURL)

		rssApp = rssAppRef
		logger.Info("rss app: pg-backed (rss.* + rankings.* + radar schema)")

		// M2: per-day interest centroid recompute (background goroutine,
		// 24h tick + immediate first run on boot).
		go rssinterest.New(pool).Run(ctx)
	} else {
		rssApp = rss.New()
		logger.Info("rss app: in-memory only (set DATABASE_URL for persistence)")
	}
	apps := []biuapp.App{rssApp, email.New(), webclip.New()}
	if cfg.TasksStorePath != "" {
		t, err := tasks.NewWithFile(cfg.TasksStorePath)
		if err != nil {
			return fmt.Errorf("tasks load: %w", err)
		}
		apps = append(apps, t)
		logger.Info("tasks app: file-backed", "path", cfg.TasksStorePath)
	} else {
		apps = append(apps, tasks.New())
		logger.Info("tasks app: in-memory only (set TASKS_STORE_PATH for persistence)")
	}
	for _, app := range apps {
		if err := reg.Register(ctx, app); err != nil {
			return fmt.Errorf("register %s: %w", app.Manifest().Name, err)
		}
	}
	logger.Info("registered apps", "names", appNames(reg))

	verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	apiSrv := api.NewServer(reg, verifier, logger)

	// Repo Apps (M1.4): the analyzer client is wired unconditionally —
	// /v1/apps/repo/analyze works even in stateless mode. Empty token =
	// anonymous GitHub API (60 req/h), warn so operators notice.
	if cfg.GitHubToken == "" {
		logger.Warn("GITHUB_TOKEN unset; repo analyze runs anonymously (GitHub 60 req/h rate limit)")
	}
	repoClient := repoanalyze.NewClient("", cfg.GitHubToken)
	apiSrv = apiSrv.WithRepoAnalyzer(repoClient)

	// Installer is only available when the DB is wired. Without it,
	// the v1.5 install endpoints return 503 (graceful degradation —
	// the v1.0 invoke surface still works for stateless deployments).
	if pool != nil {
		// Authz client is wired in M2.4-cont; for now AllowAll lets
		// the install path work end-to-end while the authz HTTP client
		// integration lands. Production will hard-fail this stub.
		apiSrv = apiSrv.WithInstaller(installs.New(pool, reg, installs.AllowAll{}))
		// Webhook receiver needs pool + registry references.
		apiSrv.SetPool(pool)
		apiSrv.SetBiuappRegistry(reg)

		// M1.5: restore dynamic apps (user_webview, gh_*) persisted in
		// app_center.apps into the in-memory registry. Without this the
		// stubs only existed in the process that handled the create POST,
		// so a restart / replica change made the apps 404.
		restored, err := installs.RestoreDynamicApps(ctx, pool, reg)
		if err != nil {
			return fmt.Errorf("restore dynamic apps: %w", err)
		}
		if restored > 0 {
			logger.Info("dynamic apps restored into registry", "count", restored)
		}
		logger.Info("installer enabled")

		// Cron dispatcher (M4.3 / M4.6). Background goroutine — owns
		// its own ctx so a slow shutdown signal still lets in-flight
		// dispatches finish via the parent ctx cancellation.
		disp := &triggers.Dispatcher{
			Pool:     pool,
			Registry: reg,
			Logger:   logger,
		}
		go disp.Run(ctx)
		logger.Info("trigger dispatcher started",
			"poll_interval", disp.PollInterval, "lock_ttl", disp.LockTTL)

		// Outbox poller (v1.5#2). Drains app_center.events to Realtime.
		// REALTIME_URL empty → Noop publisher (events still stamp; tests
		// + dev without realtime stay happy).
		var pub outbox.Publisher = outbox.Noop{}
		if cfg.RealtimeURL != "" {
			pub = outbox.NewRealtime(cfg.RealtimeURL, logger)
			logger.Info("outbox publisher → realtime", "url", cfg.RealtimeURL)
		} else {
			logger.Warn("REALTIME_URL unset; outbox publisher = noop (events stamp but no SSE fanout)")
		}
		poller := &outbox.Poller{
			Pool:      pool,
			Publisher: pub,
			Logger:    logger,
		}
		go func() {
			if err := poller.Run(ctx); err != nil &&
				!errors.Is(err, context.Canceled) {
				logger.Error("outbox poller exited", "err", err)
			}
		}()
		logger.Info("outbox poller started")

		// Repo Apps release poller (M2.1, tech plan §2.5). Ticker +
		// immediate first run + ctx exit, same driver shape as the
		// rankings scheduler below. The due query gates per-row, so the
		// tick only needs to be "often enough": the base interval when
		// it's short (tests/dev), capped at 5min in production.
		repoPoller := repoanalyze.NewPoller(pool, repoClient)
		repoPoller.Interval = cfg.RepoPollInterval
		repoPoller.Logger = logger
		go func() {
			tickEvery := cfg.RepoPollInterval
			if tickEvery <= 0 || tickEvery > 5*time.Minute {
				tickEvery = 5 * time.Minute
			}
			tick := time.NewTicker(tickEvery)
			defer tick.Stop()
			runOnce := func() {
				stats, err := repoPoller.PollAll(ctx)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("repo poller: poll failed", "err", err)
					return
				}
				if stats.Considered > 0 {
					logger.Info("repo poller: tick",
						"considered", stats.Considered,
						"ok", stats.OK, "errors", stats.Errors,
						"updates", stats.Updates)
				}
			}
			runOnce()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					runOnce()
				}
			}
		}()
		logger.Info("repo release poller started", "interval", cfg.RepoPollInterval)
	}

	// Radar pipeline — wires the matcher between fetcher hooks and
	// dispatcher. Both RSS entries (per-feed scope) and rankings
	// items (global scope) feed this single pipeline.
	var radarRunner func(ctx context.Context, candidates []radar.Candidate)
	var radarDispatcher *radar.Dispatcher // shared by keyword + cosine paths (M8.2)
	if radarStore != nil {
		radarDispatcher = radar.NewDispatcher(pool)
		radarDispatcher.Logger = logger

		// M9: 注入 actions.Runner — 4 个 action 实现.
		// task / skill 需要 per-user JWT signer.
		actSigner := bauth.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 24*time.Hour)
		actSignFor := func(userID string) (string, error) {
			return actSigner.Sign(&bauth.Claims{UserID: userID, Plan: "team"})
		}
		impls := []actions.Action{
			actions.NewNotify(pool, nil /* TODO Notifier */, logger),
		}
		if cfg.BrainURL != "" {
			impls = append(impls, actions.NewWiki(&radarWikiAppender{
				brainURL: cfg.BrainURL,
				signFor:  actSignFor,
				http:     &http.Client{Timeout: 10 * time.Second},
			}))
		}
		impls = append(impls,
			actions.NewTask(&actions.HTTPTaskInvoker{
				BaseURL: cfg.SelfBaseURL,
				HTTP:    &http.Client{Timeout: 10 * time.Second},
				SignFor: actSignFor,
			}),
		)
		if cfg.RuntimeURL != "" {
			impls = append(impls, actions.NewSkill(&actions.HTTPSkillInvoker{
				RuntimeURL: cfg.RuntimeURL,
				HTTP:       &http.Client{Timeout: 30 * time.Second},
				SignFor:    actSignFor,
			}))
		}
		actRunner := actions.NewRunner(impls...)
		radarDispatcher.Runner = &actionDispatcherAdapter{r: actRunner}
		logger.Info("radar action runner wired", "types", actRunner.Types())

		dispatcher := radarDispatcher
		radarRunner = func(ctx context.Context, candidates []radar.Candidate) {
			if len(candidates) == 0 {
				return
			}
			rules, err := radarStore.ListEnabledRulesAll(ctx)
			if err != nil {
				logger.Error("radar: list rules", "err", err.Error())
				return
			}
			if len(rules) == 0 {
				return
			}
			hits := radar.MatchBatch(ctx, rules, candidates)
			if len(hits) == 0 {
				return
			}
			survived, err := radarStore.FilterCooldown(ctx, hits)
			if err != nil {
				logger.Error("radar: cooldown filter", "err", err.Error())
				return
			}
			if len(survived) == 0 {
				return
			}
			survived = radar.AggregateForBurst(survived)
			written, err := radarStore.WriteHits(ctx, survived)
			if err != nil {
				logger.Error("radar: write hits", "err", err.Error())
				return
			}
			for range written {
				// M8.2: 此路径是 keyword + semantic_token fallback (内存匹配,
				// 不走 embedding). 真 cosine 路径在下面 radarDispatcher
				// goroutine 里, mode="cosine".
				rss.RecordRadarHit("keyword")
			}
			if err := dispatcher.Dispatch(ctx, written); err != nil {
				logger.Error("radar: dispatch", "err", err.Error())
			}
			logger.Info("radar: fired", "hits", len(written))
		}

		// Hook RSS fetcher (P0).
		if rssAppRef != nil {
			rssAppRef.SchedulerRef().OnNew = func(ctx context.Context, entries []rss.NewEntry) {
				cands := make([]radar.Candidate, len(entries))
				for i, e := range entries {
					cands[i] = radar.Candidate{
						Source:       "rss:" + e.FeedID,
						Title:        e.Title,
						URL:          e.URL,
						TitleHash:    e.TitleHash,
						OwnerScope:   e.OwnerScope,
						OwnerScopeID: e.OwnerScopeID,
					}
				}
				radarRunner(ctx, cands)
			}
		}
		logger.Info("radar pipeline wired")
	}

	// Rankings refresh scheduler — pulls newsnow snapshots into
	// rankings.* on a periodic tick. Tick interval is uniform (each
	// board's own refresh_sec gates whether DueBoards returns it),
	// so we don't need a cron expression here.
	if rankingsStore != nil {
		client := rankings.NewClient(cfg.NewsNowBaseURL)
		sched := rankings.NewScheduler(rankingsStore, client)
		sched.Logger = logger
		if radarRunner != nil {
			sched.OnNew = func(ctx context.Context, items []rankings.NewItem) {
				cands := make([]radar.Candidate, len(items))
				for i, it := range items {
					cands[i] = radar.Candidate{
						Source:    it.BoardID,
						Title:     it.Title,
						URL:       it.URL,
						TitleHash: it.TitleHash,
						// boards are global — no OwnerScope
					}
				}
				radarRunner(ctx, cands)
			}
		}
		go func() {
			tick := time.NewTicker(2 * time.Minute)
			defer tick.Stop()
			runOnce := func() {
				stats, err := sched.RefreshAll(ctx)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("rankings: refresh failed", "err", err)
					return
				}
				if stats.Considered > 0 {
					logger.Info("rankings: refresh tick",
						"considered", stats.Considered,
						"ok", stats.OK, "warn", stats.Warn,
						"errors", stats.Errors, "new_items", stats.NewItems)
				}
			}
			runOnce()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					runOnce()
				}
			}
		}()
		logger.Info("rankings scheduler started", "newsnow_base", cfg.NewsNowBaseURL)
	}

	// Digest worker — AI 摘要 + 重要度 / topics 给 entries 卡片用 (M1).
	// 启动 8 worker pool 后, 每 30s 跑一次 backfill 扫 unprocessed entries
	// 入队 (50 条上限, 一次 tick 最多消化 50 条新条目, 留余地给批处理).
	if pool != nil && cfg.ModelRelayURL != "" {
		dw := digest.New(pool, cfg.ModelRelayURL)
		dw.Logger = logger
		dw.Model = cfg.RSSDigestModel
		// Bearer for model-relay calls. Backfill jobs ship without a
		// caller context, so we mint a per-user token derived from
		// the feed's scope_id (real user_id) — the call then bills
		// against THAT user's quota / BYOK credentials and avoids
		// the "service account has no credentials" failure mode.
		// HS256 with the shared JWT_SECRET works in dev/test; prod
		// JWKS deployments need identity /v1/internal/sign-service-token.
		signer := bauth.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 24*time.Hour)
		dw.SignFor = func(userID string) (string, error) {
			return signer.Sign(&bauth.Claims{
				UserID: userID,
				Plan:   "team", // permissive plan; per-user quota still applies
			})
		}
		logger.Info("digest: per-user signer wired (HS256, 24h ttl)")
		dw.Start(ctx)
		go func() {
			tick := time.NewTicker(30 * time.Second)
			defer tick.Stop()
			runBackfill := func() {
				n, err := dw.BackfillUnprocessed(ctx, 50)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("digest: backfill", "err", err.Error())
				}
				if n > 0 {
					logger.Info("digest: backfilled", "n", n)
				}
			}
			runBackfill()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					runBackfill()
				}
			}
		}()
		logger.Info("rss digest worker started", "model_relay", cfg.ModelRelayURL)
	}

	// M8.2: embed worker (entries.embedding 回填) + rule embedding 同步 hook
	// + cosine 雷达 tick. embed 跟 digest 拆开是因为 LLM (chat) 和 embedding
	// 是不同 endpoint / 不同失败模式; 一边坏不应阻塞另一边.
	if pool != nil && cfg.ModelRelayURL != "" {
		ew := rssembed.New(pool, cfg.ModelRelayURL)
		ew.Logger = logger
		ew.Model = cfg.RSSEmbedModel
		signer := bauth.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 24*time.Hour)
		ew.SignFor = func(userID string) (string, error) {
			return signer.Sign(&bauth.Claims{UserID: userID, Plan: "team"})
		}
		ew.Start(ctx)
		go func() {
			tick := time.NewTicker(2 * time.Minute)
			defer tick.Stop()
			runBackfill := func() {
				n, err := ew.BackfillUnprocessed(ctx, 50)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("embed: backfill", "err", err.Error())
				}
				if n > 0 {
					logger.Info("embed: backfilled", "n", n)
				}
			}
			runBackfill()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					runBackfill()
				}
			}
		}()
		logger.Info("rss embed worker started", "model_relay", cfg.ModelRelayURL,
			"model", cfg.RSSEmbedModel)

		// 注入 rule embedding hook — rss app 在 rule create/update 时
		// 调它算 query embedding. 用 rssAppRef (concrete *rss.App) 因为
		// WithEmbedQuery 不在 biuapp.App 接口上.
		if rssAppRef != nil {
			rssAppRef.WithEmbedQuery(func(ctx context.Context, text string) ([]float32, string, error) {
				return ew.EmbedQuery(ctx, text)
			})
		}

		// M8.4: TTS 简报合成器. 走 model-relay /v1/audio/speech +
		// rss.audio_cache 24h. 复用 ew 的 SignFor (per-user JWT) 让 TTS
		// 调用计在用户名下.
		if rssAppRef != nil {
			synth := rssbriefing.New(pool, cfg.ModelRelayURL)
			synth.Logger = logger
			synth.SignFor = ew.SignFor
			rssAppRef.WithBriefingSynth(&briefingSDKAdapter{s: synth})
			logger.Info("rss briefing synth wired",
				"model", synth.Model, "voice", synth.Voice)
		}

		// M13.5: podcast transcribe worker. Scans audio-enclosure entries
		// not yet transcribed → model-relay /v1/audio/transcriptions →
		// writes content_text + re-triggers the digest. Reuses ew.SignFor
		// so transcription is billed to the feed owner.
		tw := rsstranscribe.New(pool, cfg.ModelRelayURL)
		tw.Logger = logger
		tw.SignFor = ew.SignFor
		if cfg.RSSTranscribeModel != "" {
			tw.Model = cfg.RSSTranscribeModel
		}
		tw.Start(ctx)
		go func() {
			tick := time.NewTicker(5 * time.Minute)
			defer tick.Stop()
			runBackfill := func() {
				n, err := tw.BackfillUnprocessed(ctx, 25)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("transcribe: backfill", "err", err.Error())
				}
				if n > 0 {
					logger.Info("transcribe: backfilled", "n", n)
				}
			}
			runBackfill()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					runBackfill()
				}
			}
		}()
		logger.Info("rss transcribe worker started", "model", tw.Model)

		// M9.4: Co-Pilot Q&A. 注入两个 hook —
		//   builder: copilot.Build (PG 查 view items)
		//   asker:   POST model-relay /v1/messages, 返完整答案
		if rssAppRef != nil {
			builder := &copilotBuilderAdapter{pool: pool}
			asker := &copilotAskerImpl{
				modelRelayURL: cfg.ModelRelayURL,
				model:         orFallback(cfg.RSSDigestModel, "glm-5.1"),
				signFor:       ew.SignFor,
				http:          &http.Client{Timeout: 60 * time.Second},
				logger:        logger,
			}
			rssAppRef.WithCopilot(builder, asker)
			logger.Info("rss copilot wired", "model", asker.model)
		}

		// M9.5: 周报 cron. 周日 08:00 UTC 之后第一次 5min tick 启动,
		// 给本周还没跑过的 active 用户生成 markdown + 写 wiki.
		// brain URL 为空时跳过 wiki 写入但仍在 weekly_runs 留 row.
		weekly := rssweekly.New(pool, cfg.BrainURL, cfg.ModelRelayURL)
		weekly.Logger = logger
		weekly.Model = orFallback(cfg.RSSDigestModel, "glm-5.1")
		weekly.SignFor = ew.SignFor
		weekly.Start(ctx)

		// Cosine 雷达 tick. 默认每 60s 跑一次扫最近 30min 写过 embedding 的
		// entries 跨所有 enabled rule 的 cosine. 失败只记 log, 不重试 —
		// 下一个 tick 会自然重做.
		go func() {
			tick := time.NewTicker(60 * time.Second)
			defer tick.Stop()
			runCosine := func() {
				hits, err := radarStore.SemanticBatch(ctx, 30*time.Minute)
				if err != nil {
					logger.Warn("radar: semantic batch", "err", err.Error())
					return
				}
				if len(hits) == 0 {
					return
				}
				survived, err := radarStore.FilterCooldown(ctx, hits)
				if err != nil || len(survived) == 0 {
					return
				}
				survived = radar.AggregateForBurst(survived)
				written, err := radarStore.WriteHits(ctx, survived)
				if err != nil {
					logger.Warn("radar: cosine write", "err", err.Error())
					return
				}
				for range written {
					rss.RecordRadarHit("cosine")
				}
				if radarDispatcher != nil {
					if err := radarDispatcher.Dispatch(ctx, written); err != nil {
						logger.Warn("radar: cosine dispatch", "err", err.Error())
					}
				}
				logger.Info("radar: cosine tick", "candidates", len(hits),
					"after_cooldown", len(survived), "written", len(written))
			}
			runCosine()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					runCosine()
				}
			}
		}()
		logger.Info("rss radar cosine tick started", "interval", "60s")
	}

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.SetReady(true)

	bmetrics.SetService(serviceName)
	bmetrics.SetServiceInfo(serviceVersion)

	// RSS active-user / feeds gauge poller. Cheap SELECTs every 60s; the
	// distinct-user one is the closest thing to a true DAU until we wire
	// real engagement events (M14). Skipped in stateless mode — the
	// poller would panic on a nil pool.
	if pool != nil {
		go pollRSSGauges(ctx, pool, logger)
	}

	mux := http.NewServeMux()
	hz.Mount(mux)
	mux.Handle("/metrics", bmetrics.Handler())
	apiSrv.Mount(mux)
	apiSrv.MountWebhooks(mux)
	apiSrv.MountShares(mux)
	apiSrv.MountSidebar(mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           bmetrics.HTTPMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		logger.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	logger.Info("app_center listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func appNames(r *biuapp.Registry) []string {
	out := []string{}
	for _, m := range r.List() {
		out = append(out, m.Name)
	}
	return out
}

// actionDispatcherAdapter — 让 *actions.Runner 满足 radar.ActionDispatcher
// (radar 包不能直接 import actions, 这里跨包桥接). RunAction 把 actions.Result
// marshal 成 jsonb bytes 给 dispatcher 写 action_runs.
type actionDispatcherAdapter struct {
	r *actions.Runner
}

func (a *actionDispatcherAdapter) RunAction(ctx context.Context, hit *radar.Hit, actionType string, configRaw []byte) ([]byte, error) {
	res, err := a.r.Run(ctx, hit, actions.ActionSpec{
		Type:   actionType,
		Config: json.RawMessage(configRaw),
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return json.Marshal(res)
}

func (a *actionDispatcherAdapter) Types() []string { return a.r.Types() }

// radarWikiAppender — actions.WikiAction 的 services 端实现.
// 调 brain Wiki POST /v1/wiki/projects/.../pages/.../blocks. 简化:
//   - 默认 project: 用户的 default project (查 brain GET /v1/wiki/projects)
//   - 路径解析 "信息流/雷达/AI 监控" → 一个 page; 不存在则建.
//
// 当前 v1 实现走 dev 简化路径: 直接 POST 一个 page 不要 nested path.
// 真路径解析留 v2 (M9 polish).
type radarWikiAppender struct {
	brainURL string
	signFor  func(userID string) (string, error)
	http     *http.Client
}

func (a *radarWikiAppender) AppendNote(ctx context.Context, userID, pagePath, markdown string) (string, string, error) {
	token, err := a.signFor(userID)
	if err != nil {
		return "", "", fmt.Errorf("wiki appender: sign: %w", err)
	}

	// 1. 找/建 default project. 先 list, 没有就 create.
	pid, err := a.ensureProject(ctx, token, "Inbox")
	if err != nil {
		return "", "", fmt.Errorf("wiki appender: ensure project: %w", err)
	}

	// 2. 找/建 page (用 pagePath 作 title — v1 简化, 不解 / 路径).
	pgID, err := a.ensurePage(ctx, token, pid, pagePath)
	if err != nil {
		return "", "", fmt.Errorf("wiki appender: ensure page: %w", err)
	}

	// 3. 追加 block (paragraph 含 markdown).
	blockID, err := a.appendBlock(ctx, token, pid, pgID, markdown)
	if err != nil {
		return "", "", fmt.Errorf("wiki appender: append block: %w", err)
	}
	return pgID, blockID, nil
}

func (a *radarWikiAppender) ensureProject(ctx context.Context, token, name string) (string, error) {
	// list
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.brainURL+"/v1/wiki/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("list status %d: %s", resp.StatusCode, body)
	}
	var listed struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	_ = json.Unmarshal(body, &listed)
	for _, p := range listed.Projects {
		if p.Name == name {
			return p.ID, nil
		}
	}
	// create
	createBody, _ := json.Marshal(map[string]any{"name": name})
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.brainURL+"/v1/wiki/projects", bytes.NewReader(createBody))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := a.http.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(io.LimitReader(resp2.Body, 64*1024))
	if resp2.StatusCode >= 300 {
		return "", fmt.Errorf("create status %d: %s", resp2.StatusCode, body2)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body2, &created)
	return created.ID, nil
}

func (a *radarWikiAppender) ensurePage(ctx context.Context, token, pid, title string) (string, error) {
	// 暂时每次新建一个 page (v2 polish: 复用同 title page).
	// brain 不返 list pages by title 的 endpoint — 后续加.
	body, _ := json.Marshal(map[string]any{"title": title})
	url := a.brainURL + "/v1/wiki/projects/" + pid + "/pages"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("create page status %d: %s", resp.StatusCode, respBody)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(respBody, &created)
	return created.ID, nil
}

func (a *radarWikiAppender) appendBlock(ctx context.Context, token, pid, pgID, markdown string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"type":    "paragraph",
		"content": map[string]any{"text": markdown},
	})
	url := a.brainURL + "/v1/wiki/projects/" + pid + "/pages/" + pgID + "/blocks"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("append block status %d: %s", resp.StatusCode, respBody)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(respBody, &created)
	return created.ID, nil
}

// copilotBuilderAdapter — 把 copilot.Build 包成 rss.CopilotContextBuilder.
type copilotBuilderAdapter struct {
	pool *pgxpool.Pool
}

func (b *copilotBuilderAdapter) BuildContext(
	ctx context.Context, userID, viewKind, currentEntryID string,
) (string, []rss.CopilotItem, error) {
	systemPrompt, items, err := rsscopilot.Build(ctx, b.pool, userID, viewKind, currentEntryID)
	if err != nil {
		return "", nil, err
	}
	out := make([]rss.CopilotItem, len(items))
	for i, it := range items {
		out[i] = rss.CopilotItem{
			N:       it.N,
			EntryID: it.EntryID,
			Title:   it.Title,
			URL:     it.URL,
			Source:  it.Source,
		}
	}
	return systemPrompt, out, nil
}

// copilotAskerImpl — 调 model-relay /v1/messages (Anthropic-shape, 跟
// digest worker 同协议). 一次性同步, 非流式.
type copilotAskerImpl struct {
	modelRelayURL string
	model         string
	signFor       func(userID string) (string, error)
	http          *http.Client
	logger        *slog.Logger
}

func (a *copilotAskerImpl) Ask(ctx context.Context, userID, systemPrompt, question string) (string, error) {
	if a.modelRelayURL == "" {
		return "", errors.New("copilot: model-relay url empty")
	}
	token, err := a.signFor(userID)
	if err != nil {
		return "", fmt.Errorf("copilot: sign: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"model":      a.model,
		"max_tokens": 1024,
		"system":     systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": question},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.modelRelayURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("copilot: status %d: %s", resp.StatusCode,
			truncate200(string(respBody)))
	}
	// model-relay /v1/messages 可能返 OpenAI-shape (choices[].message.content)
	// 或 Anthropic-shape (content[].text). 都试一下.
	var openai struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(respBody, &openai) == nil && len(openai.Choices) > 0 {
		if c := openai.Choices[0].Message.Content; c != "" {
			return c, nil
		}
	}
	var anthropic struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &anthropic); err == nil {
		var sb []byte
		for _, c := range anthropic.Content {
			if c.Type == "text" {
				sb = append(sb, c.Text...)
			}
		}
		if len(sb) > 0 {
			return string(sb), nil
		}
	}
	return "", fmt.Errorf("copilot: empty response from model-relay")
}

func truncate200(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "…"
}

func orFallback(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// briefingSDKAdapter — 把 briefing.Synthesizer 包成 rss.BriefingSynth.
// 跨包之间的字段是同名同义, 一对一映射.
type briefingSDKAdapter struct {
	s *rssbriefing.Synthesizer
}

func (a *briefingSDKAdapter) SynthForUser(
	ctx context.Context, userID, scriptText string, headlineIDs []string, headlineN int,
) (*rss.BriefingResult, error) {
	r, err := a.s.SynthForUser(ctx, userID, scriptText, headlineIDs, headlineN)
	if err != nil {
		return nil, err
	}
	return &rss.BriefingResult{
		Mp3:        r.Mp3,
		Script:     r.Script,
		Voice:      r.Voice,
		Model:      r.Model,
		Characters: r.Characters,
		Cached:     r.Cached,
		HeadlineN:  r.HeadlineN,
	}, nil
}

// pollRSSGauges keeps the rss_active_users / rss_feeds_total gauges
// fresh by SELECT count(distinct ...). Cheap on the dev set; if the
// query gets slow at scale switch to a materialised view refreshed via
// pg_cron.
func pollRSSGauges(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	run := func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		windows := []struct {
			label  string
			cutoff string
		}{
			{"1d", "1 day"},
			{"7d", "7 days"},
			{"30d", "30 days"},
		}
		for _, w := range windows {
			var n int64
			// last_fetched_at 是 v2 我们能拿到的最近活动信号。真正 engagement
			// signal 等 M14 加 engagement_events 后再切。
			err := pool.QueryRow(c, `
				SELECT COUNT(DISTINCT scope_id)
				FROM rss.feeds
				WHERE scope='user' AND enabled
				  AND last_fetched_at > now() - $1::interval`,
				w.cutoff,
			).Scan(&n)
			if err != nil {
				logger.Warn("rss gauges: active_users", "window", w.label, "err", err.Error())
				continue
			}
			rss.SetActiveUsers(w.label, n)
		}

		var feeds int64
		if err := pool.QueryRow(c,
			`SELECT COUNT(*) FROM rss.feeds WHERE enabled`,
		).Scan(&feeds); err != nil {
			logger.Warn("rss gauges: feeds_total", "err", err.Error())
		} else {
			rss.SetFeedsTotal(feeds)
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}
