// Package bridge exposes the biu agent over a tiny HTTP+WebSocket surface
// so an IDE (or a remote operator UI) can drive the engine
// out-of-process. WebSocket is the only stream transport — wire format
// is SDK Protocol v1 (StdoutMessage union, see schema/sdk/v1/service.json).
//
// Routes:
//
//   POST   /v1/code/sessions                  → { id }                  create session
//   POST   /v1/code/sessions/:id/messages     body { prompt }            submit a turn
//   GET    /v1/code/sessions/:id/ws           WebSocket text frames      stream events
//   GET    /v1/code/sessions/:id/cost         JSON cost snapshot
//   POST   /v1/code/sessions/:id/compact      run manual compact
//   DELETE /v1/code/sessions/:id              close session
//
// Authentication: a single bearer token from Options.AuthToken — set
// to a non-empty value to require it. Empty string disables auth
// (loopback / dev only).

package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code"
	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// Options configures the HTTP bridge.
type Options struct {
	// AgentFactory builds a fresh Agent for every new session. Factory
	// receives an `extras` parameter the bridge uses to inject runtime
	// dependencies the caller cannot know in advance —— most importantly
	// PermissionPolicy that asks the connected WS client.
	//
	// Typical production caller:
	//
	//   AgentFactory: func(extras AgentExtras) (*biumindkit.Agent, error) {
	//       opts := myBaseOptions
	//       opts.PermissionPolicy = extras.PermissionPolicy
	//       return biumindkit.New(opts)
	//   }
	//
	// Tests inject stubs by ignoring extras.
	AgentFactory func(extras AgentExtras) (*biumindkit.Agent, error)

	// AuthToken, when non-empty, must appear in the
	// `Authorization: Bearer <token>` header on every request.
	AuthToken string

	// AttachmentDir 是 POST /attachments 上传文件落盘根目录。空 → ~/.biu/sessions
	// 下用 session_id 子目录。Tests 可以指定 t.TempDir()。
	AttachmentDir string

	// CommitGenerator 是编码模块 AI commit msg 的 LLM 缝(走 model-relay,满足 I6)。
	// 空 → git.generateCommitMessage 返回明确错误(daemon 未配 provider)。
	CommitGenerator gitassist.Generator
}

// AgentExtras 是 bridge 在 session 创建时塞给 factory 的 per-session 依赖。
// factory 用其中字段填 biumindkit.Options。
type AgentExtras struct {
	// PermissionPolicy 用于让 engine 询问"这个工具是否允许"。bridge 提供的
	// 实现通过 WS 把请求发给 client，等 30s 内回复或自动 deny。
	PermissionPolicy biumindkit.PermissionPolicyFn
}

// Server hosts the HTTP handler. Construct via NewServer + mount
// onto your own *http.ServeMux, or use ListenAndServe for the all-in-
// one case.
type Server struct {
	opt Options

	mu       sync.Mutex
	sessions map[string]*sessionRec

	// code 是编码模块（BiuMind Code）的能力内核 —— PTY/Git/FS 分发器，
	// 跨 /v1/code/ws 连接共享（单进程一个，故断线重连可重新 attach）。
	code *code.Service
}

// sessionRec is the per-session backing state. The agent runs on its
// own goroutine; events flow as already-translated sdkproto.Frame so
// every consumer (live WS listener or replay) ships identical wire bytes.
type sessionRec struct {
	id    string
	agent *biumindkit.Agent

	mu            sync.Mutex
	eventCh       chan sdkproto.Frame // nil when no listener attached
	pendingCancel context.CancelFunc

	// Ring buffer of recent SDK frames keyed by monotonically-increasing
	// id. Lets a reconnecting client pass `?last_event_id=N` and pick
	// up where it left off without losing anything from a network blip.
	// Frames also flow through eventCh for active listeners — the buffer
	// is purely for replay.
	eventLog []bufferedEvent
	nextID   int64

	// permMu 保护 pendingPerms。独立于 mu —— PermissionPolicy 可能在持有 mu
	// 的代码里被调用，分开锁避免死锁。
	permMu       sync.Mutex
	pendingPerms map[string]chan biumindkit.PermissionDecision
}

