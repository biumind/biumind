// Identity service entry point.
package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
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
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	bmetrics "github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	"github.com/biumind/biumind/services/identity/internal/admin"
	"github.com/biumind/biumind/services/identity/internal/api"
	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/biumind/biumind/services/identity/internal/byok"
	"github.com/biumind/biumind/services/identity/internal/credits"
	"github.com/biumind/biumind/services/identity/internal/cron"
	"github.com/biumind/biumind/services/identity/internal/events"
	"github.com/biumind/biumind/services/identity/internal/internalapi"
	"github.com/biumind/biumind/services/identity/internal/passwords"
	"github.com/biumind/biumind/services/identity/internal/realtimepub"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/biumind/biumind/services/identity/internal/token"
	"github.com/google/uuid"
)

// uuidParse — internalapi.Lookup 的轻量 helper. 不分 nil / err 类型, 上层
// 只需要"能不能解析成有效 UUID"; 解析失败一律视作未找到.
func uuidParse(s string) (uuid.UUID, error) { return uuid.Parse(s) }

const (
	serviceName    = "identity"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string        `env:"LISTEN_ADDR" default:":7004"`
	Environment  string        `env:"BIUMIND_ENV" default:"dev"`
	LogLevel     string        `env:"BIUMIND_LOG_LEVEL" default:"info"`
	OtlpEndpoint string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT" default:""`
	DatabaseURL  string        `env:"DATABASE_URL" required:"true"`
	// MigrationsDir 启动自动跑 goose up. 容器里默认 /etc/biumind/migrations/identity
	// (Dockerfile COPY 自 services/identity/migrations). 留空跳过 (单测).
	MigrationsDir string        `env:"BIUMIND_MIGRATIONS_DIR" default:"/etc/biumind/migrations/identity"`
	JWTSecret    string        `env:"JWT_SECRET" required:"true"`
	JWTIssuer    string        `env:"JWT_ISSUER" default:"https://identity.biumind.local"`
	JWTAudience  string        `env:"JWT_AUDIENCE" default:"biumind-api"`
	AccessTTL  time.Duration `env:"IDENTITY_ACCESS_TTL" default:"15m"`
	RefreshTTL time.Duration `env:"IDENTITY_REFRESH_TTL" default:"720h"`
	// RefreshAbsoluteTTL — refresh_token 绝对 cap (created_at + this), rotation
	// 不重置。详见 BiuMind-Identity-Session-Design §3.1。1 年是合理默认。
	RefreshAbsoluteTTL time.Duration `env:"IDENTITY_REFRESH_ABSOLUTE_TTL" default:"8760h"`

	// RefreshReuseGrace — refresh rotation 宽限窗口 (Auth0 Reuse Interval /
	// Okta 30s grace 同款): rotate 后窗口内重放老 refresh_token 沿 rotated_to
	// 链找回 head 直接 200, 不判 token_reuse 整族撤销。0 用默认 10s。
	RefreshReuseGrace time.Duration `env:"IDENTITY_REFRESH_REUSE_GRACE" default:"10s"`
	// RefreshGraceKey — grace replay 密文 (rotated_token_enc) 的密钥原料,
	// 内部 sha256 派生 32B AES key (api.DeriveGraceKey)。空时从 JWT_SECRET
	// 派生; HS256 也没有则用 RSA 私钥 PEM。都不可得 → grace replay 禁用。
	// 严禁进 git; 修改后老密文无法解密 (grace replay 静默失效, 不致命)。
	RefreshGraceKey string `env:"IDENTITY_REFRESH_GRACE_KEY" default:""`

	// JWTSigningKeyFile — path to RS256 private key. When set, Identity
	// signs RS256 tokens + serves /.well-known/jwks.json. Empty falls
	// back to HS256 with JWTSecret (dev / tests). The file is created
	// (mode 0600) on first run if missing.
	JWTSigningKeyFile string `env:"JWT_SIGNING_KEY_FILE" default:""`

	// BootstrapSuperAdmins — 启动时自动提升为 superadmin 的邮箱列表.
	// 逗号分隔. 邮箱必须已经在 identity.users 中 (用户已注册). 已是
	// superadmin 的跳过. 用途: 首次部署后给运维种子账号开后台权限,
	// 不用手动 SQL update. 后续 superadmin 创建可在后台 UI 操作.
	BootstrapSuperAdmins string `env:"BIUMIND_BOOTSTRAP_SUPERADMINS" default:""`

	// PrometheusURL — observability profile 起来时填. 用于 Vue admin 自画
	// 图表代理 (/v1/admin/monitor/query → prom HTTP API).
	// 空表示 monitor query endpoint 返 503.
	PrometheusURL string `env:"BIUMIND_PROMETHEUS_URL" default:""`

	// W3 — NATS billing events. 空时走 NoopPublisher (nothing 发到 NATS),
	// 不报错; 集成测试默认空. 生产 / docker-compose 配 nats://nats:4222.
	NatsURL string `env:"NATS_URL" default:""`
	// JetStream replicas (生产 cluster 调 3, dev / single-node 默认 1).
	BillingEventsReplicas int `env:"BILLING_EVENTS_REPLICAS" default:"1"`

	// InternalToken — service-to-service shared bearer for /v1/internal/*.
	// 空时 internal endpoints 完全不挂（mp-login / 公开 API 不受影响）。
	// aigc 等同集群服务调 credits.{Consume,Refund,Grant} 必须配。
	InternalToken string `env:"IDENTITY_INTERNAL_TOKEN" default:""`

	// BYOKMasterKey — base64 编码的 32 字节 AES-256 主密钥, 加密用户上传的
	// 上游 API Key. 空时 BYOK 功能完全禁用 (PUT /v1/identity/me/api-keys 返 404).
	// 生成: openssl rand -base64 32. 生产从 KMS / k8s secret 注入.
	// 严禁进 git; 重启后需保持值不变, 否则旧密文无法解密.
	BYOKMasterKey string `env:"BYOK_MASTER_KEY" default:""`

	// MiniApp 第三方登录配置. 任一平台留空时, 对应 /v1/auth/<platform>/mp-login
	// 返 503. 生产由 KMS 注入到 env. dev 可不配, 不影响其他端点.
	WechatMPAppID     string `env:"WECHAT_MP_APPID" default:""`
	WechatMPAppSecret string `env:"WECHAT_MP_APPSECRET" default:""`

	AlipayMPAppID      string `env:"ALIPAY_MP_APPID" default:""`
	AlipayMPPrivateKey string `env:"ALIPAY_MP_PRIVATE_KEY" default:""`
	AlipayMPPublicKey  string `env:"ALIPAY_MP_PUBLIC_KEY" default:""`

	ToutiaoMPAppID     string `env:"TOUTIAO_MP_APPID" default:""`
	ToutiaoMPAppSecret string `env:"TOUTIAO_MP_APPSECRET" default:""`

	BaiduMPAppID     string `env:"BAIDU_MP_APPID" default:""`
	BaiduMPAppSecret string `env:"BAIDU_MP_APPSECRET" default:""`

	QQMPAppID     string `env:"QQ_MP_APPID" default:""`
	QQMPAppSecret string `env:"QQ_MP_APPSECRET" default:""`

	KuaishouMPAppID     string `env:"KUAISHOU_MP_APPID" default:""`
	KuaishouMPAppSecret string `env:"KUAISHOU_MP_APPSECRET" default:""`

	JDMPAppID     string `env:"JD_MP_APPID" default:""`
	JDMPAppSecret string `env:"JD_MP_APPSECRET" default:""`

	LarkMPAppID     string `env:"LARK_MP_APPID" default:""`
	LarkMPAppSecret string `env:"LARK_MP_APPSECRET" default:""`

	// H5 OAuth 2.0 网页授权 — 与 MP 凭据独立 (开放平台 / 公众号 appid).
	// FrontendBaseURL 必须配, callback 完成跳 <base>/pages/me/oauth-return#...
	WechatWebAppID     string `env:"WECHAT_WEB_APPID" default:""`
	WechatWebAppSecret string `env:"WECHAT_WEB_APPSECRET" default:""`
	H5FrontendBaseURL  string `env:"H5_FRONTEND_BASE_URL" default:""`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "identity: %v\n", err)
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

	pool, err := bdb.New(ctx, bdb.Defaults(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	// Schema 自动 migrate. checkTable 用 'identity.users' 做 baseline 探针:
	// 表存在但 goose_db_version 不存在 → 老库, 把版本号 <= baselineMax 的
	// migration 标记为 applied (不重跑), 高于 baselineMax 的正常 goose up.
	// 全新库 → goose up 跑全部.
	//
	// baselineMax = 17: 涵盖 00001~00017 (R0 已部署的全部历史 migration).
	// 18+ (W1 起 credit_holds / pricing_book / user_api_keys / billing_*) 都是
	// 新增, 由 dbmigrate 正常 goose-up 应用.
	//
	// **绝对不要往下调这个值**. 已 applied 的 migration 被 baseline 跳过
	// 是正确; 但下调会让某些 v9-v17 被当作未跑重跑, 报 "already exists".
	//
	// 历史 (供后人理解): R0 时 baselineMax=8 因为只跑过 00001~00008. 之后
	// 服务重启时 needsBaseline 返 false (goose 表已存在), goose-up 应用 9-17.
	// 但 dev 环境某次因数据库重置导致 goose 表被清空, 重启时 needsBaseline
	// 成立但 baselineMax=8 只 mark 1-8 → 9-17 重跑撞表. W1 拉到 17 修复.
	const baselineMax int64 = 17
	if cfg.MigrationsDir != "" {
		if err := dbmigrate.Run(ctx, pool, serviceName, cfg.MigrationsDir, "identity.users", baselineMax); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	// Pick signing mode. RS256+JWKS in production; HS256 fallback for
	// dev / tests when no key file is configured.
	var signer *bauth.Signer
	var verifier *bauth.Verifier
	var rsaKey *rsa.PrivateKey // grace key 派生兜底原料 (见下)
	if cfg.JWTSigningKeyFile != "" {
		key, err := bauth.LoadOrCreateRSAKey(cfg.JWTSigningKeyFile)
		if err != nil {
			return fmt.Errorf("rsa key: %w", err)
		}
		rsaKey = key
		kid := bauth.DeriveKid(&key.PublicKey)
		signer = bauth.NewRSASigner(key, kid, cfg.JWTIssuer, cfg.JWTAudience, cfg.AccessTTL)
		// For its own /me + /refresh handlers Identity verifies its own
		// tokens; an HS256-or-RS256 verifier built from the same Signer
		// would be circular. Use a tiny in-process trust loop: build a
		// verifier whose public-key cache is pre-warmed with our own kid.
		verifier = bauth.NewVerifier("", cfg.JWTIssuer, cfg.JWTAudience)
		// HS256 path is unused; RS256 path needs JWKS — point at our
		// own endpoint so the cache lazy-loads on first verify.
		// (Identity always serves /.well-known/jwks.json, see below.)
		verifier = bauth.NewJWKSVerifier(
			"http://127.0.0.1"+cfg.ListenAddr+"/.well-known/jwks.json",
			cfg.JWTIssuer, cfg.JWTAudience, 10*time.Minute,
		)
		slog.Default().Info("identity: RS256 + JWKS",
			"kid", kid, "key_file", cfg.JWTSigningKeyFile)
	} else {
		signer = bauth.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.AccessTTL)
		verifier = bauth.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
		slog.Default().Warn("identity: HS256 — set JWT_SIGNING_KEY_FILE for production RS256/JWKS")
	}

	// grace replay key 派生链: 显式 IDENTITY_REFRESH_GRACE_KEY > JWT_SECRET >
	// RSA 私钥 PEM。都不可得 → nil, grace replay 禁用 (Warn 一次; 宽限窗口内
	// 重放退回 reuse detection 语义, 其余功能不受影响)。
	var graceKey []byte
	switch {
	case cfg.RefreshGraceKey != "":
		graceKey = api.DeriveGraceKey(cfg.RefreshGraceKey)
	case cfg.JWTSecret != "":
		graceKey = api.DeriveGraceKey(cfg.JWTSecret)
	case rsaKey != nil:
		graceKey = api.DeriveGraceKey(string(pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
		})))
	default:
		slog.Default().Warn("identity: refresh grace replay disabled — no IDENTITY_REFRESH_GRACE_KEY / JWT_SECRET / RSA key material")
	}

	identityStore := store.New(pool)

	// RBAC role cache — 启动时一次性 load identity.role_permissions.
	// fail-soft: 表不存在 (postgres volume 已存在但新 migration 没跑) 时
	// 只 warn 不退出, 让 admin 4 个 endpoint 还能用 hasAnyRole 基础检查.
	// /v1/identity/me 会返回空 permissions, 前端容错即可.
	// 跑过 migration 00003_rbac.sql 后重启 identity 就恢复.
	roleCache := bauth.NewRoleCache(pool)
	if err := roleCache.Reload(ctx); err != nil {
		slog.Warn("rbac: load failed, RBAC features degraded",
			"err", err,
			"hint", "run migration 00003_rbac.sql + restart identity")
	} else {
		slog.Info("rbac: loaded", "roles", len(roleCache.RoleNames()))
	}

	// audit 双写: ring buffer (1000条内存兜底) + audit.events 表 (持久化).
	// PG 写失败时 ring 仍保存, slog warn; PG 查失败时降级 ring.
	// 这里要先于 api.Server 构造 — login 失败/成功审计依赖它.
	pgAuditor := admin.NewPGAudit(pool)
	auditor := admin.NewCompositeAudit(
		pgAuditor,
		admin.NewAuditLog(1000),
		slog.Default(),
	)

	// system_config — 邮箱验证邮件 + 告警邮件共用 store, 这里建一次复用.
	systemConfigStore := admin.NewSystemConfigStore(pool)

	s := &api.Server{
		Store:          identityStore,
		Signer:         signer,
		Verifier:       verifier,
		AccessTTL:          cfg.AccessTTL,
		RefreshTTL:         cfg.RefreshTTL,
		RefreshAbsoluteTTL: cfg.RefreshAbsoluteTTL,
		RefreshReuseGrace:  cfg.RefreshReuseGrace,
		RefreshGraceKey:    graceKey,
		PasswordParams: passwords.DefaultParams,
		Logger:         slog.Default(),
		RoleCache:      roleCache,
		Audit:          auditor,
		LoginThrottle:  api.NewLoginThrottle(),
		VerificationMailer: &api.VerificationMailer{
			Cfg:    systemConfigStore,
			Logger: slog.Default(),
		},
		VerificationThrottle:  api.NewVerificationThrottle(),
		PasswordResetThrottle: api.NewVerificationThrottle(),
		// brute-force 告警邮件读 alert.email 配置 — 跟 alertmanager 邮件同 SMTP.
		SystemConfig: systemConfigStore,
		// MiniApp 9 端登录 — 留空时端点返 503. dev / 单测可不配.
		WechatMP: &api.WechatMPConfig{
			AppID:     cfg.WechatMPAppID,
			AppSecret: cfg.WechatMPAppSecret,
		},
		AlipayMP: &api.AlipayMPConfig{
			AppID:      cfg.AlipayMPAppID,
			PrivateKey: cfg.AlipayMPPrivateKey,
			PublicKey:  cfg.AlipayMPPublicKey,
		},
		ToutiaoMP: &api.ToutiaoMPConfig{
			AppID:     cfg.ToutiaoMPAppID,
			AppSecret: cfg.ToutiaoMPAppSecret,
		},
		BaiduMP: &api.BaiduMPConfig{
			AppID:     cfg.BaiduMPAppID,
			AppSecret: cfg.BaiduMPAppSecret,
		},
		QQMP: &api.QQMPConfig{
			AppID:     cfg.QQMPAppID,
			AppSecret: cfg.QQMPAppSecret,
		},
		KuaishouMP: &api.KuaishouMPConfig{
			AppID:     cfg.KuaishouMPAppID,
			AppSecret: cfg.KuaishouMPAppSecret,
		},
		JDMP: &api.JDMPConfig{
			AppID:     cfg.JDMPAppID,
			AppSecret: cfg.JDMPAppSecret,
		},
		LarkMP: &api.LarkMPConfig{
			AppID:     cfg.LarkMPAppID,
			AppSecret: cfg.LarkMPAppSecret,
		},
		WechatWeb: &api.WechatWebConfig{
			AppID:           cfg.WechatWebAppID,
			AppSecret:       cfg.WechatWebAppSecret,
			FrontendBaseURL: cfg.H5FrontendBaseURL,
		},
	}

	// 后台 admin REST API — RBAC, 当前 commit 是最小可用版 (admin/superadmin
	// 全权), 完整 RBAC (按 endpoint 细分 permission) 是后续 commit.
	adminServer := admin.New(
		admin.NewPGStore(identityStore),
		auditor,
		verifier,
		slog.Default(),
	)

	// 服务健康监控 — 后台 goroutine 每 10s 调各服务 /healthz, cache 状态.
	// 容器详情走 cadvisor (Prometheus 派生), 不再挂 docker.sock.
	mon := admin.NewMonitor(cfg.PrometheusURL, slog.Default())
	mon.Start(ctx)
	adminServer.Monitor = mon
	adminServer.SystemConfig = systemConfigStore
	adminServer.RBAC = admin.NewRBACStore(pool)
	adminServer.RoleCache = roleCache // PUT 矩阵后 reload, 让权限变化即时生效
	adminServer.AuditSummary = pgAuditor // dashboard 卡片读这里聚合
	adminServer.Plans = s.Plans          // W2-9: PlanLimits 从 DB 读
	if cfg.PrometheusURL != "" {
		slog.Info("monitor: prometheus query proxy enabled", "url", cfg.PrometheusURL)
	}

	// 启动时把 BIUMIND_BOOTSTRAP_SUPERADMINS 列出的邮箱提升为 superadmin.
	// 邮箱不存在的 (用户尚未注册) 跳过, 等用户注册后下次重启再提升.
	bootstrapAdminsCtx, cancelBootstrap := context.WithTimeout(ctx, 10*time.Second)
	bootstrapAdmins(bootstrapAdminsCtx, identityStore, cfg.BootstrapSuperAdmins)
	cancelBootstrap()

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.AddProbe("postgres", bdb.HealthProbe(pool))
	hz.SetReady(true)

	// Prometheus metrics — 给本服务命名 + 注册版本元信息.
	bmetrics.SetService(serviceName)
	bmetrics.SetServiceInfo(serviceVersion)

	// 积分系统 — 注入 *credits.Service 让 api / internalapi / admin 都能用.
	creditsSvc := credits.New(pool)
	s.Credits = creditsSvc
	adminServer.Credits = creditsSvc

	// W2-5 / W2-6 会员体系 — 注入 plans + subscriptions 仓储, 让 GET /v1/plans
	// + GET /v1/subscriptions/me endpoints 工作.
	s.Plans = billing.NewPlansRepo(pool)
	s.Subscriptions = billing.NewSubscriptionsRepo(pool)
	s.Subscriptions.SetPlansRepo(s.Plans)
	// W5-8 trial 防刷; W6-7/8 优惠券 + 邀请奖励. 全 nil 兜底已有, 这里默认全 wire,
	// 实际只在 endpoint 被命中时才查 DB.
	s.Trial = billing.NewTrialChecker(pool)
	s.Coupons = billing.NewCouponRepo(pool)
	s.Referrals = billing.NewReferralRepo(pool)
	slog.Info("billing plans + subscriptions + trial + coupons + referrals wired")

	// W3 — NATS billing events. NatsURL 空时全程 Noop, 不影响功能.
	if cfg.NatsURL != "" {
		natsBus, err := bus.Connect(cfg.NatsURL, "identity", cfg.Environment)
		if err != nil {
			return fmt.Errorf("nats connect: %w", err)
		}
		defer natsBus.Close()
		js, err := natsBus.JetStream()
		if err != nil {
			return fmt.Errorf("nats jetstream: %w", err)
		}
		if err := events.EnsureStream(ctx, js, cfg.Environment, cfg.BillingEventsReplicas); err != nil {
			return fmt.Errorf("ensure billing events stream: %w", err)
		}
		pub := events.NewNATSPublisher(js, cfg.Environment, slog.Default())
		creditsSvc.SetPublisher(pub)
		s.Subscriptions.SetPublisher(pub)
		slog.Info("billing events publisher wired",
			"stream", events.StreamName,
			"subject_prefix", events.SubjectPrefix(cfg.Environment),
			"replicas", cfg.BillingEventsReplicas)

		// PERI-4b — 公告发布经 Realtime SSE 推送。复用同一条 core NATS 连接
		// (billing events 走 JetStream,公告通知走 core subject)。
		s.AnnouncementNotifier = &realtimepub.AnnouncementPublisher{
			Bus:    natsBus,
			Env:    cfg.Environment,
			Logger: slog.Default(),
		}
		slog.Info("announcement realtime publisher wired",
			"subject", "biumind."+cfg.Environment+".identity.announcement.realtime",
			"topic", realtimepub.AnnounceTopic)

		// W3-5 PG sink (MVP). 提前 3 个月预留分区, 起 sink goroutine
		// 订阅 stream → 批量写 billing.events. 切 ClickHouse 时换这一段
		// (见 dev plan §11.5 O-1).
		if err := events.EnsureFuturePartitions(ctx, pool, 3); err != nil {
			slog.Warn("billing events: partition pre-create failed", "err", err.Error())
		}
		sink := events.NewSink(pool, js, cfg.Environment, slog.Default())
		go func() {
			if err := sink.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("billing events sink exited", "err", err.Error())
			}
		}()
		slog.Info("billing events sink running",
			"consumer", events.SinkConsumerName,
			"batch_max", events.SinkBatchMax,
			"flush_interval", events.SinkFlushInterval.String())
	} else {
		slog.Info("billing events publisher disabled (NATS_URL empty)")
	}

	// BYOK — 用户自带 API Key. BYOK_MASTER_KEY 留空则禁用 (生产必须配 base64 32B).
	if cfg.BYOKMasterKey != "" {
		cipher, err := byok.NewCipherFromBase64(cfg.BYOKMasterKey)
		if err != nil {
			return fmt.Errorf("byok cipher: %w", err)
		}
		byokStore := byok.NewStore(pool, cipher)
		byokValidator := byok.NewValidator()
		s.BYOK = byokStore
		s.BYOKValidator = byokValidator
		slog.Info("byok enabled")
	} else {
		slog.Info("byok disabled (BYOK_MASTER_KEY empty)")
	}

	// 流式预扣 reaper — 每 30s 扫一次 expired holds 释放对应 packages.
	go runHoldsReaper(ctx, creditsSvc)

	// W4-4: 月初 1 号 00:30 Asia/Shanghai 给所有 active 订阅发月度积分 + 重置 quota.
	monthlyJob := cron.NewMonthlyGrant(pool, creditsSvc, cron.MonthlyGrantConfig{}, slog.Default())
	go func() {
		if err := monthlyJob.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("monthly grant job exited", "err", err.Error())
		}
	}()
	slog.Info("monthly grant job running", "trigger", "1st 00:30 Asia/Shanghai")

	// refresh_token reaper — 每 1h 物理回收 revoked > 30d / absolute > 7d 的行
	// (BiuMind-Identity-Session-Design §4.2)。零值 ReaperConfig 用合理默认。
	go token.RunReaper(ctx, identityStore, token.ReaperConfig{}, slog.Default())

	mux := http.NewServeMux()
	hz.Mount(mux)
	s.Mount(mux)
	adminServer.Mount(mux)

	// internalapi — service-to-service. 当前两组：plan 查询 + credits 操作.
	internalSrv := internalapi.New(cfg.InternalToken, func(userID string) (string, error) {
		// 走 store 直接查 plan; 不存在用户返 ErrNotFound 让 handler 退化为 free.
		uid, err := uuidParse(userID)
		if err != nil {
			return "", internalapi.ErrNotFound
		}
		u, err := identityStore.GetUserByID(ctx, uid)
		if err != nil {
			return "", internalapi.ErrNotFound
		}
		return u.Plan, nil
	})
	internalSrv.Mount(mux)
	internalSrv.MountCredits(mux, creditsSvc)
	internalSrv.MountBYOK(mux, s.BYOK)

	mux.Handle("/metrics", bmetrics.Handler())
	// Public JWKS endpoint — only meaningful in RS256 mode; HS256 mode
	// returns 404 from JWKSHandler so misconfigured callers fail loudly.
	mux.HandleFunc("GET /.well-known/jwks.json", bauth.JWKSHandler(signer))

	// Wrap mux with HTTP metrics middleware then CORS. /metrics 自身被
	// middleware 跳过, 不进自循环.
	corsHandler := bcors.Default().Wrap(bmetrics.HTTPMiddleware(mux))
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           corsHandler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutdown signaled")
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()

	slog.Info("identity listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// bootstrapAdmins 启动时把 .env BIUMIND_BOOTSTRAP_SUPERADMINS 邮箱列表
// 提升为 superadmin. 邮箱不存在 / 已是 superadmin → 跳过, 不报错.
//
// 用 INFO 日志记录每个动作, 运维启动时能从 docker logs 看到谁被提升.
func bootstrapAdmins(ctx context.Context, s *store.Store, csvEmails string) {
	if strings.TrimSpace(csvEmails) == "" {
		return
	}
	for _, raw := range strings.Split(csvEmails, ",") {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}
		promoted, err := s.PromoteByEmail(ctx, email, "superadmin")
		switch {
		case err != nil:
			slog.Warn("bootstrap superadmin: db error",
				"email", email, "err", err)
		case promoted:
			slog.Info("bootstrap superadmin: promoted", "email", email)
		default:
			slog.Debug("bootstrap superadmin: noop (not found or already superadmin)",
				"email", email)
		}
	}
}

// runHoldsReaper — 后台 goroutine, 每 30s 调一次 ReapExpired 释放过期 hold.
//
// 单实例足够: ReapExpired 内部对每条 hold 锁行 + status 二次校验, 多副本并发
// 跑也是幂等的 (其它副本看到 status='expired' 直接跳过). 即便如此, 当前 dev
// 单实例运行避免无意义的 DB 竞争.
//
// 有错误日志但不中断 — reaper 只清账户脏数据, 临时失败下次再来.
func runHoldsReaper(ctx context.Context, svc *credits.Service) {
	const interval = 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := svc.ReapExpired(ctx, 200)
			if err != nil {
				slog.Warn("holds reaper", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("holds reaper", "expired_processed", n)
			}
		}
	}
}
