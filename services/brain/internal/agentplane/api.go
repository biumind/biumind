// Agent Plane HTTP API（S3-2）—— environments CRUD。
//
// 路由：
//   POST   /v1/agent/environments              注册
//   POST   /v1/agent/environments/{id}/heartbeat  续租
//   GET    /v1/agent/environments              列出当前用户的 environments
//   DELETE /v1/agent/environments/{id}         注销
//
// 鉴权：所有端点走 Bearer JWT，user_id 严格匹配（store 层做最终匹配）。
//
// **不做的**：
//   - environment_secret 颁发 —— S3-4 X25519 上线后单独考虑；当前 user JWT
//     就是 environment 的隐式凭证（worker_kind=biu_daemon 是用户机器；
//     runtime 是 K8s pod 用 service account JWT）
//   - 鉴权 PAT vs 短期 JWT 的区分 —— 复用 services/brain 现有 bauth.Verifier
//
// 后续 stage 会继续在这个文件加路由（sessions、session_results 等）。

package agentplane

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/agentcrypto"
	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

type Server struct {
	Store    *Store
	Verifier *bauth.Verifier
	Signer   *bauth.Signer // 用于签发 session_token；nil 时 refresh 端点禁用
	// Queue 用于 agent/task mode 创建 session 时投递 work；nil 时这两条
	// 路径仍写 session 行 + 回 session_token，但不实际派发任务（dev / 测试）。
	// 生产部署 queue 必填。
	Queue *Queue
	// Ingress 用于 WS session stream（S3-5）；nil 时该路由不挂（dev 友好）。
	Ingress *Ingress
	// ChatRunner 在 mode=chat 创建 session 时进程内驱动 biumindkit。nil 时
	// chat 创建仍写 session 行 + 回 token 但不实际跑 LLM（dev 默认）。
	// 生产部署需要 wire 平台 Anthropic API key + 工具注册表。
	ChatRunner *ChatRunner
	// KeyResolver 现取用户明文 key + endpoint (P3: 来自 identity, brain 不
	// 再存 key_vaults_encrypted)。可空(测试 / dev / 未配 IDENTITY_URL),空
	// 时 ResolveBYOKCreds 不走 BYOK → agent/task 走平台兜底。生产 wired 是
	// *providerspkg.IdentityBYOKClient。
	KeyResolver BYOKKeyResolver
	// ChatStore 让 WS chat / agent 路径把对话轮落库到 chat.messages 并服务端
	// 组装多轮历史(brain 作为真相源,Runtime v3 §8.2 翻案)。可空(dev /
	// 无持久化)→ 退化为不落库 + 单轮(向后兼容)。
	ChatStore *chatpkg.Store
	// Transcript 累积 assistant 文本并在 turn 终止时落 assistant 轮。可空。
	Transcript *TranscriptRecorder
	Logger     *slog.Logger

	// workerAcks 是 S3-8 worker poll 端点的 ack-token → fetched work 索引。
	// MountWorkerRoutes 时按需 lazy 初始化。
	workerAcks *ackRegistry
}

// NewServer 构造一个 agent plane HTTP server。signer / queue / ingress 可空
// （详见各字段注释）。生产部署都填上。
func NewServer(store *Store, v *bauth.Verifier, signer *bauth.Signer, queue *Queue, ingress *Ingress, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		Store: store, Verifier: v, Signer: signer,
		Queue: queue, Ingress: ingress, Logger: logger,
	}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST   /v1/agent/environments", s.requireAuth(s.handleRegister))
	mux.HandleFunc("POST   /v1/agent/environments/{id}/heartbeat", s.requireAuth(s.handleHeartbeat))
	mux.HandleFunc("GET    /v1/agent/environments", s.requireAuth(s.handleList))
	mux.HandleFunc("DELETE /v1/agent/environments/{id}", s.requireAuth(s.handleDelete))
	// S3-9: session_token refresh
	mux.HandleFunc("POST   /v1/agent/sessions/{id}/refresh-token", s.requireAuth(s.handleRefreshSessionToken))
	// S3-6: CreateSession + mode 分流
	s.MountSessionRoutes(mux)
	// S3-5: WS session stream（Ingress nil 时跳过）
	s.MountIngressRoutes(mux)
	// S3-8: worker poll / ack / publish（Queue nil 时跳过）
	s.MountWorkerRoutes(mux)
	// R6.1: 设备配对 + device token 管理（D5）
	s.MountDeviceRoutes(mux)
}

