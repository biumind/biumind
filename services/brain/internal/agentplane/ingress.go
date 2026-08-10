// WS session ingress（S3-5A）—— 客户端连 brain 副本拿 session frame 流。
//
// 路径：`GET /v1/agent/sessions/{id}/stream` —— WS 升级。
//
// 鉴权：必须带短效 session_token（S3-9 颁发）。token scope 必须匹配 URL
// 里的 session_id —— 拿 sessionA token 操作 sessionB 拒绝。
//
// 数据流（本 stage S3-5A）：
//
//	server→client：ingress 在 brain 副本里订阅 `biu.session.<sid>.out`
//	               —— worker（或未来 brain 进程内 chat 模式）发到这条
//	               subject 的帧广播给所有连这个 session 的 WS。
//	client→server：WS 入站 JSON 帧 → publish 到 `biu.session.<sid>.in`
//	               —— worker 端（S3-8）订阅这条 subject 收回。
//
// 多副本 fanout 自动 work：JetStream broker 把消息推给每个 consumer，每副本
// 各起 ephemeral consumer。local fanout 不绕 NATS 的优化（同副本上多个 WS）
// 留给后续；当前每帧绕 broker 一圈也就 ~ms 级。
//
// **本 stage 不做** —— 留给 S3-5B：
//   - resume by `?since_seq=<n>` （需 raw OrderedConsumer with
//     DeliverByStartSequence；当前 bus.JetStream wrapper 不暴露）
//   - SessionDesynced fallback（since_seq < oldest 时）
//   - 多 client 跨副本 broadcast 测试

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// 心跳：30s ping，60s 收不到 pong 就断（跟 biu bridge 对齐）
	ingressPingPeriod = 30 * time.Second
	ingressPongWait   = 60 * time.Second
	ingressWriteWait  = 10 * time.Second

	// 单条 frame 上限。LLM streaming 单 chunk 通常 ≤ 4 KB；单条 frame
	// >32 KB 极不正常，可能是 client 行为异常或 buggy worker，直接断。
	ingressMaxFrameSize = 32 * 1024
)

