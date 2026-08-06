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

// Tests cover only what doesn't need a live pgxpool: descriptor
// shape, owner-id guard, input validation. Hybrid recall ranking is
// covered by memory/store/store_test.go.

func TestMemoryRecallDescriptor(t *testing.T) {
	tool := MemoryRecall(nil, embed.NewStub(8))
	if tool.Name != "memory_recall" {
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

func TestMemoryRecallRequiresUserContext(t *testing.T) {
	tool := MemoryRecall(nil, embed.NewStub(8))
	_, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"query":"foo","project_id":"`+uuid.New().String()+`"}`))
	if err == nil || !strings.Contains(err.Error(), "user identity") {
		t.Errorf("expected missing-user error, got %v", err)
	}
}

func TestMemoryRecallEmptyQueryRejected(t *testing.T) {
	tool := MemoryRecall(nil, embed.NewStub(8))
	ctx := tools.WithUserID(context.Background(), uuid.New())
	_, err := tool.Invoke(ctx, json.RawMessage(
		`{"query":"","project_id":"`+uuid.New().String()+`"}`))
	if err == nil || !strings.Contains(err.Error(), "query required") {
		t.Errorf("expected query-required error, got %v", err)
	}
}

func TestMemoryRecallProjectIDRequired(t *testing.T) {
	tool := MemoryRecall(nil, embed.NewStub(8))
	ctx := tools.WithUserID(context.Background(), uuid.New())
	_, err := tool.Invoke(ctx, json.RawMessage(`{"query":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "project_id required") {
		t.Errorf("expected project_id-required error, got %v", err)
	}
}

func TestMemoryRecallBadProjectID(t *testing.T) {
	tool := MemoryRecall(nil, embed.NewStub(8))
	ctx := tools.WithUserID(context.Background(), uuid.New())
	_, err := tool.Invoke(ctx, json.RawMessage(
		`{"query":"x","project_id":"not-a-uuid"}`))
	if err == nil || !strings.Contains(err.Error(), "bad project_id") {
		t.Errorf("expected bad-project-id error, got %v", err)
	}
}

func TestMemoryRecallInvalidKindRejected(t *testing.T) {
	tool := MemoryRecall(nil, embed.NewStub(8))
	ctx := tools.WithUserID(context.Background(), uuid.New())
	pid := uuid.New().String()
	_, err := tool.Invoke(ctx, json.RawMessage(
		`{"query":"x","project_id":"`+pid+`","kind":"bogus"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid kind") {
		t.Errorf("expected invalid-kind error, got %v", err)
	}
}
