// Session capture and replay.
//
// A live agent session is just a `<-chan Event`. To make it replayable
// we tee the channel into an append-only JSONL file. To play it back we
// produce another `<-chan Event` from that file, indistinguishable from
// the live shape so the same renderer code drives both.
//
// File format: one JSON-encoded [Event] per line. Future migrations can
// add fields safely (renderers ignore unknown keys).

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Recorder tees a live event channel to a JSONL file. The returned
// channel emits the same events in the same order; the writer is only
// flushed when the source channel closes (and Close() returns).
type Recorder struct {
	w        io.WriteCloser
	bw       *bufio.Writer
	mu       sync.Mutex
	wroteEnd bool
}

// NewRecorder opens path for append-only writes. Caller MUST Close().
func NewRecorder(path string) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	return &Recorder{
		w:  f,
		bw: bufio.NewWriter(f),
	}, nil
}

// Tee wraps `in` so each event is mirrored to the file before being
// passed through to the returned channel.
//
// The recorder takes ownership of draining `in`; the caller must consume
// the returned channel until close.
func (r *Recorder) Tee(in <-chan Event) <-chan Event {
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		for ev := range in {
			r.write(ev)
			out <- ev
		}
	}()
	return out
}

// WriteEvent writes a single event without going through Tee. Useful for
// adapters that want to record EventSystem frames they synthesise.
func (r *Recorder) WriteEvent(ev Event) error {
	return r.write(ev)
}

func (r *Recorder) write(ev Event) error {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.bw.Write(b); err != nil {
		return err
	}
	if err := r.bw.WriteByte('\n'); err != nil {
		return err
	}
	// Flush eagerly so a `tail -f session.jsonl` works for live debugging.
	return r.bw.Flush()
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.bw.Flush(); err != nil {
		return err
	}
	return r.w.Close()
}

// Replay reads a recorded session JSONL and produces an Event channel.
// Cancel ctx to stop; the channel always closes when the file ends or
// ctx is cancelled.
//
// `realtime=true` honours the original timestamps (sleeps between
// events to match the original cadence). false = drain as fast as the
// reader can decode, useful for tests and "skip to end" UI.
func Replay(ctx context.Context, path string, realtime bool) (<-chan Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		defer f.Close()
		dec := json.NewDecoder(bufio.NewReader(f))
		var prev time.Time
		for dec.More() {
			var ev Event
			if err := dec.Decode(&ev); err != nil {
				out <- Event{
					Type:      EventError,
					Timestamp: time.Now().UTC(),
					Content:   "replay decode: " + err.Error(),
				}
				return
			}
			if realtime && !prev.IsZero() && !ev.Timestamp.IsZero() {
				delay := ev.Timestamp.Sub(prev)
				if delay > 0 && delay < 30*time.Second {
					select {
					case <-ctx.Done():
						return
					case <-time.After(delay):
					}
				}
			}
			prev = ev.Timestamp
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}()
	return out, nil
}
