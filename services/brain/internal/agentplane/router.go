// Agent Plane 路由层（S3-6）—— `POST /v1/agent/sessions` 按 mode 分流：
//
//	mode=chat  → 不绑 environment；session 行就位，等 S4 切到 biumindkit
//	             内嵌执行（brain 进程内跑 Anthropic）
//	mode=agent → environment_id 必填；校验 env 在线 + 属于本用户 →
//	             insert session → EnqueueWork(env_id, payload)
//	mode=task  → environment_id 可空；从 runtime pool 选一个 →
//	             insert session → EnqueueWork(env_id, payload)
//
// 所有 mode 都返回 `{session_id, session_token, expires_at, mode,
// jetstream_subject_in/out}`。session_token 走 S3-9 的 30min JWT。
//
// finalize hook：`FinalizeSessionResult` 由 S3-5 ingress 在收到
// SDKResultMessage 时调；这里只提供 helper，wire-up 在 S3-5 落地。

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// stampToolPolicy（R6.3 / D7）：若 environment 关联了 device，把该设备的
// agent_devices.tool_policy preset stamp 进 payload，daemon 取它与本地
// --tool-policy 交集做能力地板。无 device（task pool / JWT 注册）/ 查不到 → 空，
// daemon 用本地 flag。查询失败仅 log 不阻断（daemon 地板兜底）。
func (s *Server) stampToolPolicy(ctx context.Context, env *Environment, payload *WorkPayload) {
	if env == nil || env.DeviceID == nil {
		return
	}
	policy, err := s.Store.GetDeviceToolPolicy(ctx, *env.DeviceID)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.Logger.Warn("agentplane: lookup device tool_policy", "device_id", *env.DeviceID, "err", err)
		}
		return
	}
	payload.ToolPolicy = policy
}

// allowedModes 跟 schema CHECK 约束一致。
var allowedModes = map[string]bool{
	"chat":  true,
	"agent": true,
	"task":  true,
}

