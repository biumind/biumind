// Channels service entry point.
//
// Registers configured drivers, mounts the API, and exposes /healthz.
// Drivers come on/off via env:
//
//	TELEGRAM_BOT_TOKEN, TELEGRAM_WEBHOOK_SECRET — enable telegram
//	CHANNELS_ENABLE_STUB=1                       — enable stub (tests/dev)
//
// Future drivers (discord/slack/feishu/email) follow the same pattern.
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
	bhealth "github.com/biumind/biumind/packages/go-sdk/biu/healthz"
	botel "github.com/biumind/biumind/packages/go-sdk/biu/otel"
	chagentplane "github.com/biumind/biumind/services/channels/internal/agentplane"
	"github.com/biumind/biumind/services/channels/internal/api"
	"github.com/biumind/biumind/services/channels/internal/driver"
	"github.com/biumind/biumind/services/channels/internal/memorybridge"
	"github.com/biumind/biumind/services/channels/internal/router"
	"github.com/google/uuid"
)

// driverMap exposes Router.drivers as a map[string]driver.Driver so the
// listener can look up by channel name. Router doesn't expose its drivers
// publicly (single owner, lookup is internal). For S12-1 we need cross-
// package access; if this becomes routine, add a Router.Drivers() method.
func driverMap(r *router.Router) map[string]driver.Driver {
	out := map[string]driver.Driver{}
	for _, name := range r.Routes() {
		if d, ok := r.Driver(name); ok {
			out[name] = d
		}
	}
	return out
}

// firstNonEmptyChannelStr 取首个非空 string；channels main 多处 fallback 用。
func firstNonEmptyChannelStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

const (
	serviceName    = "channels"
	serviceVersion = "0.1.0"
	schemaVersion  = 1
)

