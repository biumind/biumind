// Package credits implements BiuMind 双账户积分系统：永久余额 + 时效余额。
//
// 扣减策略（不变量）：
//
//	优先扣最早过期的时效包 → 同 expires_at 按 created_at 升序 → 时效全扣完才扣永久。
//
// 退款策略（不变量）：
//
//	按 credit_logs.consume_breakdown_json 反向遍历（后扣的先退），原路径回填到对应
//	credit_packages。已过期的包不再回填（钱蒸发；流水仍记录实际退还金额）。
//
// 幂等：所有写接口（Consume / Refund / Grant / Recharge）都接 idempotency_key，
// 同 (user_id, idempotency_key) 重复请求只生效一次（DB UNIQUE 索引兜底）。
//
// 设计来源:
//
//	docs/BiuMind-AIGC-Storage-Design.md §2.4
//	docs/BiuMind-AIGC-Migration-Plan.md §2.4
//	packages/proto/biumind/credits/v1/credits.proto
//	services/identity/migrations/00017_credits.sql
package credits

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

// ─── Sentinel errors ──────────────────────────────────

var (
	ErrInsufficientCredits   = errors.New("credits: insufficient balance")
	ErrLogNotFound           = errors.New("credits: original log not found")
	ErrLogIsNotConsumption   = errors.New("credits: original log is not a consumption (delta >= 0)")
	ErrAmountExceedsOriginal = errors.New("credits: refund amount exceeds remaining refundable")
	ErrInvalidAmount         = errors.New("credits: amount must be > 0")
	ErrAllPackagesExpired    = errors.New("credits: all original packages have expired; nothing to refund")
	ErrInvalidKindExpiresAt  = errors.New("credits: time_limited requires expires_at; permanent must not have it")
)

// ─── Domain types ─────────────────────────────────────

// PackageKind / PackageSource / LogRefType 与 proto 同名同语义。
type PackageKind string

const (
	KindPermanent   PackageKind = "permanent"
	KindTimeLimited PackageKind = "time_limited"
)

type PackageSource string

const (
	SourceRecharge  PackageSource = "recharge"
	SourcePlanGrant PackageSource = "plan_grant"
	SourceReward    PackageSource = "reward"
	SourceRefund    PackageSource = "refund"
	SourceAdmin     PackageSource = "admin"
)

type LogRefType string

const (
	RefAIGCTask    LogRefType = "aigc_task"
	RefChatMessage LogRefType = "chat_message"
	RefRecharge    LogRefType = "recharge"
	RefPlanGrant   LogRefType = "plan_grant"
	RefRefund      LogRefType = "refund"
	RefReward      LogRefType = "reward"
	RefAdmin       LogRefType = "admin"

	// v0.3 M5 — 多模态 sync API request hold/log ref_types.
	// 跟 migration 00029 同步; Go 层校验防止下游传未注册值.
	RefEmbeddingRequest   LogRefType = "embedding_request"
	RefRerankRequest      LogRefType = "rerank_request"
	RefAudioSpeechRequest LogRefType = "audio_speech_request"
	RefImageRequest       LogRefType = "image_request"
	RefVideoRequest       LogRefType = "video_request"
)

type Balance struct {
	UserID                     uuid.UUID  `json:"user_id"`
	PermanentBalance           int64      `json:"permanent_balance"`
	TimeLimitedBalance         int64      `json:"time_limited_balance"`
	TimeLimitedEarliestExpires *time.Time `json:"time_limited_earliest_expires,omitempty"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

func (b *Balance) Total() int64 { return b.PermanentBalance + b.TimeLimitedBalance }

type Package struct {
	ID            uuid.UUID     `json:"id"`
	UserID        uuid.UUID     `json:"user_id"`
	Kind          PackageKind   `json:"kind"`
	Source        PackageSource `json:"source"`
	InitialAmount int64         `json:"initial_amount"`
	Remaining     int64         `json:"remaining"`
	ExpiresAt     *time.Time    `json:"expires_at,omitempty"`
	Metadata      []byte        `json:"metadata,omitempty"` // raw jsonb
	CreatedAt     time.Time     `json:"created_at"`
}

type Log struct {
	ID               uuid.UUID   `json:"id"`
	UserID           uuid.UUID   `json:"user_id"`
	Delta            int64       `json:"delta"` // >0 入账, <0 出账
	ConsumeBreakdown []Breakdown `json:"consume_breakdown,omitempty"`
	BalanceAfter     int64       `json:"balance_after"`
	RefType          LogRefType  `json:"ref_type"`
	RefID            string      `json:"ref_id,omitempty"`
	Remark           string      `json:"remark,omitempty"`
	RefundOfLogID    *uuid.UUID  `json:"refund_of_log_id,omitempty"`
	IdempotencyKey   string      `json:"idempotency_key,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
}

