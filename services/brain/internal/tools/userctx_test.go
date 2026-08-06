package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestUserIDRoundTrip(t *testing.T) {
	id := uuid.New()
	ctx := WithUserID(context.Background(), id)
	got := UserIDFromContext(ctx)
	if got != id {
		t.Errorf("got %v want %v", got, id)
	}
}

func TestUserIDFromContextEmpty(t *testing.T) {
	got := UserIDFromContext(context.Background())
	if got != uuid.Nil {
		t.Errorf("got %v want uuid.Nil", got)
	}
}

func TestWithUserIDNilNoOp(t *testing.T) {
	// Passing uuid.Nil should not poison the context — downstream
	// reads should still return Nil so the caller can detect "no
	// user identity attached" cleanly.
	ctx := WithUserID(context.Background(), uuid.Nil)
	if UserIDFromContext(ctx) != uuid.Nil {
		t.Error("expected no-op when called with uuid.Nil")
	}
}
