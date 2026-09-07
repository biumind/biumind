// P3-c durable resume —— elicitation 中断后的恢复端点：
//
//	POST /v1/agent/sessions/{id}/resume         paused chat session 重跑
//	GET  /v1/agent/sessions/{id}/elicitations   列出待答提问（客户端重建表单）
//	GET  /v1/agent/sessions/{id}/result         session 最终态摘要（补 S3-5B
//	                                            desync 提示引用的死端点）
//
// resume 语义（拍板 §6.3-4，恢复语义 = 重跑 + 答案注入）：
//
//  1. 仅 paused 的 chat session（environment_id IS NULL）可调 —— daemon
//     模式靠 work redeliver 天然重跑重问，无 resume 概念；active/failed/
//     completed 一律 409。
//  2. 仍有 pending 未答的 elicitation → 409 elicitation_pending（响应体带
//     待答提问快照，客户端可直接渲染表单）。
//  3. 答案齐 → ResumeSessionCAS 翻回 active（并发守卫）→ 往 chat.messages
//     落一条 synthetic user 消息（多题合并「我已回答你的提问：Q:… A:…
//     请继续。」）→ AssembleHistory 重组历史 → ChatRunner.RunSession 走
//     现有 chat turn 链路续跑（不新造引擎调用）→ 推 SessionResumed 帧。
//
// 诚实边界：写类工具是 at-least-once —— 重跑会把中断 turn 已执行过的写
// 工具再跑一遍（设计 §6.3 C1 拍板接受）。task 模式（runtime 云沙箱）
// AskUser 未接通，不覆盖。

package agentplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// MountResumeRoutes 注册 durable resume 相关路由。Server.Mount 调它。
func (s *Server) MountResumeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/agent/sessions/{id}/resume", s.requireAuth(s.handleResumeSession))
	mux.HandleFunc("GET  /v1/agent/sessions/{id}/elicitations", s.requireAuth(s.handleListElicitations))
	mux.HandleFunc("GET  /v1/agent/sessions/{id}/result", s.requireAuth(s.handleSessionResult))
}

// ─── GET elicitations（客户端重建表单）────────────────────────

func (s *Server) handleListElicitations(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	if _, err := s.Store.GetSession(r.Context(), uid, sessionID); err != nil {
		s.handleStoreErr(w, err) // 跨用户 / 不存在一律 404
		return
	}
	rows, err := s.Store.ListPendingElicitations(r.Context(), sessionID)
	if err != nil {
		s.serverErr(w, "list elicitations", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"elicitations": elicitationsOut(rows)})
}

// elicitationsOut 把 elicitation 行的 payload 快照摊平成客户端重建表单
// 的形状（与 control_request 的 x-biumind-question 逐字段对应）。
func elicitationsOut(rows []Elicitation) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		m := map[string]any{
			"request_id": e.RequestID.String(),
			"session_id": e.SessionID.String(),
			"status":     e.Status,
			"created_at": e.CreatedAt.UnixMilli(),
			"expires_at": e.ExpiresAt.UnixMilli(),
		}
		var payload map[string]any
		if json.Unmarshal(e.Payload, &payload) == nil {
			for _, k := range []string{"question", "header", "multi_select", "options"} {
				if v, ok := payload[k]; ok {
					m[k] = v
				}
			}
		}
		out = append(out, m)
	}
	return out
}

// ─── POST resume ──────────────────────────────────────────────

