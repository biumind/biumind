// `biu app run` — local dev server for App authors (M17.2).
//
//	biu app run --dev [--source DIR] [--addr 127.0.0.1:7099]
//	             [--mock fixtures/]
//	             [--no-subproc]                # view-only Apps don't need go run
//
// Loop:
//
//  1. Parse + validate manifest.yaml (errors print and abort)
//  2. Bind 127.0.0.1:7099 — desktop client polls /v1/dev/health and
//     pulls the app list from /v1/dev/apps
//  3. Start subprocess `go run ./...` from --source (skipped under
//     --no-subproc or when --mock is set)
//  4. Watch manifest.yaml + *.go → on change: revalidate manifest,
//     restart subprocess
//  5. Read stdin: 'r' = manual restart, 'q' = quit, 'h' = help
//
// Invoke routing (when client / agent calls /v1/dev/apps/<slug>/invoke):
//
//   - If --mock fixtures/<action>.json exists, serve it
//   - Else, return 503 "subproc_invoke_not_yet_wired" — proxying into
//     the Go subprocess requires a stable RPC convention which lands
//     in M14 (Container form). For M17, mocks cover the dev loop and
//     real invoke goes through `biu app pack` + install path.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/devserver"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/spf13/cobra"
)

func newAppRunCmd() *cobra.Command {
	var (
		devMode   bool
		source    string
		addr      string
		mockDir   string
		noSubproc bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run an App locally for development",
		Long: "Spawn the App subprocess + bind a local dev server\n" +
			"that the desktop client picks up under \"开发中\" panel.\n\n" +
			"Press 'r' to restart the subprocess, 'q' to quit.",
		RunE: func(_ *cobra.Command, _ []string) error {
			if !devMode {
				return clierr.Newf("biu app run", "%s",
					"only --dev mode is supported in v2.0; pass --dev")
			}
			if source == "" {
				source = "."
			}
			abs, err := filepath.Abs(source)
			if err != nil {
				return clierr.Wrapf("biu app run", err, "%s", err.Error())
			}
			return runDev(devOpts{
				SourceDir: abs,
				Addr:      addr,
				MockDir:   mockDir,
				NoSubproc: noSubproc || mockDir != "",
			})
		},
	}
	cmd.Flags().BoolVar(&devMode, "dev", false, "enable dev mode (required)")
	cmd.Flags().StringVar(&source, "source", "", "App source directory (default: cwd)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7099", "dev server bind address")
	cmd.Flags().StringVar(&mockDir, "mock", "", "fixtures dir; serve <action>.json instead of subproc")
	cmd.Flags().BoolVar(&noSubproc, "no-subproc", false, "skip the `go run` subprocess (view-only Apps)")
	return cmd
}

type devOpts struct {
	SourceDir string
	Addr      string
	MockDir   string
	NoSubproc bool
}

func runDev(opts devOpts) error {
	// 1. Load + validate manifest.
	manifestPath := filepath.Join(opts.SourceDir, "manifest.yaml")
	app, err := loadDevApp(manifestPath, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ %s v%s — %s\n",
		app.Identifier, app.Version, app.Title)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 2. Build invoker and dev server.
	invoker := newDevInvoker(opts)
	srv := devserver.New(invoker)
	srv.SetApps([]devserver.DevApp{*app})

	bound, errCh, err := srv.Start(ctx, opts.Addr)
	if err != nil {
		return clierr.Wrapf("biu app run", err, "%s", err.Error())
	}
	fmt.Fprintf(os.Stderr, "▶ dev server listening on http://%s\n", bound)
	fmt.Fprintf(os.Stderr, "  GET /v1/dev/apps     — visible to local desktop client\n")
	fmt.Fprintf(os.Stderr, "  GET /v1/dev/events   — SSE state stream\n")

	// 3. Subprocess (skipped for view-only / mock mode).
	var procMgr *devserver.SubprocMgr
	if !opts.NoSubproc {
		procMgr = devserver.NewSubprocMgr(devserver.SubprocConfig{
			Dir:       opts.SourceDir,
			LogPrefix: fmt.Sprintf("[%s] ", app.Identifier),
			OnLog: func(line string) {
				fmt.Fprint(os.Stderr, line)
				srv.PushEvent(devserver.Event{
					Kind: devserver.EventSubprocLog, Slug: app.Slug,
					Message: strings.TrimRight(line, "\n"),
				})
			},
		})
		if err := procMgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ subprocess start failed: %v\n", err)
		} else {
			srv.PushEvent(devserver.Event{
				Kind: devserver.EventSubprocStarted, Slug: app.Slug,
				Detail: map[string]any{"pid": procMgr.PID()},
			})
		}
	} else if opts.MockDir != "" {
		fmt.Fprintf(os.Stderr, "▶ mock mode: serving fixtures from %s\n", opts.MockDir)
	}

	// 4. File watcher — manifest + Go source.
	watcher := &devserver.Watcher{
		Paths:    []string{manifestPath, opts.SourceDir},
		Exts:     []string{".yaml", ".go"},
		Interval: 500 * time.Millisecond,
	}
	go watcher.Run(ctx, func(changed []string) {
		// Reload manifest on every change; cheap and predictable.
		newApp, err := loadDevApp(manifestPath, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ manifest reload: %v\n", err)
			srv.PushEvent(devserver.Event{
				Kind: devserver.EventValidationError, Slug: app.Slug,
				Message: err.Error(),
			})
			return
		}
		srv.SetApps([]devserver.DevApp{*newApp})
		srv.PushEvent(devserver.Event{
			Kind: devserver.EventManifestReloaded, Slug: newApp.Slug,
			Detail: map[string]any{"changed": changed, "version": newApp.Version},
		})
		fmt.Fprintf(os.Stderr, "↻ manifest reloaded (changed: %d)\n", len(changed))

		// If Go source changed and we have a subprocess, restart it.
		if procMgr != nil && hasGoChange(changed) {
			fmt.Fprintln(os.Stderr, "↻ restarting subprocess (Go source changed)")
			if err := procMgr.Restart(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "✗ restart failed: %v\n", err)
			} else {
				srv.PushEvent(devserver.Event{
					Kind: devserver.EventSubprocStarted, Slug: newApp.Slug,
					Detail: map[string]any{"pid": procMgr.PID()},
				})
			}
		}
	})

	// 5. Stdin keystrokes.
	go readStdinKeys(ctx, cancel, procMgr, srv, app.Slug)

	fmt.Fprintln(os.Stderr, "  press 'r' to restart subprocess, 'q' to quit")

	select {
	case <-ctx.Done():
		// graceful shutdown
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "dev server error: %v\n", err)
		}
	}

	if procMgr != nil {
		_ = procMgr.Stop()
	}
	fmt.Fprintln(os.Stderr, "✓ shutdown")
	return nil
}

