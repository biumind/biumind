// ElicitationCenter —— chat 模式 agent 提问表单（user.ask）的进程内
// pending map：request_id → answer chan。
//
// 链路（agent-ask-form P1-b）：
//
//	biumindkit 引擎 AskUserQuestion
//	  → ChatRunner.askUserFn 组 control_request{elicitation, mode:form}
//	    帧 + Register(requestID)，publish 到 biu.session.<sid>.out
//	  → 客户端 FormCard 作答，回 control_response{action, content}
//	  → ingress.maybeRoutePermissionResponse 按 request_id 命中本 map
//	    → Resolve 唤醒等候的 askUser goroutine
//
// 红线（设计 §3.4）：**绝不从持久化状态重建**。map 只在内存；brain 重启
// = 未答表单按超时 soft error 收场，诚实降级。多副本部署下回包必须落回
// 发提问的那个进程（chat session 本身就绑定在创建它的副本上，天然成立）。

package agentplane

import (
	"log/slog"
	"sync"
	"time"
)

// ElicitationTimeout 是 pending 提问等客户端回包的最长时间。比 daemon
// permission 的 30s 长 —— 表单要人读题 + 作答（设计 §2.3.3）。超时 =
// 用户未答 → 工具 soft error，模型自行降级。var（非 const）以便测试
// 缩小；生产代码不改它。
var ElicitationTimeout = 5 * time.Minute

// ElicitationAnswer 是客户端 ElicitationResponse 回包的进程内形态。
// Content 是自由 JSON（schema 校验在 ChatRunner.answerFromElicitation
// 做，那里拿得到提问的 option 集合）。
type ElicitationAnswer struct {
	Action  string         // accept | decline | cancel
	Content map[string]any // accept 时的表单值
}

// ElicitationCenter 是 request_id → answer chan 的注册表。chan buffered(1)，
// Resolve 永不阻塞。并发 chat session 之间靠 request_id（uuid）天然隔离。
type ElicitationCenter struct {
	mu      sync.Mutex
	pending map[string]chan ElicitationAnswer
	logger  *slog.Logger
}

func NewElicitationCenter(logger *slog.Logger) *ElicitationCenter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ElicitationCenter{pending: map[string]chan ElicitationAnswer{}, logger: logger}
}

// Register 登记一个 pending 提问，返回应答 chan（buffered 1）。调用方
// 负责在退出时 Cancel（无论结果）释放表项。
func (c *ElicitationCenter) Register(requestID string) <-chan ElicitationAnswer {
	ch := make(chan ElicitationAnswer, 1)
	c.mu.Lock()
	// 同 requestID 重复注册（理论不可能，uuid）→ 旧的直接顶掉，旧 chan
	// 没人写，等它的 goroutine 走超时退出。
	c.pending[requestID] = ch
	c.mu.Unlock()
	return ch
}

// Cancel 摘掉 pending 表项。Register 的调用方 defer 它。
func (c *ElicitationCenter) Cancel(requestID string) {
	c.mu.Lock()
	delete(c.pending, requestID)
	c.mu.Unlock()
}

// Has 报告 requestID 是否是本进程发出的 pending 提问。ingress 用它把
// elicitation 回包从 permission 回包里分流出来（两路回包共用
// control_response 帧型，只能靠 request_id 注册表分辨）。
func (c *ElicitationCenter) Has(requestID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pending[requestID]
	return ok
}

// Resolve 投递回包唤醒等候方。找不到 requestID（已超时 / 不属于本进程 /
// 重复回包）→ 静默丢弃，返回 false。
func (c *ElicitationCenter) Resolve(requestID string, ans ElicitationAnswer) bool {
	c.mu.Lock()
	ch, ok := c.pending[requestID]
	if ok {
		delete(c.pending, requestID)
	}
	c.mu.Unlock()
	if !ok {
		return false
	}
	ch <- ans // buffered(1)，不阻塞
	return true
}