// permissionTimeout 是单条 can_use_tool 等待客户端响应的最大时长。超过
// 即自动 deny；客户端若要 deny 必须在这之前发回包。
//
// 用 var 而不是 const 让测试能临时缩短到几百毫秒。
var permissionTimeout = 30 * time.Second

// askPermission 是注入给 biumindkit Agent 的 PermissionPolicyFn。engine 触发
// PermissionAsk 时由 biumindkit 内部调用：
//
//  1. 生成 request_id；注册 chan 到 pendingPerms map
//  2. 包成 SDKControlRequest{can_use_tool} 推到 eventCh，让 wsHandler 写给
//     客户端
//  3. 阻塞等 chan 收到 decision，或 30s timeout，或 ctx 取消（turn 被 interrupt）
//  4. 任何错误路径都回 PermDeny —— "默认拒绝"是安全侧
func (rec *sessionRec) askPermission(
	ctx context.Context,
	req biumindkit.PermissionRequest,
) biumindkit.PermissionDecision {
	requestID := newID()
	respCh := make(chan biumindkit.PermissionDecision, 1)

	rec.permMu.Lock()
	if rec.pendingPerms == nil {
		rec.pendingPerms = map[string]chan biumindkit.PermissionDecision{}
	}
	rec.pendingPerms[requestID] = respCh
	rec.permMu.Unlock()

	defer func() {
		rec.permMu.Lock()
		delete(rec.pendingPerms, requestID)
		rec.permMu.Unlock()
	}()

	// 把 PermissionRequest 包成 SDK Protocol 帧
	inputJSON, _ := json.Marshal(req.Input)
	frame := &sdkproto.SDKControlRequest{
		Type:      sdkproto.TypeControlRequest,
		RequestID: requestID,
		Request: &sdkproto.PermissionRequest{
			SubtypeF:       sdkproto.SubtypeCanUseTool,
			ToolName:       req.ToolName,
			Input:          inputJSON,
			ToolUseID:      req.ToolUseID,
			DecisionReason: req.Reason,
		},
	}

	// 推到 eventCh —— wsHandler main loop 会写给 client。如果当前没 client 连
	// 接（eventCh nil）或推送本身被 ctx 打断，直接 deny。
	rec.mu.Lock()
	ch := rec.eventCh
	rec.mu.Unlock()
	if ch == nil {
		return biumindkit.PermDeny
	}
	select {
	case ch <- frame:
	case <-ctx.Done():
		return biumindkit.PermDeny
	case <-time.After(2 * time.Second):
		// eventCh 满 + 没消费者 = 客户端慢；等 2s 没塞进去就放弃
		return biumindkit.PermDeny
	}

	// 等 client 答复
	select {
	case decision := <-respCh:
		return decision
	case <-ctx.Done():
		return biumindkit.PermDeny
	case <-time.After(permissionTimeout):
		return biumindkit.PermDeny
	}
}

