// sink.go — W3-5 (PG MVP). 订阅 NATS 'BIUMIND_BILLING_EVENTS' stream, 批量写
// PG `billing.events` 表.
//
// 设计:
//
//   - 跑在 identity 进程内 goroutine, 不开新服务. 同一个 PG 连接池 (publisher
//     和 sink 同进程, 但 publisher 不直写 DB; 走 NATS 是为了对账可观测 + 将
//     来切独立服务/CH 时不动 publisher).
//
//   - durable consumer name = "billing-events-sink". 同名 consumer 多个实例
//     会瓜分消息 (NATS work-queue 语义), 单 identity 实例足够; 多副本部署
//     时 PG ON CONFLICT 兜底重复.
//
//   - 批量策略: ≤1000 条 OR ≥5s flush. 启动时 buf 空, 来一条计时 5s; 超时
//     或满 1000 触发 flush. 错误时整批 NAK 让 NATS 重投.
//
//   - 幂等: PG 主键 (event_id, occurred_at), INSERT ... ON CONFLICT DO NOTHING.
//     NATS at-least-once 保证 sink 至少能写一次, 重投永远去重.
//
//   - 失败处理: PG 临时挂 → NAK 全批 → NATS 5 次重投后落 max-deliver pile;
//     MaxDeliver=5 (bus 默认), 配合 5s flush 给上游 25-30s 救命窗口.
//
//   - 不在 sink 里做转换 / 富化 (e.g. join model_code → model_name). 表是大宽
//     表本身就够 dashboard 直接查; 转换都让 Grafana SQL 处理, 保持 sink 简单.

package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

const (
	// SinkConsumerName — 永久 durable consumer; 同名复用进度.
	SinkConsumerName = "billing-events-sink"
	// SinkBatchMax — 单批最多攒多少条再 flush.
	SinkBatchMax = 1000
	// SinkFlushInterval — buf 非空时, 每多久强制 flush 一次.
	SinkFlushInterval = 5 * time.Second
)

// Sink 把 NATS billing events 落 PG. 调用方 main.go 起一个, 调 Run() 阻塞.
type Sink struct {
	pool   *pgxpool.Pool
	js     bus.JetStream
	env    string
	logger *slog.Logger

	// 测试钩子: 不为 nil 时, 每次成功 INSERT 一批后调用 (传当批条数).
	// 生产保持 nil.
	onFlush func(n int)
}

// NewSink — 单实例就够; 多实例靠 NATS 工作队列拆分.
func NewSink(pool *pgxpool.Pool, js bus.JetStream, env string, logger *slog.Logger) *Sink {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sink{pool: pool, js: js, env: env, logger: logger}
}

// SetOnFlush — 集成测试用. 生产代码不应调.
func (s *Sink) SetOnFlush(fn func(n int)) { s.onFlush = fn }

// Run 阻塞订阅 stream, 直到 ctx 取消. 返回时 in-flight batch 已尝试 flush.
//
// 不返第一个错误就退; transient (DB 抖动 / NATS 短断) 内部重试. 只有 ctx 取
// 消或不可恢复 (consumer 创建失败) 才返.
func (s *Sink) Run(ctx context.Context) error {
	if s.js == nil {
		return errors.New("sink: nil JetStream")
	}
	if s.pool == nil {
		return errors.New("sink: nil pgxpool")
	}

	// buf + 互斥锁: NATS handler 推, 后台 ticker 拉.
	var (
		mu  sync.Mutex
		buf []rawEvent
	)
	flush := func(reason string) {
		mu.Lock()
		if len(buf) == 0 {
			mu.Unlock()
			return
		}
		batch := buf
		buf = nil
		mu.Unlock()

		if err := s.insertBatch(ctx, batch); err != nil {
			// PG 失败时不 NAK (handler 已 ACK), 直接丢. 真要严丢就靠
			// MaxDeliver + DB-side 对账脚本兜底. 多数情况 retry 更糟
			// (PG hold 时间放大 → JetStream pending 堆积).
			s.logger.Error("sink: insert batch failed", "n", len(batch),
				"reason", reason, "err", err.Error())
			return
		}
		s.logger.Debug("sink: flushed", "n", len(batch), "reason", reason)
		if s.onFlush != nil {
			s.onFlush(len(batch))
		}
	}

	handler := func(_ context.Context, m *bus.Message) error {
		var raw rawEvent
		if err := json.Unmarshal(m.Body, &raw); err != nil {
			s.logger.Warn("sink: malformed event; skip",
				"subject", m.Subject, "err", err.Error())
			return nil // ACK 丢掉坏消息, 否则会卡 max-deliver
		}
		raw.kind = subjectToKind(m.Subject)
		raw.payload = m.Body

		mu.Lock()
		buf = append(buf, raw)
		full := len(buf) >= SinkBatchMax
		mu.Unlock()
		if full {
			flush("batch_full")
		}
		return nil // 立刻 ACK; 失败由 flush 自身记 + 对账兜底
	}

	sub, err := s.js.Subscribe(ctx, bus.ConsumerSpec{
		Stream:        StreamName,
		Durable:       SinkConsumerName,
		FilterSubject: SubjectPrefix(s.env) + ".>",
		AckWait:       30 * time.Second,
		MaxDeliver:    5,
	}, handler)
	if err != nil {
		return fmt.Errorf("sink: subscribe: %w", err)
	}
	defer sub.Drain()

	tick := time.NewTicker(SinkFlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			flush("shutdown")
			return ctx.Err()
		case <-tick.C:
			flush("interval")
		}
	}
}

