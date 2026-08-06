package cache

import (
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	c, err := New(100, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Set("k1", Entry{Decision: DecisionAllow, Reason: "ok"})
	got, ok := c.Get("k1")
	if !ok {
		t.Fatal("miss")
	}
	if got.Decision != DecisionAllow {
		t.Errorf("got %v", got)
	}
}

func TestExpiry(t *testing.T) {
	c, _ := New(10, 5*time.Millisecond)
	c.Set("k", Entry{Decision: DecisionAllow})
	time.Sleep(10 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expected expired miss")
	}
}

func TestClear(t *testing.T) {
	c, _ := New(10, time.Minute)
	c.Set("a", Entry{Decision: DecisionAllow})
	c.Set("b", Entry{Decision: DecisionDeny})
	c.Clear()
	if c.Len() != 0 {
		t.Fatal("Clear failed")
	}
}