// answerPermission 是 read pump 收到 SDKControlResponse 时调用的入口 ——
// 把 decision 投递给等候的 askPermission goroutine。
//
// resp.Response.Subtype == "error" → deny；
// resp.Response.Subtype == "success" → 解析 response.Response 字段（应该是
// `permissions.json#/$defs/PermissionResult`：{behavior: "allow"|"deny", ...}），
// 任何解析错误回 deny。
//
// 找不到 request_id 静默丢弃 —— 客户端可能发了重复响应 / askPermission 已
// timeout 删 map。
func (rec *sessionRec) answerPermission(resp *sdkproto.SDKControlResponse) {
	if resp == nil || resp.Response == nil {
		return
	}
	rec.permMu.Lock()
	respCh, ok := rec.pendingPerms[resp.Response.RequestID]
	if ok {
		delete(rec.pendingPerms, resp.Response.RequestID)
	}
	rec.permMu.Unlock()
	if !ok {
		return
	}
	defer close(respCh)

	if resp.Response.Subtype == sdkproto.ControlSubtypeError {
		respCh <- biumindkit.PermDeny
		return
	}

	// 解析 PermissionResult
	var result struct {
		Behavior  string `json:"behavior"`
		Interrupt *bool  `json:"interrupt,omitempty"`
	}
	if len(resp.Response.Response) > 0 {
		_ = json.Unmarshal(resp.Response.Response, &result)
	}
	switch result.Behavior {
	case sdkproto.PermissionAllow:
		respCh <- biumindkit.PermAllow
	default:
		// deny / ask / 空字符串都视作 deny
		respCh <- biumindkit.PermDeny
	}
}

// bufferedEvent is one entry in the per-session replay ring.
// 直接存 sdkproto.Frame —— replay 时跟 live push 走完全一样的 wire 编码路径。
type bufferedEvent struct {
	ID    int64
	Frame sdkproto.Frame
}

// eventBufferCap caps the per-session ring buffer. 256 fits a
// long-running turn (a typical biu turn produces 30-80 events) while
// keeping memory bounded.
const eventBufferCap = 256

// NewServer wires a fresh server with the supplied options.
func NewServer(opt Options) (*Server, error) {
	if opt.AgentFactory == nil {
		return nil, errors.New("bridge: AgentFactory required")
	}
	codeSvc := code.NewService()
	if opt.CommitGenerator != nil {
		codeSvc.SetCommitGenerator(opt.CommitGenerator)
	}
	// 启动期幂等安装 agent hook(PERI-1,best-effort 异步);node/版本不够则任务启动时回退轮询。
	code.InstallHooksOnStartup()
	return &Server{
		opt:      opt,
		sessions: map[string]*sessionRec{},
		code:     codeSvc,
	}, nil
}

// Close 释放 server 持有的进程资源 —— 当前是杀掉所有活跃 PTY。daemon graceful
// shutdown 时调用（biu serve）。聊天 session 由各自 agent.Close 处理，不在此。
func (s *Server) Close() {
	if s.code != nil {
		s.code.CloseAll()
	}
}

// Handler returns an http.Handler that mounts every bridge route. Use
// this when embedding in an existing server. For a standalone
// process, see ListenAndServe.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/code/sessions", s.create)
	mux.HandleFunc("DELETE /v1/code/sessions/{id}", s.delete)
	mux.HandleFunc("POST /v1/code/sessions/{id}/messages", s.submit)
	mux.HandleFunc("GET /v1/code/sessions/{id}/ws", s.wsHandler)
	mux.HandleFunc("GET /v1/code/ws", s.codeWS)
	mux.HandleFunc("GET /v1/code/sessions/{id}/cost", s.cost)
	mux.HandleFunc("POST /v1/code/sessions/{id}/compact", s.compact)
	mux.HandleFunc("POST /v1/code/sessions/{id}/attachments", s.uploadAttachment)
	return s.authMiddleware(mux)
}

// ListenAndServe starts a server on addr. Blocks. Convenience for
// `biu bridge --listen`.
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

// Listen binds addr without serving. Use this when the caller needs
// the resolved address (e.g. `--listen :0` in tests, or anything that
// wants to print the real port back to the user) before calling
// http.Serve on the returned listener.
//
// On success, returns the bound *net.TCPListener (so callers can
// inspect Addr()) and the handler ready to be served. The caller
// owns lifecycle: typically `defer ln.Close()` then `http.Serve(ln,
// handler)` from a goroutine.
func (s *Server) Listen(addr string) (net.Listener, http.Handler, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	return ln, s.Handler(), nil
}

