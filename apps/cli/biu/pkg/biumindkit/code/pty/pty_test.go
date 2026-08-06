package pty

import (
	"sync"
	"testing"
	"time"
)

// 首字节直发:空闲态收到一段输出应立即 flush,不等 flushInterval。
func TestBatcherFirstByteDirect(t *testing.T) {
	in := make(chan []byte, 4)
	got := make(chan []byte, 8)
	go runBatcher("p1", in, func(_ string, data []byte) {
		cp := make([]byte, len(data))
		copy(cp, data)
		got <- cp
	})
	defer close(in)

	start := time.Now()
	in <- []byte("x")
	select {
	case d := <-got:
		if string(d) != "x" {
			t.Fatalf("got %q, want %q", d, "x")
		}
		if elapsed := time.Since(start); elapsed >= flushInterval {
			t.Fatalf("first byte took %v, expected immediate (< %v)", elapsed, flushInterval)
		}
	case <-time.After(flushInterval):
		t.Fatal("first byte not flushed immediately (waited a full flushInterval)")
	}
}

// 高频突发:大量小块快速涌入应合帧成远少于块数的 onChunk 调用,且字节零丢失。
func TestBatcherCoalescesBurst(t *testing.T) {
	const chunks = 50
	const each = 100
	in := make(chan []byte)
	var mu sync.Mutex
	var calls, total int
	done := make(chan struct{})
	go func() {
		runBatcher("p1", in, func(_ string, data []byte) {
			mu.Lock()
			calls++
			total += len(data)
			mu.Unlock()
		})
		close(done)
	}()

	for i := 0; i < chunks; i++ {
		in <- make([]byte, each)
	}
	close(in)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if total != chunks*each {
		t.Fatalf("byte loss: got %d, want %d", total, chunks*each)
	}
	if calls >= chunks {
		t.Fatalf("no coalescing: %d onChunk calls for %d chunks", calls, chunks)
	}
}

// channel 关闭时残余批必须被 flush,且顺序保持(不丢尾段输出)。
func TestBatcherFlushesOnClose(t *testing.T) {
	in := make(chan []byte, 4)
	var mu sync.Mutex
	var buf []byte
	done := make(chan struct{})
	go func() {
		runBatcher("p1", in, func(_ string, data []byte) {
			mu.Lock()
			buf = append(buf, data...)
			mu.Unlock()
		})
		close(done)
	}()

	in <- []byte("a")
	in <- []byte("b")
	in <- []byte("c")
	close(in)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if string(buf) != "abc" {
		t.Fatalf("got %q, want %q (bytes lost or reordered across close)", buf, "abc")
	}
}

// maxBatchBytes 阈值:窗口内累积超阈值应即时 flush,避免单帧过大。
func TestBatcherFlushesAtMaxBytes(t *testing.T) {
	in := make(chan []byte)
	var mu sync.Mutex
	var maxSeen, total int
	done := make(chan struct{})
	go func() {
		runBatcher("p1", in, func(_ string, data []byte) {
			mu.Lock()
			if len(data) > maxSeen {
				maxSeen = len(data)
			}
			total += len(data)
			mu.Unlock()
		})
		close(done)
	}()

	// 推 ~3×maxBatchBytes 的数据(分成 1KB 小块,快于 flushInterval)。
	const block = 1024
	n := (maxBatchBytes*3)/block + 1
	for i := 0; i < n; i++ {
		in <- make([]byte, block)
	}
	close(in)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if total != n*block {
		t.Fatalf("byte loss: got %d, want %d", total, n*block)
	}
	// 单帧不应远超 maxBatchBytes(留一块容差:直发首块 + 阈值触发).
	if maxSeen > maxBatchBytes+block {
		t.Fatalf("a frame exceeded maxBatchBytes: maxSeen=%d, cap=%d", maxSeen, maxBatchBytes)
	}
}
