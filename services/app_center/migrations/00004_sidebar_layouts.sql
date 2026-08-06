-- +goose Up
-- +goose StatementBegin

-- ─── Sidebar layouts (M1.4) ────────────────────────────────────────
--
-- Per-user, per-scope (desktop|mobile) sidebar layout. Decision §10A.6:
-- desktop and mobile layouts are kept SEPARATE — different form factors
-- have different slot counts and ordering habits, sharing one layout
-- has been more confusing than helpful in user testing of similar
-- products.
--
-- items is a typed-via-jsonb array:
--   [
--     { "kind": "system", "ref": "chat",            "hidden": false },
--     { "kind": "system", "ref": "wiki",            "hidden": false },
--     { "kind": "app",    "ref": "<install_id>",    "badge": true },
--     ...
--   ]
--
-- Validation rules (enforced in app code, not DB):
--   * kind ∈ {"system", "app"}
--   * for kind="app": ref must point to a valid installations.id owned
--     by this user/org; uninstalled installs are silently filtered on
--     read (see installations ON DELETE pruning trigger below)
--   * for kind="system": ref must be in the platform whitelist (see
--     M8.4 sidebar/system_refs.go)
--
-- The version column is the lock counter for optimistic concurrency:
-- PUT /v1/sidebar/layout requires expected_version == current; mismatch
-- → 409 Conflict and client refetches.

CREATE SCHEMA IF NOT EXISTS app_center;

CREATE TABLE IF NOT EXISTS app_center.sidebar_layouts (
    user_id           uuid NOT NULL,                          -- soft ref to identity.users.id
    scope             text NOT NULL CHECK (scope IN ('desktop', 'mobile')),

    items             jsonb NOT NULL DEFAULT '[]'::jsonb,

    -- Optimistic lock counter. PUT increments by 1; concurrent edits
    -- from two devices race here, the loser gets 409 and refetches.
    version           int NOT NULL DEFAULT 1 CHECK (version >= 1),

    -- Diagnostic: which device wrote this most recently. Lets the
    -- 409 UI tell the user "your other device just edited this".
    -- Free-form text — clients pass their device_name; not validated.
    updated_by_device text NOT NULL DEFAULT '',

    updated_at        timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, scope)
);

-- No additional indexes needed — the PK covers the only query pattern
-- (GET layout for current user × scope). If we later add admin
-- "who has app X pinned" queries, an expression index on items would
-- be considered.

-- ─── Cascading cleanup: uninstall → prune sidebar items ───────────
--
-- When an installation is deleted, its install_id may still be
-- referenced from app_center.sidebar_layouts.items. Rather than read-
-- time filtering (always paying the cost), we prune at uninstall time
-- via a trigger. items with kind='app' and ref matching the deleted
-- install_id are removed; the version is bumped so any concurrent
-- editor's PUT will 409 and pick up the fresh layout.
--
-- Why a trigger and not app code:
--   * Keeps invariant local to the schema (works regardless of who
--     deletes the installation row — admin script, GC job, whatever).
--   * Single SQL — no race window between DELETE and prune.

CREATE OR REPLACE FUNCTION app_center.prune_sidebar_on_uninstall() RETURNS trigger AS $$
BEGIN
    UPDATE app_center.sidebar_layouts
       SET items      = COALESCE(
                          (SELECT jsonb_agg(item)
                             FROM jsonb_array_elements(items) AS item
                            WHERE NOT (item->>'kind' = 'app'
                                       AND item->>'ref' = OLD.id::text)),
                          '[]'::jsonb
                        ),
           version    = version + 1,
           updated_at = now()
     WHERE items @> jsonb_build_array(jsonb_build_object('kind', 'app',
                                                          'ref',  OLD.id::text));
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sidebar_prune_on_uninstall ON app_center.installations;
CREATE TRIGGER sidebar_prune_on_uninstall
    AFTER DELETE ON app_center.installations
    FOR EACH ROW EXECUTE FUNCTION app_center.prune_sidebar_on_uninstall();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS sidebar_prune_on_uninstall ON app_center.installations;
DROP FUNCTION IF EXISTS app_center.prune_sidebar_on_uninstall();
DROP TABLE IF EXISTS app_center.sidebar_layouts;

-- +goose StatementEnd
