// Agent Plane work queue（S3-3）—— brain 入口。
//
// 架构：用 NATS JetStream 替代 SQL queue + LISTEN/NOTIFY。每个 environment
// 一个 subject `biu.work.<env_id>`；worker 用 durable consumer pull。死
// worker 由 JetStream 的 AckWait + redelivery 自动接管，不用 brain 实现。
//
// 当前 stage 只做 brain 这边：
//   - EnsureWorkStream(ctx)：boot 时建一次，幂等
//   - EnqueueWork(ctx, envID, workID, payload)：路由到 environment 的 subject
//
// Worker 端的 Pull/Fetch loop 在 S3-8 biu CLI 注册模式里实化。
//
// 流配置：
//   - Subjects: ["biu.work.>"]
//   - Retention: WorkQueue（首次 ack 后删 —— 严格工作队列，不允许 fanout）
//   - MaxAge: 24h（一天没人取走的任务认定无效）
//   - MaxMsgs: 10000（防爆磁盘 —— 默认 file storage）
//
// 幂等：每条 publish 走 `Nats-Msg-Id` 头。重复 enqueue（比如客户端 retry）
// JetStream 在 dedupeWindow 内自动去重，不会双发给 worker。

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// WorkStreamName 是 JetStream stream 名。常量集中放避免 typo —— EnsureStream
// 跟 Subscribe 都要引用。
const WorkStreamName = "BIU_AGENT_WORK"

// WorkSubjectPrefix 是 work subject 命名前缀。
//
//	biu.work.<env_id>          → 路由给特定 environment 的任务
//	biu.work.pool.<pool_tag>   → S3-6 用：runtime 池子负载均衡（暂未启用）
const WorkSubjectPrefix = "biu.work."

// ControlStreamName / ControlSubjectPrefix —— 反向 control plane（cancel /
// reload / 等带外指令）。跟 work 流分开因为：
//
//   - 时效性更短（cancel 30s 没拉走基本就没意义了）
//   - 优先级高于 work（worker 应该有独立 goroutine 长轮询 control，不被
//     work 占满）
//   - 删 retention 用 WorkQueue（一条 control 只投给一个 worker，不 fanout）
//
// 当前 control payload schema (JSON)：
//
//	{ "type": "cancel_session", "session_id": "<uuid>" }
//
// 后续可加 reload / set_model 等。worker 不识别的 type 应该 ack 后忽略 —— 旧
// worker 在不重启的情况下也能 silently 跳过新指令。
const (
	ControlStreamName    = "BIU_AGENT_CONTROL"
	ControlSubjectPrefix = "biu.control."
)

// ControlSubject 拼出针对一个 environment 的 control subject。
func ControlSubject(envID string) string {
	return ControlSubjectPrefix + envID
}

// SessionStreamName / SessionSubjectPrefix —— S3-5 ingress 用。一个共享
// stream 接所有 session 帧（不是 per-session）：per-session-stream 在
// 1000+ 并发会话下伤性能（broker metadata + heap allocation）。
//
// MaxMsgs / MaxAge 限制是 stream 级；OrderedConsumer + FilterSubject
// 在订阅侧做隔离 —— 跟 per-stream 一样的客户端体验。
const (
	SessionStreamName    = "BIU_SESSIONS"
	SessionSubjectPrefix = "biu.session."
)

// dedupeWindow —— JetStream 幂等去重窗口。客户端 retry 同一 work_id 在窗口
// 内不会双发。10min 足够覆盖网络抖动 + brain 重启间隙。
const dedupeWindow = 10 * time.Minute

// FrameObserver 在每帧成功 publish 到 .out 后被旁路调用 —— TranscriptRecorder
// 用它把 assistant 轮累积落库。实现必须自己兜异常/不阻塞(在 publish 热路径上)。
type FrameObserver interface {
	ObserveFrame(ctx context.Context, sessionID uuid.UUID, payload []byte)
}

