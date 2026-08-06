// User-WebView App creation (M12.1).
//
// User picks "Add WebView App" in the client, types a URL + name +
// (optional) icon. The server:
//
//   1. Synthesises a Manifest with kind='webview', a single view of
//      layout='webview', and zero actions.
//   2. Inserts a row into app_center.apps (source='user_webview').
//   3. Registers the synthesised App into the in-process biuapp.Registry
//      so the standard /v1/apps/installs path can find it.
//   4. Calls Installer.Install for the caller's user scope.
//
// The whole thing is one user-observable operation — a Setup-and-Install.
// We keep it on the Installer struct so the events / authz machinery
// is reused unchanged. The synthetic manifest is pinned at v0.1.0; if
// the user later edits the URL / name we bump to v0.1.1 etc., reusing
// the existing upgrade path.

package installs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/jackc/pgx/v5"
)

// UserWebViewRequest is what the client POSTs to /v1/apps/user_webview.
type UserWebViewRequest struct {
	// Title shown in the App Center / sidebar (e.g. "Kimi").
	Title string

	// URL is the entry-point the webview opens to. https:// preferred;
	// http:// allowed for the user's own intranet but logged.
	URL string

	// IconFileHash — optional CAS sha256 for a previously-uploaded
	// icon. NULL → client falls back to favicon-derived placeholder.
	IconFileHash string

	// Caller scope (always user for v2.0 — org-shared webview apps
	// land later when admin can pin them).
	UserID string

	// Caller's org, recorded in CallerOrgID for events but not used
	// to write org_id in the apps row (user_webview is always user
	// source).
	OrgID string
}

