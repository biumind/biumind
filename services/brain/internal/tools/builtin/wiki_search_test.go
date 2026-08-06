package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// Tests cover only what doesn't need a real pgxpool: descriptor
// shape, owner-id guard, input validation. Full ANN behaviour lives
// in chunks/store_test.go.

func TestWikiSearchDescriptor(t *testing.T) {
	tool := WikiSearch(nil, embed.NewStub(8))
	if tool.Name != "wiki_search" {
		t.Errorf("name: %q", tool.Name)
	}
	if !tool.Runtime.AvailableIn(tools.ExecutionCloud) ||
		!tool.Runtime.AvailableIn(tools.ExecutionClient) {
		t.Errorf("expected RuntimeBoth")
	}
	if tool.Invoke == nil {
		t.Fatal("Invoke nil")
	}
}

func TestWikiSearchRequiresUserContext(t *testing.T) {
	tool := WikiSearch(nil, embed.NewStub(8))
	_, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"query":"foo"}`))
	if err == nil ||
		!strings.Contains(err.Error(), "user identity") {
		t.Errorf("expected missing-user error, got %v", err)
	}
}

func TestWikiSearchEmptyQueryRejected(t *testing.T) {
	tool := WikiSearch(nil, embed.NewStub(8))
	ctx := tools.WithUserID(context.Background(), uuid.New())
	_, err := tool.Invoke(ctx, json.RawMessage(`{"query":""}`))
	if err == nil || !strings.Contains(err.Error(), "query required") {
		t.Errorf("expected query-required error, got %v", err)
	}
}

func TestWikiSearchBadProjectID(t *testing.T) {
	tool := WikiSearch(nil, embed.NewStub(8))
	ctx := tools.WithUserID(context.Background(), uuid.New())
	_, err := tool.Invoke(ctx, json.RawMessage(
		`{"query":"x","project_id":"not-a-uuid"}`))
	if err == nil || !strings.Contains(err.Error(), "bad project_id") {
		t.Errorf("expected bad-project-id error, got %v", err)
	}
}