// Queue 是 brain 这边对 work queue 的入口。零依赖 store —— 它只对 NATS。
type Queue struct {
	// jsMu 保护 js —— readiness reconciler 在 broker 连上 + 流 ensured 后
	// 经 SetJS 灌入句柄(可能是进程启动好几秒之后);灌入前所有方法返回
	// "nil JetStream handle" 错误。
	jsMu     sync.RWMutex
	js       bus.JetStream
	observer FrameObserver // 可空;SetObserver 注入 TranscriptRecorder

	// durable consumer 缓存 (per env_id). FetchWork/FetchControl 首次 create-or-update
	// 后入缓存, 后续命中直接 Fetch, 不再每次发 $JS.API.CONSUMER.CREATE RPC —— 该
	// RPC 在 StorageFile fsync + 并发时偶发耗时 4-5.5s (NATS WRN "took too long"),
	// 绑 HTTP request ctx 会 context deadline exceeded (见 ensureWorkConsumer 注释).
	workCons sync.Map // uuid.UUID -> jetstream.Consumer
	ctrlCons sync.Map // uuid.UUID -> jetstream.Consumer
}

// NewQueue 构造一个 Queue。js 通常来自 `bus.NewBus(...).JetStream()`；
// 也可传 nil 之后由 readiness reconciler 经 SetJS 后补。EnsureWorkStream
// 至少调一次（生产由 reconciler 负责，幂等）。
func NewQueue(js bus.JetStream) *Queue {
	return &Queue{js: js}
}

// SetJS 后补 / 替换 JetStream 句柄。readiness reconciler 在 streams_ready
// 时调；nats.go 的 JetStream 句柄能跨断线重连存活,所以断线期不需要清。
func (q *Queue) SetJS(js bus.JetStream) {
	q.jsMu.Lock()
	q.js = js
	q.jsMu.Unlock()
}

// getJS 返回当前 JetStream 句柄（未 SetJS / NewQueue(nil) 时为 nil）。
func (q *Queue) getJS() bus.JetStream {
	q.jsMu.RLock()
	defer q.jsMu.RUnlock()
	return q.js
}

// SetObserver 注入帧观察者(TranscriptRecorder)。可空 → 退化为今天行为。
func (q *Queue) SetObserver(o FrameObserver) { q.observer = o }

// EnsureSessionStream 创建 session 帧流（S3-5 ingress 用）。所有 session
// 共用同一 stream，subject 隔离。
//
// 跟 work stream 不同：
//   - Retention=Limits（不是 WorkQueue）—— 多个 ingress consumer 都能读，
//     fanout / 重复读合法（多 client 看同 session）
//   - MaxAge=1h —— 短期回放窗口；超过 fallback SessionDesynced
func (q *Queue) EnsureSessionStream(ctx context.Context) error {
	js := q.getJS()
	if js == nil {
		return errors.New("agentplane: nil JetStream handle")
	}
	return js.EnsureStream(ctx, bus.StreamSpec{
		Name:      SessionStreamName,
		Subjects:  []string{SessionSubjectPrefix + ">"},
		Retention: bus.RetentionLimits,
		Storage:   bus.StorageFile,
		MaxAge:    1 * time.Hour,
	})
}

// SessionSubjectOut 拼出"server→client"方向的 subject。
//
//	biu.session.<sid>.out   服务端推（assistant frames / system events）
//	biu.session.<sid>.in    客户端推（user messages / control requests）
func SessionSubjectOut(sessionID string) string {
	return SessionSubjectPrefix + sessionID + ".out"
}

// SessionSubjectIn 同上反向。
func SessionSubjectIn(sessionID string) string {
	return SessionSubjectPrefix + sessionID + ".in"
}

// FetchedWork 是 FetchWork 返回的一条任务。Body 是 publisher 投递时的
// JSON payload；Ack/Nak 让调用方在处理后告诉 broker 是否成功，决定
// 是否 redeliver。
type FetchedWork struct {
	Body []byte
	ack  func() error
	nak  func() error
}

