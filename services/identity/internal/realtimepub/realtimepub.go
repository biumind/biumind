// Package realtimepub publishes Realtime SSE frames from identity (PERI-4b).
//
// 公告发布时,identity 向 core NATS subject `biumind.<env>.identity.announcement.realtime`
// 发一帧,realtime 服务(订阅 `biumind.<env>.*.*.realtime`)按 topic 扇出给在线 SSE 连接。
// 这是 BiuMind 的通知下发正道:Chat 双向流走 WebSocket SDK Protocol,而 Realtime 多 topic
// 通知走 SSE(见 CLAUDE.md 硬约束)。
//
// 广播语义:公告对全体用户可见,identity 无法枚举在线用户,所以发到单一全局 topic
// `announce:all`,由 realtime 扇出。authz 侧 Cedar 放行该 topic kind(沿用 app:catalog
// 广播先例,见 deploy/.../authz/policies/00-system.cedar)。
//
// payload 只带公告 id(不含正文),客户端收到后重拉 /v1/announcements —— 那里服务端按
// per-user 读态 + 版本门槛算出真正可见集。
package realtimepub

import (
	"context"
	"log/slog"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

// AnnounceTopic 是全局公告广播 topic。须与客户端 announcements_realtime.dart 的
// announcementsTopic 常量一致。
const AnnounceTopic = "announce:all"

// AnnouncementPublisher 实现 api.AnnouncementNotifier。best-effort:发布失败只记
// 日志,绝不阻塞 admin 请求(公告已入库,客户端轮询兜底)。
type AnnouncementPublisher struct {
	Bus    bus.Bus
	Env    string
	Logger *slog.Logger
}

// NotifyAnnouncementPublished 在公告 published=true 时被调用。
func (p *AnnouncementPublisher) NotifyAnnouncementPublished(id string) {
	if p == nil || p.Bus == nil {
		return
	}
	subject := "biumind." + p.Env + ".identity.announcement.realtime"
	wire := map[string]any{
		"topic": AnnounceTopic,
		"kind":  "announcement.published",
		"payload": map[string]any{
			"id": id,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Bus.Publish(ctx, subject, wire); err != nil && p.Logger != nil {
		p.Logger.Warn("announcement realtime publish failed",
			"id", id, "subject", subject, "err", err.Error())
	}
}
