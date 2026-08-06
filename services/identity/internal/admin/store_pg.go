// store_pg — adapter from store.Store (the data layer) to admin.Store
// (this package's interface). admin keeps its own thin Store interface
// so unit tests can inject fakes without spinning up Postgres; the real
// runtime wires the adapter through main.go.

package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/biumind/biumind/services/identity/internal/store"
)

// pgStore implements admin.Store on top of *store.Store.
type pgStore struct {
	inner *store.Store
}

// NewPGStore returns an admin.Store backed by Postgres.
func NewPGStore(inner *store.Store) Store {
	return &pgStore{inner: inner}
}

func (p *pgStore) ListUsers(query string, limit, offset int) ([]User, int, error) {
	users, total, err := p.inner.ListUsersForAdmin(context.Background(), query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]User, 0, len(users))
	for _, u := range users {
		out = append(out, toAdminUser(u))
	}
	return out, total, nil
}

func (p *pgStore) GetUser(id string) (*User, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	u, err := p.inner.GetUserByID(context.Background(), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	out := toAdminUser(u)
	return &out, nil
}

func (p *pgStore) SetUserPlan(id string, plan billing.Plan, actor string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	if err := p.inner.SetUserPlan(context.Background(), uid, string(plan)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("user not found")
		}
		return err
	}
	// actor 已经写在 audit ring buffer (admin.handleSetPlan), 这里不重复.
	return nil
}

func (p *pgStore) SetUserRole(id, role, actor, reason string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	var actorUID uuid.UUID
	if actor != "" {
		if a, err := uuid.Parse(actor); err == nil {
			actorUID = a
		}
	}
	if err := p.inner.SetUserRole(context.Background(), uid, role, actorUID, reason); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("user not found")
		}
		return err
	}
	return nil
}

func (p *pgStore) RevokeAllSessions(id string) (int64, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return 0, fmt.Errorf("invalid user id: %w", err)
	}
	return p.inner.RevokeAllRefreshTokens(context.Background(), uid)
}

func (p *pgStore) CountUsersByRole(role string) (int, error) {
	return p.inner.CountUsersByRole(context.Background(), role)
}

func toAdminUser(u *store.User) User {
	return User{
		ID:        u.ID.String(),
		Email:     u.Email,
		Plan:      u.Plan,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
	}
}