// Breakdown 描述一次出账（或退款）涉及哪个 package、扣/退多少。
// 出账时记最初扣的明细；退款时记实际回填的明细。
//
// W4-3: 多 Source 字段, 区分 "package" (默认, 兼容旧数据) 和 "quota"
// (套餐月度配额扣减). Source="quota" 时 PackageID 为零值, RefType 必填
// (用于退款时定位 user_quota_usage 的哪一行回退).
type Breakdown struct {
	Source    string     `json:"source,omitempty"`     // "" / "package" / "quota"
	PackageID uuid.UUID  `json:"package_id,omitempty"` // source="quota" 时零值
	RefType   LogRefType `json:"ref_type,omitempty"`   // source="quota" 时填
	Amount    int64      `json:"amount"`
}

// IsQuota 兼容旧数据 (空 Source) 默认视作 package.
func (b Breakdown) IsQuota() bool { return b.Source == "quota" }

// ─── Service ──────────────────────────────────────────

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time // 可注入，便于测试时间相关逻辑

	// W3-3: NATS 事件发布. 默认 NoopPublisher; main.go 注入真实
	// NATS publisher 后才实际发到 JetStream. 发布失败只 log,
	// 不回滚 DB 事务 — 钱不能因为 NATS 连不上就丢, 对账脚本兜底.
	pub events.Publisher
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now, pub: events.NoopPublisher{}}
}

// SetClock 仅供测试时间相关分支用（过期包跳过等）。
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetPublisher — main.go 注入 NATS publisher. 单测保持 NoopPublisher.
func (s *Service) SetPublisher(p events.Publisher) {
	if p == nil {
		p = events.NoopPublisher{}
	}
	s.pub = p
}

// ─── Read paths ───────────────────────────────────────

