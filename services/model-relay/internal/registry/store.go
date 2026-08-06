// Store is the entry point to the model_relay data layer. Each Repo
// receives the same *pgxpool.Pool; the pool's connection management
// keeps them safe to share across goroutines.
//
// Composition over inheritance: NewStore returns a struct whose fields
// are the typed repos. Callers say store.Models.Get(...) instead of
// store.GetModel(...). This keeps each repo file small and testable on
// its own.
//
// Errors: lower-level pgx errors are wrapped with fmt.Errorf and a
// short prefix identifying the call site ("models.get: ..."). Sentinel
// errors (ErrNotFound, ErrConflict) are exposed as package-level vars.

package registry

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors. Callers MUST use errors.Is for matching — the
// underlying error includes context that's useful in logs.
var (
	ErrNotFound = errors.New("registry: not found")
	ErrConflict = errors.New("registry: conflict")
)

// Store holds repository handles for every table in the model_relay
// schema. Construct with NewStore; callers access typed sub-repos
// via the named fields.
type Store struct {
	Pool *pgxpool.Pool

	Providers   *ProviderRepo
	Credentials *CredentialRepo
	Models      *ModelRepo
	Channels      *ChannelRepo
	Pricing       *PricingRepo
	PricingRules  *PricingRulesRepo
	FxRates       *FxRateRepo
	Groups        *ModelGroupRepo
	UsageLog      *UsageLogRepo
}

// NewStore wires every repo against the same pool. Returns a fully
// usable Store; no further init required.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:        pool,
		Providers:   &ProviderRepo{pool: pool},
		Credentials: &CredentialRepo{pool: pool},
		Models:      &ModelRepo{pool: pool},
		Channels:     &ChannelRepo{pool: pool},
		Pricing:      &PricingRepo{pool: pool},
		PricingRules: &PricingRulesRepo{pool: pool},
		FxRates:      &FxRateRepo{pool: pool},
		Groups:       &ModelGroupRepo{pool: pool},
		UsageLog:     &UsageLogRepo{pool: pool},
	}
}

// translateErr maps common pgx errors to the package's sentinels.
// Repo callers use this in their tail to keep error handling consistent.
func translateErr(callsite string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", callsite, ErrNotFound)
	}
	// 23505 = unique_violation; 23503 = foreign_key_violation. The pgx
	// type assertion approach lives in /packages/go-sdk/biu/dbmigrate;
	// here we keep it dependency-free and rely on string matching as a
	// fallback when pgx.PgError isn't wrapped.
	msg := err.Error()
	if contains(msg, "23505") || contains(msg, "duplicate key") {
		return fmt.Errorf("%s: %w: %v", callsite, ErrConflict, err)
	}
	return fmt.Errorf("%s: %w", callsite, err)
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