// Ack 标记 work 处理成功 —— broker 删除消息（WorkQueue retention）。
func (w *FetchedWork) Ack() error {
	if w.ack == nil {
		return nil
	}
	return w.ack()
}

// Nak 标记处理失败 —— broker 在 AckWait 后 redeliver 给同 durable 的
// 下个 fetch。
func (w *FetchedWork) Nak() error {
	if w.nak == nil {
		return nil
	}
	return w.nak()
}

// FetchWork 是 worker poller 的拉取入口。给定 environment_id，pull-fetch
// 一条 work（或 wait 内没消息时返回 nil, nil 表示空轮询）。
//
// 使用 raw JetStream durable consumer per env_id（名字 `worker-<env_id>`）。
// 多个 worker 实例（同 env_id）共享 durable —— work-queue 语义，每条消息
// 投递给恰好一个 fetch 调用。
func (q *Queue) FetchWork(ctx context.Context, envID uuid.UUID, wait time.Duration) (*FetchedWork, error) {
	if q.getJS() == nil {
		return nil, errors.New("agentplane: nil JetStream handle")
	}
	if envID == uuid.Nil {
		return nil, errors.New("agentplane: env_id required")
	}
	cons, err := q.ensureWorkConsumer(envID)
	if err != nil {
		return nil, fmt.Errorf("agentplane: ensure worker consumer: %w", err)
	}

	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(wait))
	if err != nil {
		return nil, fmt.Errorf("agentplane: fetch work: %w", err)
	}
	for msg := range batch.Messages() {
		body := msg.Data()
		return &FetchedWork{Body: body, ack: msg.Ack, nak: msg.Nak}, nil
	}
	if err := batch.Error(); err != nil {
		return nil, fmt.Errorf("agentplane: fetch error: %w", err)
	}
	return nil, nil // 超时无消息
}

// ensureWorkConsumer 返回 env 对应的 durable work consumer: 缓存命中直接返,
// miss 才 create-or-update 并入缓存.
//
// create-or-update 用独立 background ctx (15s timeout), 不绑调用方 fetch 的
// request ctx —— 历史教训 (2026-07): FetchWork 之前每次轮询都
// rawJS.CreateOrUpdateConsumer(r.Context()), 而 r.Context() 是 HTTP request ctx,
// 其生命周期短于偶发的 CONSUMER.CREATE 耗时 (StorageFile fsync + worker/control
// 并发 create, NATS 实测 took 4-5.5s); client long-poll 提前断开 → ctx done
// → "ensure worker consumer: context deadline exceeded" → brain 返 500.
// 改为: durable consumer 创建一次入缓存 (后续命中零 RPC) + create 用 background
// ctx (client 断开不中断 create). rawJS nil 检查保留 (兼容 js 未注入).
func (q *Queue) ensureWorkConsumer(envID uuid.UUID) (jetstream.Consumer, error) {
	if v, ok := q.workCons.Load(envID); ok {
		return v.(jetstream.Consumer), nil
	}
	js := q.getJS()
	if js == nil {
		return nil, errors.New("agentplane: nil JetStream handle")
	}
	rawJS := js.RawJetStream()
	if rawJS == nil {
		return nil, errors.New("agentplane: raw JetStream unavailable")
	}
	createCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cons, err := rawJS.CreateOrUpdateConsumer(createCtx, WorkStreamName, jetstream.ConsumerConfig{
		Durable:       fmt.Sprintf("worker-%s", envID),
		Name:          fmt.Sprintf("worker-%s", envID),
		FilterSubject: WorkSubjectPrefix + envID.String(),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second, // 死 worker → 60s 后 redeliver
		MaxDeliver:    5,                // 失败 5 次后丢入 max-deliver pile
	})
	if err != nil {
		return nil, err
	}
	// 并发 fetch (同 env) 可能同时 miss → 各 create 一次; LoadOrStore 保证只用
	// 第一个入缓存的 cons (后续 create 的 cons 等价, 丢弃无副作用).
	if actual, loaded := q.workCons.LoadOrStore(envID, cons); loaded {
		return actual.(jetstream.Consumer), nil
	}
	return cons, nil
}