// CreateSessionAPIReq 是 HTTP 入参（独立于 store.CreateSessionReq —— 后者
// 只含 DB 字段；这个还要带 prompt + pool_tag 等路由层概念）。
type CreateSessionAPIReq struct {
	Mode          string `json:"mode"`
	EnvironmentID string `json:"environment_id,omitempty"` // agent 必填
	ThreadID      string `json:"thread_id,omitempty"`
	Model         string `json:"model,omitempty"`
	// ProviderID 锁定走哪个 chat.providers.provider_id slug。空 → brain
	// 自己挑(老语义)。同 model id 多 provider 时用此字段消歧。
	ProviderID   string `json:"provider_id,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	Prompt       string `json:"prompt,omitempty"`   // agent/task 首条 user msg
	PoolTag      string `json:"pool_tag,omitempty"` // task only
	// Workdir 透传给 daemon 作为 chdir + biumindkit Options.Cwd。
	// chat 模式忽略；agent / task 模式由客户端从 chat.threads.workdir 读出
	// 后传过来。安全门：daemon 端 --allowed-roots flag 校验是否在白名单内。
	Workdir string `json:"workdir,omitempty"`
	// RuntimeEnvMode 是工具执行环境（Runtime v3 轴 B）：'none' | 'local' |
	// 'cloud'。空 → 按 mode 推默认（chat=none、agent=local、task=cloud）。
	// chat 模式忽略（恒 none）。决定 worker 端选哪个 ToolExecHost。
	RuntimeEnvMode string `json:"runtime_env_mode,omitempty"`
	// Backend（Runtime v3 R3/Q3）：'biumindkit'(默认/空) | 'claude-cli' |
	// 'codex-cli'。agent 模式选外部 CLI backend 时由 daemon spawn 对应 CLI。
	Backend string `json:"backend,omitempty"`
	// Images 是当前 turn 用户附带的图片附件（chat 模式 vision 模型才有效）。
	// 客户端 composer 拖拽 / 粘贴 / picker 收的图，base64 编码后透传过来。
	// 大小约束链：客户端发送前压缩到单图 ≤1MB / 长边 ≤1568px（对齐下游
	// 厂商限制：Claude 单图 5MB 且 >1568px 会被服务端降采样）；site nginx
	// client_max_body_size 20m；handler 层 20MB MaxBytesReader 兜底
	// （见 handleCreateSession）。
	Images []ChatImageInput `json:"images,omitempty"`
	// History 是当前 turn **之前**的对话历史（chat 模式多轮上下文，Runtime v3
	// R4）。客户端按时间升序带最近 N 轮（client 端截断防膨胀）；brain 不持久化
	// WS chat 消息，多轮上下文完全由客户端经此字段带入（维持 Agent Plane 与
	// chat.Store 解耦）。空 = 单轮（向后兼容）。仅 chat 模式用。
	History []ChatTurn `json:"history,omitempty"`
	// UserMessageID / AssistantMessageID 是 client 为本轮预生成的 message uuid
	// （方案3：本地 message.id == brain chat.messages.id，编辑/删除上行直连）。
	// 空时 brain 走 gen_random_uuid（向后兼容旧 client）。user id 用于落 user
	// 轮 PK；assistant id 经 Transcript recorder 存到 result 帧落 assistant 轮用。
	UserMessageID      string `json:"user_message_id,omitempty"`
	AssistantMessageID string `json:"assistant_message_id,omitempty"`
	// ClientSideRecordID/BaseURL/Protocol（B2）：client-side BYOK 信号。Flutter
	// 命中 client-side（identity is_client_side=true 记录 + 本地 keychain 有 key）
	// 时透传。brain 不碰 key —— key 经 daemon loopback 注入本机 daemon 内存，
	// 不经 brain/NATS。brain 仅透传 record_id（daemon 从内存 store 取 key）+
	// base_url/protocol（daemon 建 engine 直连上游，跳 model-relay）。空 → daemon
	// 走 relay（cloud BYOK / 平台池）。
	ClientSideRecordID string `json:"client_side_record_id,omitempty"`
	ClientSideBaseURL  string `json:"client_side_base_url,omitempty"`
	ClientSideProtocol string `json:"client_side_protocol,omitempty"`
}

// ChatImageInput 是 HTTP 请求里单张图片的载体。MimeType 必填（image/png /
// image/jpeg / image/webp），Data 是 base64 编码的字节流（不带 data: 前缀）。
type ChatImageInput struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

// ChatTurn 是一轮历史消息。Role: "user" | "assistant"。
type ChatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// WorkPayload 是 brain 投递给 worker 的任务体。worker 端拿到后 spawn engine
// 跑这个 prompt。schema 故意松散 —— 字段会随 S4/S5 迭代。
//
// 敏感字段（API key 等）后续可走 envelope encryption (S3-4) —— 当前 stage
// 全字段明文，等下个 stage 启用加密层。
type WorkPayload struct {
	SessionID uuid.UUID `json:"session_id"`
	UserID    uuid.UUID `json:"user_id"`
	Mode      string    `json:"mode"`
	Prompt    string    `json:"prompt,omitempty"`
	Model     string    `json:"model,omitempty"`
	// ProviderID 锁定走哪个 chat.providers.provider_id slug。空 → 老语义。
	ProviderID   string `json:"provider_id,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	// Workdir 让 daemon 知道在哪个目录跑工具。空 → daemon 用启动时的 cwd。
	// daemon 端 worker.go::handleWork 把它写到 biumindkit Options.Cwd +
	// 通过 PermissionUpdate.AddDirectories 加入工作目录白名单。
	Workdir string `json:"workdir,omitempty"`
	// RuntimeEnvMode 是工具执行环境（Runtime v3 轴 B）：'none' | 'local' |
	// 'cloud'。daemon / runtime 据此选 biumindkit ToolExecHost：none=无外设、
	// local=本机进程执行、cloud=services/sandbox 容器（R5 落地，当前 stub）。
	// agent / task 模式有意义；chat 不投 NATS。
	RuntimeEnvMode string `json:"runtime_env_mode,omitempty"`
	// Backend（Runtime v3 R3/Q3）：外部 CLI backend 标识；daemon 据此决定
	// 走 agent.Runner（外部 CLI 自执行,D1）还是内建 biumindkit。空=biumindkit。
	Backend string `json:"backend,omitempty"`
	// ToolPolicy（R6.3 / D7）：目标 device 的 agent_devices.tool_policy preset
	// （readonly|workspace-write|full）。daemon 取它与本地 --tool-policy 的交集做
	// 能力地板。空 → 该 environment 无关联 device（task pool / JWT 注册）或 device
	// 无 policy，daemon 用本地 flag 地板。**非密**——明文 stamp。
	ToolPolicy string `json:"tool_policy,omitempty"`
	// UserBearer 是用户 JWT(`Authorization: Bearer ...`),brain 从 create-session
	// 请求头抽出透传:
	//   - chat 模式: brain 进程内 ChatRunner.resolveCreds 用它走 model-relay
	//     PassThrough(per-user 计费 + admin channel 路由 + BYOK 都通)。
	//   - agent / task 模式(P4): 投 NATS 给 daemon,daemon 用它做 model-relay
	//     的 Authorization → relay 拿 claims.UserID 原生解析该 user 的 BYOK
	//     (BYOK.Match),与 chat 路径同构。空(daemon 离线重派 / 未带 JWT) →
	//     daemon 回退 BIUMIND_TOKEN/PAT 走平台池,BYOK 不生效。
	//
	// 安全: user JWT 走 NATS in-flight 敏感数据,但短命(session TTL ~1h,
	// 每 turn create-session 带 fresh token)。NATS 部署假设受信网络 + 鉴权。
	UserBearer string `json:"user_bearer,omitempty"`
	// Images 透传给 ChatRunner.RunSession，仅 chat 模式 vision 用。agent / task
	// 模式经 NATS 投递到 daemon —— 当前 daemon 路径不消费 Images 字段，
	// 多模态走 chat 通路（非 daemon）。
	Images []ChatImageInput `json:"images,omitempty"`
	// History 是 prior 多轮上下文。§8.2 翻案后由 **brain 服务端**从 chat.messages
	// 组装(persistUserAndAssemble),不再来自客户端。chat 模式进程内交 ChatRunner;
	// agent 模式**进 NATS payload** 投给 daemon 作 biumindkit PriorMessages。
	History []ChatTurn `json:"history,omitempty"`
	// ClientSideRecordID/BaseURL/Protocol（B2）：见 CreateSessionAPIReq 同名字段。
	// brain 仅透传（不碰 key）；daemon 据此从 loopback 内存 store 取 key 建 engine。
	ClientSideRecordID string `json:"client_side_record_id,omitempty"`
	ClientSideBaseURL  string `json:"client_side_base_url,omitempty"`
	ClientSideProtocol string `json:"client_side_protocol,omitempty"`
}