// GetBalance 直接读 user_credits（聚合视图，每次写都同步刷新）。
// 用户首次访问 user_credits 不存在时返回零余额。
func (s *Service) GetBalance(ctx context.Context, userID uuid.UUID) (*Balance, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT user_id, permanent_balance, time_limited_balance,
		       time_limited_earliest_expires, updated_at
		FROM identity.user_credits
		WHERE user_id = $1
	`, userID)
	bal, err := scanBalance(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return &Balance{UserID: userID, UpdatedAt: s.now()}, nil
	}
	return bal, err
}

func (s *Service) ListPackages(ctx context.Context, userID uuid.UUID, kind PackageKind, includeExhausted bool, limit, offset int) ([]*Package, error) {
	q := `
		SELECT id, user_id, kind, source, initial_amount, remaining,
		       expires_at, metadata, created_at
		FROM identity.credit_packages
		WHERE user_id = $1
	`
	args := []any{userID}
	if kind != "" {
		q += fmt.Sprintf(" AND kind = $%d", len(args)+1)
		args = append(args, string(kind))
	}
	if !includeExhausted {
		q += " AND remaining > 0"
	}
	q += " ORDER BY expires_at NULLS LAST, created_at"
	q += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		p, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) ListLogs(ctx context.Context, userID uuid.UUID, refType LogRefType, since *time.Time, limit, offset int) ([]*Log, error) {
	q := `
		SELECT id, user_id, delta, consume_breakdown_json, balance_after,
		       ref_type, ref_id, remark, refund_of_log_id, idempotency_key, created_at
		FROM identity.credit_logs
		WHERE user_id = $1
	`
	args := []any{userID}
	if refType != "" {
		q += fmt.Sprintf(" AND ref_type = $%d", len(args)+1)
		args = append(args, string(refType))
	}
	if since != nil {
		q += fmt.Sprintf(" AND created_at >= $%d", len(args)+1)
		args = append(args, *since)
	}
	q += " ORDER BY created_at DESC"
	q += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ─── Write paths（事务内） ───────────────────────────

// ConsumeArgs 是 Consume 的入参。idempotencyKey 强烈推荐填（如 task_id）。
//
// W3-7: ModelCode / ProviderCode 不写入 credit_logs (审计粒度由 RefID 兜底),
// 只透传到 NATS 事件供 dashboard 模型分布 / 毛利率统计. 留空时事件相应字段
// 也为空; 不影响 Consume 主路径.
type ConsumeArgs struct {
	UserID         uuid.UUID
	Amount         int64
	RefType        LogRefType
	RefID          string
	Remark         string
	IdempotencyKey string

	// 仅用于 NATS 事件发布; 不进 DB.
	ModelCode    string
	ProviderCode string
	UpstreamUSD  float64
	UpstreamCNY  float64
}

// Consume 扣减 Amount 积分。优先扣最早过期的时效包，扣完跳下一个，最后扣永久。
// 余额不足返回 ErrInsufficientCredits（事务回滚，packages 不动）。
func (s *Service) Consume(ctx context.Context, a ConsumeArgs) (*Log, *Balance, error) {
	if a.Amount <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	return s.tx(ctx, func(tx pgx.Tx) (*Log, *Balance, error) {
		// 0. 幂等检查
		if a.IdempotencyKey != "" {
			if existing, err := findLogByIdempotency(ctx, tx, a.UserID, a.IdempotencyKey); err != nil {
				return nil, nil, err
			} else if existing != nil {
				bal, err := refreshBalance(ctx, tx, a.UserID, s.now())
				return existing, bal, err
			}
		}

		// 1. W4-3: 优先 quota 扣减. quota 走完才落到 packages.
		now := s.now()
		alloc, err := AllocateQuotaInTx(ctx, tx, a.UserID, a.RefType, a.Amount, now)
		if err != nil {
			return nil, nil, err
		}
		var breakdown []Breakdown
		if alloc.QuotaAmount > 0 {
			breakdown = append(breakdown, Breakdown{
				Source:  "quota",
				RefType: a.RefType,
				Amount:  alloc.QuotaAmount,
			})
			if err := RecordQuotaUsage(ctx, tx, a.UserID, a.RefType, alloc.QuotaAmount, now); err != nil {
				return nil, nil, err
			}
		}
		// 2. 剩余从 packages 扣 (与原逻辑一致).
		if alloc.PackageAmount > 0 {
			pkgBreakdown, err := reserveFromPackages(ctx, tx, a.UserID, alloc.PackageAmount, now)
			if err != nil {
				return nil, nil, err
			}
			breakdown = append(breakdown, pkgBreakdown...)
		}

		// 3. 重算余额
		bal, err := refreshBalance(ctx, tx, a.UserID, now)
		if err != nil {
			return nil, nil, err
		}

		// 4. 写流水
		log, err := insertLog(ctx, tx, insertLogArgs{
			UserID:         a.UserID,
			Delta:          -a.Amount,
			Breakdown:      breakdown,
			BalanceAfter:   bal.Total(),
			RefType:        a.RefType,
			RefID:          a.RefID,
			Remark:         a.Remark,
			IdempotencyKey: a.IdempotencyKey,
		})
		if err != nil {
			return nil, nil, err
		}
		// W3-3: NATS 事件发布. 在事务 commit 前发, 但 publisher 失败不
		// 回滚 (NoopPublisher 永远不返错; 真 NATS 失败让对账脚本兜底).
		_ = s.pub.PublishConsume(ctx, events.ConsumeEvent{
			Common: events.Common{
				UserID:         a.UserID,
				IdempotencyKey: a.IdempotencyKey,
			},
			LogID:        log.ID,
			Amount:       a.Amount,
			RefType:      string(a.RefType),
			RefID:        a.RefID,
			ModelCode:    a.ModelCode,
			ProviderCode: a.ProviderCode,
			UpstreamUSD:  a.UpstreamUSD,
			UpstreamCNY:  a.UpstreamCNY,
		})
		return log, bal, nil
	})
}

// RefundArgs 是 Refund 的入参。Amount 可小于等于原扣减 |delta|（支持部分退款）。
// 已部分退款的可继续退，但累计退款不能超过原 |delta|。
type RefundArgs struct {
	OriginalLogID  uuid.UUID
	Amount         int64
	Remark         string
	IdempotencyKey string
}

// Refund 按原扣减 log 的 breakdown 反向遍历（后扣先退），原路径回填到 packages。
// 已过期的时效包跳过（视为钱蒸发）；最终实际退款金额可能小于 Amount 时仍记流水但
// 返回 ErrAllPackagesExpired（实际为零退款时）。
func (s *Service) Refund(ctx context.Context, a RefundArgs) (*Log, *Balance, error) {
	if a.Amount <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	return s.tx(ctx, func(tx pgx.Tx) (*Log, *Balance, error) {
		// 0. 幂等
		if a.IdempotencyKey != "" {
			if existing, err := findRefundByIdempotency(ctx, tx, a.OriginalLogID, a.IdempotencyKey); err != nil {
				return nil, nil, err
			} else if existing != nil {
				bal, err := refreshBalance(ctx, tx, existing.UserID, s.now())
				return existing, bal, err
			}
		}

		// 1. 锁原 log
		orig, err := lockLogByID(ctx, tx, a.OriginalLogID)
		if err != nil {
			return nil, nil, err
		}
		if orig == nil {
			return nil, nil, ErrLogNotFound
		}
		if orig.Delta >= 0 {
			return nil, nil, ErrLogIsNotConsumption
		}
		origAbs := -orig.Delta

		// 2. 累计已退款 + Amount <= |orig.delta|？
		alreadyRefunded, err := sumRefundedAmount(ctx, tx, orig.ID)
		if err != nil {
			return nil, nil, err
		}
		if alreadyRefunded+a.Amount > origAbs {
			return nil, nil, ErrAmountExceedsOriginal
		}

		// 3. 反向遍历 breakdown，依次回填到 package / quota（已过期的 package 跳过）
		now := s.now()
		var restored []Breakdown
		left := a.Amount
		for i := len(orig.ConsumeBreakdown) - 1; i >= 0 && left > 0; i-- {
			step := orig.ConsumeBreakdown[i]
			take := step.Amount
			if take > left {
				take = left
			}
			// W4-3: source=quota 段反向退到 user_quota_usage.
			if step.IsQuota() {
				if err := RefundQuotaUsage(ctx, tx, orig.UserID, step.RefType, take, now); err != nil {
					return nil, nil, err
				}
				restored = append(restored, Breakdown{
					Source:  "quota",
					RefType: step.RefType,
					Amount:  take,
				})
				left -= take
				continue
			}
			// source=package: 锁 + 检查 package 状态
			pkg, err := lockPackageByID(ctx, tx, step.PackageID)
			if err != nil {
				return nil, nil, err
			}
			if pkg == nil {
				continue
			}
			// 已过期 → 跳过（钱蒸发）
			if pkg.ExpiresAt != nil && !pkg.ExpiresAt.After(now) {
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE identity.credit_packages
				SET remaining = remaining + $1
				WHERE id = $2
			`, take, pkg.ID); err != nil {
				return nil, nil, err
			}
			restored = append(restored, Breakdown{PackageID: pkg.ID, Amount: take})
			left -= take
		}

		actuallyRefunded := a.Amount - left
		if actuallyRefunded == 0 {
			return nil, nil, ErrAllPackagesExpired
		}

		// 4. 重算余额
		bal, err := refreshBalance(ctx, tx, orig.UserID, now)
		if err != nil {
			return nil, nil, err
		}

		// 5. 写退款流水
		origID := orig.ID
		log, err := insertLog(ctx, tx, insertLogArgs{
			UserID:         orig.UserID,
			Delta:          actuallyRefunded,
			Breakdown:      restored,
			BalanceAfter:   bal.Total(),
			RefType:        RefRefund,
			RefID:          orig.RefID,
			Remark:         a.Remark,
			RefundOfLogID:  &origID,
			IdempotencyKey: a.IdempotencyKey,
		})
		if err != nil {
			return nil, nil, err
		}
		// W3-3 publish refund event
		_ = s.pub.PublishRefund(ctx, events.RefundEvent{
			Common: events.Common{
				UserID:         orig.UserID,
				IdempotencyKey: a.IdempotencyKey,
			},
			LogID:         log.ID,
			RefundOfLogID: orig.ID,
			Amount:        actuallyRefunded,
		})
		return log, bal, nil
	})
}