// upgrader 同 biu bridge 配置（loopback dev + 公网 nginx 都过同一逻辑；
// CSRF 不在协议层防，靠 nginx + bauth）。
var ingressUpgrader = websocket.Upgrader{
	ReadBufferSize:  ingressMaxFrameSize,
	WriteBufferSize: 4 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Ingress 是 WS session stream handler 的依赖容器。
type Ingress struct {
	js       bus.JetStream
	store    *Store
	verifier *bauth.Verifier
	logger   *slog.Logger

	// jsFn 非空时优先于 js —— 每次请求惰性取当前 JetStream 句柄（生产
	// 接 readiness reconciler：broker 后补就绪 / 断线恢复后无需重启即
	// 自愈）。NewIngress 的 js 参数是静态等价物,主要给测试用。
	jsFn func() bus.JetStream

	// queue 用于 EnqueueControl —— 收到 control_cancel_request 时把 cancel
	// 推到对应 environment 的 control 流。nil = 没注入(dev/不带 NATS),那条
	// 路径退化为 best-effort log。
	queue *Queue

	// chatInterrupt 是 chat-mode session 的进程内打断 hook。session 的
	// engine 跑在 brain 进程内,没经过 environment 转发 —— 直接调这里。
	// 由 main.go 注入:cb 闭包到 ChatRunner.InterruptSession。nil-safe。
	chatInterrupt func(sessionID uuid.UUID) bool

	// activeMu 保护 active connection 计数 —— 用于 graceful shutdown 跟
	// metrics。当前只是简单数 +1/-1。
	activeMu    sync.Mutex
	activeCount int
}

// NewIngress 构造一个 WS ingress。js 可空 —— 生产传 nil 并用 SetJSFunc
// 接 readiness；nil 且未设 jsFn 时 stream 路径恒定 503 no_jetstream。
func NewIngress(js bus.JetStream, store *Store, v *bauth.Verifier, logger *slog.Logger) *Ingress {
	if logger == nil {
		logger = slog.Default()
	}
	return &Ingress{js: js, store: store, verifier: v, logger: logger}
}

// SetJSFunc 让 ingress 每次请求惰性解析 JetStream（接 readiness
// reconciler 的 JetStream()）。boot 时调一次,之后无需锁。
func (i *Ingress) SetJSFunc(fn func() bus.JetStream) { i.jsFn = fn }

// currentJS 返回本次请求应使用的 JetStream 句柄。
func (i *Ingress) currentJS() bus.JetStream {
	if i.jsFn != nil {
		return i.jsFn()
	}
	return i.js
}

// SetQueue 注入 control 队列引用。main.go 在 NewIngress 之后调一次,
// 让 ingress 能对 cancel 请求反向 EnqueueControl。
func (i *Ingress) SetQueue(q *Queue) { i.queue = q }

// SetChatInterrupt 注入 chat-mode 进程内打断回调。fn(sessionID) 返
// true 表示找到了在跑的 chat session 并触发了 cancel,false 表示没匹配
// (该会话不是 chat 模式 / 已结束)。fn==nil 关闭 chat 直通路径,所有
// cancel 都走 environment control 队列。
func (i *Ingress) SetChatInterrupt(fn func(sessionID uuid.UUID) bool) {
	i.chatInterrupt = fn
}

// MountIngressRoutes 注册 WS 路由。Server.Mount 调它。
//
// **无条件挂载** —— 历史事故：启动时 NATS 未就绪 → Ingress 为 nil → 路
// 由不挂 → 请求落默认 mux 404 → 客户端无限转圈。现在路由恒在：JetStream
// 未就绪时 handleStream 返 503 no_jetstream，readiness 就绪后不重启自愈。
// Ingress 本身为 nil（main.go 不该这么 wire）也挂固定 503，不留 404。
func (s *Server) MountIngressRoutes(mux *http.ServeMux) {
	if s.Ingress == nil {
		mux.HandleFunc("GET /v1/agent/sessions/{id}/stream", func(w http.ResponseWriter, _ *http.Request) {
			writeErr(w, http.StatusServiceUnavailable, "no_jetstream", "ingress not wired")
		})
		return
	}
	// **不**走 requireAuth middleware —— WS 升级前用 query token 校验
	// （header Authorization 在浏览器 WS 客户端不容易设；query token
	// 是 OAuth WS 的标准模式）
	mux.HandleFunc("GET /v1/agent/sessions/{id}/stream", s.Ingress.handleStream)
}

// ActiveCount 返回当前连接数（metrics / debug）。
func (i *Ingress) ActiveCount() int {
	i.activeMu.Lock()
	defer i.activeMu.Unlock()
	return i.activeCount
}

func (i *Ingress) incActive() {
	i.activeMu.Lock()
	i.activeCount++
	i.activeMu.Unlock()
}

func (i *Ingress) decActive() {
	i.activeMu.Lock()
	i.activeCount--
	i.activeMu.Unlock()
}

// handleStream 是 WS 升级入口。流程：
//  1. 解 session_id（URL）+ session_token（query 或 header）
//  2. 校验 session_token：签名 + scope 必含 session:<id>
//  3. 查 agent_sessions 行：必须存在 + 属于 token user_id
//  4. 升级 WS
//  5. 起 read pump（client→server）+ write pump（server→client）+ ping ticker
//  6. 任一方向断开 → 清理另一方向 + JetStream subscription
func (i *Ingress) handleStream(w http.ResponseWriter, r *http.Request) {
	i.logger.Info("ingress: stream request", "path", r.URL.Path, "remote", r.RemoteAddr)
	// 每次请求取当前 JS —— readiness 后补就绪 / 断线恢复都在这里自然
	// 生效,不需要重启进程。nil → 503 no_jetstream（路由已恒挂载）。
	js := i.currentJS()
	if js == nil {
		i.logger.Warn("ingress: no_jetstream")
		writeErr(w, http.StatusServiceUnavailable, "no_jetstream",
			"ingress requires JetStream; broker offline or not ready yet")
		return
	}

	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		i.logger.Warn("ingress: bad_session_id", "err", err)
		writeErr(w, http.StatusBadRequest, "bad_session_id", err.Error())
		return
	}

	// session_token：浏览器 WS 不方便设 header，先查 query；fallback header
	tok := r.URL.Query().Get("session_token")
	if tok == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			tok = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if tok == "" {
		writeErr(w, http.StatusUnauthorized, "missing_session_token",
			"need session_token query param or Bearer header")
		return
	}
	claims, err := VerifySessionToken(i.verifier, tok, sessionID)
	if err != nil {
		i.logger.Warn("ingress: invalid_session_token", "session_id", sessionID, "err", err)
		writeErr(w, http.StatusUnauthorized, "invalid_session_token", err.Error())
		return
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user_id", "claims missing user_id")
		return
	}

	// 校验 session 仍然存在（可能已 finalize / cancel）
	sess, err := i.store.GetSession(r.Context(), userID, sessionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if sess.State == "completed" || sess.State == "failed" || sess.State == "cancelled" {
		i.logger.Warn("ingress: session_finalized", "session_id", sessionID, "state", sess.State)
		// session 已终态 —— 客户端应改读 agent_session_results 摘要，
		// 不要 attach WS（attach 也没数据）。S3-5B SessionDesynced 走类
		// 似路径，这里先简单 409。
		writeErr(w, http.StatusConflict, "session_finalized",
			fmt.Sprintf("session is %s; read result via /v1/agent/sessions/%s/result", sess.State, sessionID))
		return
	}
	i.logger.Info("ingress: upgrading", "session_id", sessionID, "state", sess.State)

	// 升级
	conn, err := ingressUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// upgrader 已写 4xx；只在 dev 跑无需 log
		return
	}
	defer conn.Close()

	i.incActive()
	defer i.decActive()

	conn.SetReadLimit(ingressMaxFrameSize)
	conn.SetReadDeadline(time.Now().Add(ingressPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(ingressPongWait))
		return nil
	})

	// writeMu —— ping (control msg) + 服务端推 + read pump 写 ack 都共用
	// conn；gorilla 不允许并发写。
	var writeMu sync.Mutex
	writeFrame := func(data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(ingressWriteWait))
		return conn.WriteMessage(websocket.TextMessage, data)
	}

	// subscribeCtx 让 read pump 断时能 cancel subscription
	subscribeCtx, cancelSubscribe := context.WithCancel(r.Context())
	defer cancelSubscribe()

	sinceSeq := parseSinceSeq(r)
	i.logger.Debug("ingress: stream params",
		"session_id", sessionID, "user_id", userID, "mode", sess.Mode,
		"since_seq", sinceSeq, "remote", r.RemoteAddr)
	cleanup, err := i.startStreamConsumer(subscribeCtx, js, conn, sessionID, sinceSeq, writeFrame, cancelSubscribe)
	if err != nil {
		// 已经写了 close frame；直接返回。
		return
	}
	defer cleanup()

	// Read pump：客户端 → JetStream `<sid>.in`
	//
	// 跑在主 goroutine。read 阻塞；ping 跟正常 close 信号通过单独 ping
	// goroutine 触发（gorilla ReadMessage 没法 select）。
	pingCtx, stopPing := context.WithCancel(r.Context())
	defer stopPing()
	go func() {
		t := time.NewTicker(ingressPingPeriod)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(ingressWriteWait))
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(ingressWriteWait))
				writeMu.Unlock()
			}
		}
	}()

	inSubject := SessionSubjectIn(sessionID.String())
	// envID 在 handleStream 已经查到(sess.EnvironmentID),传给 cancel 路由
	// 避免每条入站消息再查一次 store。chat-mode session 上没绑 environment,
	// 用 nil 让 maybeRouteCancel 走进程内打断分支。
	var envID *uuid.UUID
	if sess.EnvironmentID != nil && *sess.EnvironmentID != uuid.Nil {
		envID = sess.EnvironmentID
	}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// 客户端断开（正常 close / 超时 / protocol error）
			return
		}
		if len(msg) == 0 {
			continue
		}
		// 帧级 Debug —— 仅 in 方向。release/info 默认关闭,debug 模式下能
		// 看到客户端 → brain 每条原始帧的 type / request_id / 字节数,排查
		// 协议偏差最有用的入口。
		if i.logger.Enabled(r.Context(), slog.LevelDebug) {
			var head inboundFrameHead
			_ = json.Unmarshal(msg, &head)
			i.logger.Debug("ingress: frame in",
				"session_id", sessionID, "frame_type", head.Type,
				"request_id", head.RequestID, "bytes", len(msg))
		}
		// 协议级旁路:control_cancel_request 不只 publish 到 .in (worker
		// 不订阅那条 subject), 还要走 control plane 反向投到 daemon。
		// chat 模式同时尝试进程内打断 —— 都是幂等 best-effort, fall through
		// 继续 publish 到 .in 让客户端可见的协议状态保持完整。
		i.maybeRouteCancel(r.Context(), sessionID, envID, msg)
		// 同样的旁路:control_response 是 client→daemon 的 permission 答复。
		// daemon 不订阅 .in,只通过 PollControl 拿 control 队列消息。
		// 当前唯一的 daemon→client 控制请求是 can_use_tool,所以所有
		// control_response 都视作 permission_response 路由(将来扩协议
		// 时这里要按 subtype 分流)。
		i.maybeRoutePermissionResponse(r.Context(), sessionID, envID, msg)
		// publish 到 in subject。失败仅 log，不断 connection —— 客户端可
		// 重发；publish 失败通常是 broker 短时间不可用。
		if err := js.Publish(r.Context(), inSubject, json.RawMessage(msg)); err != nil {
			i.logger.Warn("ingress: publish in failed",
				"session_id", sessionID, "err", err)
		}
	}
}

