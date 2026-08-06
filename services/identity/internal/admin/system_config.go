// system_config — 后台 KV 配置 (SMTP / feature flags / Stripe key etc).
//
// 设计:
//   - schema 是 jsonb, 结构由调用方约定 (key='alert.email' → SMTP 对象)
//   - secret=true 的 value 在 GET 时按 RBAC 脱敏 (非 superadmin 看 ***)
//   - 改值必须 superadmin, 写 audit
//
// 不放 env 不放配置文件的理由:
//   - 改邮箱不用 SSH 改文件 + 重启容器
//   - 改动有 audit (谁改的什么时候)
//   - 多人协作时不会丢同步

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/mailer"
)

// SystemConfigStore — DB 操作.
type SystemConfigStore struct {
	pool *pgxpool.Pool
}

func NewSystemConfigStore(pool *pgxpool.Pool) *SystemConfigStore {
	return &SystemConfigStore{pool: pool}
}

type SystemConfigEntry struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	Secret      bool            `json:"secret"`
	Description string          `json:"description"`
	// UpdatedAt 走 time.Time + JSON 自动 RFC3339 序列化. pgx binary protocol
	// 下 timestamptz 不能直接 scan 进 *string, 必须用 time.Time.
	UpdatedAt time.Time `json:"updated_at"`
}

// Get 拿单条配置. 不存在返 nil, nil (非错).
func (s *SystemConfigStore) Get(ctx context.Context, key string) (*SystemConfigEntry, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT key, value, secret, description, updated_at
		FROM biumind_system.config WHERE key = $1
	`, key)
	var e SystemConfigEntry
	var rawValue []byte
	err := row.Scan(&e.Key, &rawValue, &e.Secret, &e.Description, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.Value = rawValue
	return &e, nil
}

// List 全部 key.
func (s *SystemConfigStore) List(ctx context.Context) ([]SystemConfigEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT key, value, secret, description, updated_at
		FROM biumind_system.config ORDER BY key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SystemConfigEntry
	for rows.Next() {
		var e SystemConfigEntry
		var rawValue []byte
		if err := rows.Scan(&e.Key, &rawValue, &e.Secret, &e.Description, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Value = rawValue
		out = append(out, e)
	}
	return out, rows.Err()
}

// Set upsert. value 必须是合法 JSON. updatedBy 写 audit.
func (s *SystemConfigStore) Set(ctx context.Context, key string, value json.RawMessage, updatedBy string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO biumind_system.config (key, value, updated_by, updated_at)
		VALUES ($1, $2, $3::uuid, now())
		ON CONFLICT (key) DO UPDATE
		  SET value = EXCLUDED.value,
		      updated_at = now(),
		      updated_by = EXCLUDED.updated_by
	`, key, []byte(value), nullableUUID(updatedBy))
	return err
}

func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ─── HTTP handlers ────────────────────────────────────