// GrantArgs 入账新积分。time_limited 必须填 ExpiresAt；permanent 不能填。
type GrantArgs struct {
	UserID         uuid.UUID
	Amount         int64
	Kind           PackageKind
	Source         PackageSource
	ExpiresAt      *time.Time
	Remark         string
	IdempotencyKey string
}

// Grant 入账 Amount 积分到一个新 package。返回新 package + 当前余额。
func (s *Service) Grant(ctx context.Context, a GrantArgs) (*Package, *Balance, error) {
	if a.Amount <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	if a.Kind == KindTimeLimited && a.ExpiresAt == nil {
		return nil, nil, ErrInvalidKindExpiresAt
	}
	if a.Kind == KindPermanent && a.ExpiresAt != nil {
		return nil, nil, ErrInvalidKindExpiresAt
	}
	var pkg *Package
	var bal *Balance
	_, _, err := s.tx(ctx, func(tx pgx.Tx) (*Log, *Balance, error) {
		// 0. 幂等检查
		if a.IdempotencyKey != "" {
			if existing, err := findLogByIdempotency(ctx, tx, a.UserID, a.IdempotencyKey); err != nil {
				return nil, nil, err
			} else if existing != nil {
				// 已存在：按流水 breakdown 找回 package
				if len(existing.ConsumeBreakdown) > 0 {
					p, err := getPackageByID(ctx, tx, existing.ConsumeBreakdown[0].PackageID)
					if err != nil {
						return nil, nil, err
					}
					pkg = p
				}
				b, err := refreshBalance(ctx, tx, a.UserID, s.now())
				bal = b
				return existing, b, err
			}
		}

		// 1. INSERT package
		row := tx.QueryRow(ctx, `
			INSERT INTO identity.credit_packages
				(user_id, kind, source, initial_amount, remaining, expires_at)
			VALUES ($1, $2, $3, $4, $4, $5)
			RETURNING id, user_id, kind, source, initial_amount, remaining,
			          expires_at, metadata, created_at
		`, a.UserID, string(a.Kind), string(a.Source), a.Amount, a.ExpiresAt)
		p, err := scanPackage(row)
		if err != nil {
			return nil, nil, err
		}
		pkg = p

		// 2. 重算余额
		b, err := refreshBalance(ctx, tx, a.UserID, s.now())
		if err != nil {
			return nil, nil, err
		}
		bal = b

		// 3. 写入账流水（breakdown 仅含一项指向新 package，便于后续退款 / 追溯）
		refType := RefRecharge
		switch a.Source {
		case SourcePlanGrant:
			refType = RefPlanGrant
		case SourceReward:
			refType = RefReward
		case SourceRefund:
			refType = RefRefund
		case SourceAdmin:
			refType = RefAdmin
		}
		log, err := insertLog(ctx, tx, insertLogArgs{
			UserID:         a.UserID,
			Delta:          a.Amount,
			Breakdown:      []Breakdown{{PackageID: p.ID, Amount: a.Amount}},
			BalanceAfter:   b.Total(),
			RefType:        refType,
			Remark:         a.Remark,
			IdempotencyKey: a.IdempotencyKey,
		})
		return log, b, err
	})
	if err != nil {
		return nil, nil, err
	}
	return pkg, bal, nil
}