// ─── Auth + helpers ───────────────────────────────────

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if s.opt.AuthToken == "" {
		return next
	}
	expected := "Bearer " + s.opt.AuthToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expected {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ─── Handlers ─────────────────────────────────────────

func (s *Server) create(w http.ResponseWriter, _ *http.Request) {
	rec := &sessionRec{id: newID()}
	// 把 askPermission 闭包给 factory —— factory 应该把它填到 biumindkit
	// Options.PermissionPolicy。这种"先建 sessionRec、构造 policy、再构造
	// agent"的顺序保证 policy 闭包能引用到 sessionRec.id 等字段。
	extras := AgentExtras{
		PermissionPolicy: rec.askPermission,
	}
	agent, err := s.opt.AgentFactory(extras)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec.agent = agent
	s.mu.Lock()
	s.sessions[rec.id] = rec
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{"id": rec.id})
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	rec, ok := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	}
	rec.mu.Lock()
	if rec.pendingCancel != nil {
		rec.pendingCancel()
	}
	rec.mu.Unlock()
	_ = rec.agent.Close()
	w.WriteHeader(http.StatusNoContent)
}

type submitBody struct {
	Prompt string `json:"prompt"`
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	}
	var body submitBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, "prompt required")
		return
	}

	// Cancel previous in-flight turn if there is one — IDE callers expect
	// the "POST messages = abort + restart" contract.
	rec.mu.Lock()
	if rec.pendingCancel != nil {
		rec.pendingCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	rec.pendingCancel = cancel

	// Reset the event channel so the next WS listener gets a clean stream.
	// Buffer is generous — IDE listener may be slow.
	if rec.eventCh != nil {
		close(rec.eventCh)
	}
	rec.eventCh = make(chan sdkproto.Frame, 64)
	ch := rec.eventCh
	rec.mu.Unlock()

	go func() {
		defer func() {
			rec.mu.Lock()
			if rec.eventCh == ch {
				close(ch)
				rec.eventCh = nil
				rec.pendingCancel = nil
			}
			rec.mu.Unlock()
			cancel()
		}()
		for ev := range rec.agent.Submit(ctx, body.Prompt) {
			frame := sdkbridge.ToSDKFrame(ev, rec.id)
			if frame == nil {
				// Some biumindkit events (AssistantText/AssistantBlock-text)
				// are duplicate snapshots of StreamingText and intentionally
				// drop to nil — see sdkbridge/mapping.go. Skip so the wire
				// + ring buffer don't carry empty entries.
				continue
			}
			rec.appendFrame(frame) // ring-buffer for resume
			select {
			case ch <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
}

// appendFrame records an SDK frame in the per-session ring buffer so a
// `?last_event_id=N` reconnect can replay missed entries. Caps at
// eventBufferCap; oldest entries drop off.
func (rec *sessionRec) appendFrame(frame sdkproto.Frame) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.nextID++
	rec.eventLog = append(rec.eventLog, bufferedEvent{
		ID: rec.nextID, Frame: frame,
	})
	if len(rec.eventLog) > eventBufferCap {
		rec.eventLog = rec.eventLog[len(rec.eventLog)-eventBufferCap:]
	}
}

// dispatchControl 收到客户端发来的 SDKControlRequest 后路由到具体动作。
// 返回 SDKControlResponse —— 调用方负责把它发回客户端。每个 request_id 应有
// 恰好一条 response（成功或失败）。
//
// 当前 S2-2 支持：
//   - interrupt：cancel 当前 turn 的 context，submit goroutine 自动收尾
//   - cancel_async_message：biumindkit 没 message-级 cancel，best-effort 用 turn-级
//   - set_model：biumindkit Agent 不暴露动态换模型，返回 error
//
// 其他 subtype（mcp_* / get_context_usage / hook_callback / ...）暂不支持 ——
// 返回 error 让调用方知道；后续 brain 集成时一并实化。
func (rec *sessionRec) dispatchControl(req *sdkproto.SDKControlRequest) sdkproto.Frame {
	if req.Request == nil {
		return controlError(req.RequestID, "missing request body")
	}
	subtype := req.Request.Subtype()
	switch subtype {
	case sdkproto.SubtypeInterrupt:
		rec.mu.Lock()
		cancel := rec.pendingCancel
		rec.mu.Unlock()
		if cancel == nil {
			return controlError(req.RequestID, "no turn in progress")
		}
		cancel()
		return controlSuccess(req.RequestID, nil)

	case sdkproto.SubtypeCancelAsyncMessage:
		// biumindkit 没 per-message cancel —— 退化成 turn-级 cancel。
		// `cancelled` 字段告诉调用方"是否真的有 turn 被中止"。
		rec.mu.Lock()
		cancel := rec.pendingCancel
		rec.mu.Unlock()
		if cancel == nil {
			return controlSuccess(req.RequestID, json.RawMessage(`{"cancelled":false}`))
		}
		cancel()
		return controlSuccess(req.RequestID, json.RawMessage(`{"cancelled":true}`))

	case sdkproto.SubtypeSetModel:
		// biumindkit Agent 没动态 SetModel API —— 想换模型必须重建 session。
		return controlError(req.RequestID, "set_model not supported by biu bridge; recreate session with desired model")

	default:
		return controlError(req.RequestID, fmt.Sprintf("control subtype %q not implemented", subtype))
	}
}

// controlSuccess 包成 SDKControlResponse{success}。response 是给调用方的具体
// 数据 payload（已 JSON 序列化的 RawMessage）；nil 表示成功但无返回值。
func controlSuccess(requestID string, response json.RawMessage) *sdkproto.SDKControlResponse {
	return &sdkproto.SDKControlResponse{
		Type: sdkproto.TypeControlResponse,
		Response: &sdkproto.ControlResponseBody{
			Subtype:   sdkproto.ControlSubtypeSuccess,
			RequestID: requestID,
			Response:  response,
		},
	}
}

// controlError 包成 SDKControlResponse{error}。错误文本直接给客户端展示。
func controlError(requestID, msg string) *sdkproto.SDKControlResponse {
	return &sdkproto.SDKControlResponse{
		Type: sdkproto.TypeControlResponse,
		Response: &sdkproto.ControlResponseBody{
			Subtype:   sdkproto.ControlSubtypeError,
			RequestID: requestID,
			Error:     msg,
		},
	}
}

// since returns every buffered event with id > lastSeen. When
// lastSeen is the zero value (no Last-Event-ID header) returns
// nothing — fresh listeners only see new events going forward.
func (rec *sessionRec) since(lastSeen int64) []bufferedEvent {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if lastSeen <= 0 {
		return nil
	}
	out := make([]bufferedEvent, 0, len(rec.eventLog))
	for _, e := range rec.eventLog {
		if e.ID > lastSeen {
			out = append(out, e)
		}
	}
	return out
}

// parseLastEventID extracts the resume cursor from `?last_event_id=N` query
// param. Returns 0 when missing or non-numeric. WS doesn't support custom
// upgrade-time headers as cleanly as SSE's Last-Event-ID, so query is the
// canonical resume mechanism.
func parseLastEventID(r *http.Request) int64 {
	c := r.URL.Query().Get("last_event_id")
	if c == "" {
		return 0
	}
	n, err := parseInt64(c)
	if err == nil && n > 0 {
		return n
	}
	return 0
}

// parseInt64 is strconv.ParseInt with a tighter signature.
func parseInt64(s string) (int64, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("non-numeric")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

func (s *Server) cost(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	}
	writeJSON(w, http.StatusOK, rec.agent.Cost())
}

func (s *Server) compact(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	}
	if err := rec.agent.Compact(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "compacted"})
}

func (s *Server) lookup(id string) (*sessionRec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	return rec, ok
}