func (s *Server) handleResumeSession(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	sess, err := s.Store.GetSession(r.Context(), uid, sessionID)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	// 守卫：仅 paused 的 chat 僵尸 session 可 resume。daemon 模式
	// （environment_id 非空）走 work redeliver 重跑,没有 resume 语义；
	// task 模式（云沙箱）AskUser 未接通,不覆盖。
	if sess.Mode != "chat" || sess.EnvironmentID != nil {
		writeErr(w, http.StatusConflict, "not_resumable_mode",
			fmt.Sprintf("mode %s with environment is resumed via work redelivery, not this endpoint", sess.Mode))
		return
	}
	if sess.State != "paused" {
		writeErr(w, http.StatusConflict, "session_not_paused",
			fmt.Sprintf("session is %s; only paused sessions can be resumed", sess.State))
		return
	}
	// 重跑要重建历史 + 落 synthetic user 消息,thread / chat 持久化缺一
	// 不可。理论上 paused chat session 都有 thread（没 thread 的 session
	// 不产生 elicitation）,但 dev 无 ChatStore 时诚实拒绝。
	if sess.ThreadID == nil || *sess.ThreadID == uuid.Nil || s.ChatStore == nil {
		writeErr(w, http.StatusConflict, "not_resumable",
			"session has no thread or chat persistence is disabled; cannot rebuild history")
		return
	}
	if s.ChatRunner == nil {
		writeErr(w, http.StatusServiceUnavailable, "chat_runner_unavailable",
			"chat runner not wired (chat mode unavailable)")
		return
	}

	ctx := r.Context()
	// 还有 pending 未答 → 409 + 待答快照,客户端先作答再来。
	pending, err := s.Store.ListPendingElicitations(ctx, sessionID)
	if err != nil {
		s.serverErr(w, "list pending elicitations", err)
		return
	}
	if len(pending) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":    "elicitation_pending",
				"message": fmt.Sprintf("%d unanswered elicitation(s); answer them first", len(pending)),
			},
			"elicitations": elicitationsOut(pending),
		})
		return
	}

	// 并发守卫：先 CAS 翻牌认领这次 resume（两个并发请求只有第一个成功）,
	// 后续步骤失败再翻回 paused 让客户端可重试。
	ok, err := s.Store.ResumeSessionCAS(ctx, sessionID)
	if err != nil {
		s.serverErr(w, "resume session", err)
		return
	}
	if !ok {
		writeErr(w, http.StatusConflict, "session_not_paused",
			"session was concurrently resumed or changed state")
		return
	}
	rollback := func() {
		if uerr := s.Store.UpdateSessionState(context.Background(), sessionID, "paused"); uerr != nil {
			s.Logger.Warn("agentplane resume: rollback to paused failed",
				"session_id", sessionID, "err", uerr)
		}
	}

	// 组 synthetic user 消息：已答未消费的答案多题合并。
	answers, err := s.Store.ListUnconsumedAnswers(ctx, sessionID)
	if err != nil {
		rollback()
		s.serverErr(w, "list answers", err)
		return
	}
	prompt := buildResumePrompt(answers)

	// 落 user 轮 + 注册 transcript + 组装历史（与 persistUserAndAssemble
	// 同构,但复用既有 session/thread,不 EnsureThread —— thread 已存在）。
	um, err := s.ChatStore.CreateMessage(ctx, chatpkg.CreateMessageInput{
		ThreadID: *sess.ThreadID, UserID: uid, Role: chatpkg.RoleUser,
		Content: prompt, Status: chatpkg.StatusSuccess,
	})
	if err != nil {
		rollback()
		s.serverErr(w, "persist resume turn", err)
		return
	}
	if s.Transcript != nil {
		s.Transcript.Begin(sessionID, *sess.ThreadID, uid, sess.Model, nil)
	}
	prior, err := s.ChatStore.AssembleHistory(ctx, *sess.ThreadID, uid, um.Position)
	if err != nil {
		rollback()
		s.serverErr(w, "assemble history", err)
		return
	}
	if err := s.Store.MarkAnswersConsumed(ctx, sessionID); err != nil {
		// 只影响「下次 resume 会不会重复注入」— 不阻断本次（拍板接受
		// 极端情况下同一答案注入两遍,模型看到重复用户消息无害）。
		s.Logger.Warn("agentplane resume: mark consumed failed",
			"session_id", sessionID, "err", err)
	}

	// 推 SessionResumed lifecycle 帧（三端已定义,本次启用生产方）。
	// 客户端可能还没重连 WS —— 帧进 NATS session stream（MaxAge 1h）,
	// 重连后 since_seq replay 拿得到。
	if q := s.queue(); q != nil {
		raw, merr := json.Marshal(&sdkproto.SessionResumed{
			Type:      sdkproto.TypeSessionResumed,
			SessionID: sessionID.String(),
		})
		if merr == nil {
			if perr := q.PublishSessionFrame(ctx, sessionID, raw); perr != nil {
				s.Logger.Warn("agentplane resume: publish session_resumed failed",
					"session_id", sessionID, "err", perr)
			}
		}
	}

	// 走现有 chat turn 链路续跑（RunSession 内部组 FrameEmitter /
	// resolveCreds / RunSingleTurn / finalize,与 createChatSession 同款）。
	history := make([]ChatTurn, 0, len(prior))
	for _, p := range prior {
		history = append(history, ChatTurn{Role: p.Role, Content: p.Content})
	}
	sess.State = "active" // CAS 已翻牌,内存副本同步给 runner 日志用
	s.ChatRunner.RunSession(detachedCtx(ctx), sess, WorkPayload{
		SessionID:    sessionID,
		UserID:       uid,
		Mode:         "chat",
		Prompt:       prompt,
		Model:        sess.Model,
		SystemPrompt: sess.SystemPrompt,
		ThreadID:     sess.ThreadID.String(),
		History:      history,
		UserBearer:   bearerFromAuthHeader(r.Header.Get("Authorization")),
	})

	s.Logger.Info("agentplane: session resumed",
		"session_id", sessionID, "answers_injected", len(answers))
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       sessionID.String(),
		"state":            "active",
		"resumed":          true,
		"answers_injected": len(answers),
	})
}

