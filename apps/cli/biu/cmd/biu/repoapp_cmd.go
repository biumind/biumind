// `biu repo-app` — local runner for GitHub repo apps (TechPlan §3,
// DevPlan M1.7–M1.13).
//
//	biu repo-app install <github-url|owner/repo> [--ref v1.2.3]
//	biu repo-app ensure <name> [--env KEY=VALUE]...   # idempotent: install if missing, start if stopped
//	biu repo-app list
//	biu repo-app run <name> [--port 0] [--env KEY=VALUE]...
//	biu repo-app stop <name>
//	biu repo-app logs <name> [-f]
//	biu repo-app update <name> [--ref ...]
//	biu repo-app remove <name>
//	biu repo-app doctor             # runtime probe report
//
// macOS/Linux only in M1. `ensure`/`run` announce the resolved URL on
// stdout as BIU_REPOAPP_URL=http://127.0.0.1:<port> (same contract as
// `biu serve`'s BIU_BRIDGE_URL) once the health check passes — the
// Flutter client parses that line.

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/repoapp"
	"github.com/spf13/cobra"
)

func newRepoAppCmd(f *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo-app",
		Short: "Run GitHub open-source projects as local web services",
	}
	cmd.AddCommand(
		newRepoAppInstallCmd(f),
		newRepoAppEnsureCmd(f),
		newRepoAppListCmd(f),
		newRepoAppRunCmd(f),
		newRepoAppStopCmd(f),
		newRepoAppLogsCmd(f),
		newRepoAppUpdateCmd(f),
		newRepoAppRemoveCmd(f),
		newRepoAppDoctorCmd(f),
		newRepoAppServeStaticCmd(),
	)
	return cmd
}

// newRepoAppServeStaticCmd is the built-in static file server used as
// the StartCmd of static-site instances (see staticDocRoot): hidden
// plumbing, not a user-facing command. It runs in the foreground — the
// runner detaches it like any other start command.
func newRepoAppServeStaticCmd() *cobra.Command {
	var dir, addr string
	cmd := &cobra.Command{
		Use:    "serve-static",
		Short:  "Serve a directory over HTTP (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				return clierr.Newf("biu repo-app serve-static", "--dir is required")
			}
			// #nosec G114 — addr is always 127.0.0.1:<port> by construction
			// (see detect.go staticDocRoot); FileServer serves the instance
			// doc root on loopback only.
			if err := http.ListenAndServe(addr, http.FileServer(http.Dir(dir))); err != nil {
				return clierr.Wrapf("biu repo-app serve-static", err, "serve %s on %s", dir, addr)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "directory to serve")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	return cmd
}

// repoAppStore resolves the instance store: [repo-app].cache_dir from
// config, else the default root (~/.biumind/repo-apps or
// $BIU_REPOAPP_ROOT).
func repoAppStore(f *rootFlags) (*repoapp.Store, error) {
	cfg, _, err := config.Load(f.cfgPath)
	if err != nil {
		return nil, err
	}
	return repoapp.NewStore(cfg.RepoApp.CacheDir)
}

// repoAppLogf is the install/update progress sink — stderr only, so
// stdout stays clean for the URL announcement.
func repoAppLogf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[repo-app] "+format+"\n", args...)
}

// errUnsupportedPlatform is the single Windows gate message.
func errUnsupportedPlatform() error {
	return clierr.Newf("biu repo-app", "%s", "not supported on Windows yet (M1 is macOS/Linux only)")
}

// ─── install ──────────────────────────────────────────────

func newRepoAppInstallCmd(f *rootFlags) *cobra.Command {
	var ref string
	cmd := &cobra.Command{
		Use:   "install <github-url|owner/repo>",
		Short: "Clone a GitHub project, detect its stack, install dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !repoapp.Supported() {
				return errUnsupportedPlatform()
			}
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app install", err, "%s", err.Error())
			}
			if _, err := doRepoAppInstall(cmd.Context(), store, args[0], ref); err != nil {
				return clierr.Wrapf("biu repo-app install", err, "%s", err.Error())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "git branch/tag to install (default: the repo's default branch)")
	return cmd
}

