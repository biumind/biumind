// Package store 是 services/aigc 对 PG aigc.* schema 的仓储层.
//
// 风格参考 services/identity/internal/store —— pgx 直写 SQL, 不引 ORM,
// 字段映射手写 (改 schema 时 grep 一遍 columns 常量即可).
//
// 设计:
//
//	docs/BiuMind-AIGC-Migration-Plan.md §2.3
//	docs/BiuMind-AIGC-Storage-Design.md §6
//	services/aigc/migrations/00001_aigc_schema.sql
package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound 是所有"按 id 查找未命中"的统一返回. 调用方用 errors.Is 检查.
var ErrNotFound = errors.New("aigc/store: not found")

// Store 是仓储入口. 所有方法都接 ctx + 返回 (*T, error) 形式.
type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// Pool 暴露底层 pgxpool, 让需要事务的调用方 (如 orchestrator 跨表写)
// 自己 Begin 控制 boundary.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// scanner 抽象 pgx.Row / pgx.Rows 共用的 Scan 接口.
type scanner interface {
	Scan(...any) error
}

// rowOrErr 把 pgx.ErrNoRows 翻译成 ErrNotFound, 其他错误透传.
func rowOrErr[T any](v *T, err error) (*T, error) {
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return v, nil
}