// buildResumePrompt 把已答 elicitation 合并成一条 synthetic user 消息
// （拍板 §6.3-4：「我已回答你的提问：Q:… A:…,请继续。」多题合并）。
// 无答案（paused 但并非卡在提问,如 brain 重启时 turn 正在跑）→ 通用
// 续跑提示。
func buildResumePrompt(answers []Elicitation) string {
	if len(answers) == 0 {
		return "（会话曾被中断，现已恢复。请继续未完成的回答。）"
	}
	var b strings.Builder
	b.WriteString("我已回答你的提问：\n")
	for _, e := range answers {
		var payload elicQuestionMeta
		_ = json.Unmarshal(e.Payload, &payload)
		var ans struct {
			Action  string         `json:"action"`
			Content map[string]any `json:"content"`
		}
		_ = json.Unmarshal(e.Answer, &ans)
		b.WriteString("Q: " + payload.Question + "\nA: ")
		switch ans.Action {
		case "accept":
			b.WriteString(formAnswerText(ans.Content))
			if notes, _ := ans.Content["notes"].(string); notes != "" {
				b.WriteString("\nNotes: " + notes)
			}
		case "decline":
			b.WriteString("（我拒绝回答这个问题）")
		case "timeout":
			b.WriteString("（此前超时未答）")
		default: // cancel 及未知
			b.WriteString("（我取消了回答）")
		}
		b.WriteString("\n")
	}
	b.WriteString("请继续。")
	return b.String()
}

// ─── GET result（补 S3-5B 死端点）─────────────────────────────

// handleSessionResult 返回 session 状态 + 最终态摘要。ingress 的 desync /
// session_finalized 提示（四处）引用这个端点 —— 此前无 handler 是死链。
// chat / agent 模式不写 agent_session_results → result 为 null,只有
// state 可读（客户端据 state 停 spinner）。
func (s *Server) handleSessionResult(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	sess, err := s.Store.GetSession(r.Context(), uid, sessionID)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	out := map[string]any{
		"session_id": sess.SessionID.String(),
		"mode":       sess.Mode,
		"state":      sess.State,
		"created_at": sess.CreatedAt.UnixMilli(),
		"updated_at": sess.UpdatedAt.UnixMilli(),
		"result":     nil,
	}
	res, err := s.Store.GetSessionResult(r.Context(), sessionID)
	if err == nil {
		out["result"] = map[string]any{
			"status":            res.Status,
			"final_text":        res.FinalText,
			"cost_usd":          res.CostUSD,
			"prompt_tokens":     res.PromptTokens,
			"completion_tokens": res.CompletionTokens,
			"duration_ms":       res.DurationMs,
			"error_message":     res.ErrorMessage,
		}
		if len(res.FinalParts) > 0 {
			out["result"].(map[string]any)["final_parts"] = json.RawMessage(res.FinalParts)
		}
	} else if err != ErrNotFound {
		s.serverErr(w, "get session result", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