// MountSessionRoutes 把 session 创建路由注册到 mux。Server.Mount 调它。
// queue 可空（测试 / 没 NATS 时 chat mode 仍能跑；agent/task 创建会 503）。
func (s *Server) MountSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/agent/sessions", s.requireAuth(s.handleCreateSession))
}

// handleCreateSession 是 mode 分流入口。
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if s.Signer == nil {
		writeErr(w, http.StatusServiceUnavailable, "signer_unavailable",
			"create session requires Signer at server boot")
		return
	}
	uid := mustUserID(r)

	var req CreateSessionAPIReq
	// 20MB 兜底上限：正常流量客户端已压缩（单图 ≤1MB base64 ~1.4MB），
	// 超了说明是异常/恶意大 body。对齐 nginx client_max_body_size 20m，
	// 超限时返回 413 + JSON 错误（而不是 decode 出一个误导性的 bad_json，
	// 或让客户端只看得见网关的 HTML 错误页）。
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "body_too_large",
				"request body exceeds 20MB limit")
			return
		}
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if !allowedModes[req.Mode] {
		writeErr(w, http.StatusBadRequest, "bad_mode",
			"mode must be one of: chat, agent, task")
		return
	}

	switch req.Mode {
	case "chat":
		s.createChatSession(w, r, uid, req)
	case "agent":
		s.createAgentSession(w, r, uid, req)
	case "task":
		s.createTaskSession(w, r, uid, req)
	}
}

// persistUserAndAssemble 把当前 user 轮落库到 chat.messages、注册 transcript
// recorder(供 assistant 轮终止时落库)、并服务端组装 prior 多轮历史返回。
//
// 这是 §8.2 翻案的核心:brain(而非客户端)是对话历史真相源。chat / agent
// 两条路径都调它,统一持久化 + 组装。
//
// 返回的 []ChatTurn 是当前 Prompt **之前**的历史(AssembleHistory 用刚落库
// user 轮的 position 做 beforePosition 排除当前轮)。以下情况返回 (nil, nil)
// (退化为单轮,向后兼容):ChatStore 未配(dev)、无 thread、Prompt 为空。
//
// error 仅在一种情况非 nil:EnsureThread 发现 thread id 已被其他 user 占用
// (chat.ErrThreadOwnedByOther —— 同设备换账号本地缓存残留旧 id,见本地数据
// 隔离设计 §3.4)。这是硬安全门,不能静默降级;调用方须映射 HTTP 409 让
// 客户端重新生成 thread id。其余持久化/组装失败维持旧语义(log + 单轮降级)。
func (s *Server) persistUserAndAssemble(
	ctx context.Context, sessionID, userID uuid.UUID, threadID *uuid.UUID, prompt, model string,
	userMsgID, assistantMsgID *uuid.UUID,
) ([]ChatTurn, error) {
	if s.ChatStore == nil || threadID == nil || *threadID == uuid.Nil || prompt == "" {
		return nil, nil
	}
	tid := *threadID
	// FK 前置:WS/agent thread 此前只在客户端 Drift,未进 brain。落消息前
	// 幂等 ensure chat.threads 行,否则 chat.messages 外键失败。title 取首条
	// prompt 作友好默认(仅首次插入生效)。跨账号 id 冲突向上抛,由路由层
	// 返回 409 —— 绝不能以当前用户身份认领别人的 thread。
	if err := s.ChatStore.EnsureThread(ctx, tid, userID, prompt, model); err != nil {
		if errors.Is(err, chatpkg.ErrThreadOwnedByOther) {
			return nil, err
		}
		s.Logger.Warn("agentplane: ensure thread failed",
			"session_id", sessionID, "thread_id", tid, "err", err)
		return nil, nil
	}
	// client_id 幂等:同一 session 的 user 轮只落一次(createSession 重试安全)。
	cid := sessionID.String() + ":user"
	um, err := s.ChatStore.CreateMessage(ctx, chatpkg.CreateMessageInput{
		ID: userMsgID, ThreadID: tid, UserID: userID, Role: chatpkg.RoleUser,
		Content: prompt, Status: chatpkg.StatusSuccess, ClientID: &cid,
	})
	if err != nil {
		s.Logger.Warn("agentplane: persist user turn failed",
			"session_id", sessionID, "thread_id", tid, "err", err)
		return nil, nil
	}
	if s.Transcript != nil {
		s.Transcript.Begin(sessionID, tid, userID, model, assistantMsgID)
	}
	prior, err := s.ChatStore.AssembleHistory(ctx, tid, userID, um.Position)
	if err != nil {
		s.Logger.Warn("agentplane: assemble history failed",
			"session_id", sessionID, "thread_id", tid, "err", err)
		return nil, nil
	}
	out := make([]ChatTurn, 0, len(prior))
	for _, p := range prior {
		out = append(out, ChatTurn{Role: p.Role, Content: p.Content})
	}
	return out, nil
}

