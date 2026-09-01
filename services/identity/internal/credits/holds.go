// holds.go — 流式预扣 / 结算 (Hold / Settle / Release).
//
// 用于 chat / agent 等流式场景: 提交时 Hold 预扣 max, 流结束按真实 token
// Settle (≤ max), 失败 / 取消 Release.
//
// 实现思路:
//
//	Hold:    锁 packages → 按消费顺序扣减 remaining → 记 hold_breakdown_json
//	         → status='held' + expires_at=now+TTL → 不写 credit_logs (Settle 才写)
//	Settle:  锁 hold + 锁 breakdown 涉及的 packages → 反向退还 (max-actual)
//	         → 写 credit_logs (delta=-actual) → status='settled'
//	Release: 锁 hold + 锁 packages → 全额反向退还 → status='released'
//	         → 不写 credit_logs (没真消费)
//	Reap:    扫 expires_at<now AND status='held' → 同 Release 逻辑 + status='expired'
//
// 与 Consume 的关系: Hold 抢同样的 packages, 相当于"预占"; 同一用户 N 个并发
// hold 各占各的钱, 总和不能超余额 (第 N+1 个 Hold insufficient).
//
// 设计: docs/BiuMind-Billing-Redesign.md §5.2.

package credits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/biumind/biumind/services/identity/internal/events"
)

// ─── Sentinel errors ──────────────────────────────────

var (
	ErrHoldNotFound       = errors.New("credits: hold not found")
	ErrHoldNotActive      = errors.New("credits: hold is not in 'held' state")
	ErrSettleExceedsHold  = errors.New("credits: settle amount exceeds hold max")
	ErrInvalidHoldRefType = errors.New("credits: hold ref_type must be chat_message / agent_step / aigc_task / embedding_request / rerank_request / audio_speech_request / image_request / video_request")
)

// ─── Domain types ─────────────────────────────────────

type HoldStatus string

const (
	HoldStatusHeld     HoldStatus = "held"
	HoldStatusSettled  HoldStatus = "settled"
	HoldStatusReleased HoldStatus = "released"
	HoldStatusExpired  HoldStatus = "expired"
)

// validHoldRefTypes 与 credit_holds.ref_type CHECK 约束对齐.
// 00018 起原始 3 类; 00029 加 5 个 v0.3 多模态 sync API request 类.
var validHoldRefTypes = map[LogRefType]struct{}{
	RefChatMessage: {},
	"agent_step":   {},
	RefAIGCTask:    {},
	// M5
	RefEmbeddingRequest:   {},
	RefRerankRequest:      {},
	RefAudioSpeechRequest: {},
	RefImageRequest:       {},
	RefVideoRequest:       {},
	// client-docproc W4（migration 00019）
	RefWikiParseRequest: {},
}

// DefaultHoldTTL — 5 分钟兜底; 长任务请走 Consume 一次性扣减.
const DefaultHoldTTL = 5 * time.Minute

type Hold struct {
	ID             uuid.UUID   `json:"id"`
	UserID         uuid.UUID   `json:"user_id"`
	RefType        LogRefType  `json:"ref_type"`
	RefID          string      `json:"ref_id,omitempty"`
	MaxAmount      int64       `json:"max_amount"`
	ActualAmount   *int64      `json:"actual_amount,omitempty"`
	Status         HoldStatus  `json:"status"`
	HoldBreakdown  []Breakdown `json:"hold_breakdown,omitempty"`
	IdempotencyKey string      `json:"idempotency_key,omitempty"`
	ExpiresAt      time.Time   `json:"expires_at"`
	CreatedAt      time.Time   `json:"created_at"`
	SettledAt      *time.Time  `json:"settled_at,omitempty"`
}

// ─── Hold ─────────────────────────────────────────────

type HoldArgs struct {
	UserID         uuid.UUID
	MaxAmount      int64
	RefType        LogRefType
	RefID          string
	IdempotencyKey string
	TTL            time.Duration // 留 0 用 DefaultHoldTTL

	// W3-7: 仅透传到 NATS 事件 (Hold + 后续 Settle); 不进 credit_holds 表.
	// Settle 时从 hold 行查不到这俩, 调用方 Settle 时若需要请显式再传.
	ModelCode    string
	ProviderCode string
}

