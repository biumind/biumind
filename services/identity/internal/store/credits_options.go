package store

// credits_options.go — 充值套餐配置的只读查询.
//
// 写路径（创建/修改套餐）走 admin 后台, 跟用户态无关, 单独 admin store.

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RechargeOption 是 identity.credit_recharge_options 一行的视图.
// 字段名跟 proto biumind.credits.v1.RechargeOption 对齐.
type RechargeOption struct {
	ID             uuid.UUID `json:"id"`
	DisplayName    string    `json:"display_name"`
	CreditsAmount  int64     `json:"credits_amount"`
	Kind           string    `json:"kind"` // permanent | time_limited
	PriceMicroCNY  int64     `json:"price_micro_cny"`
	ValidDays      int       `json:"valid_days"`
	Enabled        bool      `json:"enabled"`
	SortOrder      int       `json:"sort_order"`
	CreatedAt      time.Time `json:"created_at"`
}

const rechargeOptionColumns = `id, display_name, credits_amount, kind,
	price_micro_cny, valid_days, enabled, sort_order, created_at`

func scanRechargeOption(row interface {
	Scan(...any) error
}) (*RechargeOption, error) {
	o := &RechargeOption{}
	err := row.Scan(
		&o.ID, &o.DisplayName, &o.CreditsAmount, &o.Kind,
		&o.PriceMicroCNY, &o.ValidDays, &o.Enabled, &o.SortOrder, &o.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return o, nil
}

// ListRechargeOptions 仅返回 enabled=true 的 (UI 用), 按 sort_order 升序.
func (s *Store) ListRechargeOptions(ctx context.Context) ([]*RechargeOption, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+rechargeOptionColumns+`
		FROM identity.credit_recharge_options
		WHERE enabled = true
		ORDER BY sort_order, created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RechargeOption
	for rows.Next() {
		o, err := scanRechargeOption(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetRechargeOption 不过滤 enabled — 调用方自己判断 (Recharge 端点会拒绝 disabled).
func (s *Store) GetRechargeOption(ctx context.Context, id uuid.UUID) (*RechargeOption, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+rechargeOptionColumns+`
		FROM identity.credit_recharge_options
		WHERE id = $1
	`, id)
	return scanRechargeOption(row)
}