// createChatSession：不绑 environment，不 enqueue work。session 行写好后
// 在进程内调 ChatRunner 异步驱动 biumindkit（S4-5）—— 客户端通过 WS
// /v1/agent/sessions/{id}/stream 接收 SDK Protocol 帧。
//
// ChatRunner 可空：dev / 测试无 API key 时跳过实际 LLM 调用，session 行
// 还是写好让 ingress / S3 测试可用。
func (s *Server) createChatSession(w http.ResponseWriter, r *http.Request, uid uuid.UUID, req CreateSessionAPIReq) {
	threadID := parseOptionalUUID(req.ThreadID)
	sess, err := s.Store.InsertSession(r.Context(), CreateSessionReq{
		UserID:       uid,
		ThreadID:     threadID,
		Mode:         "chat",
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
	})
	if err != nil {
		s.serverErr(w, "insert chat session", err)
		return
	}

	// S4-5：拉起进程内 chat runner。Prompt 或 Images 至少有一边才起 runner——
	// 纯图问 "这是什么?" 也合法（vision 模型场景）。两边都空就是空 session。
	if s.ChatRunner != nil && (req.Prompt != "" || len(req.Images) > 0) {
		// §8.2 翻案:历史由 brain 服务端从 chat.messages 组装,不再用客户端
		// 带来的 req.History。落当前 user 轮 + 注册 transcript + 取 prior 多轮。
		history, perr := s.persistUserAndAssemble(
			r.Context(), sess.SessionID, uid, threadID, req.Prompt, req.Model,
			parseOptionalUUID(req.UserMessageID), parseOptionalUUID(req.AssistantMessageID))
		if perr != nil {
			// thread id 已被其他账号占用(本地数据隔离 §3.4)—— 409 + 明确
			// code 让客户端重新生成 id;刚插入的 session 标 failed,不留指向
			// 他人 thread 的活跃 session。
			_ = s.Store.UpdateSessionState(r.Context(), sess.SessionID, "failed")
			writeErr(w, http.StatusConflict, "thread_owned_by_other",
				"thread_id already belongs to another account; generate a new thread id")
			return
		}
		payload := WorkPayload{
			SessionID:    sess.SessionID,
			UserID:       uid,
			Mode:         "chat",
			Prompt:       req.Prompt,
			Model:        req.Model,
			ProviderID:   req.ProviderID,
			SystemPrompt: req.SystemPrompt,
			ThreadID:     req.ThreadID,
			Workdir:      req.Workdir,
			Images:       req.Images,
			History:      history,
			// 透传 user JWT 给 ChatRunner — biumindkit 拿来当 Bearer 喂
			// model-relay,使 chat 模式跟 chat send v1 / agent BYOK 一致
			// 走"用户身份 + admin channel 路由 + 平台计费"链路。
			UserBearer: bearerFromAuthHeader(r.Header.Get("Authorization")),
		}
		// detached ctx —— 父请求 ctx 在 HTTP response 写完后会 cancel；
		// chat runner 要继续跑到 LLM 完。background ctx 让它不受 HTTP
		// 生命周期限制，靠 timeout 兜底（biumindkit 自己有 MaxToolTurns）。
		s.ChatRunner.RunSession(detachedCtx(r.Context()), sess, payload)
	}

	s.writeSessionCreated(w, uid, sess)
}

// detachedCtx 把 r.Context() 的 values 拷出来但去掉 cancellation，让 chat
// runner 跑完整 LLM turn，不被 HTTP handler 退出截断。
func detachedCtx(parent context.Context) context.Context {
	// 简单做法 —— 用 background 不带 values。chat runner 不需要 user id /
	// trace 这层 brain 已经处理过；所以 values 丢掉无影响。
	_ = parent
	return context.Background()
}