// ─── 内部 helpers ─────────────────────────────────────

// tx 执行带写锁 + 幂等的事务. 所有 Consume / Refund / Grant 走这条路.
func (s *Service) tx(ctx context.Context, fn func(pgx.Tx) (*Log, *Balance, error)) (*Log, *Balance, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	log, bal, err := fn(tx)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return log, bal, nil
}

type insertLogArgs struct {
	UserID         uuid.UUID
	Delta          int64
	Breakdown      []Breakdown
	BalanceAfter   int64
	RefType        LogRefType
	RefID          string
	Remark         string
	RefundOfLogID  *uuid.UUID
	IdempotencyKey string
}

func insertLog(ctx context.Context, tx pgx.Tx, a insertLogArgs) (*Log, error) {
	var bdJSON any
	if len(a.Breakdown) > 0 {
		j, err := json.Marshal(a.Breakdown)
		if err != nil {
			return nil, err
		}
		bdJSON = j
	}
	var idemp any
	if a.IdempotencyKey != "" {
		idemp = a.IdempotencyKey
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO identity.credit_logs
			(user_id, delta, consume_breakdown_json, balance_after,
			 ref_type, ref_id, remark, refund_of_log_id, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, user_id, delta, consume_breakdown_json, balance_after,
		          ref_type, ref_id, remark, refund_of_log_id, idempotency_key, created_at
	`,
		a.UserID, a.Delta, bdJSON, a.BalanceAfter,
		string(a.RefType), nullableStr(a.RefID), nullableStr(a.Remark),
		a.RefundOfLogID, idemp,
	)
	return scanLog(row)
}

func refreshBalance(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time) (*Balance, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO identity.user_credits
			(user_id, permanent_balance, time_limited_balance,
			 time_limited_earliest_expires, updated_at)
		SELECT $1,
		       COALESCE(SUM(remaining) FILTER (WHERE kind='permanent'), 0),
		       COALESCE(SUM(remaining) FILTER (
		           WHERE kind='time_limited' AND (expires_at IS NULL OR expires_at > $2)
		       ), 0),
		       MIN(expires_at) FILTER (
		           WHERE kind='time_limited' AND remaining > 0 AND expires_at > $2
		       ),
		       $2
		FROM identity.credit_packages
		WHERE user_id = $1
		ON CONFLICT (user_id) DO UPDATE SET
			permanent_balance             = EXCLUDED.permanent_balance,
			time_limited_balance          = EXCLUDED.time_limited_balance,
			time_limited_earliest_expires = EXCLUDED.time_limited_earliest_expires,
			updated_at                    = EXCLUDED.updated_at
		RETURNING user_id, permanent_balance, time_limited_balance,
		          time_limited_earliest_expires, updated_at
	`, userID, now)
	return scanBalance(row)
}

func findLogByIdempotency(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string) (*Log, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, delta, consume_breakdown_json, balance_after,
		       ref_type, ref_id, remark, refund_of_log_id, idempotency_key, created_at
		FROM identity.credit_logs
		WHERE user_id = $1 AND idempotency_key = $2
	`, userID, key)
	l, err := scanLog(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

// findRefundByIdempotency 查找针对同一原 log 的退款流水（幂等键）。
func findRefundByIdempotency(ctx context.Context, tx pgx.Tx, originalLogID uuid.UUID, key string) (*Log, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, delta, consume_breakdown_json, balance_after,
		       ref_type, ref_id, remark, refund_of_log_id, idempotency_key, created_at
		FROM identity.credit_logs
		WHERE refund_of_log_id = $1 AND idempotency_key = $2
	`, originalLogID, key)
	l, err := scanLog(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

func lockLogByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Log, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, delta, consume_breakdown_json, balance_after,
		       ref_type, ref_id, remark, refund_of_log_id, idempotency_key, created_at
		FROM identity.credit_logs
		WHERE id = $1
		FOR UPDATE
	`, id)
	l, err := scanLog(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

func lockPackageByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Package, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, kind, source, initial_amount, remaining,
		       expires_at, metadata, created_at
		FROM identity.credit_packages
		WHERE id = $1
		FOR UPDATE
	`, id)
	p, err := scanPackage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func getPackageByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Package, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, kind, source, initial_amount, remaining,
		       expires_at, metadata, created_at
		FROM identity.credit_packages
		WHERE id = $1
	`, id)
	p, err := scanPackage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func sumRefundedAmount(ctx context.Context, tx pgx.Tx, originalLogID uuid.UUID) (int64, error) {
	var sum int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(delta), 0)
		FROM identity.credit_logs
		WHERE refund_of_log_id = $1
	`, originalLogID).Scan(&sum)
	return sum, err
}