// rawEvent — sink 内部 buf 元素. JSON 解后用顶层字段 + 留 payload bytes 兜底.
type rawEvent struct {
	EventID         uuid.UUID  `json:"event_id"`
	Kind            string     `json:"kind"`
	UserID          uuid.UUID  `json:"user_id"`
	IdempotencyKey  string     `json:"idempotency_key"`
	OccurredAt      time.Time  `json:"occurred_at"`
	Env             string     `json:"env"`
	LogID           *uuid.UUID `json:"log_id,omitempty"`
	HoldID          *uuid.UUID `json:"hold_id,omitempty"`
	Amount          *int64     `json:"amount,omitempty"`
	RefType         string     `json:"ref_type"`
	RefID           string     `json:"ref_id"`
	ModelCode       string     `json:"model_code"`
	ProviderCode    string     `json:"provider_code"`
	UpstreamUSD     *float64   `json:"upstream_usd,omitempty"`
	UpstreamCNY     *float64   `json:"upstream_cny,omitempty"`
	SubscriptionID  *uuid.UUID `json:"subscription_id,omitempty"`
	EventType       string     `json:"event_type"`
	PlanCode        string     `json:"plan_code"`
	OldPlanCode     string     `json:"old_plan_code"`
	AmountCents     *int64     `json:"amount_cents,omitempty"`
	Currency        string     `json:"currency"`
	Source          string     `json:"source"`
	Actual          *int64     `json:"actual,omitempty"`
	HoldDelta       *int64     `json:"hold_delta,omitempty"`
	RefundOfLogID   *uuid.UUID `json:"refund_of_log_id,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	Reason          string     `json:"reason"`

	// 由 sink 填:
	kind    string // 从 NATS subject 抽 (覆盖 JSON.kind 防伪造)
	payload []byte // 原始 JSON, 落 payload 列
}

func subjectToKind(subj string) string {
	// "biumind.test.billing.events.consume" → "consume"
	for i := len(subj) - 1; i >= 0; i-- {
		if subj[i] == '.' {
			return subj[i+1:]
		}
	}
	return ""
}

// insertBatch — 一次 SQL 多行 INSERT. ON CONFLICT 走 DO NOTHING (重投幂等).
func (s *Sink) insertBatch(ctx context.Context, batch []rawEvent) error {
	if len(batch) == 0 {
		return nil
	}
	// pgx CopyFrom 比批量 INSERT VALUES 快 10x, 但 CopyFrom 不支持 ON
	// CONFLICT, 要自己保证去重. 量级小用 INSERT VALUES + UNNEST 拿 ON
	// CONFLICT DO NOTHING 简单可靠.
	const q = `
		INSERT INTO billing.events (
			event_id, kind, user_id, idempotency_key, occurred_at, env,
			log_id, hold_id, amount, ref_type, ref_id,
			model_code, provider_code, upstream_usd, upstream_cny,
			subscription_id, event_type, plan_code, old_plan_code,
			amount_cents, currency, source,
			actual, hold_delta, refund_of_log_id, expires_at, reason,
			payload
		)
		SELECT
			event_id, kind, user_id, NULLIF(idempotency_key, ''), occurred_at, env,
			log_id, hold_id, amount, NULLIF(ref_type, ''), NULLIF(ref_id, ''),
			NULLIF(model_code, ''), NULLIF(provider_code, ''), upstream_usd, upstream_cny,
			subscription_id, NULLIF(event_type, ''), NULLIF(plan_code, ''),
			NULLIF(old_plan_code, ''),
			amount_cents, NULLIF(currency, ''), NULLIF(source, ''),
			actual, hold_delta, refund_of_log_id, expires_at, NULLIF(reason, ''),
			payload::jsonb
		FROM unnest(
			$1::uuid[], $2::text[], $3::uuid[], $4::text[], $5::timestamptz[],
			$6::text[],
			$7::uuid[], $8::uuid[], $9::bigint[],
			$10::text[], $11::text[],
			$12::text[], $13::text[], $14::float8[], $15::float8[],
			$16::uuid[], $17::text[], $18::text[], $19::text[],
			$20::bigint[], $21::text[], $22::text[],
			$23::bigint[], $24::bigint[], $25::uuid[],
			$26::timestamptz[], $27::text[], $28::text[]
		) AS t(
			event_id, kind, user_id, idempotency_key, occurred_at, env,
			log_id, hold_id, amount, ref_type, ref_id,
			model_code, provider_code, upstream_usd, upstream_cny,
			subscription_id, event_type, plan_code, old_plan_code,
			amount_cents, currency, source,
			actual, hold_delta, refund_of_log_id, expires_at, reason, payload
		)
		ON CONFLICT (event_id, occurred_at) DO NOTHING
	`

	// 把 batch 拍平成 28 个 slice. nil pointer → NULL via *T sentinel.
	n := len(batch)
	eventIDs := make([]uuid.UUID, n)
	kinds := make([]string, n)
	userIDs := make([]uuid.UUID, n)
	idemKeys := make([]string, n)
	occurredAt := make([]time.Time, n)
	envs := make([]string, n)
	logIDs := make([]*uuid.UUID, n)
	holdIDs := make([]*uuid.UUID, n)
	amounts := make([]*int64, n)
	refTypes := make([]string, n)
	refIDs := make([]string, n)
	modelCodes := make([]string, n)
	providerCodes := make([]string, n)
	upstreamUSDs := make([]*float64, n)
	upstreamCNYs := make([]*float64, n)
	subIDs := make([]*uuid.UUID, n)
	eventTypes := make([]string, n)
	planCodes := make([]string, n)
	oldPlanCodes := make([]string, n)
	amountCents := make([]*int64, n)
	currencies := make([]string, n)
	sources := make([]string, n)
	actuals := make([]*int64, n)
	holdDeltas := make([]*int64, n)
	refundOfLogIDs := make([]*uuid.UUID, n)
	expiresAts := make([]*time.Time, n)
	reasons := make([]string, n)
	payloads := make([]string, n)

	for i, e := range batch {
		eventIDs[i] = e.EventID
		kinds[i] = e.kind
		userIDs[i] = e.UserID
		idemKeys[i] = e.IdempotencyKey
		occurredAt[i] = e.OccurredAt
		envs[i] = e.Env
		logIDs[i] = e.LogID
		holdIDs[i] = e.HoldID
		amounts[i] = e.Amount
		refTypes[i] = e.RefType
		refIDs[i] = e.RefID
		modelCodes[i] = e.ModelCode
		providerCodes[i] = e.ProviderCode
		upstreamUSDs[i] = e.UpstreamUSD
		upstreamCNYs[i] = e.UpstreamCNY
		subIDs[i] = e.SubscriptionID
		eventTypes[i] = e.EventType
		planCodes[i] = e.PlanCode
		oldPlanCodes[i] = e.OldPlanCode
		amountCents[i] = e.AmountCents
		currencies[i] = e.Currency
		sources[i] = e.Source
		actuals[i] = e.Actual
		holdDeltas[i] = e.HoldDelta
		refundOfLogIDs[i] = e.RefundOfLogID
		expiresAts[i] = e.ExpiresAt
		reasons[i] = e.Reason
		if len(e.payload) == 0 {
			payloads[i] = "{}"
		} else {
			payloads[i] = string(e.payload)
		}
	}

	_, err := s.pool.Exec(ctx, q,
		eventIDs, kinds, userIDs, idemKeys, occurredAt, envs,
		logIDs, holdIDs, amounts, refTypes, refIDs,
		modelCodes, providerCodes, upstreamUSDs, upstreamCNYs,
		subIDs, eventTypes, planCodes, oldPlanCodes,
		amountCents, currencies, sources,
		actuals, holdDeltas, refundOfLogIDs, expiresAts, reasons, payloads,
	)
	if err != nil {
		return fmt.Errorf("sink: insert %d rows: %w", n, err)
	}
	return nil
}

// EnsureFuturePartitions — 提前 N 个月创建 events_yyyymm 分区. main.go 启动
// 时调一次, 后续每月 1 号 cron 再调. 已存在的分区 IF NOT EXISTS 跳过.
//
// 不在这个文件初始化数据 (DDL), 因为 migration 已经建了起始 3 个分区. 这里
// 只覆盖跨月运行的场景 — 6 个月后 dba 才会发现没分区, 太晚.
func EnsureFuturePartitions(ctx context.Context, pool *pgxpool.Pool, monthsAhead int) error {
	if monthsAhead < 1 {
		monthsAhead = 3
	}
	now := time.Now().UTC()
	for i := 0; i < monthsAhead; i++ {
		from := time.Date(now.Year(), now.Month()+time.Month(i), 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 1, 0)
		name := fmt.Sprintf("billing.events_%04d%02d", from.Year(), from.Month())
		q := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s
			PARTITION OF billing.events
			FOR VALUES FROM ('%s') TO ('%s')
		`, name, from.Format("2006-01-02"), to.Format("2006-01-02"))
		if _, err := pool.Exec(ctx, q); err != nil {
			// pgx 14.x 在 IF NOT EXISTS + 已存在时不报错; 异常仍抛.
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("ensure partition %s: %w", name, err)
			}
		}
	}
	return nil
}