// resolveRuntimeEnvMode 按 mode 推工具执行环境（Runtime v3 轴 B）默认值并校验
// 客户端显式值：task 恒 'cloud'（在 runtime 容器跑）；agent 默认 'local'（本机
// daemon），客户端可显式选 none/local/cloud；其余（chat 等）'none'。非法值回退
// 默认。worker 据此选 biumindkit ToolExecHost。
func resolveRuntimeEnvMode(mode, requested string) string {
	switch mode {
	case "task":
		return "cloud"
	case "agent":
		switch requested {
		case "none", "local", "cloud":
			return requested
		default:
			return "local"
		}
	default:
		return "none"
	}
}

// createAgentSession：environment_id 必填；校验 env 状态。
func (s *Server) createAgentSession(w http.ResponseWriter, r *http.Request, uid uuid.UUID, req CreateSessionAPIReq) {
	envID, err := uuid.Parse(req.EnvironmentID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_environment_id",
			"environment_id required for mode=agent")
		return
	}
	env, err := s.Store.GetEnvironment(r.Context(), uid, envID)
	if err != nil {
		// 跨用户访问 / 不存在 / 也包含没 owner 的 runtime（biu_daemon
		// 才走这里）—— 一律 404，不区分原因
		s.handleStoreErr(w, err)
		return
	}
	if env.WorkerKind != "biu_daemon" && env.WorkerKind != "biu_cli" {
		// runtime 走 task mode，agent mode 用 biu_daemon
		writeErr(w, http.StatusBadRequest, "wrong_worker_kind",
			fmt.Sprintf("environment is %s; agent mode expects biu_daemon", env.WorkerKind))
		return
	}

	online := env.State == "online"
	// R7：离线设备 → 任务排队，设备上线后重派。但只有**配对设备**（env 有
	// device_id）能稳定重派（device_id 是跨重启锚点；env_id 每次重启 churn）。
	// 离线且无 device_id（裸 PAT 注册）→ 无法可靠重派，保持 409。
	if !online && env.DeviceID == nil {
		writeErr(w, http.StatusConflict, "environment_offline",
			fmt.Sprintf("environment %s is %s, not online (pair the device to enable offline queueing)", env.EnvironmentID, env.State))
		return
	}

	// R7 离线上限**预检**：在建 session 行之前廉价拒掉常态超限，避免白建一条
	// pending session（FK 要求 session 先于 pending_work，超限拒绝时它就成了无
	// pending_work 的孤儿——janitor 的 orphan/expire sweep 都扫不到它）。原子守卫
	// 在 InsertPendingWorkIfUnderLimit 里兜并发竞争。
	if !online {
		if n, cerr := s.Store.CountPendingWorkByDevice(r.Context(), *env.DeviceID); cerr == nil && n >= maxPendingPerDevice {
			writeErr(w, http.StatusTooManyRequests, "pending_queue_full",
				fmt.Sprintf("device already has %d queued tasks (max %d)", n, maxPendingPerDevice))
			return
		}
	}

	threadID := parseOptionalUUID(req.ThreadID)
	envIDCopy := env.EnvironmentID
	renv := resolveRuntimeEnvMode("agent", req.RuntimeEnvMode)
	state := "active"
	if !online {
		state = "pending" // R7：等设备上线
	}
	sess, err := s.Store.InsertSession(r.Context(), CreateSessionReq{
		UserID:         uid,
		EnvironmentID:  &envIDCopy,
		ThreadID:       threadID,
		Mode:           "agent",
		Model:          req.Model,
		SystemPrompt:   req.SystemPrompt,
		RuntimeEnvMode: renv,
		Backend:        req.Backend,
		State:          state,
	})
	if err != nil {
		s.serverErr(w, "insert agent session", err)
		return
	}

	// §8.2 翻案:落当前 user 轮 + 服务端组装 prior 多轮(brain 真相源)。
	// online 分支把 history 透传给 daemon 作 PriorMessages;offline(pending)
	// 分支当前不带(重派路径暂不复用,见 plan 风险项),但 user 轮已落库,
	// 设备上线重派时历史仍在 chat.messages(后续可让重派路径重新组装)。
	history, perr := s.persistUserAndAssemble(
		r.Context(), sess.SessionID, uid, threadID, req.Prompt, req.Model,
		parseOptionalUUID(req.UserMessageID), parseOptionalUUID(req.AssistantMessageID))
	if perr != nil {
		// thread id 已被其他账号占用(本地数据隔离 §3.4)—— 同 chat 分支,
		// 409 + code 让客户端重新生成 id;session 标 failed 不留孤儿。
		_ = s.Store.UpdateSessionState(r.Context(), sess.SessionID, "failed")
		writeErr(w, http.StatusConflict, "thread_owned_by_other",
			"thread_id already belongs to another account; generate a new thread id")
		return
	}

	spec := agentWorkSpec{
		SessionID: sess.SessionID, UserID: uid,
		Prompt: req.Prompt, Model: req.Model, ProviderID: req.ProviderID,
		SystemPrompt: req.SystemPrompt, ThreadID: req.ThreadID, Workdir: req.Workdir,
		RuntimeEnvMode: renv, Backend: req.Backend,
		UserBearer:         bearerFromAuthHeader(r.Header.Get("Authorization")),
		History:            history,
		ClientSideRecordID: req.ClientSideRecordID,
		ClientSideBaseURL:  req.ClientSideBaseURL,
		ClientSideProtocol: req.ClientSideProtocol,
	}

	if !online {
		// R7 离线分支：持久化请求参数（不含 BYOK），设备重连(handleRegister)时
		// 重建 WorkPayload 重新 enqueue。原子守卫上限（关掉预检→插入的竞争窗口）。
		ok, perr := s.Store.InsertPendingWorkIfUnderLimit(r.Context(), PendingWork{
			SessionID: sess.SessionID, UserID: uid, DeviceID: *env.DeviceID,
			Prompt: req.Prompt, Model: req.Model, ProviderID: req.ProviderID,
			SystemPrompt: req.SystemPrompt, ThreadID: req.ThreadID, Workdir: req.Workdir,
			RuntimeEnvMode: renv, Backend: req.Backend,
		}, time.Now().Add(pendingWorkTTL), maxPendingPerDevice)
		if perr != nil {
			s.serverErr(w, "queue pending work", perr)
			return
		}
		if !ok {
			// 预检后并发抢满了配额——刚建的 pending session 没有 work 兜底，
			// 立即标 failed 收尾（否则成 janitor 扫不到的 pending 孤儿），再拒。
			_ = s.Store.UpdateSessionState(r.Context(), sess.SessionID, "failed")
			writeErr(w, http.StatusTooManyRequests, "pending_queue_full",
				fmt.Sprintf("device queue full (max %d)", maxPendingPerDevice))
			return
		}
		s.Logger.Info("agentplane: agent task queued for offline device",
			"session_id", sess.SessionID, "device_id", *env.DeviceID)
		s.writeSessionCreated(w, uid, sess)
		return
	}

	// 在线分支：直接 enqueue 到 environment 的 subject。queue 可空（dev /
	// 测试无 NATS，或 readiness 尚未就绪）—— 跳过仍返回 session token，
	// 但 worker 不会收到任务。
	if q := s.queue(); q != nil {
		payload := s.buildAgentWorkPayload(r.Context(), env, spec)
		// workID 用 session_id —— 一个 session 第一条 work 的天然唯一 ID。
		if err := q.EnqueueWork(r.Context(), env.EnvironmentID, sess.SessionID.String(), payload); err != nil {
			s.serverErr(w, "enqueue agent work", err)
			return
		}
	}

	s.writeSessionCreated(w, uid, sess)
}