type Config struct {
	ListenAddr   string `env:"LISTEN_ADDR" default:":7007"`
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

	RuntimeURL    string `env:"RUNTIME_URL" default:""`
	RuntimeBearer string `env:"RUNTIME_BEARER" default:""`

	// NatsURL — when set, inbound webhooks publish to
	// `biumind.<env>.channels.inbound.<channel>` instead of HTTP-forwarding
	// to Runtime. Empty falls back to the direct-HTTP path.
	NatsURL string `env:"NATS_URL" default:""`

	// BusUseJetStream — when true (and NATS_URL set), publishes go
	// through JetStream instead of core NATS pub-sub. Production
	// default; disable only for dev brokers without JetStream
	// enabled. Stream `BIUMIND_CHANNELS` is auto-ensured on boot.
	BusUseJetStream bool `env:"BUS_USE_JETSTREAM" default:"false"`

	TelegramToken  string `env:"TELEGRAM_BOT_TOKEN" default:""`
	TelegramSecret string `env:"TELEGRAM_WEBHOOK_SECRET" default:""`

	SlackBotToken      string `env:"SLACK_BOT_TOKEN" default:""`
	SlackSigningSecret string `env:"SLACK_SIGNING_SECRET" default:""`

	DiscordBotToken  string `env:"DISCORD_BOT_TOKEN" default:""`
	DiscordPublicKey string `env:"DISCORD_PUBLIC_KEY" default:""`

	FeishuBotToken          string `env:"FEISHU_BOT_TOKEN" default:""`
	FeishuVerificationToken string `env:"FEISHU_VERIFICATION_TOKEN" default:""`
	FeishuEncryptKey        string `env:"FEISHU_ENCRYPT_KEY" default:""`

	// Email — pick one inbound vendor (mailgun | postmark) and one
	// outbound SMTP. Inbound is gated on EmailVendor; outbound on
	// SMTPHost. Either side can be left disabled by leaving its set
	// of vars empty (one-direction deployments are common — receive
	// support tickets, send marketing replies through a separate
	// channel).
	EmailVendor       string `env:"EMAIL_VENDOR" default:""` // "mailgun" | "postmark"
	EmailMailgunKey   string `env:"EMAIL_MAILGUN_SIGNING_KEY" default:""`
	EmailPostmarkAuth string `env:"EMAIL_POSTMARK_BASIC_AUTH" default:""`
	EmailSMTPHost     string `env:"EMAIL_SMTP_HOST" default:""`
	EmailSMTPPort     int    `env:"EMAIL_SMTP_PORT" default:"587"`
	EmailSMTPUser     string `env:"EMAIL_SMTP_USER" default:""`
	EmailSMTPPass     string `env:"EMAIL_SMTP_PASS" default:""`
	EmailFromAddress  string `env:"EMAIL_FROM_ADDRESS" default:""`

	EnableStub bool `env:"CHANNELS_ENABLE_STUB" default:"false"`

	// ─── Memory bridge ──────────────────────────────────────────
	// When all three are set the Router queries Brain.Memory before
	// forwarding inbound messages to Runtime. Empty disables.
	BrainURL          string `env:"CHANNELS_BRAIN_URL"          default:""`
	BrainBearer       string `env:"CHANNELS_BRAIN_BEARER"       default:""`
	MemoryProjectID   string `env:"CHANNELS_MEMORY_PROJECT_ID"  default:""`
	MemoryRecallLimit int    `env:"CHANNELS_MEMORY_RECALL_LIMIT" default:"5"`

	// ─── Agent Plane integration（S12-1） ─────────────────────
	// 全部三个都有时启用：channels Inbound 走 brain Agent Plane（POST
	// /v1/agent/sessions + listener 订 .out 帧 → driver.Send）。新路径
	// 失败时降级到 JS / HTTP 老路径。
	//   AGENT_PLANE_BRAIN_URL: 同 CHANNELS_BRAIN_URL；分开 env 让灰度
	//                         可只切一边
	//   AGENT_PLANE_ADMIN_USER_ID: 自签 admin JWT 的 user_id claim
	//   AGENT_PLANE_POOL_TAG: 创 task session 时附带，让 brain 选指定
	//                         pool 的 runtime
	AgentPlaneBrainURL    string `env:"AGENT_PLANE_BRAIN_URL"     default:""`
	AgentPlaneAdminUserID string `env:"AGENT_PLANE_ADMIN_USER_ID" default:""`
	AgentPlanePoolTag     string `env:"AGENT_PLANE_POOL_TAG"      default:""`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "channels: %v\n", err)
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

	r := router.New(logger).WithEnv(cfg.Environment)

	// Optional memory bridge — opt-in via three env vars. Without them
	// AgentPlane task creation gets called without recall context.
	if cfg.BrainURL != "" && cfg.BrainBearer != "" && cfg.MemoryProjectID != "" {
		mb := memorybridge.New(cfg.BrainURL, cfg.BrainBearer, cfg.MemoryProjectID)
		if cfg.MemoryRecallLimit > 0 {
			mb.Limit = cfg.MemoryRecallLimit
		}
		r.Memory = mb
		logger.Info("memory bridge enabled",
			"brain", cfg.BrainURL,
			"project", cfg.MemoryProjectID,
			"limit", mb.Limit)
	}

	// NATS bus —— S11-4 后只剩 listener 订 brain session frame 用。
	// Connect 失败不阻止启动；listener 会 nil 时 user 收不到回复 + log warn。
	var natsJS bus.JetStream
	if cfg.NatsURL != "" {
		b, err := bus.Connect(cfg.NatsURL, "channels", cfg.Environment)
		if err != nil {
			logger.Warn("bus connect failed", "err", err, "url", cfg.NatsURL)
		} else {
			defer b.Close()
			logger.Info("bus connected", "url", cfg.NatsURL, "env", cfg.Environment)
			if js, jerr := b.JetStream(); jerr != nil {
				logger.Warn("JetStream init failed; agentplane listener disabled",
					"err", jerr)
			} else {
				natsJS = js
				logger.Info("JetStream wired (agentplane listener path)",
					"stream", "BIU_SESSIONS")
			}
		}
	}
	registered := 0
	if cfg.TelegramToken != "" {
		r.Register(driver.NewTelegram(cfg.TelegramToken, cfg.TelegramSecret))
		logger.Info("registered driver", "name", "telegram",
			"webhook_secret", cfg.TelegramSecret != "")
		registered++
	}
	if cfg.SlackBotToken != "" {
		r.Register(driver.NewSlack(cfg.SlackBotToken, cfg.SlackSigningSecret))
		logger.Info("registered driver", "name", "slack",
			"signing_secret", cfg.SlackSigningSecret != "")
		registered++
	}
	if cfg.DiscordBotToken != "" {
		d, err := driver.NewDiscord(cfg.DiscordBotToken, cfg.DiscordPublicKey)
		if err != nil {
			return fmt.Errorf("discord init: %w", err)
		}
		r.Register(d)
		logger.Info("registered driver", "name", "discord",
			"public_key", cfg.DiscordPublicKey != "")
		registered++
	}
	if cfg.FeishuBotToken != "" {
		r.Register(driver.NewFeishu(
			cfg.FeishuBotToken, cfg.FeishuVerificationToken, cfg.FeishuEncryptKey))
		logger.Info("registered driver", "name", "feishu",
			"verify_token", cfg.FeishuVerificationToken != "",
			"encrypt", cfg.FeishuEncryptKey != "")
		registered++
	}
	if cfg.EmailVendor != "" || cfg.EmailSMTPHost != "" {
		ev := driver.EmailVendor(cfg.EmailVendor)
		if ev == "" {
			// SMTP-only deployments still need a vendor tag so
			// VerifyAndParse rejects unsigned webhook posts cleanly.
			ev = driver.VendorMailgun
		}
		ed := driver.NewEmail(ev)
		ed.MailgunSigningKey = cfg.EmailMailgunKey
		ed.PostmarkBasicAuth = cfg.EmailPostmarkAuth
		ed.SMTPHost = cfg.EmailSMTPHost
		ed.SMTPPort = cfg.EmailSMTPPort
		ed.SMTPUsername = cfg.EmailSMTPUser
		ed.SMTPPassword = cfg.EmailSMTPPass
		ed.FromAddress = cfg.EmailFromAddress
		r.Register(ed)
		logger.Info("registered driver", "name", "email",
			"vendor", ev,
			"smtp", cfg.EmailSMTPHost != "")
		registered++
	}
	if cfg.EnableStub {
		r.Register(driver.NewStub())
		logger.Warn("registered STUB driver — tests/dev only")
		registered++
	}
	if registered == 0 {
		logger.Warn("no drivers registered; service will only serve /healthz and /v1/channels")
	}

	// S12-1: Agent Plane integration —— 把 Inbound 切到 brain Agent Plane
	// （POST /v1/agent/sessions + 订 .out → driver.Send）。失败时 Router
	// 自动降级到老 JS / HTTP 路径。
	if cfg.AgentPlaneBrainURL != "" && cfg.AgentPlaneAdminUserID != "" {
		adminUID, err := uuid.Parse(cfg.AgentPlaneAdminUserID)
		if err != nil {
			return fmt.Errorf("parse AGENT_PLANE_ADMIN_USER_ID: %w", err)
		}
		// 24h admin JWT —— 跟 runtime 同款。channels 单实例长跑 24h+ 时
		// 当前不轮换；过期后心跳/创会话会 401，运维 SIGHUP 重启。
		signer := bauth.NewSigner(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, 24*time.Hour)
		adminTok, err := signer.Sign(&bauth.Claims{UserID: adminUID.String()})
		if err != nil {
			return fmt.Errorf("sign agentplane admin token: %w", err)
		}
		apClient := chagentplane.NewClient(cfg.AgentPlaneBrainURL, adminTok, nil)

		// listener 走 channels 已经 wire 好的 JetStream（同 broker）。
		// 没 JS 时 listener 起不来，CreateTaskSession 仍会成功 → log warn
		// 让运维知道 user 收不到回复。
		var listener *chagentplane.Listener
		if natsJS != nil {
			listener = chagentplane.NewListener(natsJS, driverMap(r), logger)
			logger.Info("agentplane listener enabled (subscribes to brain session out subjects)")
		} else {
			logger.Warn("agentplane listener disabled (no JetStream wired); replies won't reach users")
		}

		r.AgentPlane = &router.AgentPlaneIntegration{
			CreateTaskSession: func(ctx context.Context, req router.AgentPlaneCreateReq) (string, error) {
				resp, err := apClient.CreateTaskSession(ctx, chagentplane.CreateTaskSessionReq{
					Prompt:       req.Prompt,
					Model:        req.Model,
					SystemPrompt: req.SystemPrompt,
					PoolTag:      firstNonEmptyChannelStr(req.PoolTag, cfg.AgentPlanePoolTag),
					ThreadID:     req.ThreadID,
				})
				if err != nil {
					return "", err
				}
				return resp.SessionID.String(), nil
			},
			SubscribeAndReply: func(ctx context.Context, sessionIDStr string, reply router.AgentPlaneReply) error {
				if listener == nil {
					return fmt.Errorf("agentplane listener not available (no JetStream)")
				}
				sid, err := uuid.Parse(sessionIDStr)
				if err != nil {
					return err
				}
				_, err = listener.SubscribeAndReply(ctx, sid, chagentplane.ReplyContext{
					Channel:        reply.Channel,
					ConversationID: reply.ConversationID,
					ReplyTo:        reply.ReplyTo,
					Recipient:      reply.Recipient,
				})
				return err
			},
		}
		logger.Info("agentplane integration enabled",
			"brain", cfg.AgentPlaneBrainURL, "pool_tag", cfg.AgentPlanePoolTag)
	} else {
		logger.Info("agentplane integration disabled (set AGENT_PLANE_BRAIN_URL + AGENT_PLANE_ADMIN_USER_ID)")
	}

	verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	apiSrv := api.NewServer(r, verifier, logger)

	hz := bhealth.New(serviceName, serviceVersion, schemaVersion)
	hz.SetReady(true)

	mux := http.NewServeMux()
	hz.Mount(mux)
	apiSrv.Mount(mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
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

	logger.Info("channels listening", "addr", cfg.ListenAddr,
		"runtime", cfg.RuntimeURL, "drivers", r.Routes())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
