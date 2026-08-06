// store.go — identity.user_api_keys 仓储 (加密读写 + 状态管理).
//
// 公开 list / get 永远不返明文; 只有 GetDecrypted 才解密 (model-relay /
// aigc-worker 内部调用走 service-to-service, 不暴露给客户端).

package byok

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Sentinel errors ──────────────────────────────────

var (
	ErrKeyNotFound            = errors.New("byok: key not found for user/provider")
	ErrInvalidProvider        = errors.New("byok: provider not in allowed list")
	ErrEmptyPlaintext         = errors.New("byok: api key plaintext must not be empty")
	ErrFailureLimitHit        = errors.New("byok: failure_count >= 5, marked invalid")
	ErrCustomRequiresEndpoint = errors.New("byok: custom provider requires base_url")
	ErrCustomRequiresModels   = errors.New("byok: custom provider requires model_globs")
)

// ValidProviders 与 migrations/00020 + 00033 的 CHECK 约束对齐 (00033 加 'custom').
var ValidProviders = map[string]struct{}{
	"anthropic":    {},
	"openai":       {},
	"deepseek":     {},
	"doubao":       {},
	"dashscope":    {},
	"volcengine":   {},
	"google":       {},
	"azure_openai": {},
	"moonshot":     {},
	"qwen":         {},
	"baichuan":     {},
	"custom":       {},
}

// ─── Domain types ─────────────────────────────────────

type Status string

const (
	StatusValid   Status = "valid"
	StatusInvalid Status = "invalid"
	StatusRevoked Status = "revoked"
	StatusExpired Status = "expired"
)