// inboundFrameHead 是为了识别 cancel 帧而做的最小解析 —— 不引 sdkproto
// 包(那边引 biumindkit 引 engine 引 cost...,brain 已经依赖这条链,但解
// 析 control 帧不需要全套 type registry)。schema 跟 sdkproto/v1
// SDKControlCancelRequest 对齐:
//
//	{ "type": "control_cancel_request", "request_id": "<id>" }
//
// 老协议 control_request{subtype:"interrupt"} 是 client→server 走 stdin
// 那条路径,跟这里的 cancel 不一样 —— 那条不在我们路径上。
type inboundFrameHead struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	// Response 字段仅 control_response 帧使用。我们只关心 request_id +
	// subtype + response 三个 leaf 字段(behavior),不展开整个 PermissionResult。
	Response *struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response,omitempty"`
		Error     string          `json:"error,omitempty"`
	} `json:"response,omitempty"`
}

// maybeRouteCancel 探测 msg 是否是 cancel 帧;是则触发反向打断。
// 返回 true 表示识别为 cancel(无论后续路由是否成功);false 表示不是 cancel
// 走默认的 publish 路径。envID 为 nil 表示 chat-mode 内进程会话(没 worker)。
//
// 每条路径都打 metric:用户能从 brain_agent_cancel_requests_total 看出
// cancel 频率 + 命中率 + 失败原因。outcome 是稳定枚举,跟 metrics.go
// 注释里的列表对齐。
func (i *Ingress) maybeRouteCancel(ctx context.Context, sessionID uuid.UUID, envID *uuid.UUID, msg []byte) bool {
	var head inboundFrameHead
	if err := json.Unmarshal(msg, &head); err != nil {
		// 协议层 bug — 客户端发了不合 sdkproto 形状的帧。alert 关键。
		metrics.RecordCancelRequest("unknown", "parse_error")
		return false
	}
	if head.Type != "control_cancel_request" {
		return false
	}
	// 1) 进程内 chat-mode 直通 —— 命中即 done, 不再走 environment。
	if i.chatInterrupt != nil && i.chatInterrupt(sessionID) {
		i.logger.Info("ingress: cancel routed in-process (chat mode)",
			"session_id", sessionID, "request_id", head.RequestID)
		metrics.RecordCancelRequest("chat", "chat_inprocess")
		return true
	}
	// 2) Daemon / runtime worker —— 走 control 队列让 worker 长轮询拉到。
	mode := "agent" // chat 没命中就剩 agent / task / unknown,统一归 agent
	if envID == nil {
		// 没 environment_id 又没 chat 直通命中:无处投递,静默丢。
		i.logger.Info("ingress: cancel — no environment + no chat handler, dropping",
			"session_id", sessionID)
		metrics.RecordCancelRequest(mode, "no_route_no_env")
		return true
	}
	if i.queue == nil {
		i.logger.Warn("ingress: cancel cannot route — Queue not wired",
			"session_id", sessionID)
		metrics.RecordCancelRequest(mode, "queue_unavailable")
		return true
	}
	payload := map[string]any{
		"type":       "cancel_session",
		"session_id": sessionID.String(),
		"request_id": head.RequestID,
	}
	if err := i.queue.EnqueueControl(ctx, *envID, payload); err != nil {
		i.logger.Warn("ingress: cancel — enqueue control failed",
			"session_id", sessionID, "env_id", *envID, "err", err)
		metrics.RecordCancelRequest(mode, "queue_unavailable")
		return true
	}
	i.logger.Info("ingress: cancel routed via control queue",
		"session_id", sessionID, "env_id", *envID)
	metrics.RecordCancelRequest(mode, "control_queue")
	return true
}