// ensureControlConsumer 同 ensureWorkConsumer, 服务 control 流 (AckWait 5s).
func (q *Queue) ensureControlConsumer(envID uuid.UUID) (jetstream.Consumer, error) {
	if v, ok := q.ctrlCons.Load(envID); ok {
		return v.(jetstream.Consumer), nil
	}
	js := q.getJS()
	if js == nil {
		return nil, errors.New("agentplane: nil JetStream handle")
	}
	rawJS := js.RawJetStream()
	if rawJS == nil {
		return nil, errors.New("agentplane: raw JetStream unavailable")
	}
	createCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cons, err := rawJS.CreateOrUpdateConsumer(createCtx, ControlStreamName, jetstream.ConsumerConfig{
		Durable:       fmt.Sprintf("control-%s", envID),
		Name:          fmt.Sprintf("control-%s", envID),
		FilterSubject: ControlSubject(envID.String()),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       5 * time.Second,
		MaxDeliver:    3,
	})
	if err != nil {
		return nil, err
	}
	if actual, loaded := q.ctrlCons.LoadOrStore(envID, cons); loaded {
		return actual.(jetstream.Consumer), nil
	}
	return cons, nil
}

// PublishSessionFrame 把一帧 SDK Protocol JSON 发到 biu.session.<sid>.out。
// worker 处理 work 时调用 —— ingress 那边订 .out 转推给 client。
func (q *Queue) PublishSessionFrame(ctx context.Context, sessionID uuid.UUID, payload []byte) error {
	js := q.getJS()
	if js == nil {
		return errors.New("agentplane: nil JetStream handle")
	}
	if err := js.Publish(ctx, SessionSubjectOut(sessionID.String()), json.RawMessage(payload)); err != nil {
		return err
	}
	// publish 成功后旁路通知 observer(累积 assistant 文本 / 终止落库)。
	// chat + agent 两模式帧都过这一点,故一处即统一覆盖。
	if q.observer != nil {
		q.observer.ObserveFrame(ctx, sessionID, payload)
	}
	return nil
}

// EnsureWorkStream 创建/更新流。**必须**在第一次 EnqueueWork 之前调用，
// 否则 publish 会因 stream 不存在失败。boot 时调一次即可，幂等。
func (q *Queue) EnsureWorkStream(ctx context.Context) error {
	js := q.getJS()
	if js == nil {
		return errors.New("agentplane: nil JetStream handle")
	}
	return js.EnsureStream(ctx, bus.StreamSpec{
		Name:      WorkStreamName,
		Subjects:  []string{WorkSubjectPrefix + ">"},
		Retention: bus.RetentionWorkQueue,
		Storage:   bus.StorageFile,
		MaxAge:    24 * time.Hour,
		// MaxMsgs / Replicas 用流默认值（bus 包默认 Replicas=1）。生产
		// 部署 cluster 后调 Replicas=3。
	})
}

// EnsureControlStream 创建/更新 control 流。MaxAge 短：30s 没拉走的
// cancel 已经没意义了（用户的耐心比这短）。
func (q *Queue) EnsureControlStream(ctx context.Context) error {
	js := q.getJS()
	if js == nil {
		return errors.New("agentplane: nil JetStream handle")
	}
	return js.EnsureStream(ctx, bus.StreamSpec{
		Name:      ControlStreamName,
		Subjects:  []string{ControlSubjectPrefix + ">"},
		Retention: bus.RetentionWorkQueue,
		Storage:   bus.StorageFile,
		MaxAge:    30 * time.Second,
	})
}

