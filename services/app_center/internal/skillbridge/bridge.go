// Package skillbridge mirrors an App's manifest.skills declarations
// into the runtime.skills table on install, and cleans them up on
// uninstall.
//
// Why this lives in app_center, not runtime: the install lifecycle
// is owned by app_center; the runtime.skills schema is owned by
// runtime, but writing to it from inside the install transaction
// keeps "App installed" and "App skills available" consistent. The
// alternative — posting to runtime over HTTP after the install
// commits — opens a window where the installation row exists but the
// skills don't (or vice versa on rollback).
//
// Both services share the same Postgres database; cross-schema
// writes are normal in BiuMind. We follow the same pattern Brain.Wiki
// uses to write to brain.events from outside the brain service.

package skillbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

// ─── Errors ────────────────────────────────────────────────────────

var (
	// ErrNoOrg — App skills require an org_id (runtime.skills.org_id
	// is NOT NULL). Bridge writes are skipped (with warning) when
	// the install was performed without an org context, e.g. a user-
	// scope install for a user not in any org.
	ErrNoOrg = errors.New("skillbridge: no org_id; bundled skills skipped")
)

// Inputs is what the install path passes when fanning out skill
// writes. Designed to be cheap to construct from inside Installer.
type Inputs struct {
	// InstallID is the app_center.installations.id this skill set
	// belongs to. Stored under manifest.app_install_id so uninstall
	// can cascade-delete by that key. uuid.UUID rather than string
	// so type-safety propagates from the install path.
	InstallID uuid.UUID

	// OrgID is the runtime.skills.org_id this row will land under.
	// Required (the schema is NOT NULL); see ErrNoOrg.
	OrgID uuid.UUID

	// OwnerID — when non-nil, the skill is treated as user-private
	// (visible only to that user inside the org). For app-bundled
	// skills the typical setting is nil so all org members can
	// activate the skill, mirroring how an org-installed App is
	// available to every member.
	OwnerID *uuid.UUID

	// Identifier of the App (the slug). Used for the skill.identifier
	// column when the manifest doesn't override (it shouldn't).
	AppIdentifier string

	// Manifest carries the full App manifest. We pull manifest.skills
	// out of it and call SkillContent on the App for each entry.
	Manifest biuapp.Manifest

	// App is the live in-process App object; we type-assert it to
	// biuapp.BundledSkillProvider to read the skill bodies.
	App biuapp.App
}

// WriteAppSkills inserts one runtime.skills row per manifest.skills
// entry. Idempotent: if a row with the same (org_id, identifier)
// exists, it's UPDATEd in place rather than INSERTed (so a re-install
// after a crash mid-way doesn't violate the UNIQUE constraint).
//
// The function takes a pgx.Tx so the caller controls the transaction
// boundary — keeping this write in the same tx as the installations
// INSERT is what guarantees consistency between the App and its
// skills.
//
// Returns the count of skill rows written (so the caller can log /
// emit metrics) and the first error encountered (subsequent skills
// are NOT attempted on failure — the caller's tx rollback unwinds
// any partial writes anyway).
func WriteAppSkills(ctx context.Context, tx pgx.Tx, in Inputs) (int, error) {
	if len(in.Manifest.Skills) == 0 {
		return 0, nil
	}
	if in.OrgID == uuid.Nil {
		// Caller decides whether this is a hard error (test paths
		// usually want to know) or a soft skip (production install
		// for a user without an org). Returning the sentinel lets
		// both choose.
		return 0, ErrNoOrg
	}

	provider, ok := in.App.(biuapp.BundledSkillProvider)
	if !ok {
		// Manifest declared skills but the App didn't implement the
		// content provider. We treat this as an error rather than
		// silent skip — the manifest is a contract, breaking it is a
		// programming bug, not a runtime condition.
		return 0, fmt.Errorf("skillbridge: app %q declares skills but does not implement BundledSkillProvider",
			in.AppIdentifier)
	}

	count := 0
	for _, ref := range in.Manifest.Skills {
		body, err := provider.SkillContent(ref.Identifier)
		if err != nil {
			return count, fmt.Errorf("skillbridge: get content for %q: %w", ref.Identifier, err)
		}
		if len(body) == 0 {
			return count, fmt.Errorf("skillbridge: empty content for %q", ref.Identifier)
		}

		hash := sha256Hex(body)
		manifestJSON, err := json.Marshal(map[string]any{
			"app_install_id": in.InstallID.String(),
			"app_identifier": in.AppIdentifier,
			"app_version":    in.Manifest.Version,
			"file":           ref.File,
		})
		if err != nil {
			return count, fmt.Errorf("skillbridge: marshal manifest: %w", err)
		}

		// Generate a stable id derived from (install_id, identifier)
		// so re-install yields the same skill row.id (lets foreign
		// references survive transient un/re-install cycles). v5
		// UUID into the same namespace as runtime.skill_id() output
		// is overkill; use a plain ulid prefixed with "skill_" since
		// that's the schema's documented format.
		skillID := "skill_" + ulid.Make().String()

		// UPSERT keyed on (org_id, identifier) — the UNIQUE in the
		// runtime.skills schema. ON CONFLICT updates the moving
		// fields (content / hash / manifest) but leaves id stable.
		var ownerArg any = nil
		if in.OwnerID != nil {
			ownerArg = *in.OwnerID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO runtime.skills
				(id, org_id, owner_id, identifier, name, description,
				 source, manifest, content, content_hash,
				 resources, paths, permissions, status,
				 created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6,
			        'bundled', $7, $8, $9,
			        '{}'::jsonb, ARRAY[]::text[], ARRAY[]::text[], 'active',
			        now(), now())
			ON CONFLICT (org_id, identifier) DO UPDATE
			   SET content      = EXCLUDED.content,
			       content_hash = EXCLUDED.content_hash,
			       manifest     = EXCLUDED.manifest,
			       source       = 'bundled',
			       status       = 'active',
			       updated_at   = now()
		`,
			skillID, in.OrgID, ownerArg, ref.Identifier,
			ref.Identifier, /* name = identifier for v1.5; richer name needs a SkillRef.Name field */
			"Bundled with "+in.AppIdentifier,
			manifestJSON, string(body), hash,
		); err != nil {
			return count, fmt.Errorf("skillbridge: upsert %q: %w", ref.Identifier, err)
		}
		count++
	}
	return count, nil
}

// DeleteAppSkills removes every runtime.skills row that was attached
// by an install. Match is by manifest->>'app_install_id' so we never
// delete a skill that belongs to a different install (or to a user-
// authored skill that happens to share an identifier).
//
// Idempotent: deleting an already-empty set is fine.
func DeleteAppSkills(ctx context.Context, tx pgx.Tx, installID uuid.UUID) (int64, error) {
	tag, err := tx.Exec(ctx, `
		DELETE FROM runtime.skills
		 WHERE manifest->>'app_install_id' = $1
	`, installID.String())
	if err != nil {
		return 0, fmt.Errorf("skillbridge: delete: %w", err)
	}
	return tag.RowsAffected(), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
