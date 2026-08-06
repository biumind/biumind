// Upgrade lifecycle (M15).
//
// Two-step model — read-only check, then transactional commit:
//
//   1. CheckUpgradable(installID) — compute the diff between the row
//      and the registry's current manifest. Pure read; the client
//      uses this to render the "升级中 / 权限变更" Modal.
//
//   2. Upgrade(req) — performs the version bump in a tx. Two paths:
//        a) auto-eligible (no new permissions + not pinned)
//           → applies immediately
//        b) requires_approval (new permissions OR caller passed
//           AcceptedNewPermissions)
//           → caller must echo back AcceptedNewPermissions; server
//             refuses if not all `added` perms are echoed
//
// In both paths we run OnUpgrade after commit; OnUpgrade error does
// NOT roll back the version (matches Install — hooks are
// best-effort lifecycle notifications, the row is the truth). The
// caller surfaces the hook error to the UI as a non-fatal warning.

package installs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/biumind/biumind/services/app_center/internal/permissions"
	"github.com/jackc/pgx/v5"
)

// ─── Errors ────────────────────────────────────────────────────────

var (
	ErrAlreadyLatest          = errors.New("upgrade: already on latest version")
	ErrPinnedVersion          = errors.New("upgrade: installation is pinned to its current version")
	ErrApprovalRequired       = errors.New("upgrade: new permissions require approval")
	ErrPermissionsNotAccepted = errors.New("upgrade: granted_permissions must include every newly-added permission")
)

// ─── Domain ────────────────────────────────────────────────────────

// PermsDiff is the symmetric difference between the install's current
// granted_permissions and the catalogue manifest's required permissions.
//
// Added — in target manifest, not in current install. These are the
//
//	ones that *require user approval* before auto-rolling.
//
// Removed — was granted, no longer requested by the new manifest.
//
//	Auto-prune on upgrade; user doesn't need to confirm.
//
// Unchanged — granted on both sides; surfaced to the UI for context
//
//	but not a decision point.
type PermsDiff struct {
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Unchanged []string `json:"unchanged"`
}

// IsBreaking returns true when there's at least one new permission —
// the install path will refuse to auto-roll without explicit consent.
func (d PermsDiff) IsBreaking() bool { return len(d.Added) > 0 }

// UpgradeStatus is what CheckUpgradable returns.
type UpgradeStatus struct {
	Available        bool      `json:"available"`
	CurrentVersion   string    `json:"current_version"`
	TargetVersion    string    `json:"target_version,omitempty"`
	RequiresApproval bool      `json:"requires_approval"`
	Pinned           bool      `json:"pinned"`
	PermsDiff        PermsDiff `json:"perms_diff"`
}

// ─── CheckUpgradable ──────────────────────────────────────────────

// CheckUpgradable returns whether the installation has a newer
// version in the registry, plus the perm diff so the client can
// render a Modal pre-emptively.
//
// We don't gate on Authz here — the caller already authenticated
// via the outer HTTP handler (and the row scope_id is the caller).
// The actual upgrade still goes through Authz inside Upgrade().
func (in *Installer) CheckUpgradable(ctx context.Context, installID string) (*UpgradeStatus, error) {
	row, err := in.Get(ctx, installID)
	if err != nil {
		return nil, err
	}
	app, ok := in.Registry.Get(row.Identifier)
	if !ok {
		// App de-registered (e.g. an org-private app whose binary
		// isn't shipped in this build). Surface as "not upgradable"
		// rather than 500.
		return &UpgradeStatus{
			Available:      false,
			CurrentVersion: row.Version,
			Pinned:         row.PinnedVersion != "",
		}, nil
	}
	manifest := app.Manifest()
	target := manifest.Version

	if !versionGreater(target, row.Version) {
		return &UpgradeStatus{
			Available:      false,
			CurrentVersion: row.Version,
			TargetVersion:  target,
			Pinned:         row.PinnedVersion != "",
		}, nil
	}

	diff := DiffPermissions(row.PermissionsGranted, manifest.Permissions)

	return &UpgradeStatus{
		Available:        true,
		CurrentVersion:   row.Version,
		TargetVersion:    target,
		RequiresApproval: diff.IsBreaking() || row.PinnedVersion != "",
		Pinned:           row.PinnedVersion != "",
		PermsDiff:        diff,
	}, nil
}

// ─── DiffPermissions ──────────────────────────────────────────────

// DiffPermissions computes Added/Removed/Unchanged. Compares the
// canonical string form (`scope:params`) — net.outbound:*.a.com is
// distinct from net.outbound:*.b.com (correctly: they grant access
// to different domains).
func DiffPermissions(current, target []string) PermsDiff {
	cs := stringSet(current)
	ts := stringSet(target)
	d := PermsDiff{}
	for p := range ts {
		if _, ok := cs[p]; ok {
			d.Unchanged = append(d.Unchanged, p)
		} else {
			d.Added = append(d.Added, p)
		}
	}
	for p := range cs {
		if _, ok := ts[p]; !ok {
			d.Removed = append(d.Removed, p)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Unchanged)
	return d
}

func stringSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		out[v] = struct{}{}
	}
	return out
}

// ─── versionGreater ───────────────────────────────────────────────
//
// We accept full semver but only need the lexical "greater than"
// for normalised forms. For v2.0 simplicity we do field-by-field
// integer compare on dot-separated tokens — that catches all v0.1.0
// → v0.2.0 and v0.1.0 → v0.1.1 cases. Pre-release tags (-alpha) are
// ignored for the compare so 0.2.0 > 0.2.0-rc1 rather than the
// "more correct" precedence Semver §11 specifies. Real precedence
// matters at marketplace publish time (v2.5); here we just want a
// stable ordering for "is upgrade available".
func versionGreater(a, b string) bool {
	cmp := compareSemver(a, b)
	return cmp > 0
}