// Hold 预扣 max_amount; 余额不足返 ErrInsufficientCredits.
//
// 命中幂等键直接返原 Hold (不重新预占).
func (s *Service) Hold(ctx context.Context, a HoldArgs) (*Hold, *Balance, error) {
	if a.MaxAmount <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	if _, ok := validHoldRefTypes[a.RefType]; !ok {
		return nil, nil, ErrInvalidHoldRefType
	}
	ttl := a.TTL
	if ttl <= 0 {
		ttl = DefaultHoldTTL
	}

	var (
		out *Hold
		bal *Balance
	)
	err := s.txDo(ctx, func(tx pgx.Tx) error {
		// 0. 幂等
		if a.IdempotencyKey != "" {
			existing, err := findHoldByIdempotency(ctx, tx, a.UserID, a.RefType, a.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				out = existing
				bal, err = refreshBalance(ctx, tx, a.UserID, s.now())
				return err
			}
		}

		// 1. W4-3: 优先 quota 预占, 剩余落 packages.
		now := s.now()
		alloc, err := AllocateQuotaInTx(ctx, tx, a.UserID, a.RefType, a.MaxAmount, now)
		if err != nil {
			return err
		}
		var breakdown []Breakdown
		if alloc.QuotaAmount > 0 {
			breakdown = append(breakdown, Breakdown{
				Source:  "quota",
				RefType: a.RefType,
				Amount:  alloc.QuotaAmount,
			})
			if err := RecordQuotaUsage(ctx, tx, a.UserID, a.RefType, alloc.QuotaAmount, now); err != nil {
				return err
			}
		}
		if alloc.PackageAmount > 0 {
			pkgBreakdown, err := reserveFromPackages(ctx, tx, a.UserID, alloc.PackageAmount, now)
			if err != nil {
				return err
			}
			breakdown = append(breakdown, pkgBreakdown...)
		}

		// 2. 重算余额
		bal, err = refreshBalance(ctx, tx, a.UserID, now)
		if err != nil {
			return err
		}

		// 3. 写 hold 行
		bdJSON, err := json.Marshal(breakdown)
		if err != nil {
			return err
		}
		expiresAt := now.Add(ttl)
		row := tx.QueryRow(ctx, `
			INSERT INTO identity.credit_holds
			    (user_id, ref_type, ref_id, max_amount, status,
			     hold_breakdown_json, idempotency_key, expires_at, created_at)
			VALUES ($1, $2, NULLIF($3,''), $4, 'held', $5, NULLIF($6,''), $7, $8)
			RETURNING id, created_at
		`, a.UserID, string(a.RefType), a.RefID, a.MaxAmount, bdJSON, a.IdempotencyKey, expiresAt, now)

		out = &Hold{
			UserID:         a.UserID,
			RefType:        a.RefType,
			RefID:          a.RefID,
			MaxAmount:      a.MaxAmount,
			Status:         HoldStatusHeld,
			HoldBreakdown:  breakdown,
			IdempotencyKey: a.IdempotencyKey,
			ExpiresAt:      expiresAt,
		}
		return row.Scan(&out.ID, &out.CreatedAt)
	})
	if err != nil {
		return nil, nil, err
	}
	// W3-3 publish hold event (publisher 失败不回滚)
	_ = s.pub.PublishHold(ctx, events.HoldEvent{
		Common: events.Common{
			UserID:         out.UserID,
			IdempotencyKey: out.IdempotencyKey,
		},
		HoldID:       out.ID,
		Amount:       out.MaxAmount,
		RefType:      string(out.RefType),
		RefID:        out.RefID,
		ModelCode:    a.ModelCode,
		ProviderCode: a.ProviderCode,
		ExpiresAt:    out.ExpiresAt,
	})
	return out, bal, nil
}

// ─── Settle ───────────────────────────────────────────

type SettleArgs struct {
	HoldID       uuid.UUID
	ActualAmount int64 // 0 = 等同 Release
	Remark       string
}

