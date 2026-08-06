package token

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type fakeReaperStore struct {
	calls   atomic.Int32
	deleted int64
	err     error
}

func (f *fakeReaperStore) ReapRefreshTokens(_ context.Context, _, _ time.Duration, _ int) (int64, error) {
	f.calls.Add(1)
	return f.deleted, f.err
}

func TestRunReaper_FiresOnTickAndStopsOnCtx(t *testing.T) {
	store := &fakeReaperStore{deleted: 3}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan struct{})
	go func() {
		RunReaper(ctx, store, ReaperConfig{Interval: 30 * time.Millisecond}, logger)
		close(done)
	}()

	// 给 3-5 个 tick 周期
	time.Sleep(120 * time.Millisecond)
	calls := store.calls.Load()
	if calls < 2 {
		t.Errorf("expected ≥2 ticks in 120ms, got %d", calls)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("RunReaper did not exit after ctx cancel")
	}
}

func TestRunReaper_ContinuesOnError(t *testing.T) {
	store := &fakeReaperStore{err: errors.New("transient")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go RunReaper(ctx, store, ReaperConfig{Interval: 20 * time.Millisecond}, logger)

	time.Sleep(80 * time.Millisecond)
	calls := store.calls.Load()
	if calls < 2 {
		t.Errorf("reaper should keep running after errors, got %d calls", calls)
	}
}

func TestReaperConfig_Defaults(t *testing.T) {
	c := ReaperConfig{}
	c.applyDefaults()
	if c.Interval != time.Hour {
		t.Errorf("default interval = %v, want 1h", c.Interval)
	}
	if c.RevokedRetention != 30*24*time.Hour {
		t.Errorf("default revoked retention = %v, want 30d", c.RevokedRetention)
	}
	if c.AbsoluteExpiredRetention != 7*24*time.Hour {
		t.Errorf("default abs retention = %v, want 7d", c.AbsoluteExpiredRetention)
	}
	if c.BatchLimit != 500 {
		t.Errorf("default batch = %d, want 500", c.BatchLimit)
	}
}