// PublicEntry — 客户端可见的字段; 不含明文密钥.
type PublicEntry struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	Provider        string     `json:"provider"`
	Label           string     `json:"label,omitempty"`
	Last4           string     `json:"last4,omitempty"`
	ConfigJSON      []byte     `json:"config,omitempty"` // jsonb raw
	Status          Status     `json:"status"`
	LastValidatedAt *time.Time `json:"last_validated_at,omitempty"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	FailureCount    int        `json:"failure_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	// 00033: custom/代理 endpoint + 协议. protocol 空 = openai_compat.
	// P1 让 model-relay 用 protocol 选 adaptor (替代模型名前缀猜测).
	BaseURL  string `json:"base_url,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	// 00034: custom 声明所用模型 (glob 通配). model-relay 按 model 匹配 custom 记录.
	ModelGlobs []string `json:"model_globs,omitempty"`
	// 00035: client-side BYOK 标记. 语义 = 「需本机出口」(relay 连不到的上游,
	// 如内网 proxy). key 仍加密存 identity, 桌面 daemon 取 key 本机直连;
	// relay/internalapi 走 GetDecrypted/MatchCustomByModel 的 WHERE 过滤仍跳过
	// 此类行 (relay 调无意义 + 保 BYOK 计费隔离).
	IsClientSide bool `json:"is_client_side,omitempty"`
}

// ─── Store ────────────────────────────────────────────

type Store struct {
	pool   *pgxpool.Pool
	cipher *Cipher
}

func NewStore(pool *pgxpool.Pool, cipher *Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

// UpsertArgs 写 / 覆盖一把 key. 新建必须提供 Plaintext; 编辑 (Plaintext 空)
// 保留原加密值不改 key. server/client-side 都加密存 identity, client-side 仅
// 标记「需本机出口」.
type UpsertArgs struct {
	UserID       uuid.UUID
	Provider     string
	Plaintext    string // 明文 API key, 加密后落库; 空 = 编辑不改 key (保留原加密值)
	Label        string
	ConfigJSON   []byte   // 可空 jsonb (Azure endpoint / region 等), 留 nil 视作 '{}'
	BaseURL      string   // 00033: custom 必填 (代理地址); 标准 provider 空 → 走默认 endpoint
	Protocol     string   // 00033: openai_compat(默认)/anthropic/google/dashscope/volcengine
	ModelGlobs   []string // 00034: custom 必填 (所用模型 glob); 标准 provider 空
	IsClientSide bool     // 00035: true = 需本机出口 (内网 proxy 等). key 仍加密存 identity.
}

// Upsert — 覆盖已有 key; status 重置为 'valid' (新 key 当作健康, validator
// 异步校正). 标准 provider 按 (user_id, provider) 唯一; custom 按
// (user_id, base_url) 唯一 (00033 两个 partial unique index, 谓词互斥).
// ON CONFLICT target 按 provider 显式选匹配 index 的列 + 谓词 (DO UPDATE
// 不支持无 target, 见下方 conflictTarget).
func (s *Store) Upsert(ctx context.Context, a UpsertArgs) (*PublicEntry, error) {
	if _, ok := ValidProviders[a.Provider]; !ok {
		return nil, ErrInvalidProvider
	}
	// custom provider 校验 (无论新建/编辑, custom 必须有 base_url + model_globs).
	if a.Provider == "custom" {
		if strings.TrimSpace(a.BaseURL) == "" {
			return nil, ErrCustomRequiresEndpoint
		}
		if len(a.ModelGlobs) == 0 {
			return nil, ErrCustomRequiresModels
		}
	}
	protocol := a.Protocol
	if protocol == "" {
		protocol = "openai_compat"
	}
	cfg := a.ConfigJSON
	if len(cfg) == 0 {
		cfg = []byte(`{}`)
	}
	globs := a.ModelGlobs
	if globs == nil {
		globs = []string{}
	}

	now := time.Now()
	var ct, nonce []byte
	var last4 string
	if a.Plaintext == "" {
		// 编辑不改 key: 复用既有行的加密值 (ON CONFLICT 命中行). 无既有行
		// (新建) → selectExistingCipher 返 ErrEmptyPlaintext (新建必须 key).
		var err error
		ct, nonce, last4, err = s.selectExistingCipher(ctx, a)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		ct, nonce, err = s.cipher.Encrypt([]byte(a.Plaintext))
		if err != nil {
			return nil, fmt.Errorf("encrypt: %w", err)
		}
		last4 = Last4(a.Plaintext)
	}

	// ON CONFLICT target 必须显式 (DO UPDATE 不支持无 target 的 arbiter 自动
	// 选择 —— 42601). 按 provider 选匹配的 partial unique index (00035 各按
	// is_client_side 拆 2 条, 方案 I: 同 provider 可 server + client-side 两行):
	//   * 标准 provider (provider <> 'custom') → (user_id, provider) 唯一
	//   * custom                            → (user_id, base_url) 唯一
	// target 的 WHERE 必须精确匹配 index predicate (含 is_client_side 字面值),
	// 否则 PG 不认 (inference mismatch). 字面 true/false, 非列名.
	side := "false"
	if a.IsClientSide {
		side = "true"
	}
	conflictTarget := "ON CONFLICT (user_id, base_url) WHERE provider = 'custom' AND is_client_side = " + side
	if a.Provider != "custom" {
		conflictTarget = "ON CONFLICT (user_id, provider) WHERE provider <> 'custom' AND is_client_side = " + side
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO identity.user_api_keys
		    (user_id, provider, label, encrypted_value, nonce, last4,
		     config_json, base_url, protocol, model_globs, status,
		     failure_count, created_at, updated_at, is_client_side)
		VALUES ($1, $2, NULLIF($3,''), $4, $5, $6, $7::jsonb,
		        NULLIF($8,''), $9, $10, 'valid', 0, $11, $11, $12)
		`+conflictTarget+` DO UPDATE SET
		    label           = EXCLUDED.label,
		    encrypted_value = EXCLUDED.encrypted_value,
		    nonce           = EXCLUDED.nonce,
		    last4           = EXCLUDED.last4,
		    config_json     = EXCLUDED.config_json,
		    base_url        = EXCLUDED.base_url,
		    protocol        = EXCLUDED.protocol,
		    model_globs     = EXCLUDED.model_globs,
		    status          = 'valid',
		    failure_count   = 0,
		    last_validated_at = NULL,
		    updated_at      = EXCLUDED.updated_at
		RETURNING id, user_id, provider, COALESCE(label, ''), COALESCE(last4, ''),
		          config_json, status, last_validated_at, last_used_at,
		          failure_count, created_at, updated_at,
		          COALESCE(base_url, ''), protocol, model_globs, is_client_side
	`, a.UserID, a.Provider, a.Label, ct, nonce, last4, string(cfg),
		a.BaseURL, protocol, globs, now, a.IsClientSide)

	return scanPublic(row)
}

