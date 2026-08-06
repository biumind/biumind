// Package ingestcmd implements `biu ingest <file>` — runs the shared ingest
// pipeline locally with the user's currently-configured Provider and outputs
// either:
//
//	JSON ready to POST to /v1/wiki/projects/{id}/pages
//	or human-readable summary
//
// Mode A/B: pipeline calls model-relay. Mode C: pipeline calls Anthropic / OpenAI direct.
package ingestcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/client/wiki"
	"github.com/biumind/biumind/packages/go-sdk/biu/ingest"
	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

type Options struct {
	Provider llm.Provider
	Model    string
	Path     string
	URL      string
	Title    string
	Tags     []string
	JSON     bool // emit machine-readable JSON instead of pretty summary
	Out      io.Writer

	// Commit options — when CommitWiki is non-nil, push the PageDraft to Wiki
	// after successful ingest. The page+block IDs returned by Wiki are added
	// to the JSON output (or printed in pretty mode).
	CommitWiki *wiki.Client
	ProjectID  string // resolved id (or name; we resolve before commit)
}

func Run(ctx context.Context, opt Options) error {
	if opt.Out == nil {
		opt.Out = os.Stdout
	}
	if opt.Provider == nil {
		return fmt.Errorf("ingest: no provider configured")
	}
	if opt.Path == "" {
		return fmt.Errorf("ingest: path required")
	}
	raw, err := os.ReadFile(opt.Path)
	if err != nil {
		return err
	}
	kind := detectKind(opt.Path)
	src := ingest.Source{
		Kind:    kind,
		URL:     orDefault(opt.URL, "file://"+absOrPath(opt.Path)),
		Title:   opt.Title,
		Content: string(raw),
		Metadata: map[string]string{
			"path": opt.Path,
		},
	}
	pipe := ingest.NewPipeline(opt.Provider, opt.Model)

	fmt.Fprintf(os.Stderr, "[biu] ingesting %s (%s) via provider=%s model=%s\n",
		opt.Path, kind, opt.Provider.Name(), orDefault(opt.Model, "claude-sonnet-4-6"))

	draft, err := pipe.Ingest(ctx, src)
	if err != nil {
		return err
	}

	// Optional: push to Wiki API.
	var commit *commitResult
	if opt.CommitWiki != nil {
		commit, err = doCommit(ctx, opt.CommitWiki, opt.ProjectID, draft)
		if err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[biu] committed: project=%s page=%s blocks=%d\n",
			commit.ProjectID, commit.PageID, commit.BlockCount)
	}

	if opt.JSON {
		out := map[string]any{"draft": draft}
		if commit != nil {
			out["commit"] = commit
		}
		js, _ := json.MarshalIndent(out, "", "  ")
		_, _ = opt.Out.Write(js)
		_, _ = opt.Out.Write([]byte{'\n'})
		return nil
	}
	prettyPrint(opt.Out, draft)
	if commit != nil {
		fmt.Fprintf(opt.Out, "\n✓ committed page %s (project=%s, %d blocks)\n",
			commit.PageID, commit.ProjectID, commit.BlockCount)
	}
	return nil
}

type commitResult struct {
	ProjectID  string `json:"project_id"`
	PageID     string `json:"page_id"`
	BlockCount int    `json:"block_count"`
}

func doCommit(ctx context.Context, wc *wiki.Client, projectIDOrName string, draft *ingest.PageDraft) (*commitResult, error) {
	proj, err := wc.ResolveProject(ctx, projectIDOrName)
	if err != nil {
		return nil, err
	}
	page, err := wc.CreatePage(ctx, proj.ID, wiki.CreatePageInput{Title: draft.Title})
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	for i, b := range draft.Blocks {
		_, err := wc.CreateBlock(ctx, proj.ID, page.ID, wiki.CreateBlockInput{
			Type:     b.Type,
			Position: float64(i + 1),
			Content:  b.Content,
		})
		if err != nil {
			return nil, fmt.Errorf("create block %d: %w", i, err)
		}
	}
	return &commitResult{
		ProjectID:  proj.ID,
		PageID:     page.ID,
		BlockCount: len(draft.Blocks),
	}, nil
}

func prettyPrint(w io.Writer, d *ingest.PageDraft) {
	fmt.Fprintf(w, "Title: %s\n", d.Title)
	if s, _ := d.Frontmatter["summary"].(string); s != "" {
		fmt.Fprintf(w, "Summary: %s\n", s)
	}
	if tags, ok := d.Outline.Tags, true; ok && len(tags) > 0 {
		fmt.Fprintf(w, "Tags: %s\n", strings.Join(tags, ", "))
	}
	fmt.Fprintf(w, "\nBlocks (%d):\n", len(d.Blocks))
	for i, b := range d.Blocks {
		switch b.Type {
		case "heading":
			lvl := 2
			if v, ok := b.Content["level"].(float64); ok {
				lvl = int(v)
			}
			fmt.Fprintf(w, "  %d. %s %s\n", i+1, strings.Repeat("#", lvl), b.Content["text"])
		case "text":
			fmt.Fprintf(w, "  %d. %s\n", i+1, truncate(stringOf(b.Content["text"]), 200))
		case "list":
			items, _ := b.Content["items"].([]any)
			fmt.Fprintf(w, "  %d. list (%d items)\n", i+1, len(items))
		case "code":
			lang, _ := b.Content["language"].(string)
			fmt.Fprintf(w, "  %d. code [%s]\n", i+1, lang)
		default:
			fmt.Fprintf(w, "  %d. %s\n", i+1, b.Type)
		}
	}
}

func detectKind(path string) ingest.SourceKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return ingest.KindMarkdown
	case ".html", ".htm":
		return ingest.KindHTML
	default:
		return ingest.KindPlainText
	}
}

func orDefault(s, fb string) string {
	if s == "" {
		return fb
	}
	return s
}

func absOrPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
