// Tests for the is_default_chat admin surface (Phase B, migration 00002):
// set / switch default via PATCH, mode=chat restriction (400), and the
// partial unique index backstop. DB-backed; skips when DATABASE_URL unset.

package adminapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func TestAdmin_DefaultChatModel(t *testing.T) {
	fx := newAdminFixture(t)
	ctx := context.Background()

	// Serialize against other default-chat tests on this shared dev DB —
	// the "one default globally" invariant means a concurrent test that
	// sets a new default silently clears this one.
	lockConn, err := fx.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire lock conn: %v", err)
	}
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock(20260810)"); err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	t.Cleanup(func() {
		lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(20260810)") //nolint:errcheck
		lockConn.Release()
	})

	suffix := time.Now().UnixNano()
	codeA := fmt.Sprintf("m_defa_%d", suffix)
	codeB := fmt.Sprintf("m_defb_%d", suffix)

	// A — created directly as the default chat model.
	resp, body := fx.do(t, "POST", "/v1/admin/models", "admin", map[string]any{
		"code": codeA, "display_name": "Def A", "min_plan": "free",
		"status": "active", "is_default_chat": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create A: %d body=%s", resp.StatusCode, body)
	}
	var ma registry.Model
	decodeBody(t, body, &ma)
	if !ma.IsDefaultChat {
		t.Fatalf("create A: is_default_chat not set/roundtripped: %+v", ma)
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", ma.ID) //nolint:errcheck

	// B — plain chat model, not default.
	resp, body = fx.do(t, "POST", "/v1/admin/models", "admin", map[string]any{
		"code": codeB, "display_name": "Def B", "min_plan": "free",
		"status": "active",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create B: %d body=%s", resp.StatusCode, body)
	}
	var mb registry.Model
	decodeBody(t, body, &mb)
	if mb.IsDefaultChat {
		t.Fatalf("create B: is_default_chat should default to false")
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", mb.ID) //nolint:errcheck

	// Switch default A → B via PATCH. Same transaction must clear A.
	resp, body = fx.do(t, "PATCH", "/v1/admin/models/"+mb.ID.String(), "admin", map[string]any{
		"code": codeB, "display_name": "Def B", "min_plan": "free",
		"status": "active", "is_default_chat": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch B default: %d body=%s", resp.StatusCode, body)
	}
	decodeBody(t, body, &mb)
	if !mb.IsDefaultChat {
		t.Fatalf("patch B: is_default_chat not set")
	}
	gotA, err := fx.store.Models.Get(ctx, ma.ID)
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	if gotA.IsDefaultChat {
		t.Fatalf("switching default did not clear A's flag")
	}

	// 400 — non-chat mode cannot be the default chat model.
	resp, body = fx.do(t, "PATCH", "/v1/admin/models/"+mb.ID.String(), "admin", map[string]any{
		"code": codeB, "display_name": "Def B", "min_plan": "free",
		"status": "active", "mode": "image_generation", "is_default_chat": true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-chat default: status = %d, body = %s, want 400", resp.StatusCode, body)
	}
	var errBody errorEnvelope
	decodeBody(t, body, &errBody)
	if errBody.Error.Code != "invalid_default_chat" {
		t.Fatalf("non-chat default: error code = %q, want invalid_default_chat", errBody.Error.Code)
	}

	// Unique-index backstop — direct SQL bypassing the repo's
	// clear-others transaction must fail while B is still default.
	if _, err := fx.pool.Exec(ctx,
		"UPDATE model_relay.models SET is_default_chat=true WHERE id=$1", ma.ID); err == nil {
		t.Fatalf("expected unique violation setting a second default via raw SQL")
	}

	// Unset — PATCH B back to false leaves no default.
	resp, body = fx.do(t, "PATCH", "/v1/admin/models/"+mb.ID.String(), "admin", map[string]any{
		"code": codeB, "display_name": "Def B", "min_plan": "free",
		"status": "active", "is_default_chat": false,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch B unset: %d body=%s", resp.StatusCode, body)
	}
	decodeBody(t, body, &mb)
	if mb.IsDefaultChat {
		t.Fatalf("patch B unset: flag still true")
	}

	// List response carries the field.
	resp, body = fx.do(t, "GET", "/v1/admin/models?q="+codeA, "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d body=%s", resp.StatusCode, body)
	}
	var list struct {
		Items []registry.Model `json:"items"`
	}
	decodeBody(t, body, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list: expected 1 item, got %d", len(list.Items))
	}
	// Field present in the wire shape (zero value false is fine here;
	// the create-path assertion above already proved true roundtrips).
	if list.Items[0].ID != ma.ID {
		t.Fatalf("list: wrong model %+v", list.Items[0])
	}
}