func compareSemver(a, b string) int {
	at := splitVersion(a)
	bt := splitVersion(b)
	for i := 0; i < len(at) || i < len(bt); i++ {
		var av, bv int
		if i < len(at) {
			av = at[i]
		}
		if i < len(bt) {
			bv = bt[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// splitVersion parses "0.2.0" → [0, 2, 0]; bad input → nil (treated
// as "0.0.0").
func splitVersion(v string) []int {
	out := []int{}
	cur := 0
	hasCur := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= '0' && c <= '9' {
			cur = cur*10 + int(c-'0')
			hasCur = true
			continue
		}
		if c == '.' {
			if hasCur {
				out = append(out, cur)
			}
			cur = 0
			hasCur = false
			continue
		}
		// Non-numeric / non-dot (e.g. "-alpha") stops parsing here;
		// we only compare the numeric prefix.
		if hasCur {
			out = append(out, cur)
		}
		return out
	}
	if hasCur {
		out = append(out, cur)
	}
	return out
}

// ─── Upgrade ──────────────────────────────────────────────────────

// UpgradeRequest captures the upgrade call body. AcceptedNewPermissions
// is what the user clicked-through in the Modal; on the auto-eligible
// path it's empty/optional.
type UpgradeRequest struct {
	InstallID              string
	AcceptedNewPermissions []string
	CallerUserID           string
	CallerOrgID            string
	CallerRoles            []string
}

// Upgrade bumps the installation's version to the registry's current
// manifest version. Two paths:
//
//   - Auto: row not pinned, no new perms. Just UPDATE + events.
//   - Approval: caller must accept every `added` permission via
//     AcceptedNewPermissions. We add accepted perms into
//     permissions_granted, drop removed perms, leave unchanged ones.
//
// OnUpgrade fires AFTER commit (best-effort; errors logged + returned
// as non-fatal warning).
func (in *Installer) Upgrade(ctx context.Context, req UpgradeRequest) (*Installation, error) {
	status, err := in.CheckUpgradable(ctx, req.InstallID)
	if err != nil {
		return nil, err
	}
	if !status.Available {
		return nil, ErrAlreadyLatest
	}

	row, _ := in.Get(ctx, req.InstallID)
	app, ok := in.Registry.Get(row.Identifier)
	if !ok {
		return nil, ErrUnknownApp
	}
	manifest := app.Manifest()

	// Authz.
	dec, err := in.Authz.Check(ctx, DecideRequest{
		Principal: Entity{Type: "User", ID: req.CallerUserID,
			Attributes: map[string]any{
				"id":     req.CallerUserID,
				"org_id": req.CallerOrgID,
				"roles":  toAnySlice(req.CallerRoles),
			}},
		Action:   permissions.ActionUpgrade,
		Resource: row.cedarEntity(),
	})
	if err != nil {
		return nil, fmt.Errorf("upgrade: authz: %w", err)
	}
	if dec.Decision != "ALLOW" {
		return nil, fmt.Errorf("%w: %s", ErrPermissionDenied, dec.Reason)
	}

	// Pin block — pinned installs require explicit ack via the
	// "remove pin" flow first; we don't try to be clever here.
	if status.Pinned {
		return nil, ErrPinnedVersion
	}

	// Approval gate: every `added` perm must be in the caller's
	// AcceptedNewPermissions. Rejects partial accept (caller can't
	// silently cherry-pick a subset of new perms — would create a
	// confusing "you have wiki.write but not net.outbound" state).
	accepted := stringSet(req.AcceptedNewPermissions)
	if status.PermsDiff.IsBreaking() {
		for _, p := range status.PermsDiff.Added {
			if _, ok := accepted[p]; !ok {
				return nil, fmt.Errorf("%w: missing accept for %q",
					ErrPermissionsNotAccepted, p)
			}
		}
	}

	// Compute the new granted set: kept (current ∩ target) + accepted.
	keepSet := stringSet(status.PermsDiff.Unchanged)
	for _, p := range req.AcceptedNewPermissions {
		keepSet[p] = struct{}{}
	}
	newGranted := make([]string, 0, len(keepSet))
	for p := range keepSet {
		newGranted = append(newGranted, p)
	}
	sort.Strings(newGranted)

	// tx.
	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("upgrade: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE app_center.installations
		   SET version = $1,
		       permissions_granted = $2,
		       updated_at = $3
		 WHERE id = $4
	`, manifest.Version, toAnySlice(newGranted), now, req.InstallID); err != nil {
		return nil, fmt.Errorf("upgrade: update: %w", err)
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   req.InstallID,
		ActorType: events.ActorUser,
		ActorID:   req.CallerUserID,
		Type:      events.AppUpgraded,
		Payload: map[string]any{
			"identifier":    row.Identifier,
			"from_version":  status.CurrentVersion,
			"to_version":    status.TargetVersion,
			"perms_added":   status.PermsDiff.Added,
			"perms_removed": status.PermsDiff.Removed,
		},
	}); err != nil {
		return nil, fmt.Errorf("upgrade: events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("upgrade: commit: %w", err)
	}

	updated := *row
	updated.Version = manifest.Version
	updated.PermissionsGranted = newGranted
	updated.UpdatedAt = now

	// Hook AFTER commit.
	hookErr := in.Registry.DispatchOnUpgrade(ctx, row.Identifier,
		hookInstall(&updated, manifest), status.CurrentVersion)
	if hookErr != nil {
		return &updated, fmt.Errorf("upgrade: OnUpgrade hook (row updated): %w", hookErr)
	}
	return &updated, nil
}