// maybeRoutePermissionResponse 探测 msg 是否是 control_response 帧;是则把
// permission 答复通过 control 队列投到 daemon。返回 true 表示识别为
// control_response (无论后续路由是否成功); false 表示不是。
//
// 当前 daemon → client 唯一会发的 control 子类型就是 can_use_tool, 所以
// 这里把所有 control_response 当作 permission_response 处理。如果将来
// daemon 开始发 set_model / mcp_status 等其它 control_request, 需要在
// askPermission map 注册原始 request_id 来分辨, 或者在协议层加
// "in_response_to_subtype" 字段。
//
// chat-mode 没有 daemon, 不可能产生 permission 反向请求 —— envID 为 nil
// 时直接 return。
func (i *Ingress) maybeRoutePermissionResponse(ctx context.Context, sessionID uuid.UUID, envID *uuid.UUID, msg []byte) bool {
	var head inboundFrameHead
	if err := json.Unmarshal(msg, &head); err != nil {
		return false
	}
	if head.Type != "control_response" {
		return false
	}
	if envID == nil {
		i.logger.Info("ingress: permission_response — no environment, dropping",
			"session_id", sessionID)
		return true
	}
	if i.queue == nil {
		i.logger.Warn("ingress: permission_response cannot route — Queue not wired",
			"session_id", sessionID)
		return true
	}
	if head.Response == nil {
		// 不该发生 —— control_response 强制带 response。日志即可。
		i.logger.Warn("ingress: permission_response missing response body",
			"session_id", sessionID)
		return true
	}
	payload := map[string]any{
		"type":       "permission_response",
		"session_id": sessionID.String(),
		"request_id": head.Response.RequestID,
		"subtype":    head.Response.Subtype,  // "success" | "error"
		"response":   head.Response.Response, // 嵌套 PermissionResult JSON
		"error":      head.Response.Error,
	}
	if err := i.queue.EnqueueControl(ctx, *envID, payload); err != nil {
		i.logger.Warn("ingress: permission_response enqueue failed",
			"session_id", sessionID, "env_id", *envID, "err", err)
		return true
	}
	i.logger.Info("ingress: permission_response routed via control queue",
		"session_id", sessionID, "env_id", *envID,
		"request_id", head.Response.RequestID)
	return true
}