// loadDevApp parses manifest.yaml + validates it, returning the
// devserver-shaped DevApp ready to publish. Errors here are surfaced
// to the user; reload paths just log them and keep the previous app
// list rather than dropping registrations.
func loadDevApp(manifestPath string, opts devOpts) (*devserver.DevApp, error) {
	m, err := biuapp.LoadManifest(manifestPath)
	if err != nil {
		return nil, clierr.Wrapf("biu app run", err, "load manifest: %s", err.Error())
	}
	if err := biuapp.Validate(m); err != nil {
		return nil, clierr.Wrapf("biu app run", err, "validate: %s", err.Error())
	}

	raw, _ := os.ReadFile(manifestPath)
	manifestJSON, _ := json.Marshal(m)
	var asMap map[string]any
	_ = json.Unmarshal(manifestJSON, &asMap)

	return &devserver.DevApp{
		Slug:        m.Slug(),
		Identifier:  m.Slug(),
		Title:       m.DisplayName(),
		Version:     m.Version,
		Manifest:    asMap,
		ManifestRaw: string(raw),
		SourcePath:  opts.SourceDir,
		Mock:        opts.MockDir != "",
	}, nil
}

func hasGoChange(changed []string) bool {
	for _, p := range changed {
		if strings.HasSuffix(p, ".go") {
			return true
		}
	}
	return false
}

// ─── Invoker — fixture lookup or fall-through to subproc ───

func newDevInvoker(opts devOpts) devserver.Invoker {
	return &devInvoker{opts: opts}
}

type devInvoker struct {
	opts devOpts
	mu   sync.Mutex
}

func (d *devInvoker) Invoke(_ context.Context, slug, action string, input json.RawMessage) (any, error) {
	if d.opts.MockDir != "" {
		return d.serveFixture(slug, action, input)
	}
	// Subprocess invoke: not implemented for v2.0 — App authors should
	// use --mock during view-iteration, then `biu app pack` + install
	// to test real action paths. We surface a clean message rather
	// than silently 404 so authors aren't confused.
	return nil, fmt.Errorf("subproc invoke not yet wired; use --mock fixtures/ for view iteration")
}

func (d *devInvoker) serveFixture(slug, action string, _ json.RawMessage) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Try {action}.json then {slug}.{action}.json.
	candidates := []string{
		filepath.Join(d.opts.MockDir, action+".json"),
		filepath.Join(d.opts.MockDir, slug+"."+action+".json"),
	}
	for _, p := range candidates {
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var out any
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("fixture %s: invalid JSON: %w", p, err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("no fixture for action %q (looked in %s)", action, d.opts.MockDir)
}

// ─── Stdin: 'r' restart, 'q' quit ─────────────────────────

func readStdinKeys(ctx context.Context, cancel context.CancelFunc, mgr *devserver.SubprocMgr, srv *devserver.Server, slug string) {
	r := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		switch b {
		case 'r', 'R':
			if mgr == nil {
				fmt.Fprintln(os.Stderr, "  (no subprocess running — nothing to restart)")
				continue
			}
			fmt.Fprintln(os.Stderr, "↻ manual restart")
			if err := mgr.Restart(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "✗ %v\n", err)
				continue
			}
			srv.PushEvent(devserver.Event{
				Kind: devserver.EventSubprocStarted, Slug: slug,
				Detail: map[string]any{"pid": mgr.PID(), "trigger": "manual"},
			})
		case 'q', 'Q':
			fmt.Fprintln(os.Stderr, "  quitting")
			cancel()
			return
		case 'h', 'H', '?':
			fmt.Fprintln(os.Stderr, "  r=restart  q=quit  h=help")
		}
	}
}