// Settle 把 hold 变为已结算: 实扣 ActualAmount, 退回 (Max-Actual) 到 packages.
// ActualAmount 必须 ≤ MaxAmount. 已 settled / released / expired 的 hold 报错.
//
// 写一条 credit_logs (delta=-actual) — 与一次性 Consume 的流水形式一致, 事件流
// 下游不用区别对待.
//
// ActualAmount=0 的语义等同 Release (允许, 但建议显式调 Release 更清晰).
func (s *Service) Settle(ctx context.Context, a SettleArgs) (*Hold, *Log, *Balance, error) {
	if a.ActualAmount < 0 {
		return nil, nil, nil, ErrInvalidAmount
	}

	var (
		hold   *Hold
		logRow *Log
		bal    *Balance
	)
	err := s.txDo(ctx, func(tx pgx.Tx) error {
		now := s.now()

		// 1. 锁 hold
		h, err := lockHoldByID(ctx, tx, a.HoldID)
		if err != nil {
			return err
		}
		if h == nil {
			return ErrHoldNotFound
		}
		if h.Status != HoldStatusHeld {
			return ErrHoldNotActive
		}
		if a.ActualAmount > h.MaxAmount {
			return ErrSettleExceedsHold
		}

		// 2. 退还 (max - actual) 到 packages (反向)
		refundAmount := h.MaxAmount - a.ActualAmount
		if refundAmount > 0 {
			if _, err := refundFromBreakdown(ctx, tx, h.UserID, h.HoldBreakdown, refundAmount, now); err != nil {
				return err
			}
		}

		// 3. 重算余额
		bal, err = refreshBalance(ctx, tx, h.UserID, now)
		if err != nil {
			return err
		}

		// 4. 写流水 (actual > 0 才写)
		var settleLog *Log
		if a.ActualAmount > 0 {
			settledBreakdown := truncateBreakdown(h.HoldBreakdown, a.ActualAmount)
			settleLog, err = insertLog(ctx, tx, insertLogArgs{
				UserID:       h.UserID,
				Delta:        -a.ActualAmount,
				Breakdown:    settledBreakdown,
				BalanceAfter: bal.Total(),
				RefType:      h.RefType,
				RefID:        h.RefID,
				Remark:       a.Remark,
				// hold_id 作为幂等基准 (settle 只能成功一次, 因为 status 检查)
				IdempotencyKey: "hold:" + h.ID.String(),
			})
			if err != nil {
				return err
			}
		}

		// 5. 标记 hold = settled
		actualAmt := a.ActualAmount
		_, err = tx.Exec(ctx, `
			UPDATE identity.credit_holds
			SET status = 'settled', actual_amount = $1, settled_at = $2
			WHERE id = $3
		`, actualAmt, now, h.ID)
		if err != nil {
			return err
		}

		h.Status = HoldStatusSettled
		h.ActualAmount = &actualAmt
		h.SettledAt = &now
		hold = h
		logRow = settleLog
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	// W3-3 publish settle event. logRow may be nil for actual=0
	// (semantically equivalent to release); we still emit settle so
	// sink can distinguish "explicitly closed" from "released".
	var settleLogID uuid.UUID
	if logRow != nil {
		settleLogID = logRow.ID
	}
	_ = s.pub.PublishSettle(ctx, events.SettleEvent{
		Common:    events.Common{UserID: hold.UserID},
		HoldID:    hold.ID,
		LogID:     settleLogID,
		Actual:    a.ActualAmount,
		HoldDelta: hold.MaxAmount - a.ActualAmount,
	})
	return hold, logRow, bal, nil
}

// ─── Release ──────────────────────────────────────────

// Release 释放 hold (用户取消 / 上游 5xx); 全额回填到 packages, 不写流水.
func (s *Service) Release(ctx context.Context, holdID uuid.UUID) (*Hold, *Balance, error) {
	var (
		hold *Hold
		bal  *Balance
	)
	err := s.txDo(ctx, func(tx pgx.Tx) error {
		now := s.now()

		h, err := lockHoldByID(ctx, tx, holdID)
		if err != nil {
			return err
		}
		if h == nil {
			return ErrHoldNotFound
		}
		if h.Status != HoldStatusHeld {
			return ErrHoldNotActive
		}

		if _, err := refundFromBreakdown(ctx, tx, h.UserID, h.HoldBreakdown, h.MaxAmount, now); err != nil {
			return err
		}

		bal, err = refreshBalance(ctx, tx, h.UserID, now)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE identity.credit_holds
			SET status = 'released'
			WHERE id = $1
		`, h.ID)
		if err != nil {
			return err
		}

		h.Status = HoldStatusReleased
		hold = h
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	// W3-3 publish release event (user_cancel)
	_ = s.pub.PublishRelease(ctx, events.ReleaseEvent{
		Common: events.Common{UserID: hold.UserID},
		HoldID: hold.ID,
		Amount: hold.MaxAmount,
		Reason: "user_cancel",
	})
	return hold, bal, nil
}

// ─── Reaper ───────────────────────────────────────────

// ReapExpired 由后台 goroutine 每 30s 调一次. 释放所有 expires_at<now 且 held 的
// hold, 标记 status='expired', 返回处理条数 (用于 Grafana).
func (s *Service) ReapExpired(ctx context.Context, batchLimit int) (int, error) {
	if batchLimit <= 0 {
		batchLimit = 200
	}
	now := s.now()

	rows, err := s.pool.Query(ctx, `
		SELECT id FROM identity.credit_holds
		WHERE status = 'held' AND expires_at < $1
		ORDER BY expires_at
		LIMIT $2
	`, now, batchLimit)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	processed := 0
	for _, id := range ids {
		var reaped *Hold
		err := s.txDo(ctx, func(tx pgx.Tx) error {
			h, err := lockHoldByID(ctx, tx, id)
			if err != nil {
				return err
			}
			// 并发竞争: 别的协程可能刚刚 settle/release 了 — 直接跳过.
			if h == nil || h.Status != HoldStatusHeld {
				return nil
			}
			if _, err := refundFromBreakdown(ctx, tx, h.UserID, h.HoldBreakdown, h.MaxAmount, now); err != nil {
				return err
			}
			if _, err := refreshBalance(ctx, tx, h.UserID, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE identity.credit_holds
				SET status = 'expired'
				WHERE id = $1
			`, h.ID); err != nil {
				return err
			}
			reaped = h
			return nil
		})
		if err != nil {
			return processed, err
		}
		if reaped != nil {
			// W3-3 publish release event (expired)
			_ = s.pub.PublishRelease(ctx, events.ReleaseEvent{
				Common: events.Common{UserID: reaped.UserID},
				HoldID: reaped.ID,
				Amount: reaped.MaxAmount,
				Reason: "expired",
			})
		}
		processed++
	}
	return processed, nil
}

