package billing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fake identity server 用于测试.
func newFake(handler http.HandlerFunc) (*httptest.Server, *Client) {
	srv := httptest.NewServer(handler)
	return srv, NewClient(srv.URL, "test-token")
}

func TestConsume_HappyPath(t *testing.T) {
	srv, c := newFake(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "no auth", 401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !contains(body, "task-1") {
			t.Errorf("body missing ref_id: %s", body)
		}
		fmt.Fprintf(w, `{
		    "log": {"id": "11111111-1111-1111-1111-111111111111"},
		    "balance": {"permanent_balance": 60, "time_limited_balance": 30}
		}`)
	})
	defer srv.Close()

	res, err := c.Consume(context.Background(), ConsumeArgs{
		UserID:  uuid.New(),
		Amount:  40,
		RefType: "aigc_task", RefID: "task-1",
		IdempotencyKey: "task-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.BalanceTotal != 90 {
		t.Errorf("total = %d, want 90", res.BalanceTotal)
	}
	if res.LogID.String() != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("log id mismatch: %s", res.LogID)
	}
}

func TestConsume_InsufficientCredits_NoRetry(t *testing.T) {
	var calls int32
	srv, c := newFake(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "credits: insufficient balance", http.StatusPaymentRequired)
	})
	defer srv.Close()

	_, err := c.Consume(context.Background(), ConsumeArgs{
		UserID: uuid.New(), Amount: 9999, RefType: "aigc_task",
	})
	if !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("want ErrInsufficientCredits, got %v", err)
	}
	// 4xx 不重试
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("4xx triggered retry: %d calls", got)
	}
}

func TestConsume_5xxRetries(t *testing.T) {
	var calls int32
	srv, c := newFake(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			http.Error(w, "boom", 503)
			return
		}
		fmt.Fprintf(w, `{"log":{"id":"11111111-1111-1111-1111-111111111111"},"balance":{"permanent_balance":0,"time_limited_balance":0}}`)
	})
	defer srv.Close()

	res, err := c.Consume(context.Background(), ConsumeArgs{
		UserID: uuid.New(), Amount: 1, RefType: "aigc_task",
	})
	if err != nil {
		t.Fatalf("eventual success after retry: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("retries = %d, want 3", got)
	}
}

func TestConsume_5xxExhaustedFails(t *testing.T) {
	var calls int32
	srv, c := newFake(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "always 503", 503)
	})
	defer srv.Close()

	_, err := c.Consume(context.Background(), ConsumeArgs{
		UserID: uuid.New(), Amount: 1, RefType: "aigc_task",
	})
	if err == nil {
		t.Fatal("want error after 3 retries")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("retries = %d, want 3", got)
	}
}

func TestConsume_ContextCancel(t *testing.T) {
	srv, c := newFake(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "503", 503) // 一直 5xx 触发重试 backoff
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.Consume(ctx, ConsumeArgs{
		UserID: uuid.New(), Amount: 1, RefType: "aigc_task",
	})
	if err == nil {
		t.Fatal("want context error")
	}
	// ctx.Err() 或上次 503 错误都可接受 — 关键是要 fail (不能死循环)
}

func TestRefund_ErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, ErrLogNotFound},
		{http.StatusConflict, ErrConflict},
		{http.StatusBadRequest, ErrBadRequest},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("status_%d", c.status), func(t *testing.T) {
			srv, cli := newFake(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "x", c.status)
			})
			defer srv.Close()
			_, err := cli.Refund(context.Background(), RefundArgs{
				OriginalLogID: uuid.New(), Amount: 10,
			})
			if !errors.Is(err, c.want) {
				t.Errorf("status %d: want %v, got %v", c.status, c.want, err)
			}
		})
	}
}

func TestGetBalanceTotal(t *testing.T) {
	srv, c := newFake(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/credits/22222222-2222-2222-2222-222222222222/balance" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"balance":{"permanent_balance":100,"time_limited_balance":50}}`)
	})
	defer srv.Close()

	uid, _ := uuid.Parse("22222222-2222-2222-2222-222222222222")
	total, err := c.GetBalanceTotal(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if total != 150 {
		t.Errorf("total = %d, want 150", total)
	}
}

func contains(b []byte, sub string) bool {
	return string(b) != "" && len(b) >= len(sub) && bytesIndex(b, sub) >= 0
}

func bytesIndex(b []byte, sub string) int {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == sub {
			return i
		}
	}
	return -1
}
