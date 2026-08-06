package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// fakeExecer captures Write's SQL and args so we can assert the shape
// without spinning a real Postgres in unit tests.
type fakeExecer struct {
	sql  string
	args []any
	err  error
}

func (f *fakeExecer) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sql = sql
	f.args = args
	return pgconn.CommandTag{}, f.err
}

func TestWrite_PopulatesAllColumns(t *testing.T) {
	f := &fakeExecer{}
	err := Write(context.Background(), f, Event{
		ScopeKind: "install",
		ScopeID:   "abc-123",
		ActorType: ActorUser,
		ActorID:   "u-9",
		Type:      AppInstalled,
		Payload:   map[string]any{"version": "0.2.0"},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(f.args) != 5 {
		t.Fatalf("got %d args, want 5: %+v", len(f.args), f.args)
	}
	if f.args[0] != "install:abc-123" {
		t.Errorf("scope = %q, want %q", f.args[0], "install:abc-123")
	}
	if f.args[1] != "user" {
		t.Errorf("actor_type = %q", f.args[1])
	}
	if f.args[2] != "u-9" {
		t.Errorf("actor_id = %q", f.args[2])
	}
	if f.args[3] != "app.installed" {
		t.Errorf("event_type = %q", f.args[3])
	}
	pl, ok := f.args[4].([]byte)
	if !ok {
		t.Fatalf("payload arg type = %T, want []byte", f.args[4])
	}
	var got map[string]any
	if err := json.Unmarshal(pl, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["version"] != "0.2.0" {
		t.Errorf("payload.version = %v", got["version"])
	}
}

func TestWrite_AcceptsRawMessage(t *testing.T) {
	f := &fakeExecer{}
	err := Write(context.Background(), f, Event{
		ScopeKind: "user", ScopeID: "u-1",
		ActorType: ActorUser, Type: SidebarLayoutChanged,
		Payload: json.RawMessage(`{"scope":"desktop","version":3}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	pl := f.args[4].([]byte)
	if string(pl) != `{"scope":"desktop","version":3}` {
		t.Errorf("payload = %s; want raw passthrough", pl)
	}
}

func TestWrite_NilPayloadBecomesEmptyObject(t *testing.T) {
	f := &fakeExecer{}
	err := Write(context.Background(), f, Event{
		ScopeKind: "app", ScopeID: "x", ActorType: ActorSystem, Type: AppPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(f.args[4].([]byte)) != `{}` {
		t.Errorf("nil payload should serialise as {}, got %s", f.args[4])
	}
}

func TestWrite_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
	}{
		{"no scope kind", Event{ScopeID: "x", ActorType: ActorUser, Type: AppInstalled}},
		{"no scope id", Event{ScopeKind: "install", ActorType: ActorUser, Type: AppInstalled}},
		{"no type", Event{ScopeKind: "install", ScopeID: "x", ActorType: ActorUser}},
		{"no actor", Event{ScopeKind: "install", ScopeID: "x", Type: AppInstalled}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Write(context.Background(), &fakeExecer{}, c.ev); err == nil {
				t.Errorf("%s: expected error", c.name)
			}
		})
	}
}

func TestWrite_PropagatesExecError(t *testing.T) {
	f := &fakeExecer{err: errors.New("db down")}
	err := Write(context.Background(), f, Event{
		ScopeKind: "install", ScopeID: "x",
		ActorType: ActorUser, Type: AppInstalled,
	})
	if err == nil || !errors.Is(err, f.err) && !contains(err.Error(), "db down") {
		t.Errorf("expected wrapped exec error, got %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
