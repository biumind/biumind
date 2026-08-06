// Subscriber consumes worker → brain task updates.
//
// Subject (env-prefixed via biu/bus): biumind.<env>.brain.wiki.ingest.update
//
// Payload kinds (job.py KIND_* constants on the worker side):
//
//	"running"   accept the task; pending → running
//	"page"      one wiki page completed; create page + blocks then
//	            AppendResultPage. The worker doesn't know page_id at
//	            send time — brain assigns it as part of CreatePage.
//	"done"      terminal: all pages emitted successfully
//	"failed"    terminal: error string in `error`
//	"cancelled" terminal: worker observed cancel signal at boundary
//
// Idempotency: every store helper invoked here gates on the task being
// non-terminal, so a re-delivered terminal update on an already-terminal
// task is a no-op (NotFound). Duplicate "page" updates would, however,
// create a duplicate wiki page — JetStream at-least-once means we MUST
// dedupe. We do that by including the worker-generated ``page_key`` in
// each update (e.g. `path`) and checking the task's progress.seen_paths
// before creating. The worker chooses page_key as the FILE block path,
// which is unique per task by construction (the FILE-block parser
// guarantees it via path-traversal validation + ingest_parse contract).
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// Update is the wire shape worker emits. Field names mirror
// workers/wiki-llm/wiki_llm/job.py:Update.to_payload().
type Update struct {
	TaskID  string                 `json:"task_id"`
	Kind    string                 `json:"kind"`
	Path    string                 `json:"path,omitempty"`
	Title   string                 `json:"title,omitempty"`
	Index   int                    `json:"index,omitempty"`
	Content string                 `json:"content,omitempty"`
	Error   string                 `json:"error,omitempty"`
	Progress map[string]any        `json:"progress,omitempty"`
}

type Subscriber struct {
	Bus    bus.Bus
	JS     bus.JetStream // optional; preferred when wired
	Env    string
	Tasks  *Store
	Wiki   *wikistore.Store
	Logger *slog.Logger

	JSStreamName string // default BIUMIND_BRAIN
	JSDurable    string // default brain-wiki-ingest

	sub bus.Subscription
}