// ─── scan helpers ─────────────────────────────────────

type scanner interface {
	Scan(...any) error
}

func scanBalance(r scanner) (*Balance, error) {
	b := &Balance{}
	err := r.Scan(&b.UserID, &b.PermanentBalance, &b.TimeLimitedBalance,
		&b.TimeLimitedEarliestExpires, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func scanPackage(r scanner) (*Package, error) {
	p := &Package{}
	var kind, source string
	err := r.Scan(&p.ID, &p.UserID, &kind, &source, &p.InitialAmount, &p.Remaining,
		&p.ExpiresAt, &p.Metadata, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.Kind = PackageKind(kind)
	p.Source = PackageSource(source)
	return p, nil
}

func scanLog(r scanner) (*Log, error) {
	l := &Log{}
	var refType string
	var refID, remark, idemp *string
	var bdJSON []byte
	err := r.Scan(&l.ID, &l.UserID, &l.Delta, &bdJSON, &l.BalanceAfter,
		&refType, &refID, &remark, &l.RefundOfLogID, &idemp, &l.CreatedAt)
	if err != nil {
		return nil, err
	}
	l.RefType = LogRefType(refType)
	if refID != nil {
		l.RefID = *refID
	}
	if remark != nil {
		l.Remark = *remark
	}
	if idemp != nil {
		l.IdempotencyKey = *idemp
	}
	if len(bdJSON) > 0 {
		if err := json.Unmarshal(bdJSON, &l.ConsumeBreakdown); err != nil {
			return nil, fmt.Errorf("decode breakdown: %w", err)
		}
	}
	return l, nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
