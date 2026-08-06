package skillbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubApp implements biuapp.App + BundledSkillProvider for tests.
type stubApp struct {
	manifest biuapp.Manifest
	content  map[string][]byte
}

func (s *stubApp) Manifest() biuapp.Manifest                     { return s.manifest }
func (s *stubApp) Init(ctx context.Context, _ biuapp.Deps) error { return nil }
func (s *stubApp) Invoke(_ context.Context, _ string, _ json.RawMessage) (any, error) {
	return nil, nil
}
func (s *stubApp) SkillContent(id string) ([]byte, error) {
	if b, ok := s.content[id]; ok {
		return b, nil
	}
	return nil, biuapp.ErrSkillNotFound
}

// noProviderApp implements App but NOT BundledSkillProvider.
type noProviderApp struct{ manifest biuapp.Manifest }

func (a *noProviderApp) Manifest() biuapp.Manifest                     { return a.manifest }
func (a *noProviderApp) Init(ctx context.Context, _ biuapp.Deps) error { return nil }
func (a *noProviderApp) Invoke(_ context.Context, _ string, _ json.RawMessage) (any, error) {
	return nil, nil
}

// ─── Unit-style tests (no DB) ─────────────────────────────────────

func TestWriteAppSkills_NoOrgErrors(t *testing.T) {
	app := &stubApp{
		manifest: biuapp.Manifest{
			ManifestExt: biuapp.ManifestExt{Skills: []biuapp.SkillRef{{Identifier: "s1", File: "s1.md"}}},
		},
		content: map[string][]byte{"s1": []byte("body")},
	}
	_, err := WriteAppSkills(context.Background(), nil, Inputs{
		InstallID: uuid.New(), AppIdentifier: "test", Manifest: app.Manifest(), App: app,
	})
	if !errors.Is(err, ErrNoOrg) {
		t.Errorf("expected ErrNoOrg, got %v", err)
	}
}