// pendingWorkTTL / maxPendingPerDevice —— R7 离线队列边界。TTL 跟 janitor 的
// 离线 env GC（7d）对齐：设备 7 天没上线，挂起任务过期标 failed。
const (
	pendingWorkTTL      = 7 * 24 * time.Hour
	maxPendingPerDevice = 20
)

// agentWorkSpec 是构建 agent WorkPayload 的中性输入（createAgentSession 在线
// 路径 + 离线重派路径共用，不依赖 HTTP req）。
type agentWorkSpec struct {
	SessionID                                                                           uuid.UUID
	UserID                                                                              uuid.UUID
	Prompt, Model, ProviderID, SystemPrompt, ThreadID, Workdir, RuntimeEnvMode, Backend string
	// UserBearer 是当前请求的用户 JWT（bearerFromAuthHeader 抽出）。在线路径
	// 从 HTTP req 来；离线重派路径无 req → 空 → daemon 回退平台池（BYOK 不生效，
	// 离线重派时原 JWT 早过期，本就无法 BYOK）。
	UserBearer string
	// ClientSideRecordID/BaseURL/Protocol（B2）：从 create-session req 透传，
	// brain 不碰 key。离线重派路径无 req → 空（client-side 同机前提，离线场景
	// daemon 不在本机 / 已重启丢 key，本就不工作，同 UserBearer 离线丢）。
	ClientSideRecordID, ClientSideBaseURL, ClientSideProtocol string
	// History 是 brain 服务端组装的 prior 多轮(不含当前 Prompt),透传给
	// daemon 作 biumindkit PriorMessages。Runtime v3 §8.2 翻案:不再由客户端带入。
	History []ChatTurn
}