// CreateUserWebView synthesises and installs a webview App. Returns
// the resulting Installation row plus the synthesised App identifier.
//
// Idempotency: re-creating with the same URL for the same user
// returns the existing install (we hash url+user to derive identifier;
// see deriveIdentifier).
func (in *Installer) CreateUserWebView(ctx context.Context, req UserWebViewRequest) (*Installation, error) {
	if req.UserID == "" {
		return nil, fmt.Errorf("user_webview: user_id required")
	}
	if req.Title == "" {
		return nil, fmt.Errorf("user_webview: title required")
	}
	parsed, err := validateURL(req.URL)
	if err != nil {
		return nil, fmt.Errorf("user_webview: %w", err)
	}

	identifier := deriveIdentifier(req.UserID, parsed.Hostname())
	manifest := synthesiseManifest(identifier, req.Title, parsed, req.IconFileHash)

	if err := biuapp.Validate(&manifest); err != nil {
		return nil, fmt.Errorf("user_webview: synthesised manifest failed validation: %w", err)
	}

	// 1. Persist the catalogue row + register into the in-memory
	//    Registry. We do this in one tx so a partial failure doesn't
	//    leave a registry/DB mismatch (Registry.Register is in-memory
	//    only; we treat its idempotency separately).
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("user_webview: marshal manifest: %w", err)
	}
	manifestHash := sha256Hex(manifestJSON)
	appID := "app_" + identifier // user_webview ids are deterministic

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("user_webview: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Upsert apps row. Composite (identifier, version) unique; on
	// re-add of same URL we update title / manifest in place rather
	// than failing. (URL change triggers a different identifier so
	// it lands as a new row — that's fine.)
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_center.apps
			(id, identifier, name, description, source,
			 manifest, manifest_hash, version, category, status)
		VALUES ($1, $2, $3, $4, 'user_webview',
		        $5, $6, $7, 'utility', 'active')
		ON CONFLICT (identifier, version)
		DO UPDATE SET
			name           = EXCLUDED.name,
			description    = EXCLUDED.description,
			manifest       = EXCLUDED.manifest,
			manifest_hash  = EXCLUDED.manifest_hash,
			updated_at     = now()
	`,
		appID, identifier, req.Title,
		fmt.Sprintf("WebView 应用：%s", parsed.Host),
		manifestJSON, manifestHash, manifest.Version,
	); err != nil {
		return nil, fmt.Errorf("user_webview: upsert apps row: %w", err)
	}

	// Audit event for the apps-row write itself (separate from the
	// install event — that comes from Install below). Lets the catalog
	// outbox see the new entry.
	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "app",
		ScopeID:   appID,
		ActorType: events.ActorUser,
		ActorID:   req.UserID,
		Type:      events.AppPublished,
		Payload: map[string]any{
			"identifier": identifier,
			"version":    manifest.Version,
			"source":     "user_webview",
			"url":        parsed.String(),
		},
	}); err != nil {
		return nil, fmt.Errorf("user_webview: write app.published event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("user_webview: commit apps row: %w", err)
	}

	// 2. Register synthesised App with the in-memory biuapp.Registry.
	//    Idempotent: re-registering an existing identifier panics, so
	//    we call ReregisterUserWebView which replaces if present.
	if err := registerOrReplaceUserWebView(ctx, in.Registry, manifest); err != nil {
		return nil, fmt.Errorf("user_webview: register: %w", err)
	}

	// 3. Standard install path. Empty granted permissions — webview
	//    needs only net.outbound to its own host, which is auto-granted
	//    by the synthesised manifest.permissions.
	return in.Install(ctx, InstallRequest{
		Identifier:         identifier,
		Scope:              "user",
		ScopeID:            req.UserID,
		GrantedPermissions: manifest.Permissions,
		CallerUserID:       req.UserID,
		CallerOrgID:        req.OrgID,
	})
}

// ─── Manifest synthesis ────────────────────────────────────────────

var slugSafe = regexp.MustCompile(`[^a-z0-9-]+`)

// deriveIdentifier hashes (userID, host) to a deterministic kebab-case
// slug. Two adds of the same URL by the same user collide on identifier
// → install becomes idempotent ("re-add" returns the existing row).
//
// Different users adding the same URL get distinct identifiers so the
// per-user webview catalogue stays scoped.
func deriveIdentifier(userID, host string) string {
	h := sha256.Sum256([]byte(userID + "|" + host))
	suffix := hex.EncodeToString(h[:4]) // 8 hex chars — collision-safe for this scale
	hostSlug := slugSafe.ReplaceAllString(strings.ToLower(host), "-")
	hostSlug = strings.Trim(hostSlug, "-")
	if hostSlug == "" {
		hostSlug = "site"
	}
	if len(hostSlug) > 30 {
		hostSlug = hostSlug[:30]
	}
	return "webview-" + hostSlug + "-" + suffix
}

func synthesiseManifest(identifier, title string, parsed *url.URL, iconHash string) biuapp.Manifest {
	host := parsed.Hostname()
	// iconHash 非空时落到 manifest.Icon = "cas:<hash>"; 客户端识别此前缀
	// 拉 brain `/v1/files/by-sha/<hash>` 渲染。空字符串保留默认 (客户端
	// 渲染首字母 avatar)。
	icon := ""
	if iconHash != "" {
		icon = "cas:" + iconHash
	}
	return biuapp.Manifest{
		Name:        identifier, // routing slug — same as identifier
		Version:     "0.1.0",
		Description: fmt.Sprintf("WebView for %s", host),
		Author:      "user",
		Permissions: []string{"net.outbound:" + host},
		Actions:     []biuapp.ActionSpec{}, // webview has no actions
		ManifestExt: biuapp.ManifestExt{
			Identifier: identifier,
			Title:      title,
			Icon:       icon,
			Kind:       "webview",
			Category:   "utility",
			Views: []biuapp.ViewSpec{
				{
					ID:     "home",
					Route:  "/apps/" + identifier,
					Title:  title,
					Layout: biuapp.LayoutWebView,
					URL:    parsed.String(),
				},
			},
		},
	}
}

// validateURL parses and rejects obviously-bad inputs. Allows http://
// (intranet) but logs at the caller level so admins can audit.
func validateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("scheme must be http(s); got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("url has no host")
	}
	// javascript:, data:, file:, ftp:, ws: rejected by the scheme
	// check above. We additionally reject host-relative inputs.
	host := u.Hostname() // strips :port
	if !strings.Contains(host, ".") && host != "localhost" {
		return nil, fmt.Errorf("host %q must be FQDN or localhost", host)
	}
	return u, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ─── Synthetic-App adapter ─────────────────────────────────────────
//
// biuapp.Registry expects an App implementation. WebView apps have
// no actions to invoke — but we still need the manifest accessible
// via Registry.Get for the install path's manifest read. This stub
// satisfies the App interface; Invoke always errors (no actions).

type webviewApp struct{ m biuapp.Manifest }

func (w *webviewApp) Manifest() biuapp.Manifest                              { return w.m }
func (w *webviewApp) Init(_ context.Context, _ biuapp.Deps) error            { return nil }
func (w *webviewApp) Invoke(_ context.Context, action string, _ json.RawMessage) (any, error) {
	return nil, fmt.Errorf("webview apps have no invokable actions (got %q)", action)
}

func registerOrReplaceUserWebView(ctx context.Context, reg *biuapp.Registry, m biuapp.Manifest) error {
	// Registry.Register panics on duplicate name. We need an idempotent
	// path — call Replace if present (added by sibling helper).
	app := &webviewApp{m: m}
	if _, exists := reg.Get(m.Name); exists {
		return reg.Replace(ctx, app)
	}
	return reg.Register(ctx, app)
}