// doRepoAppInstall performs clone → detect → bootstrap → deps and
// records runtime.json. Shared by `install` and `ensure` (which
// auto-installs when the name is actually a repo arg). Returns the
// instance slug.
func doRepoAppInstall(ctx context.Context, store *repoapp.Store, arg, ref string) (string, error) {
	slug, cloneURL, err := repoapp.ParseRepoArg(arg)
	if err != nil {
		return "", err
	}
	if store.Exists(slug) {
		return "", fmt.Errorf("%q is already installed — use `biu repo-app update %s` to move it to a new ref", slug, slug)
	}
	inst, err := store.Create(slug)
	if err != nil {
		return "", err
	}

	repoAppLogf("cloning %s (ref=%s) ...", cloneURL, orDefault(ref, "default branch"))
	sha, err := repoapp.CloneOrFetch(ctx, cloneURL, ref, inst.RepoDir())
	if err != nil {
		return "", err
	}
	repoAppLogf("checked out %s", sha[:12])

	plan, err := repoapp.PlanStack(inst.RepoDir(), slug)
	if err != nil {
		return "", err
	}
	repoAppLogf("detected stack=%s pm=%s entry=%s", plan.Stack, orDefault(plan.PackageManager, "—"), plan.Entry)

	extras, err := repoAppBootstrapAndDeps(ctx, inst, plan)
	if err != nil {
		return "", err
	}

	ri := &repoapp.RuntimeInfo{
		RepoURL:        cloneURL,
		Ref:            ref,
		InstalledSHA:   sha,
		Stack:          string(plan.Stack),
		PackageManager: plan.PackageManager,
		StartCmd:       plan.StartCmd,
		HealthPath:     "/",
		PathExtra:      extras,
	}
	if err := repoapp.SaveRuntime(inst.Dir, ri); err != nil {
		return "", err
	}
	repoAppLogf("✓ installed %s → %s", slug, inst.Dir)
	repoAppLogf("  next: biu repo-app run %s", slug)
	return slug, nil
}

// repoAppBootstrapAndDeps probes runtimes, bootstraps whatever is
// missing (uv/mise/toolchains), then installs project dependencies.
// It returns the PATH entries (managed toolchains, project venv) the
// CALLER must persist into runtime.json — install writes runtime.json
// only after this returns, so persisting inside here would be a no-op
// (smoke-found bug: python instances started without .venv/bin on
// PATH, `python: command not found`).
func repoAppBootstrapAndDeps(ctx context.Context, inst repoapp.Instance, plan repoapp.StackPlan) ([]string, error) {
	probes := map[string]repoapp.Probe{}
	for _, p := range repoapp.Doctor(ctx) {
		probes[p.Name] = p
	}
	boot := repoapp.DecideBootstrap(plan, probes)
	if boot.DockerMissing {
		return nil, fmt.Errorf("this project ships a Dockerfile but Docker was not found — please install Docker first: https://docs.docker.com/get-docker/")
	}
	for _, note := range boot.Notes {
		repoAppLogf("%s", note)
	}
	pathExtra, err := repoapp.Bootstrap(ctx, boot, repoAppLogf)
	if err != nil {
		return nil, err
	}
	depExtra, err := repoapp.InstallDeps(ctx, inst, plan, pathExtra, repoAppLogf)
	if err != nil {
		return nil, err
	}
	return append(pathExtra, depExtra...), nil
}

// ─── ensure ───────────────────────────────────────────────