// buildAgentWorkPayload stamp tool_policy + 塞 UserBearer，产出投递给 daemon 的
// WorkPayload。P4: BYOK 不再由 brain 预解析投递 —— daemon 拿 UserBearer 自己打
// model-relay，relay 按 claims.UserID 原生解析 BYOK（同 chat 路径）。旧的
// sealBYOK/EncBYOK/BYOKKey 加密投递链已删（P1 起 relay 不再读 X-Biumind-LLM-Key
// header fast-path，那条链是死信）。
func (s *Server) buildAgentWorkPayload(ctx context.Context, env *Environment, spec agentWorkSpec) WorkPayload {
	payload := WorkPayload{
		SessionID:          spec.SessionID,
		UserID:             spec.UserID,
		Mode:               "agent",
		Prompt:             spec.Prompt,
		Model:              spec.Model,
		ProviderID:         spec.ProviderID,
		SystemPrompt:       spec.SystemPrompt,
		ThreadID:           spec.ThreadID,
		Workdir:            spec.Workdir,
		RuntimeEnvMode:     spec.RuntimeEnvMode,
		Backend:            spec.Backend,
		UserBearer:         spec.UserBearer,
		History:            spec.History,
		ClientSideRecordID: spec.ClientSideRecordID,
		ClientSideBaseURL:  spec.ClientSideBaseURL,
		ClientSideProtocol: spec.ClientSideProtocol,
	}
	s.stampToolPolicy(ctx, env, &payload) // R6.3 per-device 能力 preset
	return payload
}

// dispatchPendingForDevice（R7）：设备重连后把它的挂起 agent 任务重新派发到新
// environment。handleRegister 在 RegisterEnvironment 成功后调（env 有 device_id）。
// 单条失败不影响其他；enqueue 成功才翻转 session + 删 pending（失败保留下次重试）。
func (s *Server) dispatchPendingForDevice(ctx context.Context, env *Environment) {
	q := s.queue()
	if q == nil || env.DeviceID == nil {
		return
	}
	pending, err := s.Store.ListPendingWorkByDevice(ctx, *env.DeviceID)
	if err != nil {
		s.Logger.Error("agentplane: list pending work for device", "device_id", *env.DeviceID, "err", err)
		return
	}
	for _, pw := range pending {
		payload := s.buildAgentWorkPayload(ctx, env, agentWorkSpec{
			SessionID: pw.SessionID, UserID: pw.UserID,
			Prompt: pw.Prompt, Model: pw.Model, ProviderID: pw.ProviderID,
			SystemPrompt: pw.SystemPrompt, ThreadID: pw.ThreadID, Workdir: pw.Workdir,
			RuntimeEnvMode: pw.RuntimeEnvMode, Backend: pw.Backend,
		})
		if err := q.EnqueueWork(ctx, env.EnvironmentID, pw.SessionID.String(), payload); err != nil {
			s.Logger.Error("agentplane: redispatch pending work", "session_id", pw.SessionID, "err", err)
			continue // 保留 pending 行，下次重连重试
		}
		if _, uerr := s.Store.UpdateSessionEnvAndState(ctx, pw.SessionID, env.EnvironmentID, "active"); uerr != nil {
			s.Logger.Error("agentplane: activate redispatched session", "session_id", pw.SessionID, "err", uerr)
			// 已 enqueue；不删 pending 以免丢失重试线索（重派幂等：session_id 作
			// Nats-Msg-Id，10min dedupe 窗口内不会双投）。
			continue
		}
		if derr := s.Store.DeletePendingWork(ctx, pw.PendingID); derr != nil {
			s.Logger.Warn("agentplane: delete dispatched pending work", "pending_id", pw.PendingID, "err", derr)
		}
		s.Logger.Info("agentplane: redispatched offline task to reconnected device",
			"session_id", pw.SessionID, "env_id", env.EnvironmentID, "device_id", *env.DeviceID)
	}
}

// createTaskSession：从 runtime pool 选一个 online environment。
func (s *Server) createTaskSession(w http.ResponseWriter, r *http.Request, uid uuid.UUID, req CreateSessionAPIReq) {
	env, err := s.Store.PickRuntimeEnvironment(r.Context(), req.PoolTag)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusServiceUnavailable, "no_runtime_available",
				fmt.Sprintf("no online runtime in pool %q", req.PoolTag))
			return
		}
		s.serverErr(w, "pick runtime", err)
		return
	}

	threadID := parseOptionalUUID(req.ThreadID)
	envIDCopy := env.EnvironmentID
	renv := resolveRuntimeEnvMode("task", req.RuntimeEnvMode) // 恒 cloud
	sess, err := s.Store.InsertSession(r.Context(), CreateSessionReq{
		UserID:         uid,
		EnvironmentID:  &envIDCopy,
		ThreadID:       threadID,
		Mode:           "task",
		Model:          req.Model,
		SystemPrompt:   req.SystemPrompt,
		RuntimeEnvMode: renv,
	})
	if err != nil {
		s.serverErr(w, "insert task session", err)
		return
	}

	if q := s.queue(); q != nil {
		payload := WorkPayload{
			SessionID:      sess.SessionID,
			UserID:         uid,
			Mode:           "task",
			Prompt:         req.Prompt,
			Model:          req.Model,
			ProviderID:     req.ProviderID,
			SystemPrompt:   req.SystemPrompt,
			ThreadID:       req.ThreadID,
			Workdir:        req.Workdir,
			RuntimeEnvMode: renv,
			UserBearer:     bearerFromAuthHeader(r.Header.Get("Authorization")),
		}
		// R6.3：task pool 的 runtime environment 通常无 device → no-op；保持对称。
		s.stampToolPolicy(r.Context(), env, &payload)
		if err := q.EnqueueWork(r.Context(), env.EnvironmentID, sess.SessionID.String(), payload); err != nil {
			s.serverErr(w, "enqueue task work", err)
			return
		}
	}

	s.writeSessionCreated(w, uid, sess)
}