// selectExistingCipher — 编辑场景 (Plaintext 空) 复用既有行的加密值, 不改 key.
// 按 conflict key (standard: user_id+provider / custom: user_id+base_url) +
// is_client_side 定位唯一行. 无既有行 (新建) → ErrEmptyPlaintext (新建必须 key).
func (s *Store) selectExistingCipher(ctx context.Context, a UpsertArgs) ([]byte, []byte, string, error) {
	var ct, nonce []byte
	var last4 string
	var err error
	if a.Provider == "custom" {
		err = s.pool.QueryRow(ctx, `
			SELECT encrypted_value, nonce, COALESCE(last4, '')
			FROM identity.user_api_keys
			WHERE user_id=$1 AND provider='custom' AND base_url=$2 AND is_client_side=$3
		`, a.UserID, a.BaseURL, a.IsClientSide).Scan(&ct, &nonce, &last4)
	} else {
		err = s.pool.QueryRow(ctx, `
			SELECT encrypted_value, nonce, COALESCE(last4, '')
			FROM identity.user_api_keys
			WHERE user_id=$1 AND provider=$2 AND is_client_side=$3
		`, a.UserID, a.Provider, a.IsClientSide).Scan(&ct, &nonce, &last4)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, "", ErrEmptyPlaintext
	}
	if err != nil {
		return nil, nil, "", err
	}
	return ct, nonce, last4, nil
}

