// Package ingestbus subscribes to NATS subject biumind.<env>.brain.ingest.requested
// and runs the **same** ingest pipeline that biu CLI uses (packages/go-sdk/biu/ingest).
//
// Wire payload:
//
//	{
//	  "source_id":  "<uuid>",
//	  "project_id": "<uuid>",
//	  "user_id":    "<uuid>",
//	  "kind":       "markdown" | "html" | "plain",
//	  "url":        "https://...",
//	  "title":      "optional",
//	  "content":    "raw text"
//	}
//
// On success, posts the resulting PageDraft as a Wiki page through the Wiki Store
// (creates page + blocks; events automatically fire to Realtime).
package ingestbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/ingest"
	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
	"github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

type Job struct {
	SourceID  string `json:"source_id"`
	ProjectID string `json:"project_id"`
	UserID    string `json:"user_id"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}

type Bus struct {
	NATSURL  string
	Subject  string
	Provider llm.Provider
	Model    string
	Store    *store.Store
	Logger   *slog.Logger

	nc  *nats.Conn
	sub *nats.Subscription
}

// Connect subscribes to the ingest subject. Caller cancels ctx to drain.
func (b *Bus) Connect(ctx context.Context) error {
	nc, err := nats.Connect(b.NATSURL,
		nats.Name("biumind-brain-ingest"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(5*time.Second),
	)
	if err != nil {
		return fmt.Errorf("ingestbus: %w", err)
	}
	b.nc = nc
	sub, err := nc.QueueSubscribe(b.Subject, "brain-ingest", b.handle(ctx))
	if err != nil {
		return err
	}
	b.sub = sub
	b.Logger.Info("ingestbus connected", "subject", b.Subject)

	go func() {
		<-ctx.Done()
		_ = b.sub.Drain()
		_ = b.nc.Drain()
	}()
	return nil
}

func (b *Bus) IsConnected() bool {
	return b.nc != nil && b.nc.IsConnected()
}

func (b *Bus) handle(parentCtx context.Context) nats.MsgHandler {
	return func(m *nats.Msg) {
		var j Job
		if err := json.Unmarshal(m.Data, &j); err != nil {
			b.Logger.Warn("ingestbus: bad job", "err", err)
			return
		}
		ctx, cancel := context.WithTimeout(parentCtx, 3*time.Minute)
		defer cancel()
		if err := b.runJob(ctx, j); err != nil {
			b.Logger.Warn("ingestbus: job failed", "source_id", j.SourceID, "err", err)
		}
	}
}

func (b *Bus) runJob(ctx context.Context, j Job) error {
	pid, err := uuid.Parse(j.ProjectID)
	if err != nil {
		return fmt.Errorf("project_id: %w", err)
	}
	pipe := ingest.NewPipeline(b.Provider, b.Model)
	draft, err := pipe.Ingest(ctx, ingest.Source{
		Kind:    ingest.SourceKind(j.Kind),
		URL:     j.URL,
		Title:   j.Title,
		Content: j.Content,
	})
	if err != nil {
		return err
	}
	page, err := b.Store.CreatePage(ctx, store.CreatePageInput{
		ProjectID: pid,
		Title:     draft.Title,
		ActorID:   j.UserID,
	})
	if err != nil {
		return err
	}
	// P1-4: page→source 归属（CLI ingest 路径）。j.SourceID 是 wiki_sources.id。
	if j.SourceID != "" {
		if sid, perr := uuid.Parse(j.SourceID); perr == nil && sid != uuid.Nil {
			if lerr := b.Store.LinkPageSource(ctx, page.ID, sid); lerr != nil {
				b.Logger.Warn("ingestbus: link page source failed",
					"page_id", page.ID, "source_id", sid, "err", lerr)
			}
		}
	}
	for i, blk := range draft.Blocks {
		_, err := b.Store.CreateBlock(ctx, store.CreateBlockInput{
			PageID:    page.ID,
			ProjectID: pid,
			Position:  float64(i + 1),
			Type:      blk.Type,
			Content:   blk.Content,
			ActorID:   j.UserID,
		})
		if err != nil {
			return fmt.Errorf("block %d: %w", i, err)
		}
	}
	b.Logger.Info("ingestbus: job done",
		"source_id", j.SourceID, "page_id", page.ID, "blocks", len(draft.Blocks))
	return nil
}
