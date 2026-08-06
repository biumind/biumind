// subscriptions.go — Phase 4 W2-4 订阅状态机.
//
// billing.subscriptions 行的生命周期:
//
//   trialing  ─────► active                  (试用期结束转付费 / Stripe 通知)
//   active    ─────► canceled                (用户取消, 仍服务到 current_period_end)
//   active    ─────► past_due                (扣款失败, Stripe retry 中)
//   past_due  ─────► active                  (恢复扣款成功)
//   past_due  ─────► canceled                (永久失败, 后续 expired)
//   active    ─────► active (plan_id 变化)   (升降级)
//   canceled  ─────► expired                 (周期结束, 转 free)
//   trialing  ─────► canceled                (试用期内取消)
//
// 状态机不允许的转换会返 ErrInvalidTransition. 终态 expired 不可逆.
//
// 写路径:
//   · CreateOrTrial — Stripe webhook subscription.created 时调
//   · Activate      — Stripe webhook subscription.updated status='active' 时
//   · MarkPastDue   — invoice.payment_failed 时
//   · Cancel        — 用户取消 / Stripe webhook subscription.deleted 时
//   · Expire        — 后台 cron, current_period_end < now AND status='canceled' 时
//   · ChangePlan    — admin 直改 / Stripe webhook plan 变化时
//
// 每次状态变更都写 subscription_events 一行 (审计).

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/identity/internal/events"
)

type SubStatus string

const (
	SubStatusTrialing SubStatus = "trialing"
	SubStatusActive   SubStatus = "active"
	SubStatusPastDue  SubStatus = "past_due"
	SubStatusCanceled SubStatus = "canceled"
	SubStatusExpired  SubStatus = "expired"
)

// IsActive — 用户当前是否能享用 plan benefits (含 trialing / past_due
// 宽限期; canceled 在周期内仍 active 但服务标记为 cancel-pending).
func (s SubStatus) IsActive() bool {
	switch s {
	case SubStatusTrialing, SubStatusActive, SubStatusPastDue, SubStatusCanceled:
		return true
	}
	return false
}

// canTransition 状态机表. 返 false 则 Cancel/Activate/etc 之类操作拒绝.
func canTransition(from, to SubStatus) bool {
	switch from {
	case SubStatusTrialing:
		return to == SubStatusActive || to == SubStatusCanceled
	case SubStatusActive:
		return to == SubStatusActive || // 升降级 (plan_id 变化)
			to == SubStatusPastDue || to == SubStatusCanceled
	case SubStatusPastDue:
		return to == SubStatusActive || to == SubStatusCanceled
	case SubStatusCanceled:
		// W5-7: 用户在 period_end 前撤销取消 → resume.
		return to == SubStatusExpired || to == SubStatusActive
	case SubStatusExpired:
		return false // 终态
	}
	return false
}

var (
	ErrInvalidTransition  = errors.New("subscriptions: invalid status transition")
	ErrSubscriptionNotFound = errors.New("subscriptions: not found")
	ErrAlreadyActive      = errors.New("subscriptions: user already has active subscription")
)

