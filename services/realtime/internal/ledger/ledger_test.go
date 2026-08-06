package ledger

import (
	"testing"
	"time"
)

func mk(id, topic string) Event {
	return Event{ID: id, Topic: topic, Kind: "x", TS: time.Now()}
}

func TestAppendReplay(t *testing.T) {
	l := New(time.Hour, 100)
	l.Append(mk("01", "t1"))
	l.Append(mk("02", "t1"))
	l.Append(mk("03", "t2"))

	got := l.Replay([]string{"t1", "t2"}, "01")
	if len(got) != 2 {
		t.Fatalf("len=%d want 2; got=%v", len(got), got)
	}
	if got[0].ID != "02" || got[1].ID != "03" {
		t.Errorf("order wrong: %+v", got)
	}
}

func TestReplayEmptyCursor(t *testing.T) {
	l := New(time.Hour, 100)
	l.Append(mk("01", "t1"))
	if got := l.Replay([]string{"t1"}, ""); len(got) != 0 {
		t.Fatalf("empty cursor must return nothing; got %v", got)
	}
}

func TestRingEviction(t *testing.T) {
	l := New(time.Hour, 3)
	l.Append(mk("01", "t"))
	l.Append(mk("02", "t"))
	l.Append(mk("03", "t"))
	l.Append(mk("04", "t"))
	l.Append(mk("05", "t"))
	got := l.Replay([]string{"t"}, "00")
	// Only last 3 retained: 03, 04, 05
	if len(got) != 3 || got[0].ID != "03" || got[2].ID != "05" {
		t.Errorf("ring eviction broken: %+v", got)
	}
}

// v2-6: IsBeyondRetention — sinceID < min retained id 时返 true (gap).
func TestIsBeyondRetention_GapDetected(t *testing.T) {
	l := New(time.Hour, 3)
	// 5 events, ring 容量 3 → 03/04/05 保留, 01/02 丢
	l.Append(mk("01", "t"))
	l.Append(mk("02", "t"))
	l.Append(mk("03", "t"))
	l.Append(mk("04", "t"))
	l.Append(mk("05", "t"))

	if !l.IsBeyondRetention([]string{"t"}, "01") {
		t.Errorf("01 < min retained 03 → 应 desync")
	}
	if !l.IsBeyondRetention([]string{"t"}, "02") {
		t.Errorf("02 < min retained 03 → 应 desync")
	}
}

func TestIsBeyondRetention_WithinWindow(t *testing.T) {
	l := New(time.Hour, 100)
	l.Append(mk("10", "t"))
	l.Append(mk("11", "t"))
	l.Append(mk("12", "t"))
	if l.IsBeyondRetention([]string{"t"}, "10") {
		t.Errorf("10 == min retained → 不应 desync (>= min 即可)")
	}
	if l.IsBeyondRetention([]string{"t"}, "11") {
		t.Errorf("11 在保留窗内 → 不应 desync")
	}
}

func TestIsBeyondRetention_EmptyCursor(t *testing.T) {
	l := New(time.Hour, 100)
	l.Append(mk("01", "t"))
	if l.IsBeyondRetention([]string{"t"}, "") {
		t.Errorf("空 sinceID 不应 desync (调用方上游已保护)")
	}
}

func TestIsBeyondRetention_NoRetainedEvents(t *testing.T) {
	l := New(time.Hour, 100)
	// 无任何事件 — client 持任意 cursor 都不算 desync (没法判断 gap)
	if l.IsBeyondRetention([]string{"t"}, "99") {
		t.Errorf("ledger 空时不应 desync")
	}
}

func TestIsBeyondRetention_FutureCursor(t *testing.T) {
	l := New(time.Hour, 100)
	l.Append(mk("10", "t"))
	if l.IsBeyondRetention([]string{"t"}, "99") {
		t.Errorf("sinceID > 所有保留 id → 客户端在未来, 不算 desync")
	}
}

func TestIsBeyondRetention_PerTopic(t *testing.T) {
	l := New(time.Hour, 3)
	// t-a 满 → 03/04/05; t-b 只 1 条 → 02
	l.Append(mk("01", "t-a"))
	l.Append(mk("02", "t-a"))
	l.Append(mk("03", "t-a"))
	l.Append(mk("04", "t-a"))
	l.Append(mk("05", "t-a"))
	l.Append(mk("02", "t-b"))

	// since=01 对 t-a 而言已 desync (min=03), 对 t-b 而言不算 (min=02 <= 01? 否 01<02 也 desync)
	// 任一 topic 满足 desync 即返 true
	if !l.IsBeyondRetention([]string{"t-a"}, "01") {
		t.Errorf("t-a min=03, since=01 → desync")
	}
	if l.IsBeyondRetention([]string{"t-b"}, "02") {
		t.Errorf("t-b min=02 == since=02 → 不 desync")
	}
}

func TestIsBeyondRetention_OverRetentionTime(t *testing.T) {
	l := New(50*time.Millisecond, 100)
	// 写一条过期事件 + 一条新鲜事件; cutoff 之后 min retained 应只算新鲜
	l.byTopic["t"] = newRing(100)
	l.byTopic["t"].add(Event{ID: "01", Topic: "t", TS: time.Now().Add(-1 * time.Hour)})
	l.byTopic["t"].add(Event{ID: "10", Topic: "t", TS: time.Now()})
	// since=05 介于过期 01 + 新鲜 10 之间 — 因为 01 不算入 cutoff, min=10, 05<10 → desync
	if !l.IsBeyondRetention([]string{"t"}, "05") {
		t.Errorf("过期事件不计 cutoff, min=10 → since=05 应 desync")
	}
}

func TestGCExpired(t *testing.T) {
	l := New(10*time.Millisecond, 100)
	l.Append(mk("01", "t"))
	time.Sleep(20 * time.Millisecond)
	l.Append(mk("02", "t"))
	if removed := l.GC(); removed != 1 {
		t.Errorf("removed=%d", removed)
	}
	if got := l.Replay([]string{"t"}, "00"); len(got) != 1 || got[0].ID != "02" {
		t.Errorf("got=%+v", got)
	}
}