// ─── Handlers ──────────────────────────────────────────────────

type registerReq struct {
	WorkerKind   string          `json:"worker_kind"`           // 'biu_daemon' | 'biu_cli' | 'runtime'
	MachineName  string          `json:"machine_name"`
	OsArch       string          `json:"os_arch,omitempty"`
	GitInfo      json.RawMessage `json:"git_info,omitempty"`    // 透传 JSONB
	Capabilities []string        `json:"capabilities,omitempty"`
	PublicKey    string          `json:"public_key,omitempty"`  // hex/base64? S3-4 决定；当前 raw 字节透传
	PoolTag      string          `json:"pool_tag,omitempty"`
}

// allowedWorkerKinds 跟 schema CHECK 约束保持一致。提前在 API 层拒绝避免
// 暴露 SQL 错误信息给客户端。
var allowedWorkerKinds = map[string]bool{
	"biu_daemon": true,
	"biu_cli":    true,
	"runtime":    true,
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if !allowedWorkerKinds[req.WorkerKind] {
		writeErr(w, http.StatusBadRequest, "bad_worker_kind",
			"worker_kind must be one of: biu_daemon, biu_cli, runtime")
		return
	}
	if strings.TrimSpace(req.MachineName) == "" {
		writeErr(w, http.StatusBadRequest, "bad_machine_name", "machine_name required")
		return
	}

	// 所有 worker（含 runtime）DB user_id 都从 JWT 取 —— runtime 用 admin /
	// 系统账号 JWT 注册，user_id = 那个账号的 uuid。Pool 选择（PickRuntimeEnvironment）
	// 不按 user_id 过滤所以共享池语义保留；admin 自己 list 时能看到所有 runtime
	// 实例（合理 —— 运维需要观察哪些 runtime 在线）。
	uidCopy := uid
	dbUserID := &uidCopy

	// R6.3：device token 注册时 claims 带 device_id → 关联 environment，让
	// session 创建能反查该设备的 tool_policy。JWT/PAT 注册无 device_id → nil。
	var deviceID *uuid.UUID
	if c := bauth.MustClaims(r.Context()); c.DeviceID != "" {
		if d, perr := uuid.Parse(c.DeviceID); perr == nil {
			deviceID = &d
		}
	}

	// R6.2: public_key 是 daemon 上报的 X25519 pubkey 的 hex。decode 成 raw
	// 32B 存（之前 []byte(string) 存的是 hex 串字节，加密会用错 key）。非法 /
	// 长度不对 → 存 nil（brain 回退明文 BYOK，back-compat）。
	var pubKey []byte
	if req.PublicKey != "" {
		if raw, derr := hex.DecodeString(req.PublicKey); derr == nil && len(raw) == agentcrypto.X25519KeySize {
			pubKey = raw
		} else {
			s.Logger.Warn("agentplane register: ignoring malformed public_key",
				"machine", req.MachineName, "len_hex", len(req.PublicKey))
		}
	}

	env, err := s.Store.RegisterEnvironment(r.Context(), CreateEnvironmentReq{
		UserID:       dbUserID,
		WorkerKind:   req.WorkerKind,
		MachineName:  strings.TrimSpace(req.MachineName),
		OsArch:       req.OsArch,
		GitInfo:      req.GitInfo,
		Capabilities: req.Capabilities,
		PublicKey:    pubKey,
		PoolTag:      req.PoolTag,
		DeviceID:     deviceID,
	})
	if err != nil {
		s.serverErr(w, "insert", err)
		return
	}
	s.Logger.Debug("agentplane api: register",
		"user_id", uid, "env_id", env.EnvironmentID,
		"worker_kind", req.WorkerKind, "machine_name", req.MachineName,
		"pool_tag", req.PoolTag, "remote", r.RemoteAddr)

	// R7：设备重连 → 把它离线期间排队的 agent 任务重新派发到这个新 environment。
	// best-effort（内部逐条 log），不阻断注册响应。仅 device token 注册（env 有
	// device_id）才有挂起任务。
	if env.DeviceID != nil {
		s.dispatchPendingForDevice(r.Context(), env)
	}

	writeJSON(w, http.StatusCreated, environmentOut(env))
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	envID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	if err := s.Store.Heartbeat(r.Context(), uid, envID); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	s.Logger.Debug("agentplane api: heartbeat",
		"user_id", uid, "env_id", envID, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	stateFilter := r.URL.Query().Get("state")
	envs, err := s.Store.ListUserEnvironments(r.Context(), uid, stateFilter)
	if err != nil {
		s.serverErr(w, "list", err)
		return
	}
	s.Logger.Debug("agentplane api: list",
		"user_id", uid, "state_filter", stateFilter, "count", len(envs))
	out := make([]map[string]any, len(envs))
	for i := range envs {
		out[i] = environmentOut(&envs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	envID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	if err := s.Store.DeleteEnvironment(r.Context(), uid, envID); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	s.Logger.Debug("agentplane api: delete",
		"user_id", uid, "env_id", envID, "remote", r.RemoteAddr)
	w.WriteHeader(http.StatusNoContent)
}

// ─── S3-9 token refresh ───────────────────────────────────────

// handleRefreshSessionToken：调用方持长效凭证（PAT / access_token）—— 通过
// requireAuth 校验后拿到 user_id；查 session 存在且属于本用户；颁发新
// 30min session_token。
//
// 失败路径：
//   - signer 未注入 → 503（部署配置缺失）
//   - session 不存在或跨用户 → 404（store 严格匹配）
//   - URL path id 不是 UUID → 400
func (s *Server) handleRefreshSessionToken(w http.ResponseWriter, r *http.Request) {
	if s.Signer == nil {
		writeErr(w, http.StatusServiceUnavailable, "signer_unavailable",
			"session_token refresh requires Signer at server boot")
		return
	}
	uid := mustUserID(r)
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}

	// 校验 session 仍然存在且属于本用户。store 严格 user_id 匹配 —— 跨
	// 租户拿 session_id 试 refresh 一律 404，避免暴露 session 存在性。
	if _, err := s.Store.GetSession(r.Context(), uid, sessionID); err != nil {
		s.handleStoreErr(w, err)
		return
	}

	tok, expiresAt, err := IssueSessionToken(s.Signer, uid, sessionID)
	if err != nil {
		s.serverErr(w, "issue_session_token", err)
		return
	}
	s.Logger.Debug("agentplane api: refresh-token",
		"user_id", uid, "session_id", sessionID,
		"expires_at_ms", expiresAt.UnixMilli())
	writeJSON(w, http.StatusOK, map[string]any{
		"session_token": tok,
		"expires_at":    expiresAt.UnixMilli(),
	})
}

// ─── Auth + helpers ───────────────────────────────────────────

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		tok := strings.TrimPrefix(auth, "Bearer ")
		// R6.1：device token（brain 本地签发的不透明 token，前缀 biu_dev_）走
		// agent_devices 校验（hash 命中 + 未吊销未过期）→ 合成该用户 claims。
		// 等价该用户在 agent plane 的权限（注册 environment / poll 自己的 work）。
		if strings.HasPrefix(tok, deviceTokenPrefix) {
			uid, devID, err := s.Store.VerifyDeviceToken(r.Context(), tok)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid_device_token", "")
				return
			}
			// R6.3：把 device_id 带进 claims —— handleRegister 据此把 environment
			// 关联到设备，session 创建时反查该设备的 tool_policy。
			next(w, r.WithContext(bauth.WithClaims(r.Context(),
				&bauth.Claims{UserID: uid.String(), DeviceID: devID.String()})))
			return
		}
		claims, err := s.Verifier.Verify(tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func mustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
}

func (s *Server) handleStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.serverErr(w, "store", err)
}

func (s *Server) serverErr(w http.ResponseWriter, op string, err error) {
	s.Logger.Error("agentplane api: "+op, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", err.Error())
}

// ─── Output shapes ──────────────────────────────────────────

func environmentOut(e *Environment) map[string]any {
	out := map[string]any{
		"environment_id": e.EnvironmentID.String(),
		"worker_kind":    e.WorkerKind,
		"machine_name":   e.MachineName,
		"state":          e.State,
		"created_at":     e.CreatedAt.UnixMilli(),
		"last_seen_at":   e.LastSeenAt.UnixMilli(),
	}
	if e.UserID != nil {
		out["user_id"] = e.UserID.String()
	}
	if e.OsArch != "" {
		out["os_arch"] = e.OsArch
	}
	if len(e.GitInfo) > 0 {
		out["git_info"] = json.RawMessage(e.GitInfo)
	}
	if len(e.Capabilities) > 0 {
		out["capabilities"] = e.Capabilities
	}
	if e.PoolTag != "" {
		out["pool_tag"] = e.PoolTag
	}
	return out
}

// ─── HTTP helpers ────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
