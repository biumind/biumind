package api

import (
	"testing"
	"time"
)

func TestVerificationThrottle_CooldownBlocksSecond(t *testing.T) {
	tt := NewVerificationThrottle()
	tt.Cooldown = 60 * time.Second
	tt.DailyCap = 100
	if ok, _ := tt.AllowAndRecord("a@x.com"); !ok {
		t.Fatal("first should pass")
	}
	if ok, retry := tt.AllowAndRecord("a@x.com"); ok || retry <= 0 {
		t.Errorf("second should be cooled down, got allow=%v retry=%s", ok, retry)
	}
}

func TestVerificationThrottle_DailyCap(t *testing.T) {
	tt := NewVerificationThrottle()
	tt.Cooldown = 0 // 关掉 cooldown 单测 cap
	tt.DailyCap = 3
	tt.Window = time.Hour
	for i := 0; i < 3; i++ {
		if ok, _ := tt.AllowAndRecord("b@x.com"); !ok {
			t.Fatalf("attempt %d: should pass", i)
		}
	}
	if ok, retry := tt.AllowAndRecord("b@x.com"); ok || retry <= 0 {
		t.Errorf("4th should hit cap, got allow=%v retry=%s", ok, retry)
	}
}

func TestVerificationThrottle_EmptyEmailRejected(t *testing.T) {
	tt := NewVerificationThrottle()
	if ok, _ := tt.AllowAndRecord(""); ok {
		t.Error("empty email should not pass")
	}
}

func TestVerificationThrottle_ResetClears(t *testing.T) {
	tt := NewVerificationThrottle()
	tt.Cooldown = time.Hour
	tt.AllowAndRecord("c@x.com")
	tt.Reset("c@x.com")
	if ok, _ := tt.AllowAndRecord("c@x.com"); !ok {
		t.Error("after Reset, next call should pass")
	}
}

func TestVerificationThrottle_CooldownExpiresThenAllow(t *testing.T) {
	tt := NewVerificationThrottle()
	tt.Cooldown = 10 * time.Millisecond
	tt.DailyCap = 100
	tt.AllowAndRecord("d@x.com")
	time.Sleep(20 * time.Millisecond)
	if ok, _ := tt.AllowAndRecord("d@x.com"); !ok {
		t.Error("after cooldown elapsed, second should pass")
	}
}
