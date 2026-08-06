package hub

import (
	"testing"
	"time"

	"github.com/biumind/biumind/services/realtime/internal/ledger"
)

func TestRegisterPublish(t *testing.T) {
	h := NewHub(8)
	c := h.Register("dev-1", "u-1", []string{"t1"})

	delivered, dropped := h.Publish(ledger.Event{ID: "01", Topic: "t1", TS: time.Now()})
	if delivered != 1 || dropped != 0 {
		t.Fatalf("delivered=%d dropped=%d", delivered, dropped)
	}
	select {
	case e := <-c.Out():
		if e.ID != "01" {
			t.Errorf("got %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	h := NewHub(8)
	c := h.Register("dev-1", "u-1", []string{"t1"})
	if err := h.Subscribe("dev-1", "t2"); err != nil {
		t.Fatal(err)
	}
	if delivered, _ := h.Publish(ledger.Event{ID: "01", Topic: "t2", TS: time.Now()}); delivered != 1 {
		t.Errorf("expected delivery to t2 sub")
	}
	if err := h.Unsubscribe("dev-1", "t2"); err != nil {
		t.Fatal(err)
	}
	// Drain any pending
	drain(c.Out())
	if delivered, _ := h.Publish(ledger.Event{ID: "02", Topic: "t2", TS: time.Now()}); delivered != 0 {
		t.Errorf("expected no delivery after unsubscribe")
	}
}

func TestSlowConsumerDropped(t *testing.T) {
	h := NewHub(2) // small buffer to trigger drop fast
	c := h.Register("dev-slow", "u-1", []string{"t1"})
	// Don't drain c.Out() — fill it, then publish more than buffer
	for i := 0; i < 5; i++ {
		h.Publish(ledger.Event{ID: id(i), Topic: "t1", TS: time.Now()})
	}
	// Eventually conn should close
	deadline := time.After(time.Second)
	for !c.closed.Load() {
		select {
		case <-deadline:
			t.Fatal("slow consumer not closed")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestReregisterClosesOld(t *testing.T) {
	h := NewHub(8)
	old := h.Register("dev-1", "u-1", nil)
	_ = h.Register("dev-1", "u-1", nil)
	deadline := time.After(time.Second)
	for !old.closed.Load() {
		select {
		case <-deadline:
			t.Fatal("old conn not closed on reregister")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func id(i int) string {
	return "0" + string(rune('0'+i))
}

func drain[T any](ch <-chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
