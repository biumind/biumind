// ModelGroupRepo handles model_groups + model_group_bindings +
// user_group_memberships in one place. The latter two are pure
// junction tables that are only useful in the context of the parent
// model_groups table, so collapsing them into one repo cuts noise.
//
// Critical invariant for MVP: every model is bound to the system
// 'default' group (DefaultGroupID = 00000000-0000-0000-0000-000000000001
// from 00003_seed.sql). The application-layer "create model" path
// inserts both the model row and the binding atomically. The repo
// itself enforces nothing — just exposes the helpers.

package registry

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelGroupRepo struct {
	pool *pgxpool.Pool
}

// ─── ModelGroup CRUD ──────────────────────────────────────────────

func (r *ModelGroupRepo) List(ctx context.Context) ([]ModelGroup, error) {
	const q = `
		SELECT id, code, name, owner_type, owner_id, description, status, created_at, updated_at
		FROM model_relay.model_groups
		WHERE status = 'active'
		ORDER BY owner_type ASC, code ASC
	`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, translateErr("groups.list", err)
	}
	defer rows.Close()

	out := make([]ModelGroup, 0, 4)
	for rows.Next() {
		var g ModelGroup
		if err := rows.Scan(
			&g.ID, &g.Code, &g.Name, &g.OwnerType, &g.OwnerID,
			&g.Description, &g.Status, &g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, translateErr("groups.list.scan", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *ModelGroupRepo) Get(ctx context.Context, id uuid.UUID) (*ModelGroup, error) {
	const q = `
		SELECT id, code, name, owner_type, owner_id, description, status, created_at, updated_at
		FROM model_relay.model_groups WHERE id = $1
	`
	var g ModelGroup
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&g.ID, &g.Code, &g.Name, &g.OwnerType, &g.OwnerID,
		&g.Description, &g.Status, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("groups.get", err)
	}
	return &g, nil
}

func (r *ModelGroupRepo) GetByCode(ctx context.Context, code string) (*ModelGroup, error) {
	const q = `
		SELECT id, code, name, owner_type, owner_id, description, status, created_at, updated_at
		FROM model_relay.model_groups WHERE code = $1
	`
	var g ModelGroup
	err := r.pool.QueryRow(ctx, q, code).Scan(
		&g.ID, &g.Code, &g.Name, &g.OwnerType, &g.OwnerID,
		&g.Description, &g.Status, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("groups.get_by_code", err)
	}
	return &g, nil
}

type ModelGroupInput struct {
	Code        string
	Name        string
	OwnerType   GroupOwnerType
	OwnerID     string // empty for system; uuid string for org/user
	Description string
}

func (in ModelGroupInput) validate() error {
	if in.Code == "" {
		return fmt.Errorf("groups: code required")
	}
	if in.Name == "" {
		return fmt.Errorf("groups: name required")
	}
	switch in.OwnerType {
	case OwnerSystem, OwnerOrg, OwnerUser:
	default:
		return fmt.Errorf("groups: invalid owner_type %q", in.OwnerType)
	}
	if in.OwnerType != OwnerSystem && in.OwnerID == "" {
		return fmt.Errorf("groups: owner_id required when owner_type=%q", in.OwnerType)
	}
	return nil
}

func (r *ModelGroupRepo) Insert(ctx context.Context, in ModelGroupInput) (*ModelGroup, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO model_relay.model_groups
			(code, name, owner_type, owner_id, description)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, code, name, owner_type, owner_id, description, status, created_at, updated_at
	`
	var g ModelGroup
	err := r.pool.QueryRow(ctx, q,
		in.Code, in.Name, in.OwnerType, in.OwnerID, in.Description,
	).Scan(
		&g.ID, &g.Code, &g.Name, &g.OwnerType, &g.OwnerID,
		&g.Description, &g.Status, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, translateErr("groups.insert", err)
	}
	return &g, nil
}

// Archive flips a group to status='archived'. Soft delete — bindings
// and memberships are kept but the resolver skips archived groups.
// The 'default' group is special-cased at the application layer:
// admin UI must reject archive attempts on it.
func (r *ModelGroupRepo) Archive(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE model_relay.model_groups SET status = 'archived', updated_at = now() WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return translateErr("groups.archive", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("groups.archive: %w", ErrNotFound)
	}
	return nil
}

// ─── Bindings (group ↔ model) ─────────────────────────────────────

// SetModelBindings is the "replace" operation: removes any binding for
// `modelID` not in `groupIDs`, adds any missing. The whole replacement
// runs in one tx so admin "save model" never sees half-state.
func (r *ModelGroupRepo) SetModelBindings(ctx context.Context, modelID uuid.UUID, groupIDs []uuid.UUID) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return translateErr("groups.set_model_bindings.begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`DELETE FROM model_relay.model_group_bindings WHERE model_id = $1 AND NOT (group_id = ANY($2::uuid[]))`,
		modelID, groupIDs,
	); err != nil {
		return translateErr("groups.set_model_bindings.delete", err)
	}

	for _, gid := range groupIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO model_relay.model_group_bindings (group_id, model_id)
			VALUES ($1, $2)
			ON CONFLICT (group_id, model_id) DO NOTHING
		`, gid, modelID); err != nil {
			return translateErr("groups.set_model_bindings.insert", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return translateErr("groups.set_model_bindings.commit", err)
	}
	return nil
}

// BindModelTx adds a single binding inside a caller-owned tx. Used by
// the model-create path so the model row + default-group binding land
// atomically.
func (r *ModelGroupRepo) BindModelTx(ctx context.Context, tx pgx.Tx, groupID, modelID uuid.UUID) error {
	const q = `
		INSERT INTO model_relay.model_group_bindings (group_id, model_id)
		VALUES ($1, $2)
		ON CONFLICT (group_id, model_id) DO NOTHING
	`
	if _, err := tx.Exec(ctx, q, groupID, modelID); err != nil {
		return translateErr("groups.bind_model_tx", err)
	}
	return nil
}

// ListGroupsForModel powers the "this model is in groups [...]" view.
func (r *ModelGroupRepo) ListGroupsForModel(ctx context.Context, modelID uuid.UUID) ([]ModelGroup, error) {
	const q = `
		SELECT g.id, g.code, g.name, g.owner_type, g.owner_id,
		       g.description, g.status, g.created_at, g.updated_at
		FROM model_relay.model_groups g
		JOIN model_relay.model_group_bindings b ON b.group_id = g.id
		WHERE b.model_id = $1 AND g.status = 'active'
		ORDER BY g.owner_type ASC, g.code ASC
	`
	rows, err := r.pool.Query(ctx, q, modelID)
	if err != nil {
		return nil, translateErr("groups.list_groups_for_model", err)
	}
	defer rows.Close()

	out := make([]ModelGroup, 0, 4)
	for rows.Next() {
		var g ModelGroup
		if err := rows.Scan(
			&g.ID, &g.Code, &g.Name, &g.OwnerType, &g.OwnerID,
			&g.Description, &g.Status, &g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, translateErr("groups.list_groups_for_model.scan", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ─── User memberships (reserved, MVP unused at write side) ────────

// ListGroupsForUser returns the active group ids the user can resolve
// against. MVP semantic: every user implicitly belongs to DefaultGroupID
// (the 'default' system group). If the user has no explicit memberships,
// we return [DefaultGroupID]; otherwise we return the explicit set
// PLUS the default (admin can opt out a user from default by writing a
// row with group_id=00000…0001 INTO archived state — Phase 3 problem).
//
// Returning a slice (not a Set) because pgx ANY($1::uuid[]) accepts
// slices directly; the caller hands this to ModelRepo.ListVisibleTo.
func (r *ModelGroupRepo) ListGroupsForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		SELECT group_id FROM model_relay.user_group_memberships
		WHERE user_id = $1
	`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, translateErr("groups.list_groups_for_user", err)
	}
	defer rows.Close()

	out := []uuid.UUID{}
	for rows.Next() {
		var gid uuid.UUID
		if err := rows.Scan(&gid); err != nil {
			return nil, translateErr("groups.list_groups_for_user.scan", err)
		}
		out = append(out, gid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// MVP short-circuit: every user is implicitly in 'default'.
	hasDefault := false
	for _, g := range out {
		if g == DefaultGroupID {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		out = append(out, DefaultGroupID)
	}
	return out, nil
}