// Run subscribes and returns immediately. The subscription drains when
// ctx is cancelled. Errors during subscribe surface to caller; per-
// message handler errors are logged at WARN and the next message is
// processed (so one bad worker doesn't poison the consumer).
func (s *Subscriber) Run(ctx context.Context) error {
	if s.Bus == nil && s.JS == nil {
		return fmt.Errorf("wiki ingest subscriber: need either Bus or JS")
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	subject := bus.Subject(s.Env, "brain", "wiki.ingest.update")

	if s.JS != nil {
		streamName := s.JSStreamName
		if streamName == "" {
			streamName = "BIUMIND_BRAIN"
		}
		durable := s.JSDurable
		if durable == "" {
			durable = "brain-wiki-ingest"
		}
		core := s.handle(ctx)
		sub, err := s.JS.Subscribe(ctx, bus.ConsumerSpec{
			Stream:        streamName,
			Durable:       durable,
			FilterSubject: subject,
		}, func(_ context.Context, m *bus.Message) error {
			core(m)
			return nil
		})
		if err != nil {
			return fmt.Errorf("wiki ingest subscriber: js subscribe: %w", err)
		}
		s.sub = sub
		s.Logger.Info("wiki ingest subscriber connected (jetstream)",
			"stream", streamName, "durable", durable, "subject", subject)
	} else {
		sub, err := s.Bus.Subscribe(subject, s.handle(ctx))
		if err != nil {
			return fmt.Errorf("wiki ingest subscriber: %w", err)
		}
		s.sub = sub
		s.Logger.Info("wiki ingest subscriber connected (core pubsub)",
			"subject", subject)
	}

	go func() {
		<-ctx.Done()
		if s.sub != nil {
			_ = s.sub.Drain()
		}
	}()
	return nil
}

// envWire is the outer publisher.NewBus envelope: { topic, kind, payload }
// — matches what BusPublisher emits via Publish(ctx, topic, kind, payload).
// We unwrap before applying our own kind-routing.
type envWire struct {
	Topic   string         `json:"topic"`
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}

func (s *Subscriber) handle(ctx context.Context) bus.Handler {
	return func(m *bus.Message) {
		var w envWire
		if err := m.Decode(&w); err != nil {
			s.Logger.Warn("wiki ingest: bad envelope", "err", err)
			return
		}
		// Re-marshal payload to typed Update; this is cheap and avoids
		// hand-rolled map-to-struct decoding.
		raw, err := json.Marshal(w.Payload)
		if err != nil {
			s.Logger.Warn("wiki ingest: payload re-marshal", "err", err)
			return
		}
		var u Update
		if err := json.Unmarshal(raw, &u); err != nil {
			s.Logger.Warn("wiki ingest: bad update", "err", err)
			return
		}
		s.Logger.DebugContext(ctx, "wiki ingest: update received",
			"subject", m.Subject, "task_id", u.TaskID, "kind", u.Kind,
			"path", u.Path, "bytes", len(raw))
		if err := s.apply(ctx, &u); err != nil {
			s.Logger.Warn("wiki ingest: apply failed",
				"task_id", u.TaskID, "kind", u.Kind, "err", err)
		}
	}
}

// apply routes one decoded Update to the right store method. Errors
// from store mutations bubble up so the bus handler can log them.
func (s *Subscriber) apply(ctx context.Context, u *Update) error {
	taskID, err := uuid.Parse(u.TaskID)
	if err != nil {
		return fmt.Errorf("bad task_id: %w", err)
	}

	switch u.Kind {
	case "running":
		err := s.Tasks.MarkRunning(ctx, taskID)
		if errors.Is(err, ErrNotFound) {
			// Already terminal (e.g. user cancelled while worker was
			// starting). Silent — running update is non-essential.
			return nil
		}
		return err

	case "page":
		return s.applyPage(ctx, taskID, u)

	case "done":
		err := s.Tasks.MarkTerminal(ctx, taskID, StatusDone, "")
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err

	case "failed":
		err := s.Tasks.MarkTerminal(ctx, taskID, StatusFailed, u.Error)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err

	case "cancelled":
		err := s.Tasks.MarkTerminal(ctx, taskID, StatusCancelled, "")
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err

	default:
		return fmt.Errorf("unknown kind %q", u.Kind)
	}
}

// applyPage creates the wiki page + blocks for one emitted FILE block,
// then records the new page_id on the task. Idempotent: if a previous
// delivery already produced a page for this `path`, we detect it via
// the task's progress.seen_paths and skip the re-create.
func (s *Subscriber) applyPage(ctx context.Context, taskID uuid.UUID, u *Update) error {
	if u.Path == "" {
		return fmt.Errorf("page update missing path")
	}

	task, err := s.Tasks.Get(ctx, taskID)
	if err != nil {
		return fmt.Errorf("task lookup: %w", err)
	}
	if IsTerminal(task.Status) {
		// Late delivery for a terminal task — drop. Keeping this path
		// silent prevents a cancelled task from being retroactively
		// "filled in" with pages the user has already declared abandoned.
		return nil
	}

	// Idempotency: did we already create a page for this path on this
	// task? progress is a free-form jsonb; we co-opt a `seen_paths`
	// list to track it.
	seen := seenPaths(task.Progress)
	for _, p := range seen {
		if p == u.Path {
			return nil // duplicate delivery, no-op
		}
	}

	title := strings.TrimSpace(u.Title)
	if title == "" {
		title = pathBasename(u.Path)
	}

	page, err := s.Wiki.CreatePage(ctx, wikistore.CreatePageInput{
		ProjectID: task.ProjectID,
		Title:     title,
		BodyMd:    u.Content,
		ActorID:   "wiki-llm-worker",
	})
	if err != nil {
		return fmt.Errorf("create page: %w", err)
	}

	// P1-4: 记录 page→source 归属（webclip 抓取的页源自 wiki_sources 行），
	// 供 relevance source-overlap 信号。upload 行同理（Phase 3 parser 后）。
	if task.SourceID != nil && *task.SourceID != uuid.Nil {
		if lerr := s.Wiki.LinkPageSource(ctx, page.ID, *task.SourceID); lerr != nil {
			s.Logger.Warn("wiki ingest: link page source failed",
				"page_id", page.ID, "source_id", *task.SourceID, "err", lerr)
		}
	}

	// §⑤ Path C：body_md 权威。CreatePage 带 BodyMd 时事务内 mdparse 投影 blocks（heading/
	// text/list/code），下游 chunks/embed/graph 无感吃 blocks。原先的 per-block CreateBlock
	// 循环已下沉到 store.insertBlocksTx，这里只负责 page 创建 + source 归属。

	// Update task: append page_id + record the path so re-deliveries
	// dedupe. progress carries the full trail for UI consumption.
	progress := mergeProgress(task.Progress, u.Path, page.ID, len(seen)+1)
	if err := s.Tasks.AppendResultPage(ctx, taskID, page.ID, progress); err != nil {
		// Page exists but task didn't update — log loudly; a re-delivery
		// would now skip via seen_paths but this divergence is unhealthy.
		return fmt.Errorf("append result page: %w", err)
	}
	s.Logger.Info("wiki ingest: page added",
		"task_id", taskID, "page_id", page.ID, "path", u.Path, "title", title)
	return nil
}

// ─── helpers ───────────────────────────────────────────────────

func seenPaths(progress map[string]any) []string {
	if progress == nil {
		return nil
	}
	v, ok := progress["seen_paths"]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mergeProgress(prev map[string]any, newPath string, pageID uuid.UUID, total int) map[string]any {
	out := map[string]any{}
	for k, v := range prev {
		out[k] = v
	}
	seen := seenPaths(prev)
	seen = append(seen, newPath)
	asAny := make([]any, 0, len(seen))
	for _, s := range seen {
		asAny = append(asAny, s)
	}
	out["seen_paths"] = asAny
	out["pages_total"] = total
	out["last_page_id"] = pageID.String()
	out["last_path"] = newPath
	return out
}

func pathBasename(p string) string {
	// Strip directory and extension; "wiki/concepts/rope.md" → "rope".
	idx := strings.LastIndex(p, "/")
	if idx >= 0 {
		p = p[idx+1:]
	}
	if dot := strings.LastIndex(p, "."); dot > 0 {
		p = p[:dot]
	}
	if p == "" {
		return "untitled"
	}
	return p
}
