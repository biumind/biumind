// sync_live_test.go — opt-in live integration test against
// basellm.github.io/llm-metadata. Skipped by default; runs only when
// SYNC_UPSTREAM_LIVE=1 is set, to keep the regular test loop offline.
//
// Run: SYNC_UPSTREAM_LIVE=1 DATABASE_URL=... go test -run TestSync_Live -v
// Cleanup is automatic on test exit — every model with upstream_ref
// stamped during the test is deleted, so the dev DB is left as-is.

package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestSync_LiveBasellm(t *testing.T) {
	if os.Getenv("SYNC_UPSTREAM_LIVE") != "1" {
		t.Skip("set SYNC_UPSTREAM_LIVE=1 to run the live sync")
	}
	fx := newAdminFixture(t)
	ctx := context.Background()

	// Cleanup: every row created during this test will have non-null
	// upstream_ref. We snapshot the set BEFORE the sync, then delete the
	// delta on exit.
	priorIDs := snapshotUpstreamSyncedModelIDs(t, fx)
	t.Cleanup(func() {
		afterIDs := snapshotUpstreamSyncedModelIDs(t, fx)
		toDelete := afterIDs.minus(priorIDs)
		t.Logf("cleanup: removing %d models synced by this test", len(toDelete))
		for id := range toDelete {
			_, _ = fx.pool.Exec(context.Background(),
				`DELETE FROM model_relay.models WHERE id=$1`, id)
		}
	})

	// First sync — wall clock + structured logging so we see scale.
	start := time.Now()
	resp, body := fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync failed: %d body=%s", resp.StatusCode, body)
	}
	var res syncResponse
	decodeBody(t, body, &res)

	t.Logf("─── first sync (basellm.github.io) ───")
	t.Logf("  added=%d  updated=%d  skipped=%d  total=%d",
		res.Added, res.Updated, res.Skipped, res.Total)
	t.Logf("  etag=%s  elapsed=%s", res.ETag, elapsed)

	if res.Total < 1000 {
		t.Fatalf("expected >1000 upstream rows, got %d", res.Total)
	}
	// Most should land — Added + Skipped (dedupes / inactive) ≈ Total.
	if res.Added+res.Updated+res.Skipped != res.Total {
		t.Errorf("counts don't reconcile: %d+%d+%d != %d",
			res.Added, res.Updated, res.Skipped, res.Total)
	}

	// Spot-check a famous model
	for _, code := range []string{
		"claude-sonnet-4-5",
		"gpt-4o",
		"deepseek-chat",
	} {
		got, err := fx.store.Models.GetByCode(ctx, code)
		if err != nil {
			t.Logf("  spot-check %q: NOT in upstream (%v)", code, err)
			continue
		}
		dump, _ := json.MarshalIndent(map[string]any{
			"code":           got.Code,
			"display_name":   got.DisplayName,
			"family":         got.Family,
			"context_window": got.ContextWindow,
			"capabilities":   got.Capabilities,
			"status":         got.Status,
			"upstream_ref":   got.UpstreamRef,
		}, "    ", "  ")
		t.Logf("  spot-check %q ✓\n    %s", code, dump)
	}

	// Second sync — ETag must short-circuit.
	t.Logf("─── second sync (should hit ETag) ───")
	start = time.Now()
	resp, body = fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	elapsed = time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second sync: %d body=%s", resp.StatusCode, body)
	}
	var res2 syncResponse
	decodeBody(t, body, &res2)
	t.Logf("  not_modified=%v  elapsed=%s", res2.NotModified, elapsed)
	if !res2.NotModified {
		t.Errorf("expected ETag short-circuit, got %+v", res2)
	}
}

// idSet is a small helper for "set of UUID strings".
type idSet map[string]struct{}

func (a idSet) minus(b idSet) idSet {
	out := idSet{}
	for k := range a {
		if _, ok := b[k]; !ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func snapshotUpstreamSyncedModelIDs(t *testing.T, fx *adminFixture) idSet {
	t.Helper()
	rows, err := fx.pool.Query(context.Background(),
		`SELECT id FROM model_relay.models WHERE upstream_ref IS NOT NULL`)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer rows.Close()
	out := idSet{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		out[id] = struct{}{}
	}
	return out
}