// GetPublic 单个 (user, provider) 的 server BYOK 公开视图. 不返明文.
// 方案 I 下同 (user, provider) 可能有 server + client-side 两行; 本方法只
// 查 server 行 (is_client_side=false), client-side 元数据查 ListPublic.
func (s *Store) GetPublic(ctx context.Context, userID uuid.UUID, provider string) (*PublicEntry, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, provider, COALESCE(label,''), COALESCE(last4,''),
		       config_json, status, last_validated_at, last_used_at,
		       failure_count, created_at, updated_at,
		       COALESCE(base_url, ''), protocol, model_globs, is_client_side
		FROM identity.user_api_keys
		WHERE user_id = $1 AND provider = $2 AND is_client_side = false
	`, userID, provider)
	e, err := scanPublic(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKeyNotFound
	}
	return e, err
}

// ListPublic 列出 user 全部 provider 公开视图 (含 invalid / revoked, 用户可见).
func (s *Store) ListPublic(ctx context.Context, userID uuid.UUID) ([]*PublicEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, provider, COALESCE(label,''), COALESCE(last4,''),
		       config_json, status, last_validated_at, last_used_at,
		       failure_count, created_at, updated_at,
		       COALESCE(base_url, ''), protocol, model_globs, is_client_side
		FROM identity.user_api_keys
		WHERE user_id = $1
		ORDER BY provider
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PublicEntry
	for rows.Next() {
		e, err := scanPublic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetDecrypted — 仅供 model-relay / aigc-worker 等内部服务通过 internalapi
// 调用. 返 (plaintext, configJSON, baseURL, protocol, error). 状态非 valid
// 视作 ErrKeyNotFound. 注意: custom provider 同 user 多 base_url 场景, 此处
// 按 (user, provider='custom') 命中第一行 —— custom 的精确路由留给 P1
// (model-relay 按 modelName→记录 映射, 当前 P0 单 custom 场景已可用).
func (s *Store) GetDecrypted(ctx context.Context, userID uuid.UUID, provider string) (string, []byte, string, string, error) {
	var ct, nonce, cfg []byte
	var status, baseURL, protocol string
	// WHERE is_client_side = false: relay 走 internalapi 调本方法, 必须永远
	// 看不到 client-side 记录 (其 encrypted_value 空占位, 解密无意义且泄漏).
	err := s.pool.QueryRow(ctx, `
		SELECT encrypted_value, nonce, config_json, status,
		       COALESCE(base_url, ''), protocol
		FROM identity.user_api_keys
		WHERE user_id = $1 AND provider = $2 AND is_client_side = false
	`, userID, provider).Scan(&ct, &nonce, &cfg, &status, &baseURL, &protocol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, "", "", ErrKeyNotFound
		}
		return "", nil, "", "", err
	}
	if Status(status) != StatusValid {
		return "", nil, "", "", ErrKeyNotFound
	}
	pt, err := s.cipher.Decrypt(ct, nonce)
	if err != nil {
		return "", nil, "", "", err
	}
	return string(pt), cfg, baseURL, protocol, nil
}

// GetDecryptedByID — 按 record id 取明文 (仅 is_client_side=true 行).
// 供 user JWT 端点 (GET /me/api-keys/{id}/credentials) 给桌面 daemon / 端侧
// _test 取 key 本机直连. server BYOK 行 (is_client_side=false) 不走此路径
// (仍只准 internal token), 避免双鉴权都能取同一 key 扩攻击面. owner-scoped:
// userID 必须匹配. 状态非 valid 视作 ErrKeyNotFound.
func (s *Store) GetDecryptedByID(ctx context.Context, userID, recordID uuid.UUID) (string, []byte, string, string, []string, error) {
	var ct, nonce, cfg []byte
	var status, baseURL, protocol string
	var globs []string
	err := s.pool.QueryRow(ctx, `
		SELECT encrypted_value, nonce, config_json, status,
		       COALESCE(base_url, ''), protocol, model_globs
		FROM identity.user_api_keys
		WHERE id=$1 AND user_id=$2 AND is_client_side=true
	`, recordID, userID).Scan(&ct, &nonce, &cfg, &status, &baseURL, &protocol, &globs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, "", "", nil, ErrKeyNotFound
		}
		return "", nil, "", "", nil, err
	}
	if Status(status) != StatusValid {
		return "", nil, "", "", nil, ErrKeyNotFound
	}
	pt, err := s.cipher.Decrypt(ct, nonce)
	if err != nil {
		return "", nil, "", "", nil, err
	}
	return string(pt), cfg, baseURL, protocol, globs, nil
}

// Revoke — 标记 status='revoked'. 不删 row (审计保留). isClientSide 精确指定
// 删 server(false) 还是 client-side(true) 行 —— 方案 I 下同 provider 可能两行,
// 必须按模式精确删, 否则误删另一模式. id 非空时进一步按 record id 精确删单条
// (custom client-side 多 base_url 场景: 同 provider 多行, 按 id 删才不误伤其余).
// id 为空退原批删行为 (server-side standard 单行场景向后兼容).
func (s *Store) Revoke(ctx context.Context, userID uuid.UUID, provider string, isClientSide bool, id *uuid.UUID) error {
	if id != nil {
		tag, err := s.pool.Exec(ctx, `
			UPDATE identity.user_api_keys
			SET status = 'revoked', updated_at = now()
			WHERE user_id = $1 AND provider = $2 AND is_client_side = $3 AND id = $4 AND status <> 'revoked'
		`, userID, provider, isClientSide, *id)
		if err != nil {
			return err
		}
		_ = tag // 0 行 (已 revoke / 不存在) 视作幂等成功.
		return nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.user_api_keys
		SET status = 'revoked', updated_at = now()
		WHERE user_id = $1 AND provider = $2 AND is_client_side = $3 AND status <> 'revoked'
	`, userID, provider, isClientSide)
	if err != nil {
		return err
	}
	_ = tag
	return nil
}