// startStreamConsumer 起 server→client 推流路径。两条分支：
//
//   - sinceSeq == 0 → 普通"实时拉"：bus.Subscribe DeliverNew durable consumer，
//     新消息推给当前 WS。
//   - sinceSeq > 0  → "resume 重放" + 接实时：用 raw OrderedConsumer with
//     DeliverByStartSequencePolicy + OptStartSeq=sinceSeq+1。先校验 sinceSeq
//     >= stream 当前 FirstSeq（没被 trim 掉）；否则发 SessionDesynced 帧 +
//     close —— 客户端按提示去 /v1/agent/sessions/{id}/result 拿最终态。
//
// 返回 cleanup 函数 + 错误。错误时已经把 close frame 写出去了。
func (i *Ingress) startStreamConsumer(
	ctx context.Context,
	js bus.JetStream,
	conn *websocket.Conn,
	sessionID uuid.UUID,
	sinceSeq uint64,
	writeFrame func([]byte) error,
	cancelSub context.CancelFunc,
) (cleanup func(), err error) {
	subject := SessionSubjectOut(sessionID.String())

	if sinceSeq == 0 {
		// 实时拉路径
		connID := uuid.New().String()[:8]
		spec := bus.ConsumerSpec{
			Stream:        SessionStreamName,
			Durable:       fmt.Sprintf("ingress-%s-%s", sessionID, connID),
			FilterSubject: subject,
			AckWait:       30 * time.Second,
			MaxDeliver:    1, // 不重传 —— frame 顺序优先
		}
		sub, subErr := js.Subscribe(ctx, spec, func(_ context.Context, m *bus.Message) error {
			if i.logger.Enabled(ctx, slog.LevelDebug) {
				i.logger.Debug("ingress: frame out",
					"session_id", sessionID, "subject", m.Subject,
					"bytes", len(m.Body))
			}
			if werr := writeFrame(m.Body); werr != nil {
				cancelSub()
				return werr
			}
			return nil
		})
		if subErr != nil {
			i.logger.Warn("ingress: subscribe live failed",
				"session_id", sessionID, "err", subErr)
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "subscribe_failed"),
				time.Now().Add(ingressWriteWait))
			return nil, subErr
		}
		return func() { _ = sub.Drain() }, nil
	}

	// Resume 路径 —— 用 raw JetStream 拿 OrderedConsumer with start-sequence。
	rawJS := js.RawJetStream()
	if rawJS == nil {
		// noop bus 或测试环境 —— resume 无法实现，告知客户端
		_ = writeSessionDesynced(writeFrame, sessionID, sinceSeq, "resume not available on this broker")
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "desynced"),
			time.Now().Add(ingressWriteWait))
		return nil, errors.New("ingress: raw JS unavailable for resume")
	}
	stream, sErr := rawJS.Stream(ctx, SessionStreamName)
	if sErr != nil {
		i.logger.Warn("ingress: get stream failed", "err", sErr)
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "stream_unavailable"),
			time.Now().Add(ingressWriteWait))
		return nil, sErr
	}
	info, iErr := stream.Info(ctx)
	if iErr != nil {
		i.logger.Warn("ingress: stream info failed", "err", iErr)
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "stream_info_failed"),
			time.Now().Add(ingressWriteWait))
		return nil, iErr
	}

	// FirstSeq 是 stream 当前持有的最早消息序号。如果 sinceSeq < FirstSeq，
	// 客户端要的历史已被 MaxAge / MaxMsgs trim 掉 —— desync。
	// 注意：sinceSeq 是"客户端**已经看过**的最大 seq"，所以等于 FirstSeq-1 也算
	// desync（要重放 FirstSeq 之前的不可能）。下面不等号略宽松：sinceSeq <
	// FirstSeq-1 才报；FirstSeq-1 <= sinceSeq < FirstSeq 时正好接上。
	if sinceSeq+1 < info.State.FirstSeq {
		_ = writeSessionDesynced(writeFrame, sessionID, sinceSeq,
			fmt.Sprintf("requested seq %d before stream first seq %d", sinceSeq, info.State.FirstSeq))
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "desynced"),
			time.Now().Add(ingressWriteWait))
		return nil, errors.New("desynced")
	}

	cons, cErr := rawJS.OrderedConsumer(ctx, SessionStreamName, jetstream.OrderedConsumerConfig{
		FilterSubjects:    []string{subject},
		DeliverPolicy:     jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:       sinceSeq + 1,
		ReplayPolicy:      jetstream.ReplayInstantPolicy,
		InactiveThreshold: 5 * time.Minute, // ephemeral，断连后 broker 自清
	})
	if cErr != nil {
		i.logger.Warn("ingress: ordered consumer failed", "err", cErr)
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "consumer_failed"),
			time.Now().Add(ingressWriteWait))
		return nil, cErr
	}
	cc, conErr := cons.Consume(func(m jetstream.Msg) {
		if werr := writeFrame(m.Data()); werr != nil {
			cancelSub()
			return
		}
		// OrderedConsumer 不需要手动 ack（自动跟踪 sequence）
	})
	if conErr != nil {
		i.logger.Warn("ingress: consume failed", "err", conErr)
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "consume_failed"),
			time.Now().Add(ingressWriteWait))
		return nil, conErr
	}
	return func() { cc.Stop() }, nil
}