func (s *Server) handleSystemConfigList(w http.ResponseWriter, r *http.Request) {
	if s.SystemConfig == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configs": []any{}})
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	isSuper := hasAnyRole(claims, "superadmin")
	entries, err := s.SystemConfig.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// 非 superadmin 看到 secret value 全脱敏
	for i := range entries {
		if entries[i].Secret && !isSuper {
			entries[i].Value = json.RawMessage(`{"_redacted":true}`)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": entries})
}

type setConfigReq struct {
	Value json.RawMessage `json:"value"`
}

func (s *Server) handleSystemConfigSet(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	if !hasAnyRole(claims, "superadmin") {
		writeErr(w, http.StatusForbidden, "forbidden", "system:write requires superadmin")
		return
	}
	if s.SystemConfig == nil {
		writeErr(w, http.StatusServiceUnavailable, "system_config_disabled", "")
		return
	}
	key := r.PathValue("key")
	var req setConfigReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !json.Valid(req.Value) {
		writeErr(w, http.StatusBadRequest, "invalid_json", "value must be valid JSON")
		return
	}
	actor := actorID(r)
	if err := s.SystemConfig.Set(r.Context(), key, req.Value, actor); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.Audit.Append(AuditEvent{
		ActorID:    actor,
		ActorIP:    ClientIP(r),
		ActorUA:    r.UserAgent(),
		Action:     "system.config.update",
		Resource:   "system_config",
		Target:     key,
		TargetType: "config_key",
		Detail:     fmt.Sprintf("key=%s", key),
		Success:    true,
	})
	writeJSON(w, http.StatusOK, map[string]any{"updated": key})
}

// ─── Test email ───────────────────────────────────────
//
// POST /v1/admin/system/test-email
//
// 让 superadmin 在保存配置前 / 后随时验证 SMTP 凭据是否能投递. 收件人
// 由请求指定 (alert.email 默认 to[0]; auth.email 由用户填). 字段缺失时
// 按 key 回退到 system_config 的存值 (尤其 smtp_pass — 前端密码框留空保
// 持不变, 这里也跟着 fallback).

type testEmailReq struct {
	// Key 可选: 'alert.email' / 'auth.email'. 提供后, 任何空字段 fallback
	// 到该 key 的存值. 不提供则要求 body 自带完整 SMTP.
	Key      string `json:"key"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	SMTPUser string `json:"smtp_user"`
	SMTPPass string `json:"smtp_pass"`
	SMTPTLS  *bool  `json:"smtp_tls"` // 指针区分"用户没传"和"用户传 false"
	From     string `json:"from"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
}

func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	if !hasAnyRole(claims, "superadmin") {
		writeErr(w, http.StatusForbidden, "forbidden", "test-email requires superadmin")
		return
	}
	if s.SystemConfig == nil {
		writeErr(w, http.StatusServiceUnavailable, "system_config_disabled", "")
		return
	}

	var req testEmailReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// fallback to stored config when fields are blank.
	stored, _ := loadStoredSMTP(r.Context(), s.SystemConfig, req.Key)
	cfg := mergeSMTPCfg(req, stored)

	to := strings.TrimSpace(req.To)
	if to == "" {
		// alert.email 兜底: 用 stored.to[0]
		if len(stored.To) > 0 {
			to = stored.To[0]
		}
	}
	if to == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "to (recipient) required")
		return
	}
	if cfg.Host == "" || cfg.From == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "smtp_host / from required")
		return
	}

	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		subject = "[BiuMind] SMTP 测试"
	}
	body := `<div style="font-family:sans-serif"><h3>BiuMind SMTP 配置测试</h3>` +
		`<p>这是一封测试邮件, 用于确认 SMTP 凭据可用. 收到此邮件即说明配置正确.</p>` +
		`<p style="color:#888;font-size:12px">由 admin /v1/admin/system/test-email 触发.</p></div>`

	actor := actorID(r)
	if err := mailer.Send(cfg, []string{to}, subject, body); err != nil {
		s.Audit.Append(AuditEvent{
			ActorID: actor, ActorIP: ClientIP(r), ActorUA: r.UserAgent(),
			Action: "system.email_test", Resource: "system_config",
			Target: req.Key, TargetType: "config_key",
			Success: false, ErrorCode: "smtp_error", ErrorMessage: err.Error(),
			Detail: fmt.Sprintf("to=%s host=%s", to, cfg.Host),
		})
		writeErr(w, http.StatusBadGateway, "smtp_error", err.Error())
		return
	}
	s.Audit.Append(AuditEvent{
		ActorID: actor, ActorIP: ClientIP(r), ActorUA: r.UserAgent(),
		Action: "system.email_test", Resource: "system_config",
		Target: req.Key, TargetType: "config_key",
		Success: true,
		Detail:  fmt.Sprintf("to=%s host=%s", to, cfg.Host),
	})
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": to})
}

// storedSMTP — 从 system_config 一条 entry 反序列化后的 SMTP 字段集合,
// alert.email + auth.email 共用. To 仅 alert.email 有值, auth.email 为空.
type storedSMTP struct {
	Host string
	Port int
	User string
	Pass string
	TLS  bool
	From string
	To   []string
}

func loadStoredSMTP(ctx context.Context, cfg *SystemConfigStore, key string) (storedSMTP, error) {
	if key == "" || cfg == nil {
		return storedSMTP{}, nil
	}
	entry, err := cfg.Get(ctx, key)
	if err != nil || entry == nil {
		return storedSMTP{}, err
	}
	var raw struct {
		SMTPHost string   `json:"smtp_host"`
		SMTPPort int      `json:"smtp_port"`
		SMTPUser string   `json:"smtp_user"`
		SMTPPass string   `json:"smtp_pass"`
		SMTPTLS  bool     `json:"smtp_tls"`
		From     string   `json:"from"`
		To       []string `json:"to"`
	}
	if err := json.Unmarshal(entry.Value, &raw); err != nil {
		return storedSMTP{}, err
	}
	return storedSMTP{
		Host: raw.SMTPHost, Port: raw.SMTPPort, User: raw.SMTPUser,
		Pass: raw.SMTPPass, TLS: raw.SMTPTLS, From: raw.From, To: raw.To,
	}, nil
}

// mergeSMTPCfg — body 字段优先, 空字段从 stored 兜底.
func mergeSMTPCfg(req testEmailReq, stored storedSMTP) mailer.SMTPConfig {
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	out := mailer.SMTPConfig{
		Host: pick(req.SMTPHost, stored.Host),
		User: pick(req.SMTPUser, stored.User),
		Pass: pick(req.SMTPPass, stored.Pass),
		From: pick(req.From, stored.From),
	}
	if req.SMTPPort > 0 {
		out.Port = req.SMTPPort
	} else {
		out.Port = stored.Port
	}
	if req.SMTPTLS != nil {
		out.TLS = *req.SMTPTLS
	} else {
		out.TLS = stored.TLS
	}
	return out
}