func TestWriteAppSkills_NoSkillsIsNoop(t *testing.T) {
	app := &stubApp{manifest: biuapp.Manifest{}}
	n, err := WriteAppSkills(context.Background(), nil, Inputs{
		InstallID: uuid.New(), OrgID: uuid.New(), AppIdentifier: "test",
		Manifest: app.Manifest(), App: app,
	})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestWriteAppSkills_RequiresProvider(t *testing.T) {
	// App declares skills in manifest but doesn't implement
	// BundledSkillProvider — that's a programmer error and should
	// surface clearly, not silently skip.
	app := &noProviderApp{
		manifest: biuapp.Manifest{
			ManifestExt: biuapp.ManifestExt{Skills: []biuapp.SkillRef{{Identifier: "s1", File: "s1.md"}}},
		},
	}
	_, err := WriteAppSkills(context.Background(), nil, Inputs{
		InstallID: uuid.New(), OrgID: uuid.New(),
		AppIdentifier: "noprov", Manifest: app.Manifest(), App: app,
	})
	if err == nil {
		t.Fatal("expected error when App lacks BundledSkillProvider")
	}
}

// ─── DB integration ─────────────────────────────────────────

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping skillbridge integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	for _, table := range []string{"runtime.skills", "app_center.installations"} {
		var exists bool
		if err := p.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`,
			table,
		).Scan(&exists); err != nil {
			t.Fatalf("table check %s: %v", table, err)
		}
		if !exists {
			t.Skipf("%s missing; apply both runtime + app_center migrations", table)
		}
	}
	return p
}

func TestWriteAppSkills_RoundTrip(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()

	app := &stubApp{
		manifest: biuapp.Manifest{
			Name:    "skb-test",
			Version: "0.1.0",
			ManifestExt: biuapp.ManifestExt{Skills: []biuapp.SkillRef{
				{Identifier: "skb-summary", File: "skills/summary.md"},
			}},
		},
		content: map[string][]byte{"skb-summary": []byte("# Summary skill body")},
	}
	installID := uuid.New()
	orgID := uuid.New()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	n, err := WriteAppSkills(ctx, tx, Inputs{
		InstallID:     installID,
		OrgID:         orgID,
		AppIdentifier: "skb-test",
		Manifest:      app.Manifest(),
		App:           app,
	})
	if err != nil {
		t.Fatalf("WriteAppSkills: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d, want 1", n)
	}

	// Verify the row landed and carries the install_id back-pointer.
	var (
		identifier string
		manifest   []byte
		source     string
	)
	if err := tx.QueryRow(ctx, `
		SELECT identifier, manifest, source FROM runtime.skills
		 WHERE org_id = $1 AND identifier = 'skb-summary'
	`, orgID).Scan(&identifier, &manifest, &source); err != nil {
		t.Fatalf("query: %v", err)
	}
	if source != "bundled" {
		t.Errorf("source = %q, want bundled", source)
	}
	var meta map[string]any
	_ = json.Unmarshal(manifest, &meta)
	if got := meta["app_install_id"]; got != installID.String() {
		t.Errorf("manifest.app_install_id = %v, want %v", got, installID)
	}

	// Idempotent re-write — same identifier, content updates in place.
	app.content["skb-summary"] = []byte("# Summary skill body v2")
	if _, err := WriteAppSkills(ctx, tx, Inputs{
		InstallID: installID, OrgID: orgID, AppIdentifier: "skb-test",
		Manifest: app.Manifest(), App: app,
	}); err != nil {
		t.Fatalf("re-write: %v", err)
	}
	var content string
	if err := tx.QueryRow(ctx, `
		SELECT content FROM runtime.skills WHERE org_id = $1 AND identifier = 'skb-summary'
	`, orgID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "# Summary skill body v2" {
		t.Errorf("content not updated: %q", content)
	}

	// Delete by install_id removes the row.
	rows, err := DeleteAppSkills(ctx, tx, installID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows != 1 {
		t.Errorf("deleted %d rows, want 1", rows)
	}
	var remaining int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM runtime.skills WHERE org_id = $1 AND identifier = 'skb-summary'`, orgID).Scan(&remaining)
	if remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", remaining)
	}
}

func TestDeleteAppSkills_ScopedByInstallID(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	orgID := uuid.New()
	installA := uuid.New()
	installB := uuid.New()

	appA := &stubApp{
		manifest: biuapp.Manifest{Name: "skb-a", Version: "0.1.0",
			ManifestExt: biuapp.ManifestExt{Skills: []biuapp.SkillRef{{Identifier: "skb-a-skill", File: "a.md"}}}},
		content: map[string][]byte{"skb-a-skill": []byte("a")},
	}
	appB := &stubApp{
		manifest: biuapp.Manifest{Name: "skb-b", Version: "0.1.0",
			ManifestExt: biuapp.ManifestExt{Skills: []biuapp.SkillRef{{Identifier: "skb-b-skill", File: "b.md"}}}},
		content: map[string][]byte{"skb-b-skill": []byte("b")},
	}
	if _, err := WriteAppSkills(ctx, tx, Inputs{
		InstallID: installA, OrgID: orgID, AppIdentifier: "skb-a",
		Manifest: appA.Manifest(), App: appA,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAppSkills(ctx, tx, Inputs{
		InstallID: installB, OrgID: orgID, AppIdentifier: "skb-b",
		Manifest: appB.Manifest(), App: appB,
	}); err != nil {
		t.Fatal(err)
	}

	// Delete only installA.
	rows, err := DeleteAppSkills(ctx, tx, installA)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("deleted %d rows from installA, want 1", rows)
	}
	// installB skill must remain.
	var remainingB int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM runtime.skills WHERE identifier = 'skb-b-skill'`).Scan(&remainingB)
	if remainingB != 1 {
		t.Errorf("installB skill should still exist, got count = %d", remainingB)
	}
}