// writeSessionDesynced 给客户端发一个 sdkproto SessionDesynced 帧。
// 客户端按规约去 /v1/agent/sessions/{id}/result 拿最终摘要。
//
// 不直接依赖 sdkproto 包（避免循环 import 风险）—— 写裸 JSON，wire 跟
// schema/sdk/v1/lifecycle.json#/SessionDesynced 完全一致。
func writeSessionDesynced(write func([]byte) error, sessionID uuid.UUID, sinceSeq uint64, reason string) error {
	frame := map[string]any{
		"type":             "biumind.session_desynced",
		"session_id":       sessionID.String(),
		"final_result_url": fmt.Sprintf("/v1/agent/sessions/%s/result", sessionID),
		"since_seq":        sinceSeq,
		"reason":           reason, // 非 schema 必填字段，调试用
	}
	body, _ := json.Marshal(frame)
	return write(body)
}

// parseSinceSeq 解析 ?since_seq=N。0 / 缺失 / 非数字 → 0（走实时路径）。
func parseSinceSeq(r *http.Request) uint64 {
	v := r.URL.Query().Get("since_seq")
	if v == "" {
		return 0
	}
	n, err := parseUint64(v)
	if err != nil {
		return 0
	}
	return n
}

// parseUint64 是简化版 strconv.ParseUint —— 拒绝负号 / 非数字 / 溢出。
func parseUint64(s string) (uint64, error) {
	if len(s) == 0 || len(s) > 19 { // 超 19 位必溢出 uint64
		return 0, errors.New("bad length")
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("non-numeric")
		}
		// 溢出 check：19 位以内 ParseUint 不会溢，简单累加足够
		n = n*10 + uint64(c-'0')
	}
	return n, nil
}
