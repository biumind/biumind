-- +goose Up
-- +goose StatementBegin

-- ─── identity.api_tokens ────────────────────────────────────────
-- Long-lived programmatic-access tokens (PAT) — see docs/P2-I-1.
--
-- The token format is `bm_<8-char-prefix>_<jwt>`:
--   * `bm_<prefix>_` is opaque framing for visual identification
--     in the UI (we show "bm_a1b2c3d4_…" in the listing).
--   * `<jwt>` is a real RS256/HS256 JWT signed by identity, with
--     `kind: "pat"` claim and a long expiry (default 1y). Every
--     downstream service already accepts JWTs in Authorization
--     headers — PAT works out of the box without code changes
--     on brain / hub / runtime / etc.
--
-- The row stores metadata + the JWT's `jti` so list/revoke target
-- the right token without ever needing the secret. Revocation
-- enforcement (denylist by jti at verification time) is wired up
-- in P2-I-1c — for v1 the row's revoked_at is informational only.
-- Operators with a strict revocation requirement run shorter TTLs
-- and rotate.
--
-- workspace_id / project_id are scopes the token authorizes — the
-- token itself can only act inside those bounds. NULL = whole-user
-- (any project the user owns).

CREATE TABLE IF NOT EXISTS identity.api_tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      uuid NOT NULL,
    workspace_id  uuid,
    project_id    uuid,
    name          text NOT NULL,
    prefix        text NOT NULL UNIQUE,
    -- jti claim of the minted JWT; UNIQUE so a single token can't
    -- be split across two rows on re-issue.
    jti           text NOT NULL UNIQUE,
    -- scopes are free-form strings for now; brain checks ones it
    -- recognises and ignores the rest. v1 known: "read", "write".
    scopes        text[] NOT NULL DEFAULT ARRAY[]::text[],
    last_used_at  timestamptz,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS api_tokens_owner_idx
    ON identity.api_tokens(owner_id, created_at DESC);

CREATE INDEX IF NOT EXISTS api_tokens_jti_idx
    ON identity.api_tokens(jti);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS identity.api_tokens;
-- +goose StatementEnd
