package adminapi

import (
	"log/slog"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"

	"github.com/biumind/biumind/services/model-relay/internal/health"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/router"
)

// Server is the admin handler bundle. Every dependency is held as a
// pointer + interface so tests can swap in fakes. No background work
// is started here — the supervisor / cache / store lifecycles belong
// to main.go.
type Server struct {
	Store        *registry.Store
	Vault        *registry.CredentialVault
	Cache        *registry.Cache
	Probe        *health.Probe
	Supervisor   *health.Supervisor
	Strategies   *router.Registry
	RoleCache    *bauth.RoleCache
	JWTVerifier  *bauth.Verifier
	Logger       *slog.Logger

	// SyncUpstreamURL is the base URL for the model metadata source.
	// Defaults to https://basellm.github.io/llm-metadata; override via
	// MODEL_RELAY_SYNC_UPSTREAM env when running against a private mirror.
	SyncUpstreamURL string

	// SyncHTTPClient is used by sync-upstream. nil = default 30s client.
	SyncHTTPClient *http.Client
}

// Mount registers every /v1/admin/* route on the given mux. Each route
// is gated by the matching RBAC permission (see
// services/identity/migrations/00016_model_relay_perms.sql for the seed
// of model_relay perms). Handlers assume claims are in ctx after the
// middleware fires.
//
// Permission map (read = "models:read", write = "models:write" unless
// noted):
//
//   /v1/admin/providers           list/get  → models:read
//                                 mutate    → models:write
//   /v1/admin/credentials         list/get  → model_credentials:read
//                                 mutate    → model_credentials:write
//                                 :test     → model_credentials:read (probe is read-only on cred row)
//   /v1/admin/models              list/get  → models:read
//                                 mutate    → models:write
//   /v1/admin/channels            list/get  → models:read
//                                 mutate    → models:write
//                                 :test     → models:read (same; just checks upstream)
//   /v1/admin/pricing/{model_id}  read      → models:read
//                                 set       → pricing:write
//   /v1/admin/fx-rates            read      → models:read
//                                 set       → fx_rates:write
//   /v1/admin/model-groups        read      → models:read
//                                 mutate    → models:write
//   /v1/admin/models/sync-upstream → models:write (writes to the catalogue)
func (s *Server) Mount(mux *http.ServeMux) {
	rc := s.RoleCache
	v := s.JWTVerifier

	// ─── providers ────────────────────────────────────────────
	mux.HandleFunc("GET /v1/admin/providers",
		rc.RequirePermission(v, "models:read")(s.handleListProviders))
	mux.HandleFunc("POST /v1/admin/providers",
		rc.RequirePermission(v, "models:write")(s.handleCreateProvider))
	mux.HandleFunc("GET /v1/admin/providers/{id}",
		rc.RequirePermission(v, "models:read")(s.handleGetProvider))
	mux.HandleFunc("PATCH /v1/admin/providers/{id}",
		rc.RequirePermission(v, "models:write")(s.handleUpdateProvider))
	mux.HandleFunc("DELETE /v1/admin/providers/{id}",
		rc.RequirePermission(v, "models:write")(s.handleDeleteProvider))

	// ─── credentials ──────────────────────────────────────────
	mux.HandleFunc("GET /v1/admin/credentials",
		rc.RequirePermission(v, "model_credentials:read")(s.handleListCredentials))
	mux.HandleFunc("POST /v1/admin/credentials",
		rc.RequirePermission(v, "model_credentials:write")(s.handleCreateCredential))
	mux.HandleFunc("GET /v1/admin/credentials/{id}",
		rc.RequirePermission(v, "model_credentials:read")(s.handleGetCredential))
	mux.HandleFunc("PATCH /v1/admin/credentials/{id}",
		rc.RequirePermission(v, "model_credentials:write")(s.handleUpdateCredential))
	mux.HandleFunc("DELETE /v1/admin/credentials/{id}",
		rc.RequirePermission(v, "model_credentials:write")(s.handleDeleteCredential))
	mux.HandleFunc("POST /v1/admin/credentials/{id}/test",
		rc.RequirePermission(v, "model_credentials:read")(s.handleTestCredential))

	// ─── models ───────────────────────────────────────────────
	mux.HandleFunc("GET /v1/admin/models",
		rc.RequirePermission(v, "models:read")(s.handleListModels))
	mux.HandleFunc("POST /v1/admin/models",
		rc.RequirePermission(v, "models:write")(s.handleCreateModel))
	mux.HandleFunc("GET /v1/admin/models/{id}",
		rc.RequirePermission(v, "models:read")(s.handleGetModel))
	mux.HandleFunc("PATCH /v1/admin/models/{id}",
		rc.RequirePermission(v, "models:write")(s.handleUpdateModel))
	mux.HandleFunc("DELETE /v1/admin/models/{id}",
		rc.RequirePermission(v, "models:write")(s.handleDeleteModel))
	mux.HandleFunc("POST /v1/admin/models/{id}/bind-groups",
		rc.RequirePermission(v, "models:write")(s.handleBindModelGroups))
	mux.HandleFunc("POST /v1/admin/models/sync-upstream",
		rc.RequirePermission(v, "models:write")(s.handleSyncUpstream))

	// ─── channels ─────────────────────────────────────────────
	mux.HandleFunc("GET /v1/admin/channels",
		rc.RequirePermission(v, "models:read")(s.handleListChannels))
	mux.HandleFunc("POST /v1/admin/channels",
		rc.RequirePermission(v, "models:write")(s.handleCreateChannel))
	mux.HandleFunc("GET /v1/admin/channels/{id}",
		rc.RequirePermission(v, "models:read")(s.handleGetChannel))
	mux.HandleFunc("PATCH /v1/admin/channels/{id}",
		rc.RequirePermission(v, "models:write")(s.handleUpdateChannel))
	mux.HandleFunc("DELETE /v1/admin/channels/{id}",
		rc.RequirePermission(v, "models:write")(s.handleDeleteChannel))
	mux.HandleFunc("POST /v1/admin/channels/{id}/test",
		rc.RequirePermission(v, "models:read")(s.handleTestChannel))

	// ─── pricing ──────────────────────────────────────────────
	mux.HandleFunc("GET /v1/admin/pricing/{model_id}",
		rc.RequirePermission(v, "models:read")(s.handleGetPricing))
	mux.HandleFunc("POST /v1/admin/pricing/{model_id}",
		rc.RequirePermission(v, "pricing:write")(s.handleSetPricing))
	mux.HandleFunc("GET /v1/admin/pricing/{model_id}/history",
		rc.RequirePermission(v, "models:read")(s.handleGetPricingHistory))

	// F2.1: pricing_rules 多维乘数 CRUD (parameter strategy 用)
	mux.HandleFunc("GET /v1/admin/models/{id}/pricing-rules",
		rc.RequirePermission(v, "models:read")(s.handleListPricingRules))
	mux.HandleFunc("POST /v1/admin/models/{id}/pricing-rules",
		rc.RequirePermission(v, "pricing:write")(s.handleAppendPricingRule))

	// ─── fx_rates ─────────────────────────────────────────────
	mux.HandleFunc("GET /v1/admin/fx-rates",
		rc.RequirePermission(v, "models:read")(s.handleListFxRates))
	mux.HandleFunc("PUT /v1/admin/fx-rates",
		rc.RequirePermission(v, "fx_rates:write")(s.handleSetFxRate))

	// ─── model_groups ─────────────────────────────────────────
	mux.HandleFunc("GET /v1/admin/model-groups",
		rc.RequirePermission(v, "models:read")(s.handleListGroups))
	mux.HandleFunc("POST /v1/admin/model-groups",
		rc.RequirePermission(v, "models:write")(s.handleCreateGroup))

	// P4 段 4 / F2.5: aigc_compat 已删. admin Vue 现在直接走
	// /v1/admin/models?mode=... + /v1/admin/models/{id}/pricing-rules.
}