// EnqueueControl 把一条 control payload 投递给指定 environment。
// payload 是 JSON-serializable map / struct (例如
// {"type":"cancel_session","session_id":"..."}).
//
// 不走 dedupe header —— cancel 的语义是「再投一次也没害」，调用方不
// 需要 retry 协调。
func (q *Queue) EnqueueControl(ctx context.Context, envID uuid.UUID, payload any) error {
	js := q.getJS()
	if js == nil {
		return errors.New("agentplane: nil JetStream handle")
	}
	if envID == uuid.Nil {
		return errors.New("agentplane: env_id required")
	}
	return js.Publish(ctx, ControlSubject(envID.String()), payload)
}

// FetchControl 是 worker control-poller 的拉取入口。跟 FetchWork 同样
// 形状,但用独立的 durable consumer (`control-<env_id>`),AckWait 5s
// (control 比 work 时效性更强,不该被慢 ack 占住)。
func (q *Queue) FetchControl(ctx context.Context, envID uuid.UUID, wait time.Duration) (*FetchedWork, error) {
	if q.getJS() == nil {
		return nil, errors.New("agentplane: nil JetStream handle")
	}
	if envID == uuid.Nil {
		return nil, errors.New("agentplane: env_id required")
	}
	cons, err := q.ensureControlConsumer(envID)
	if err != nil {
		return nil, fmt.Errorf("agentplane: ensure control consumer: %w", err)
	}
	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(wait))
	if err != nil {
		return nil, fmt.Errorf("agentplane: fetch control: %w", err)
	}
	for msg := range batch.Messages() {
		body := msg.Data()
		return &FetchedWork{Body: body, ack: msg.Ack, nak: msg.Nak}, nil
	}
	if err := batch.Error(); err != nil {
		return nil, fmt.Errorf("agentplane: fetch control error: %w", err)
	}
	return nil, nil
}

// EnqueueWork 把一条 work 投递到指定 environment 的 subject。
//
// workID 用作幂等键 —— 调用方负责生成（典型：`uuid.New()`）。同一 workID
// 在 dedupeWindow 内重复 enqueue 只会落一条；这让上层（HTTP retry / restart
// 重新提交）天然 at-most-once-per-id 而不需要外层加锁。
//
// payload 是任意 JSON 可序列化对象（biu.bus.JetStream.Publish 内部 marshal）。
// 典型 payload 是 `{session_id, mode, prompt, model, ...}`，schema 由路由
// 层（S3-6）定。
func (q *Queue) EnqueueWork(
	ctx context.Context,
	envID uuid.UUID,
	workID string,
	payload any,
) error {
	js := q.getJS()
	if js == nil {
		return errors.New("agentplane: nil JetStream handle")
	}
	if envID == uuid.Nil {
		return errors.New("agentplane: env_id required")
	}
	if workID == "" {
		return errors.New("agentplane: work_id required")
	}
	subject := WorkSubjectPrefix + envID.String()
	return js.Publish(ctx, subject, payload, bus.Header{
		Key:   "Nats-Msg-Id",
		Value: workID,
	})
}

// EnqueuePoolWork 把 work 投递到一个 worker 池子（按 pool_tag 路由）。
// Task 模式 brain 选不出具体 environment 时用 —— 任意一个池内 runtime
// 副本 fetch 到都能干。S3-6 路由策略落地后调用。
//
// 当前 S3-3 暂不让 brain 调用方使用 —— 但 subject 命名先约定好，让 S3-8
// worker 创建 consumer 时能 filter 到。
func (q *Queue) EnqueuePoolWork(
	ctx context.Context,
	poolTag string,
	workID string,
	payload any,
) error {
	js := q.getJS()
	if js == nil {
		return errors.New("agentplane: nil JetStream handle")
	}
	if poolTag == "" {
		return errors.New("agentplane: pool_tag required")
	}
	if workID == "" {
		return errors.New("agentplane: work_id required")
	}
	subject := fmt.Sprintf("%spool.%s", WorkSubjectPrefix, poolTag)
	return js.Publish(ctx, subject, payload, bus.Header{
		Key:   "Nats-Msg-Id",
		Value: workID,
	})
}