// IncrementFailure — resolver 调上游失败时调. ≥5 自动 invalid, 返 true.
func (s *Store) IncrementFailure(ctx context.Context, userID uuid.UUID, provider string) (bool, error) {
	const maxFailures = 5
	var newCount int
	var newStatus string
	err := s.pool.QueryRow(ctx, `
		UPDATE identity.user_api_keys
		SET failure_count = failure_count + 1,
		    status = CASE WHEN failure_count + 1 >= $3 AND status = 'valid' THEN 'invalid' ELSE status END,
		    updated_at = now()
		WHERE user_id = $1 AND provider = $2
		RETURNING failure_count, status
	`, userID, provider, maxFailures).Scan(&newCount, &newStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrKeyNotFound
		}
		return false, err
	}
	autoInvalid := Status(newStatus) == StatusInvalid && newCount == maxFailures
	return autoInvalid, nil
}

// MarkValidated — validator ping 完成后调. valid=true 重置 failure_count.
func (s *Store) MarkValidated(ctx context.Context, userID uuid.UUID, provider string, valid bool) error {
	now := time.Now()
	var newStatus Status = StatusValid
	if !valid {
		newStatus = StatusInvalid
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.user_api_keys
		SET status            = $3,
		    failure_count     = CASE WHEN $3::text = 'valid' THEN 0 ELSE failure_count END,
		    last_validated_at = $4,
		    updated_at        = $4
		WHERE user_id = $1 AND provider = $2 AND status <> 'revoked'
	`, userID, provider, string(newStatus), now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrKeyNotFound
	}
	return nil
}

// TouchUsed — resolver 命中时异步打点. 写 last_used_at; 不影响 status.
func (s *Store) TouchUsed(ctx context.Context, userID uuid.UUID, provider string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.user_api_keys
		SET last_used_at = now()
		WHERE user_id = $1 AND provider = $2
	`, userID, provider)
	return err
}

// ─── helpers ──────────────────────────────────────────

func scanPublic(row pgx.Row) (*PublicEntry, error) {
	var e PublicEntry
	var status string
	if err := row.Scan(
		&e.ID, &e.UserID, &e.Provider, &e.Label, &e.Last4,
		&e.ConfigJSON, &status, &e.LastValidatedAt, &e.LastUsedAt,
		&e.FailureCount, &e.CreatedAt, &e.UpdatedAt,
		&e.BaseURL, &e.Protocol, &e.ModelGlobs, &e.IsClientSide,
	); err != nil {
		return nil, err
	}
	e.Status = Status(status)
	return &e, nil
}

// MatchCustomByModel — model-relay CredsResolver 在 catalog 失败时调:
// 按 model 匹配用户的 custom BYOK 记录. 拉所有 valid custom → Go 里按
// model_globs 匹配 → 返首个命中的明文 + endpoint + 协议. 无命中 → ErrKeyNotFound.
// 解耦: 匹配逻辑在 Go (不依赖 PG LIKE), 边界可控.
func (s *Store) MatchCustomByModel(ctx context.Context, userID uuid.UUID, model string) (string, []byte, string, string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT encrypted_value, nonce, config_json,
		       COALESCE(base_url, ''), protocol, model_globs
		FROM identity.user_api_keys
		WHERE user_id = $1 AND provider = 'custom' AND status = 'valid'
		          AND is_client_side = false
		ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return "", nil, "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var ct, nonce, cfg []byte
		var baseURL, protocol string
		var globs []string
		if err := rows.Scan(&ct, &nonce, &cfg, &baseURL, &protocol, &globs); err != nil {
			return "", nil, "", "", err
		}
		if !globMatch(globs, model) {
			continue
		}
		pt, derr := s.cipher.Decrypt(ct, nonce)
		if derr != nil {
			return "", nil, "", "", derr
		}
		return string(pt), cfg, baseURL, protocol, nil
	}
	if err := rows.Err(); err != nil {
		return "", nil, "", "", err
	}
	return "", nil, "", "", ErrKeyNotFound
}

// globMatch — model 是否匹配任一 glob pattern.
//
//	'*'      → 匹配任意
//	'glm-*'  → HasPrefix 'glm-'
//	'gpt-4o' → 精确
func globMatch(globs []string, model string) bool {
	for _, g := range globs {
		switch {
		case g == "*":
			return true
		case strings.HasSuffix(g, "*"):
			if strings.HasPrefix(model, strings.TrimSuffix(g, "*")) {
				return true
			}
		default:
			if g == model {
				return true
			}
		}
	}
	return false
}
