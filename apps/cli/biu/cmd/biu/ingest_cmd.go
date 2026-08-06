// `biu ingest <file>` — single-shot pipeline that parses a local
// file, runs the two-step CoT, and (with --commit) pushes a Wiki
// page. Pre-engine path: bypasses the agent loop because there's no
// model decision to make beyond the canonical ingest sequence.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/biumind/biumind/apps/cli/biu/cmd/biu/wiring"
	"github.com/biumind/biumind/apps/cli/biu/internal/client/wiki"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/ingestcmd"
	"github.com/spf13/cobra"
)

func newIngestCmd(f *rootFlags) *cobra.Command {
	var (
		url       string
		title     string
		jsonOut   bool
		commit    bool
		project   string
		wikiURL   string
		wikiToken string
	)
	c := &cobra.Command{
		Use:   "ingest <file>",
		Short: "Parse a local file, run two-step CoT, optionally commit to Wiki",
		Long: `Reads a local file (markdown / html / plain text), parses it,
runs the two-step CoT pipeline against the currently configured Provider,
and prints the resulting PageDraft.

With --commit --project <id|name>, the draft is pushed into Wiki via the
Wiki API (creates page + blocks).

Examples:
  biu ingest README.md
  biu ingest --json notes.md > page.json
  biu ingest --commit --project Notes README.md
  biu ingest --mode direct --commit --project Research paper.txt`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			cfg, _, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}
			provider, mode, err := wiring.BuildProvider(cfg, f.wiringFlags())
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[biu] mode=%s provider=%s\n", mode, provider.Name())

			opts := ingestcmd.Options{
				Provider: provider,
				Model:    firstNonEmpty(f.model, cfg.Default.Model),
				Path:     args[0],
				URL:      url,
				Title:    title,
				JSON:     jsonOut,
			}
			if commit {
				if project == "" {
					return fmt.Errorf("--commit requires --project <id|name>")
				}
				wurl := firstNonEmpty(wikiURL, f.relayURL, os.Getenv("BIUMIND_MODEL_RELAY_URL"), cfg.Relay.Endpoint)
				wtok := firstNonEmpty(wikiToken, f.token, os.Getenv("BIUMIND_TOKEN"), cfg.Relay.VirtualKey)
				if wurl == "" || wtok == "" {
					return fmt.Errorf("--commit needs Wiki URL + token (set [model-relay] or use --wiki-url / --wiki-token)")
				}
				opts.CommitWiki = wiki.New(wurl, wtok)
				opts.ProjectID = project
			}
			return ingestcmd.Run(ctx, opts)
		},
	}
	c.Flags().StringVar(&url, "url", "", "source URL (defaults to file://...)")
	c.Flags().StringVar(&title, "title", "", "override page title")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit PageDraft JSON to stdout")
	c.Flags().BoolVar(&commit, "commit", false, "POST PageDraft to Wiki API")
	c.Flags().StringVar(&project, "project", "", "project id or name (required with --commit)")
	c.Flags().StringVar(&wikiURL, "wiki-url", "", "override Wiki API endpoint (defaults to model-relay.endpoint)")
	c.Flags().StringVar(&wikiToken, "wiki-token", "", "override Wiki API bearer token")
	return c
}
