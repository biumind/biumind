// Worker-facing HTTP endpoints（S3-8 brain 端）。
//
// biu daemon / runtime 不直接连 NATS（部署边界 + 凭证管理 + 网络隔离）。
// 走 brain HTTPS 拉 work / 推 frame，brain 内部对接 JetStream。
//
//	POST /v1/agent/work/{env_id}/poll?wait=30s
//	     → 200 {body: <work_payload>, ack_token: "..."}  消息体（JSON）
//	     → 204                                            wait 期内无消息
//	     → 404                                            env_id 不属于本用户
//	POST /v1/agent/work/{env_id}/ack/{ack_token}
//	     → 200                                            处理成功，删消息
//	POST /v1/agent/work/{env_id}/nak/{ack_token}
//	     → 200                                            处理失败，AckWait 后 redeliver
//	POST /v1/agent/sessions/{id}/publish
//	     body: SDK Protocol frame JSON
//	     → 202                                            发到 biu.session.<sid>.out
//	     → 404                                            session 不存在 / 跨用户
//
// 鉴权：所有端点用 long-lived 凭证（PAT / access_token），跟 environment
// CRUD 一致。worker 拿 PAT 注册 environment，之后用同一 PAT 拉 work / 推 frame。
//
// **关键设计**：HTTP poll 不是 SQL queue 的"select for update" —— 它转手
// JetStream 的 pull-fetch（`Queue.FetchWork`），broker 那边照样有
// AckWait + redelivery 跟 work-queue retention 保护。这一层只是把 NATS
// pull 协议翻译成 HTTP，所以 ack/nak token 必须保留对应的 jetstream.Msg
// 引用。
//
// **ack_token 生命周期**：内存 map (sessionRec-style)，token=uuid，
// 关联 fetched message。worker 拿到 work_payload + ack_token 后，处理
// 完调 ack/nak。token 不持久化 —— brain 重启会丢；JetStream AckWait
// (60s) 后自动 redeliver，重启后另一个 worker 会收到。

package agentplane

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// pendingAck 是 ack/nak token 跟 jetstream message 的索引。
//
// 每个 brain 副本独立 map —— 同一 env_id 的 work 一定是一个 brain 副本拉
// 到（durable consumer + filter subject 路由），同一个副本管 ack 不存在
// 跨副本问题。
type ackRegistry struct {
	mu      sync.Mutex
	pending map[string]*FetchedWork
}

func newAckRegistry() *ackRegistry {
	return &ackRegistry{pending: map[string]*FetchedWork{}}
}

func (r *ackRegistry) put(token string, w *FetchedWork) {
	r.mu.Lock()
	r.pending[token] = w
	r.mu.Unlock()
}

func (r *ackRegistry) take(token string) *FetchedWork {
	r.mu.Lock()
	w := r.pending[token]
	delete(r.pending, token)
	r.mu.Unlock()
	return w
}

// MountWorkerRoutes 注册 worker 端点。Server.Mount 调它。Queue nil 时
// 跳过（dev / NATS 不可用时）。
func (s *Server) MountWorkerRoutes(mux *http.ServeMux) {
	if s.Queue == nil {
		return
	}
	if s.workerAcks == nil {
		s.workerAcks = newAckRegistry()
	}
	mux.HandleFunc("POST /v1/agent/work/{env_id}/poll", s.requireAuth(s.handleWorkPoll))
	mux.HandleFunc("POST /v1/agent/work/{env_id}/ack/{token}", s.requireAuth(s.handleWorkAck))
	mux.HandleFunc("POST /v1/agent/work/{env_id}/nak/{token}", s.requireAuth(s.handleWorkNak))
	mux.HandleFunc("POST /v1/agent/sessions/{id}/publish", s.requireAuth(s.handlePublishFrame))
	// Control plane (cancel / 后续 reload 等) —— 跟 work 平行的反向 polling
	// 通道。worker 用独立 goroutine 长轮询这条端点,不被 work 占满。
	mux.HandleFunc("POST /v1/agent/control/{env_id}/poll", s.requireAuth(s.handleControlPoll))
	mux.HandleFunc("POST /v1/agent/control/{env_id}/ack/{token}", s.requireAuth(s.handleControlAck))
}

// pollWaitMax —— `wait` query 上限。HTTP keep-alive 不喜欢极长请求；30s
// 既能等到大部分 work，又不超出大多数 LB / 反代默认 timeout。
const pollWaitMax = 30 * time.Second