type Subscription struct {
	ID                   uuid.UUID
	UserID               uuid.UUID
	PlanID               uuid.UUID
	Status               SubStatus
	CurrentPeriodStart   time.Time
	CurrentPeriodEnd     time.Time
	TrialEndAt           *time.Time
	CancelAt             *time.Time
	CanceledAt           *time.Time
	ExpiredAt            *time.Time
	BillingCycle         string // 'monthly' | 'yearly' | 'lifetime'
	StripeCustomerID     string // "" if NULL
	StripeSubscriptionID string // "" if NULL
	Metadata             json.RawMessage
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type SubscriptionsRepo struct {
	pool *pgxpool.Pool

	// W3-3: NATS 发布. 默认 NoopPublisher; main.go 注入真 publisher.
	pub events.Publisher
	// PlansRepo 用于发布事件时查 plan code. nil 时只发 plan_id 不发 code.
	plans *PlansRepo
}

func NewSubscriptionsRepo(pool *pgxpool.Pool) *SubscriptionsRepo {
	return &SubscriptionsRepo{pool: pool, pub: events.NoopPublisher{}}
}

// SetPublisher — main.go wire 真 NATS publisher.
func (r *SubscriptionsRepo) SetPublisher(p events.Publisher) {
	if p == nil {
		p = events.NoopPublisher{}
	}
	r.pub = p
}

// SetPlansRepo — 让 publish 路径能把 plan_id → code 翻译用于事件载荷.
func (r *SubscriptionsRepo) SetPlansRepo(p *PlansRepo) { r.plans = p }

// Pool — 测试 cleanup 用 (DELETE 写过的数据). 生产路径不应直接用这个, 走
// repo 函数让 audit event 一并发出.
func (r *SubscriptionsRepo) Pool() *pgxpool.Pool { return r.pool }

// planCode — 内部 helper, 查不到 / nil plansRepo 时返空串.
func (r *SubscriptionsRepo) planCode(ctx context.Context, id *uuid.UUID) string {
	if id == nil || *id == uuid.Nil || r.plans == nil {
		return ""
	}
	p, err := r.plans.GetByID(ctx, *id)
	if err != nil {
		return ""
	}
	return string(p.Code)
}

// ─── reads ────────────────────────────────────────────

const subSelect = `
	SELECT id, user_id, plan_id, status,
	       current_period_start, current_period_end,
	       trial_end_at, cancel_at, canceled_at, expired_at,
	       billing_cycle,
	       COALESCE(stripe_customer_id, ''),
	       COALESCE(stripe_subscription_id, ''),
	       metadata, created_at, updated_at
	FROM billing.subscriptions
`

func scanSub(scan func(...any) error) (Subscription, error) {
	var s Subscription
	var status string
	var meta []byte
	if err := scan(
		&s.ID, &s.UserID, &s.PlanID, &status,
		&s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.TrialEndAt, &s.CancelAt, &s.CanceledAt, &s.ExpiredAt,
		&s.BillingCycle, &s.StripeCustomerID, &s.StripeSubscriptionID,
		&meta, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return Subscription{}, err
	}
	s.Status = SubStatus(status)
	if len(meta) > 0 {
		s.Metadata = meta
	}
	return s, nil
}

// GetActiveByUser 返用户当前活跃订阅 (trialing/active/past_due/canceled
// 任意一种, 但必须未 expired). 用于 GET /v1/subscriptions/me.
//
// schema 上 (user_id) WHERE status IN (trialing,active,past_due) 是
// partial unique. canceled 不在 unique 里, 所以可能并存 1 active +
// N canceled (历史). 这里只取最新一条 (created_at DESC).
func (r *SubscriptionsRepo) GetActiveByUser(ctx context.Context, userID uuid.UUID) (*Subscription, error) {
	q := subSelect + ` WHERE user_id = $1
		AND status IN ('trialing', 'active', 'past_due', 'canceled')
		ORDER BY created_at DESC LIMIT 1`
	row := r.pool.QueryRow(ctx, q, userID)
	s, err := scanSub(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetByStripeSubID 按 Stripe sub_xxx 查; webhook 用来 idempotent 处理.
func (r *SubscriptionsRepo) GetByStripeSubID(ctx context.Context, stripeSubID string) (*Subscription, error) {
	q := subSelect + ` WHERE stripe_subscription_id = $1 LIMIT 1`
	row := r.pool.QueryRow(ctx, q, stripeSubID)
	s, err := scanSub(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListByUser 返用户全部订阅历史 (含 expired), created_at DESC.
func (r *SubscriptionsRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]Subscription, error) {
	q := subSelect + ` WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Subscription, 0, 4)
	for rows.Next() {
		s, err := scanSub(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── writes (state machine) ──────────────────────────

// CreateInput is the writable shape for new subscriptions.
type CreateInput struct {
	UserID               uuid.UUID
	PlanID               uuid.UUID
	Status               SubStatus // 初始一般是 trialing 或 active
	CurrentPeriodStart   time.Time
	CurrentPeriodEnd     time.Time
	TrialEndAt           *time.Time
	BillingCycle         string
	StripeCustomerID     string
	StripeSubscriptionID string
	Metadata             json.RawMessage
}

// Create 创建一行新订阅 + 写 subscription_events 'created' 一行.
//
// 调用方 (Stripe webhook 或 admin) 必须保证用户没有已 active 的订阅 ——
// schema 上 partial unique index 会硬挡 (返回 23505 unique violation,
// translateErr 转 ErrAlreadyActive).
func (r *SubscriptionsRepo) Create(ctx context.Context, in CreateInput) (*Subscription, error) {
	if in.Status == "" {
		in.Status = SubStatusTrialing
	}
	if in.BillingCycle == "" {
		in.BillingCycle = "monthly"
	}
	meta := in.Metadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	q := `
		INSERT INTO billing.subscriptions
			(user_id, plan_id, status,
			 current_period_start, current_period_end,
			 trial_end_at, billing_cycle,
			 stripe_customer_id, stripe_subscription_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7,
		        NULLIF($8, ''), NULLIF($9, ''), $10::jsonb)
		RETURNING ` + subSelectColumns()
	row := r.pool.QueryRow(ctx, q,
		in.UserID, in.PlanID, string(in.Status),
		in.CurrentPeriodStart, in.CurrentPeriodEnd,
		in.TrialEndAt, in.BillingCycle,
		in.StripeCustomerID, in.StripeSubscriptionID, meta,
	)
	s, err := scanSub(row.Scan)
	if err != nil {
		// translate unique violation 23505 → ErrAlreadyActive
		if isUniqueViolation(err) {
			return nil, ErrAlreadyActive
		}
		return nil, fmt.Errorf("subscriptions.create: %w", err)
	}

	if err := r.appendEvent(ctx, eventInput{
		SubscriptionID: s.ID,
		UserID:         s.UserID,
		EventType:      "created",
		ToPlanID:       &s.PlanID,
		ToStatus:       string(s.Status),
		Metadata:       in.Metadata,
	}); err != nil {
		// 审计写入失败不阻塞 main 业务 (best-effort) — 但记 warning
		return &s, nil
	}
	// W3-3 publish subscription event
	_ = r.pub.PublishSubscription(ctx, events.SubscriptionEvent{
		Common:         events.Common{UserID: s.UserID},
		SubscriptionID: s.ID,
		EventType:      "created",
		PlanCode:       r.planCode(ctx, &s.PlanID),
	})
	return &s, nil
}

// subSelectColumns 抽取 RETURNING 用的列字符串, 避免 subSelect 重复.
func subSelectColumns() string {
	return `id, user_id, plan_id, status,
	        current_period_start, current_period_end,
	        trial_end_at, cancel_at, canceled_at, expired_at,
	        billing_cycle,
	        COALESCE(stripe_customer_id, ''),
	        COALESCE(stripe_subscription_id, ''),
	        metadata, created_at, updated_at`
}

// Transition 状态机变更. 调用方传当前 row + 期望新状态. 不允许的转换
// 返 ErrInvalidTransition, 调用方业务层应当报错.
//
// 同时写 subscription_events 一行.
func (r *SubscriptionsRepo) Transition(ctx context.Context, sub *Subscription, to SubStatus, reason string, stripeEventID string) (*Subscription, error) {
	if !canTransition(sub.Status, to) {
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, sub.Status, to)
	}
	now := time.Now().UTC()

	// 状态特定字段:
	//   to=canceled → canceled_at = now
	//   to=expired → expired_at = now
	q := `
		UPDATE billing.subscriptions
		SET status = $1,
		    canceled_at = CASE WHEN $1 = 'canceled' THEN $2 ELSE canceled_at END,
		    expired_at  = CASE WHEN $1 = 'expired'  THEN $2 ELSE expired_at  END,
		    updated_at  = $2
		WHERE id = $3
		RETURNING ` + subSelectColumns()

	row := r.pool.QueryRow(ctx, q, string(to), now, sub.ID)
	updated, err := scanSub(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("subscriptions.transition: %w", err)
	}

	// 审计事件
	eventType := transitionEventType(sub.Status, to)
	if err := r.appendEvent(ctx, eventInput{
		SubscriptionID: updated.ID,
		UserID:         updated.UserID,
		EventType:      eventType,
		FromStatus:     string(sub.Status),
		ToStatus:       string(to),
		StripeEventID:  stripeEventID,
		Metadata:       reasonToMetadata(reason),
	}); err != nil {
		return &updated, nil
	}
	// W3-3 publish subscription event
	_ = r.pub.PublishSubscription(ctx, events.SubscriptionEvent{
		Common:         events.Common{UserID: updated.UserID, IdempotencyKey: stripeEventID},
		SubscriptionID: updated.ID,
		EventType:      eventType,
		PlanCode:       r.planCode(ctx, &updated.PlanID),
	})
	return &updated, nil
}

// ChangePlan 升降级: status 不变 (一般是 active → active, plan_id 变).
// 写 'upgraded' 或 'downgraded' 事件 (按 plans.sort_order 比较).
func (r *SubscriptionsRepo) ChangePlan(ctx context.Context, sub *Subscription, newPlanID uuid.UUID, plansRepo *PlansRepo, stripeEventID string) (*Subscription, error) {
	if sub.PlanID == newPlanID {
		return sub, nil // no-op
	}
	if sub.Status != SubStatusActive && sub.Status != SubStatusTrialing {
		return nil, fmt.Errorf("%w: cannot change plan in status %s", ErrInvalidTransition, sub.Status)
	}
	now := time.Now().UTC()

	// 比较 sort_order 决定 upgraded / downgraded
	eventType := "upgraded"
	if plansRepo != nil {
		oldP, err1 := plansRepo.GetByID(ctx, sub.PlanID)
		newP, err2 := plansRepo.GetByID(ctx, newPlanID)
		if err1 == nil && err2 == nil && newP.SortOrder < oldP.SortOrder {
			eventType = "downgraded"
		}
	}

	q := `
		UPDATE billing.subscriptions
		SET plan_id = $1, updated_at = $2
		WHERE id = $3
		RETURNING ` + subSelectColumns()

	// (用别名版本, 因为 RETURNING 与 INSERT 同字段集)
	row := r.pool.QueryRow(ctx, q, newPlanID, now, sub.ID)
	updated, err := scanSub(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("subscriptions.change_plan: %w", err)
	}
	if err := r.appendEvent(ctx, eventInput{
		SubscriptionID: updated.ID,
		UserID:         updated.UserID,
		EventType:      eventType,
		FromPlanID:     &sub.PlanID,
		ToPlanID:       &newPlanID,
		FromStatus:     string(sub.Status),
		ToStatus:       string(updated.Status),
		StripeEventID:  stripeEventID,
	}); err != nil {
		return &updated, nil
	}
	// W3-3 publish subscription event (upgrade / downgrade)
	oldID := sub.PlanID
	_ = r.pub.PublishSubscription(ctx, events.SubscriptionEvent{
		Common:         events.Common{UserID: updated.UserID, IdempotencyKey: stripeEventID},
		SubscriptionID: updated.ID,
		EventType:      eventType,
		PlanCode:       r.planCode(ctx, &newPlanID),
		OldPlanCode:    r.planCode(ctx, &oldID),
	})
	return &updated, nil
}

// transitionEventType 把 (from, to) 翻译到 subscription_events.event_type
// 词汇表. 与 schema CHECK 字符串对齐.
func transitionEventType(from, to SubStatus) string {
	switch {
	case to == SubStatusActive && from == SubStatusTrialing:
		return "activated"
	case to == SubStatusActive && from == SubStatusPastDue:
		return "recovered"
	case to == SubStatusPastDue:
		return "past_due"
	case to == SubStatusCanceled:
		return "canceled"
	case to == SubStatusExpired:
		return "expired"
	}
	return "transitioned"
}

// reasonToMetadata wraps a free-form reason string into jsonb metadata.
func reasonToMetadata(reason string) json.RawMessage {
	if reason == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]string{"reason": reason})
	return b
}

// ─── events (audit) ──────────────────────────────────

type eventInput struct {
	SubscriptionID uuid.UUID
	UserID         uuid.UUID
	EventType      string
	FromPlanID     *uuid.UUID
	ToPlanID       *uuid.UUID
	FromStatus     string
	ToStatus       string
	StripeEventID  string
	Metadata       json.RawMessage
}

func (r *SubscriptionsRepo) appendEvent(ctx context.Context, in eventInput) error {
	meta := in.Metadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	const q = `
		INSERT INTO billing.subscription_events
			(subscription_id, user_id, event_type,
			 from_plan_id, to_plan_id, from_status, to_status,
			 stripe_event_id, metadata)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''),
		        NULLIF($8, ''), $9::jsonb)
		ON CONFLICT (stripe_event_id) DO NOTHING
	`
	_, err := r.pool.Exec(ctx, q,
		in.SubscriptionID, in.UserID, in.EventType,
		in.FromPlanID, in.ToPlanID, in.FromStatus, in.ToStatus,
		in.StripeEventID, meta,
	)
	return err
}

// ListEvents — 按 subscription_id 查审计流, created_at DESC.
type SubEvent struct {
	ID             uuid.UUID
	SubscriptionID uuid.UUID
	UserID         uuid.UUID
	EventType      string
	FromPlanID     *uuid.UUID
	ToPlanID       *uuid.UUID
	FromStatus     string
	ToStatus       string
	StripeEventID  string
	Metadata       json.RawMessage
	CreatedAt      time.Time
}

func (r *SubscriptionsRepo) ListEvents(ctx context.Context, subID uuid.UUID) ([]SubEvent, error) {
	const q = `
		SELECT id, subscription_id, user_id, event_type,
		       from_plan_id, to_plan_id,
		       COALESCE(from_status, ''), COALESCE(to_status, ''),
		       COALESCE(stripe_event_id, ''),
		       metadata, created_at
		FROM billing.subscription_events
		WHERE subscription_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, subID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SubEvent, 0, 8)
	for rows.Next() {
		var e SubEvent
		var meta []byte
		if err := rows.Scan(
			&e.ID, &e.SubscriptionID, &e.UserID, &e.EventType,
			&e.FromPlanID, &e.ToPlanID, &e.FromStatus, &e.ToStatus,
			&e.StripeEventID, &meta, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			e.Metadata = meta
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// isUniqueViolation 检查 pgx 错误是否是 23505 (unique_violation).
// 用于 Create 翻译 ErrAlreadyActive.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps PgError with PgError.Code = "23505"
	var pgErr interface {
		SQLState() string
	}
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