// writeSessionCreated 是 3 个 mode 共用的成功响应写出。颁发 session_token +
// 把 jetstream subject 给客户端（让它知道往哪 ingress）。
func (s *Server) writeSessionCreated(w http.ResponseWriter, userID uuid.UUID, sess *Session) {
	tok, expiresAt, err := IssueSessionToken(s.Signer, userID, sess.SessionID)
	if err != nil {
		s.serverErr(w, "issue session_token", err)
		return
	}
	subjectIn := "biu.session." + sess.SessionID.String() + ".in"
	subjectOut := "biu.session." + sess.SessionID.String() + ".out"
	out := map[string]any{
		"session_id":            sess.SessionID.String(),
		"session_token":         tok,
		"expires_at":            expiresAt.UnixMilli(),
		"mode":                  sess.Mode,
		"state":                 sess.State,
		"jetstream_subject_in":  subjectIn,
		"jetstream_subject_out": subjectOut,
		"created_at":            sess.CreatedAt.UnixMilli(),
	}
	if sess.EnvironmentID != nil {
		out["environment_id"] = sess.EnvironmentID.String()
	}
	if sess.ThreadID != nil {
		out["thread_id"] = sess.ThreadID.String()
	}
	writeJSON(w, http.StatusCreated, out)
}

// ─── Finalize hook（S3-5 ingress 触发） ────────────────────────

// FinalizeOpts 是 ingress 检测到 SDKResultMessage 时收集的字段。所有字段
// optional —— ingress 能取到啥就传啥，不能取到留空让 store 写 NULL。
type FinalizeOpts struct {
	Status           string // 'completed' | 'failed' | 'cancelled'
	FinalText        string
	FinalParts       []byte // JSONB raw
	ToolCallsSummary []byte
	CostUSD          float64
	PromptTokens     int
	CompletionTokens int
	DurationMs       int64
	ErrorMessage     string
}

// FinalizeSessionResult 是 ingress 在 SDKResultMessage 时调。Task 模式
// 写 agent_session_results 一行；chat / agent 模式仅 update agent_sessions
// state（chat.messages 走老路径，那是 S4 集成时的事）。
//
// 调用方拿到 *Session（ingress 自己会查），由它决定 mode 然后路由。
func FinalizeSessionResult(
	ctx context.Context,
	store *Store,
	sess *Session,
	opts FinalizeOpts,
) error {
	if sess == nil {
		return errors.New("agentplane: nil session")
	}
	if sess.Mode == "task" {
		// Task 模式：写最终态摘要
		if err := store.InsertSessionResult(ctx, SessionResult{
			SessionID:        sess.SessionID,
			Status:           opts.Status,
			FinalText:        opts.FinalText,
			FinalParts:       opts.FinalParts,
			ToolCallsSummary: opts.ToolCallsSummary,
			CostUSD:          opts.CostUSD,
			PromptTokens:     opts.PromptTokens,
			CompletionTokens: opts.CompletionTokens,
			DurationMs:       opts.DurationMs,
			ErrorMessage:     opts.ErrorMessage,
		}); err != nil {
			return fmt.Errorf("agentplane: finalize task: %w", err)
		}
	}
	// 任何 mode 都把 session state 切到终态
	if err := store.UpdateSessionState(ctx, sess.SessionID, opts.Status); err != nil {
		return fmt.Errorf("agentplane: update session state: %w", err)
	}
	return nil
}

// parseOptionalUUID —— 空字符串 → nil（DB 写 NULL）；非法 UUID 静默 nil
// （路由层选择不强校验 thread_id，避免拒绝 chat thread 还没创建的 session）。
// bearerFromAuthHeader 抽 Authorization 头里 Bearer 后面的 token。空 / 格
// 式不对一律返空字符串(调用方需要靠空字符串决定 fallback,见
// ChatRunner.resolveCreds)。
func bearerFromAuthHeader(auth string) string {
	const prefix = "Bearer "
	if len(auth) <= len(prefix) {
		return ""
	}
	if auth[:len(prefix)] != prefix {
		return ""
	}
	return auth[len(prefix):]
}

func parseOptionalUUID(s string) *uuid.UUID {
	if s == "" {
		return nil
	}
	if id, err := uuid.Parse(s); err == nil {
		return &id
	}
	return nil
}

// 为了让 Server 编译，确保 bauth 类型仍 in scope（router.go 不直接用但
// 不引就会有 unused import warning 在 Edit 时—— 已经在 api.go 引入；
// 这里保持纯逻辑模块）
var _ = bauth.NewSigner
