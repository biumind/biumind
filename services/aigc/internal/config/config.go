// Package config holds aigc service configuration loaded via biu/config from env.
package config

import "time"

type Config struct {
	// 服务监听
	ListenAddr  string `env:"LISTEN_ADDR" default:":7011"`
	Environment string `env:"BIUMIND_ENV" default:"dev"`
	LogLevel    string `env:"BIUMIND_LOG_LEVEL" default:"info"`

	// OTel
	OtlpEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" default:""`

	// JWT 鉴权（与 biumind 其他服务保持同一份签发）
	// 优先 JWKS（RS256 from identity）；JWTSecret 是 dev/test fallback (HS256)。
	JWTSecret       string `env:"JWT_SECRET" required:"true"`
	JWTIssuer       string `env:"JWT_ISSUER" default:"https://identity.biumind.local"`
	JWTAudience     string `env:"JWT_AUDIENCE" default:"biumind-api"`
	IdentityJWKSURL string `env:"IDENTITY_JWKS_URL" default:""`

	// PG 主库（aigc.* schema）。空 = 跳过 DB 初始化（仅启动 /healthz 探活，便于 e2e）。
	DatabaseURL   string `env:"AIGC_DATABASE_URL"   default:""`
	MigrationsDir string `env:"AIGC_MIGRATIONS_DIR" default:"services/aigc/migrations"`

	// Identity 服务（积分扣减 / 退款 / 余额查询）。
	// 形如 http://identity:7000；空 = 走 stub（dev 模式）。
	IdentityURL string `env:"AIGC_IDENTITY_URL" default:""`

	// Identity 内部 RPC 共享 bearer (与 identity 服务 IDENTITY_INTERNAL_TOKEN 一致).
	// 空时 billing 完全禁用，POST /v1/generations 返 503.
	InternalToken string `env:"IDENTITY_INTERNAL_TOKEN" default:""`

	// model-relay 服务 URL (P4.S1.3). 用于 /v1/internal/credentials/{id}/get-decrypted
	// 解密 platform 凭证. 空 → 跳过 resolver, worker 走 env fallback.
	ModelRelayURL string `env:"AIGC_MODEL_RELAY_URL" default:""`

	// Authz Cedar 决策服务. 空时退化到 AlwaysAllow (dev) + 启动 warning.
	AuthzURL string `env:"AUTHZ_URL" default:""`

	// NATS（任务下发 + 进度回推）
	NATSURL string `env:"NATS_URL" default:""`

	// MinIO / S3 兼容存储
	S3Endpoint  string `env:"AIGC_S3_ENDPOINT"   default:""`
	S3AccessKey string `env:"AIGC_S3_ACCESS_KEY" default:""`
	S3SecretKey string `env:"AIGC_S3_SECRET_KEY" default:""`
	S3Region    string `env:"AIGC_S3_REGION"     default:"us-east-1"`
	S3UseSSL    bool   `env:"AIGC_S3_USE_SSL"    default:"false"`

	// 桶名（5 个，详见 BiuMind-AIGC-Storage-Design.md §4）
	BucketUploads     string `env:"AIGC_BUCKET_UPLOADS"     default:"biumind-aigc-uploads"`
	BucketOutputs     string `env:"AIGC_BUCKET_OUTPUTS"     default:"biumind-aigc-outputs"`
	BucketDerivatives string `env:"AIGC_BUCKET_DERIVATIVES" default:"biumind-aigc-derivatives"`
	BucketPublic      string `env:"AIGC_BUCKET_PUBLIC"      default:"biumind-aigc-public"`
	BucketTemp        string `env:"AIGC_BUCKET_TEMP"        default:"biumind-aigc-temp"`

	// 上游 provider key（worker 用；服务也保留是为了 admin 配置回写）
	DashscopeAPIKey string `env:"DASHSCOPE_API_KEY"  default:""`
	VolcengineAK    string `env:"VOLCENGINE_AK"      default:""`
	VolcengineSK    string `env:"VOLCENGINE_SK"      default:""`

	// 限制
	MaxRequestBodyBytes int64         `env:"AIGC_MAX_REQ_BODY" default:"10485760"` // 10 MiB
	HTTPReadTimeout     time.Duration `env:"AIGC_HTTP_READ_TIMEOUT"  default:"30s"`
	HTTPWriteTimeout    time.Duration `env:"AIGC_HTTP_WRITE_TIMEOUT" default:"60s"`
}
