package chat

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// 服务端历史组装默认值 —— 与 Server 的 ContextBudgetTokens/MinHistory 默认
// 对齐(send.go:94-95)。Agent Plane WS/agent 路径复用,使 brain 成为对话
// 历史的真相源(不再依赖客户端自带 history,见 Runtime v3 §8.2 翻案)。
const (
	DefaultContextBudgetTokens = 100_000
	DefaultMinHistory          = 10
)

// EnsureThread 幂等确保 chat.threads 里存在指定 id 的 thread —— Agent Plane
// 的 WS/agent thread 此前只在客户端本地 Drift,从未持久化到 brain(实测
// agent_sessions.thread_id 在 chat.threads 全 miss)。chat.messages 有
// thread_id→chat.threads 外键,落 user/assistant 轮前必须先 ensure,否则
// FK 失败、历史落不了库。
//
// ON CONFLICT (id) DO NOTHING:已存在(任意 owner / 含 v1 chat 老 thread)即跳过;
// id 是客户端生成 uuid,跨用户碰撞概率可忽略。title/model 仅在首次插入生效
// (后续 DO NOTHING 不覆盖)。title 取截断的首条 prompt 作友好默认。
func (s *Store) EnsureThread(ctx context.Context, threadID, userID uuid.UUID, title, model string) error {
	if threadID == uuid.Nil || userID == uuid.Nil {
		return fmt.Errorf("%w: thread_id and user_id required", ErrInvalid)
	}
	if len(title) > 80 {
		title = title[:80]
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat.threads (id, user_id, title, model)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (id) DO NOTHING
	`, threadID, userID, title, model)
	if err != nil {
		return fmt.Errorf("ensure thread: %w", err)
	}
	return nil
}

// AssembleHistory 取某 thread 中 position < beforePosition 的历史(即"当前
// user 轮之前"),丢弃被覆盖的重生成兄弟轮(pickLatestSiblings),按 token
// 预算线性裁剪(buildHubMessages),返回文本级 []PriorTurn(user/assistant)。
//
// 这是 Agent Plane(WS chat / agent 两条路径)统一调用的服务端组装入口 ——
// 复用 chat send v1 已有的 ListMessages + pickLatestSiblings + buildHubMessages
// 机制,把多轮上下文的真相源收敛到 brain(chat.messages),不再由客户端经
// WorkPayload.History 带入(避免 N 端各切一套 + 跨设备断裂)。
//
// beforePosition 传当前刚落库的 user 轮 position —— 排除它本身,避免与
// chat_runner 重新 append 的当前 Prompt 重复。工具轮(role=tool)P1 不进
// 模型历史(buildHubMessages 非 group-aware),故这里只映射 user/assistant。
func (s *Store) AssembleHistory(
	ctx context.Context,
	threadID, userID uuid.UUID,
	beforePosition int64,
) ([]PriorTurn, error) {
	msgs, err := s.ListMessages(ctx, ListMessagesInput{
		ThreadID:       threadID,
		UserID:         userID,
		BeforePosition: &beforePosition,
		Limit:          500,
	})
	if err != nil {
		return nil, err
	}
	// 多次重生成同一 prompt 时只留最新兄弟轮(否则模型看到自己答同一问两次)。
	msgs = pickLatestSiblings(msgs)
	// token 预算裁剪(4 chars≈1 token,保留最近 MinHistory 轮后按预算丢旧轮)。
	hub := buildHubMessages(msgs, DefaultMinHistory, DefaultContextBudgetTokens)
	out := make([]PriorTurn, 0, len(hub))
	for _, m := range hub {
		if m.Role != RoleUser && m.Role != RoleAssistant {
			continue
		}
		out = append(out, PriorTurn{Role: m.Role, Content: m.Content})
	}
	return out, nil
}