// ─── Read paths ───────────────────────────────────────

// GetHold 直接读, 用于调试 / 客服查具体 hold.
func (s *Service) GetHold(ctx context.Context, id uuid.UUID) (*Hold, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, ref_type, ref_id, max_amount, actual_amount, status,
		       hold_breakdown_json, idempotency_key, expires_at, created_at, settled_at
		FROM identity.credit_holds
		WHERE id = $1
	`, id)
	h, err := scanHold(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrHoldNotFound
	}
	return h, err
}

// ─── Helpers (private) ────────────────────────────────

// reserveFromPackages 锁定用户的可用 packages 并按消费顺序扣减 amount.
// 与 Consume 内部逻辑一致, 但未来可重构成共享函数. 单独留一份避免改动 Consume
// 风险.
func reserveFromPackages(ctx context.Context, tx pgx.Tx, userID uuid.UUID, amount int64, now time.Time) ([]Breakdown, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, kind, remaining, expires_at
		FROM identity.credit_packages
		WHERE user_id = $1
		  AND remaining > 0
		  AND (expires_at IS NULL OR expires_at > $2)
		ORDER BY
		    CASE kind WHEN 'time_limited' THEN 0 ELSE 1 END,
		    expires_at NULLS LAST,
		    created_at
		FOR UPDATE
	`, userID, now)
	if err != nil {
		return nil, err
	}
	type pkgRow struct {
		id        uuid.UUID
		remaining int64
	}
	var pkgs []pkgRow
	for rows.Next() {
		var p pkgRow
		var kind string
		var expiresAt *time.Time
		if err := rows.Scan(&p.id, &kind, &p.remaining, &expiresAt); err != nil {
			rows.Close()
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var breakdown []Breakdown
	left := amount
	for _, p := range pkgs {
		if left <= 0 {
			break
		}
		take := p.remaining
		if take > left {
			take = left
		}
		if _, err := tx.Exec(ctx, `
			UPDATE identity.credit_packages
			SET remaining = remaining - $1
			WHERE id = $2
		`, take, p.id); err != nil {
			return nil, err
		}
		breakdown = append(breakdown, Breakdown{PackageID: p.id, Amount: take})
		left -= take
	}
	if left > 0 {
		return nil, ErrInsufficientCredits
	}
	return breakdown, nil
}

// refundFromBreakdown 反向遍历 breakdown, 把 amount 退还到对应 packages
// (或 quota, W4-3). 已过期的 package 跳过 (钱蒸发 — 与 Refund 同语义);
// 实际退还可能 < amount. 返回实际退还金额.
func refundFromBreakdown(ctx context.Context, tx pgx.Tx, userID uuid.UUID, breakdown []Breakdown, amount int64, now time.Time) (int64, error) {
	left := amount
	for i := len(breakdown) - 1; i >= 0 && left > 0; i-- {
		step := breakdown[i]
		take := step.Amount
		if take > left {
			take = left
		}
		// W4-3: quota 段反向退到 user_quota_usage.
		if step.IsQuota() {
			if err := RefundQuotaUsage(ctx, tx, userID, step.RefType, take, now); err != nil {
				return 0, err
			}
			left -= take
			continue
		}
		pkg, err := lockPackageByID(ctx, tx, step.PackageID)
		if err != nil {
			return 0, err
		}
		if pkg == nil {
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE identity.credit_packages
			SET remaining = remaining + $1
			WHERE id = $2
		`, take, pkg.ID); err != nil {
			return 0, err
		}
		left -= take
	}
	return amount - left, nil
}

// truncateBreakdown 截取 breakdown 前若干步, 累计正好 = actual. 用于 Settle 写
// credit_logs.consume_breakdown_json (与原扣"前缀"对齐).
func truncateBreakdown(breakdown []Breakdown, actual int64) []Breakdown {
	if actual <= 0 {
		return nil
	}
	var out []Breakdown
	left := actual
	for _, b := range breakdown {
		if left <= 0 {
			break
		}
		take := b.Amount
		if take > left {
			take = left
		}
		out = append(out, Breakdown{PackageID: b.PackageID, Amount: take})
		left -= take
	}
	return out
}

func findHoldByIdempotency(ctx context.Context, tx pgx.Tx, userID uuid.UUID, refType LogRefType, key string) (*Hold, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, ref_type, ref_id, max_amount, actual_amount, status,
		       hold_breakdown_json, idempotency_key, expires_at, created_at, settled_at
		FROM identity.credit_holds
		WHERE user_id = $1 AND ref_type = $2 AND idempotency_key = $3
		FOR UPDATE
	`, userID, string(refType), key)
	h, err := scanHold(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return h, err
}

func lockHoldByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Hold, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, ref_type, ref_id, max_amount, actual_amount, status,
		       hold_breakdown_json, idempotency_key, expires_at, created_at, settled_at
		FROM identity.credit_holds
		WHERE id = $1
		FOR UPDATE
	`, id)
	h, err := scanHold(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return h, err
}

func scanHold(row pgx.Row) (*Hold, error) {
	var h Hold
	var refType, status string
	var refID, idemKey *string
	var actual *int64
	var settledAt *time.Time
	var breakdownJSON []byte
	if err := row.Scan(
		&h.ID, &h.UserID, &refType, &refID, &h.MaxAmount, &actual, &status,
		&breakdownJSON, &idemKey, &h.ExpiresAt, &h.CreatedAt, &settledAt,
	); err != nil {
		return nil, err
	}
	h.RefType = LogRefType(refType)
	h.Status = HoldStatus(status)
	if refID != nil {
		h.RefID = *refID
	}
	if idemKey != nil {
		h.IdempotencyKey = *idemKey
	}
	h.ActualAmount = actual
	h.SettledAt = settledAt
	if len(breakdownJSON) > 0 {
		if err := json.Unmarshal(breakdownJSON, &h.HoldBreakdown); err != nil {
			return nil, fmt.Errorf("hold breakdown json: %w", err)
		}
	}
	return &h, nil
}

// txDo — 与 credits.go 的 tx 同语义但只返 error (Hold/Settle/Release 各自管返
// 出参, 用闭包变量逃逸出来). 重用 pool.BeginTx + Commit/Rollback 模式.
func (s *Service) txDo(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // commit 后是 no-op
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