// handleWorkPoll：GET style long-poll，但用 POST 因为有副作用（消费消息）。
//
// 流程：
//  1. 校 user 拥有 environment
//  2. Queue.FetchWork(env_id, wait)
//  3. 有消息 → 生成 ack_token，注册 → 返回 {body, ack_token}
//  4. 无消息（超时）→ 204 No Content
func (s *Server) handleWorkPoll(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	envID, err := uuid.Parse(r.PathValue("env_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_env_id", err.Error())
		return
	}
	// 严格校验所有权（cross-tenant 一律 404；user_id NULL 的 runtime 由
	// runtime 自己注册时填同一调用方的 user_id 走标准路径）
	env, err := s.Store.GetEnvironment(r.Context(), uid, envID)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}

	wait := pollWaitMax
	if v := r.URL.Query().Get("wait"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil && d > 0 && d <= pollWaitMax {
			wait = d
		}
	}

	work, err := s.Queue.FetchWork(r.Context(), env.EnvironmentID, wait)
	if err != nil {
		s.serverErr(w, "fetch work", err)
		return
	}
	if work == nil {
		s.Logger.Debug("agentplane worker_api: poll empty",
			"user_id", uid, "env_id", envID, "wait", wait.String())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	token := uuid.NewString()
	s.workerAcks.put(token, work)

	s.Logger.Debug("agentplane worker_api: poll fetched",
		"user_id", uid, "env_id", envID, "ack_token", token,
		"bytes", len(work.Body))
	// body 是 worker 投递时 publish 的 raw JSON —— 透明转给客户端（worker
	// 端按 WorkPayload schema 反序列）。
	writeJSON(w, http.StatusOK, map[string]any{
		"ack_token": token,
		"body":      json.RawMessage(work.Body),
	})
}

func (s *Server) handleWorkAck(w http.ResponseWriter, r *http.Request) {
	s.handleAckOrNak(w, r, true)
}

func (s *Server) handleWorkNak(w http.ResponseWriter, r *http.Request) {
	s.handleAckOrNak(w, r, false)
}

// handleControlPoll: 跟 handleWorkPoll 同样形状,但拉的是 control 队列
// (biu.control.<env_id>)。Worker 端独立 goroutine 长轮询;收到一条 cancel
// 就立刻 ack(broker 无 redeliver) 然后调 InterruptSession。
func (s *Server) handleControlPoll(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	envID, err := uuid.Parse(r.PathValue("env_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_env_id", err.Error())
		return
	}
	env, err := s.Store.GetEnvironment(r.Context(), uid, envID)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	wait := pollWaitMax
	if v := r.URL.Query().Get("wait"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil && d > 0 && d <= pollWaitMax {
			wait = d
		}
	}
	ctrl, err := s.Queue.FetchControl(r.Context(), env.EnvironmentID, wait)
	if err != nil {
		s.serverErr(w, "fetch control", err)
		return
	}
	if ctrl == nil {
		s.Logger.Debug("agentplane worker_api: control poll empty",
			"user_id", uid, "env_id", envID, "wait", wait.String())
		w.WriteHeader(http.StatusNoContent)
		return
	}
	token := uuid.NewString()
	s.workerAcks.put(token, ctrl)
	s.Logger.Debug("agentplane worker_api: control poll fetched",
		"user_id", uid, "env_id", envID, "ack_token", token,
		"bytes", len(ctrl.Body))
	writeJSON(w, http.StatusOK, map[string]any{
		"ack_token": token,
		"body":      json.RawMessage(ctrl.Body),
	})
}

// handleControlAck: control 消息只走 ack 路径(cancel 没意义 nak —— 重投
// 也是同一个 cancel),所以单一 endpoint 即可。
func (s *Server) handleControlAck(w http.ResponseWriter, r *http.Request) {
	s.handleAckOrNak(w, r, true)
}

// handleAckOrNak 共享：env_id 校验 + token 取出 + 调 ack/nak。
// 找不到 token 视作 idempotent（已经处理过，404 静默成 200 避免 worker 重试）。
func (s *Server) handleAckOrNak(w http.ResponseWriter, r *http.Request, isAck bool) {
	uid := mustUserID(r)
	envID, err := uuid.Parse(r.PathValue("env_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_env_id", err.Error())
		return
	}
	if _, err := s.Store.GetEnvironment(r.Context(), uid, envID); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	token := r.PathValue("token")
	work := s.workerAcks.take(token)
	if work == nil {
		// 重试 / 重启后的兜底 —— 200 让 worker 不卡在重试循环上
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "noop": true})
		return
	}
	if isAck {
		_ = work.Ack() // 失败也无所谓 —— 60s AckWait 后 broker 会自动 redeliver
	} else {
		_ = work.Nak()
	}
	s.Logger.Debug("agentplane worker_api: ack",
		"user_id", uid, "env_id", envID, "ack_token", token, "is_ack", isAck)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePublishFrame：worker 把一帧 SDK Protocol JSON publish 到 session
// 的 .out subject，让 ingress 转给客户端。
func (s *Server) handlePublishFrame(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_session_id", err.Error())
		return
	}
	if _, err := s.Store.GetSession(r.Context(), uid, sessionID); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	// 单帧上限 32KB，跟 ingress 同口径（worker 推大 frame 是 buggy，断它）
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	if len(body) > 32*1024 {
		writeErr(w, http.StatusRequestEntityTooLarge, "frame_too_large",
			"frame body exceeds 32KB limit")
		return
	}
	if len(body) == 0 {
		writeErr(w, http.StatusBadRequest, "empty_body", "frame body required")
		return
	}
	if err := s.Queue.PublishSessionFrame(r.Context(), sessionID, body); err != nil {
		s.serverErr(w, "publish frame", err)
		return
	}
	s.Logger.Debug("agentplane worker_api: publish frame",
		"user_id", uid, "session_id", sessionID, "bytes", len(body))
	w.WriteHeader(http.StatusAccepted)
}

// 让接口签名稳定 —— Mount 处需要 workerAcks 字段
var _ = errors.New
