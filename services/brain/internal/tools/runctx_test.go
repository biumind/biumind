package tools

import (
	"context"
	"testing"
)

func TestRunIDRoundTrip(t *testing.T) {
	ctx := WithRunID(context.Background(), "run-123")
	if got := RunIDFromContext(ctx); got != "run-123" {
		t.Fatalf("got %q, want run-123", got)
	}
}

func TestRunIDFromContextEmpty(t *testing.T) {
	if got := RunIDFromContext(context.Background()); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestWithRunIDEmptyNoOp(t *testing.T) {
	ctx := context.Background()
	if got := WithRunID(ctx, ""); got != ctx {
		t.Fatal("empty run id must return the same context")
	}
	if RunIDFromContext(ctx) != "" {
		t.Fatal("empty run id must not tag the context")
	}
}