func newRepoAppEnsureCmd(f *rootFlags) *cobra.Command {
	var envPairs []string
	cmd := &cobra.Command{
		Use:   "ensure <name|github-url|owner/repo>",
		Short: "Idempotent: install if missing, start if stopped; announce the URL on stdout",
		Long: "Idempotent bring-up used by the desktop client: installs the repo when the\n" +
			"argument is a GitHub URL / owner/repo and not installed yet, starts the runner\n" +
			"when stopped, reuses the live instance otherwise. Prints\n" +
			"BIU_REPOAPP_URL=http://127.0.0.1:<port> on stdout once healthy.\n" +
			"Repeatable --env KEY=VALUE pairs are merged into the instance .env before start\n" +
			"(this is how the client delivers the confirmed config, secrets included).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !repoapp.Supported() {
				return errUnsupportedPlatform()
			}
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app ensure", err, "%s", err.Error())
			}
			slug, _, parseErr := repoapp.ParseRepoArg(args[0])
			if parseErr != nil {
				// Maybe the argument is the slug of an already-installed
				// instance (bare `name` form). Guard against path
				// traversal: store slugs never contain separators.
				if strings.ContainsAny(args[0], `/\`) || strings.Contains(args[0], "..") ||
					!store.Exists(args[0]) {
					return clierr.Newf("biu repo-app ensure", "%s",
						fmt.Sprintf("%q is not installed — run `biu repo-app install <github-url>` first", args[0]))
				}
				slug = args[0]
			} else if !store.Exists(slug) {
				if _, err := doRepoAppInstall(cmd.Context(), store, args[0], ""); err != nil {
					return clierr.Wrapf("biu repo-app ensure", err, "%s", err.Error())
				}
			}
			if err := mergeRepoAppEnv(store.Instance(slug).EnvPath(), envPairs); err != nil {
				return clierr.Wrapf("biu repo-app ensure", err, "%s", err.Error())
			}
			url, err := (&repoapp.Runner{Store: store}).Start(cmd.Context(), slug, repoapp.StartOptions{})
			if err != nil {
				return clierr.Wrapf("biu repo-app ensure", err, "%s", err.Error())
			}
			announceRepoAppURL(url)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&envPairs, "env", nil, "KEY=VALUE merged into the instance .env (repeatable)")
	return cmd
}

// mergeRepoAppEnv parses KEY=VALUE pairs and merges them over the
// instance .env (a flag entry wins over the existing value), then
// rewrites the file — WriteEnvFile keeps the 0600 mode the secrets
// contract (TechPlan §3.5 D9) requires. A nil/empty pair list is a
// no-op so callers can invoke it unconditionally.
func mergeRepoAppEnv(envPath string, pairs []string) error {
	if len(pairs) == 0 {
		return nil
	}
	kv, err := repoapp.LoadEnvFile(envPath)
	if err != nil {
		return fmt.Errorf("read .env: %w", err)
	}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return fmt.Errorf("invalid --env %q — expected KEY=VALUE", p)
		}
		kv[k] = v
	}
	if err := repoapp.WriteEnvFile(envPath, kv); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}
	return nil
}

// announceRepoAppURL prints the stdout announcement the desktop client
// parses (BIU_BRIDGE_URL precedent in serve_cmd.go).
func announceRepoAppURL(url string) {
	fmt.Printf("BIU_REPOAPP_URL=%s\n", url)
	os.Stdout.Sync()
	fmt.Fprintf(os.Stderr, "[repo-app] serving on %s\n", url)
}

// ─── list ─────────────────────────────────────────────────

func newRepoAppListCmd(f *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed repo apps and their run state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app list", err, "%s", err.Error())
			}
			instances, err := store.List()
			if err != nil {
				return clierr.Wrapf("biu repo-app list", err, "%s", err.Error())
			}
			out := cmd.OutOrStdout()
			if len(instances) == 0 {
				fmt.Fprintln(out, "no repo apps installed — try `biu repo-app install <github-url>`")
				return nil
			}
			runner := &repoapp.Runner{Store: store}
			for _, inst := range instances {
				ri, err := repoapp.LoadRuntime(inst.Dir)
				if err != nil {
					fmt.Fprintf(out, "%-28s (unreadable runtime.json: %v)\n", inst.Slug, err)
					continue
				}
				state := "stopped"
				if pid, running := runner.Status(inst.Slug); running {
					state = fmt.Sprintf("running pid=%d http://127.0.0.1:%d", pid, ri.Port)
				}
				fmt.Fprintf(out, "%-28s %-7s ref=%-12s %s\n",
					inst.Slug, ri.Stack, orDefault(ri.Ref, "default"), state)
			}
			return nil
		},
	}
	return cmd
}

// ─── run / stop / logs ────────────────────────────────────

func newRepoAppRunCmd(f *rootFlags) *cobra.Command {
	var (
		port     int
		envPairs []string
	)
	cmd := &cobra.Command{
		Use:   "run <name>",
		Short: "Start the app as a detached local service (127.0.0.1 only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !repoapp.Supported() {
				return errUnsupportedPlatform()
			}
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app run", err, "%s", err.Error())
			}
			if err := mergeRepoAppEnv(store.Instance(args[0]).EnvPath(), envPairs); err != nil {
				return clierr.Wrapf("biu repo-app run", err, "%s", err.Error())
			}
			url, err := (&repoapp.Runner{Store: store}).Start(cmd.Context(), args[0], repoapp.StartOptions{Port: port})
			if err != nil {
				return clierr.Wrapf("biu repo-app run", err, "%s", err.Error())
			}
			announceRepoAppURL(url)
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "port to bind on 127.0.0.1 (0 = OS-assigned)")
	cmd.Flags().StringArrayVar(&envPairs, "env", nil, "KEY=VALUE merged into the instance .env (repeatable)")
	return cmd
}

func newRepoAppStopCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop the app's runner (SIGTERM, then SIGKILL after 3s)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if !repoapp.Supported() {
				return errUnsupportedPlatform()
			}
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app stop", err, "%s", err.Error())
			}
			if err := (&repoapp.Runner{Store: store}).Stop(args[0]); err != nil {
				return clierr.Wrapf("biu repo-app stop", err, "%s", err.Error())
			}
			fmt.Fprintf(os.Stderr, "[repo-app] stopped %s\n", args[0])
			return nil
		},
	}
}

func newRepoAppLogsCmd(f *rootFlags) *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Print the app's run log (-f to follow)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app logs", err, "%s", err.Error())
			}
			runner := &repoapp.Runner{Store: store}
			if err := runner.Logs(cmd.Context(), args[0], follow, cmd.OutOrStdout()); err != nil {
				return clierr.Wrapf("biu repo-app logs", err, "%s", err.Error())
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow appended log output")
	return cmd
}

// ─── update / remove ──────────────────────────────────────

func newRepoAppUpdateCmd(f *rootFlags) *cobra.Command {
	var ref string
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Fetch a new ref, reinstall dependencies, restart",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !repoapp.Supported() {
				return errUnsupportedPlatform()
			}
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app update", err, "%s", err.Error())
			}
			slug := args[0]
			inst := store.Instance(slug)
			ri, err := repoapp.LoadRuntime(inst.Dir)
			if err != nil {
				return clierr.Newf("biu repo-app update", "%s",
					fmt.Sprintf("%q is not installed", slug))
			}
			targetRef := ref
			if targetRef == "" {
				targetRef = ri.Ref
			}
			runner := &repoapp.Runner{Store: store}
			if err := runner.Stop(slug); err != nil {
				return clierr.Wrapf("biu repo-app update", err, "%s", err.Error())
			}
			repoAppLogf("updating to ref=%s ...", orDefault(targetRef, "default branch"))
			sha, err := repoapp.CloneOrFetch(cmd.Context(), ri.RepoURL, targetRef, inst.RepoDir())
			if err != nil {
				return clierr.Wrapf("biu repo-app update", err, "%s", err.Error())
			}
			plan, err := repoapp.PlanStack(inst.RepoDir(), slug)
			if err != nil {
				return clierr.Wrapf("biu repo-app update", err, "%s", err.Error())
			}
			// Keep the recorded ref/sha current before deps install.
			ri.Ref = targetRef
			ri.InstalledSHA = sha
			ri.Stack = string(plan.Stack)
			ri.PackageManager = plan.PackageManager
			ri.StartCmd = plan.StartCmd
			extras, err := repoAppBootstrapAndDeps(cmd.Context(), inst, plan)
			if err != nil {
				return clierr.Wrapf("biu repo-app update", err, "%s", err.Error())
			}
			ri.PathExtra = extras
			if err := repoapp.SaveRuntime(inst.Dir, ri); err != nil {
				return clierr.Wrapf("biu repo-app update", err, "%s", err.Error())
			}
			url, err := runner.Start(cmd.Context(), slug, repoapp.StartOptions{})
			if err != nil {
				return clierr.Wrapf("biu repo-app update", err, "%s", err.Error())
			}
			repoAppLogf("✓ updated %s → %s", slug, sha[:12])
			announceRepoAppURL(url)
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "git branch/tag to move to (default: the installed ref)")
	return cmd
}

func newRepoAppRemoveCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Stop the app and delete its instance directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if !repoapp.Supported() {
				return errUnsupportedPlatform()
			}
			store, err := repoAppStore(f)
			if err != nil {
				return clierr.Wrapf("biu repo-app remove", err, "%s", err.Error())
			}
			slug := args[0]
			if !store.Exists(slug) {
				return clierr.Newf("biu repo-app remove", "%s", fmt.Sprintf("%q is not installed", slug))
			}
			runner := &repoapp.Runner{Store: store}
			if err := runner.Stop(slug); err != nil {
				return clierr.Wrapf("biu repo-app remove", err, "%s", err.Error())
			}
			if err := store.Remove(slug); err != nil {
				return clierr.Wrapf("biu repo-app remove", err, "%s", err.Error())
			}
			fmt.Fprintf(os.Stderr, "[repo-app] removed %s\n", slug)
			return nil
		},
	}
}

// ─── doctor ───────────────────────────────────────────────

func newRepoAppDoctorCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Probe runtimes (git/python3/uv/node/mise/docker) and report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "platform supported: %v (M1 is macOS/Linux only)\n", repoapp.Supported())
			for _, p := range repoapp.Doctor(cmd.Context()) {
				switch p.Source {
				case repoapp.SourceMissing:
					fmt.Fprintf(out, "%-8s missing\n", p.Name)
				default:
					fmt.Fprintf(out, "%-8s %-7s %s  %s\n", p.Name, p.Source, p.Path, p.Version)
				}
			}
			return nil
		},
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
