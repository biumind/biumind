// `biu auth login | logout | status` subcommands.
//
// Provider settings come from [auth] in config.toml — same shape as
// the OAuth Config struct so users can switch IdPs without changing
// code:
//
//   [auth]
//   authorize_url   = "https://platform.biumind.app/oauth/authorize"
//   token_url       = "https://platform.biumind.app/v1/oauth/token"
//   client_id       = "..."
//   scopes          = ["openid", "profile", "code"]
//   callback_port   = 0          # 0 = pick free
//   manual_redirect = "https://platform.biumind.app/oauth/code/callback"
//
// Token storage: ~/.biu/auth.json (mode 0600). The agent loop /
// provider adapters read it via oauth.Store.

package main

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
	"github.com/spf13/cobra"
)

// loadConfig is a thin wrapper that respects --config + env env override
// so each subcommand doesn't reinvent the wheel.
func loadConfig(f *rootFlags) (*config.Config, string, error) {
	return config.Load(f.cfgPath)
}

// buildOAuthConfig folds the [auth] config into oauth.Config. Env vars
// override TOML so `BIU_OAUTH_CLIENT_ID=… biu auth login` works without
// editing config.toml.
func buildOAuthConfig(cfg *config.Config) oauth.Config {
	out := oauth.Config{
		AuthorizeURL: firstNonEmpty(os.Getenv("BIU_OAUTH_AUTHORIZE_URL"), cfg.Auth.AuthorizeURL),
		TokenURL:     firstNonEmpty(os.Getenv("BIU_OAUTH_TOKEN_URL"), cfg.Auth.TokenURL),
		ClientID:     firstNonEmpty(os.Getenv("BIU_OAUTH_CLIENT_ID"), cfg.Auth.ClientID),
		Scopes:       cfg.Auth.Scopes,
		CallbackPort: cfg.Auth.CallbackPort,
		ManualRedirectURL: firstNonEmpty(
			os.Getenv("BIU_OAUTH_MANUAL_REDIRECT"), cfg.Auth.ManualRedirectURL),
	}
	if len(out.Scopes) == 0 {
		out.Scopes = []string{"openid", "profile"}
	}
	return out
}

func newAuthCmd(f *rootFlags) *cobra.Command {
	c := &cobra.Command{
		Use:   "auth",
		Short: "Manage OAuth credentials (login / logout / status)",
	}
	c.AddCommand(newAuthLoginCmd(f), newAuthLogoutCmd(f), newAuthStatusCmd(f),
		newAuthMigrateCmd(f))
	return c
}

// newAuthMigrateCmd moves tokens from the legacy ~/.biu/auth.json file
// into the OS keychain. One-shot, idempotent: re-running after a
// successful migration is a no-op (the file is already gone). On
// hosts without a keychain the command exits with a clear note —
// nothing is moved, the file stays where it is.
func newAuthMigrateCmd(_ *rootFlags) *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Move credentials from ~/.biu/auth.json into the OS keychain",
		Long: `Move credentials from the legacy file store into the OS keychain.

biu auto-picks the keychain backend on every run; this command is for
users upgrading from an older biu that wrote to ~/.biu/auth.json. After
a successful migration the file is deleted and future logins / refreshes
flow through the keychain.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filePath, err := oauth.FileStorePath()
			if err != nil {
				return clierr.Wrapf("auth migrate", err, "resolve file store path")
			}
			fileSrc, err := oauth.NewStore(filePath)
			if err != nil {
				return clierr.Wrapf("auth migrate", err, "open file store")
			}
			t, err := fileSrc.Load()
			if err != nil {
				return clierr.Wrapf("auth migrate", err, "read %s",
					clierr.DisplayPath(filePath))
			}
			if t.AccessToken == "" {
				fmt.Fprintf(os.Stderr, "[biu] nothing to migrate — %s has no tokens\n",
					clierr.DisplayPath(filePath))
				return nil
			}

			dst, err := oauth.Open("")
			if err != nil {
				return clierr.Wrapf("auth migrate", err, "open keychain")
			}
			if dst.Backend() == "file" {
				return clierr.WithHint(
					clierr.Newf("auth migrate", "no OS keychain available on this host"),
					"tokens stay in "+clierr.DisplayPath(filePath)+" (mode 0600); install libsecret-tools on Linux or run on macOS to enable keychain")
			}

			if dryRun {
				fmt.Fprintf(os.Stderr, "[biu] dry-run: would migrate tokens from %s → %s (%s)\n",
					clierr.DisplayPath(filePath), dst.Path(), dst.Backend())
				return nil
			}

			migrated, err := dst.Migrate(fileSrc)
			if err != nil {
				return clierr.Wrapf("auth migrate", err, "transfer tokens")
			}
			if !migrated {
				fmt.Fprintln(os.Stderr, "[biu] nothing to migrate")
				return nil
			}
			fmt.Fprintf(os.Stderr, "[biu] migrated → %s (backend=%s)\n", dst.Path(), dst.Backend())
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print intent without writing")
	return c
}

func newAuthLoginCmd(f *rootFlags) *cobra.Command {
	var manual bool
	c := &cobra.Command{
		Use:   "login",
		Short: "Sign in via OAuth (PKCE) and persist tokens to ~/.biu/auth.json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig(f)
			if err != nil {
				return err
			}
			oc := buildOAuthConfig(cfg)
			if oc.AuthorizeURL == "" || oc.TokenURL == "" || oc.ClientID == "" {
				return clierr.WithHint(
					clierr.Newf("auth login", "[auth] section incomplete: authorize_url, token_url, client_id all required"),
					"add the [auth] block to ~/.biu/config.toml or set BIU_OAUTH_AUTHORIZE_URL / BIU_OAUTH_TOKEN_URL / BIU_OAUTH_CLIENT_ID")
			}
			store, err := oauth.Open("")
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()

			if manual {
				return runManualLogin(ctx, oc, store)
			}

			login := oauth.Login{
				Config: oc,
				UrlOpener: func(authURL string) {
					fmt.Fprintln(os.Stderr, "[biu] open this URL in your browser to log in:")
					fmt.Fprintln(os.Stderr, "      "+authURL)
					_ = openBrowser(authURL)
				},
			}
			res, err := login.Run(ctx)
			if err != nil {
				return clierr.WithHint(
					clierr.Wrapf("auth login", err, "browser flow failed"),
					"try `--manual` for SSH/sandboxed environments, or check your IdP settings")
			}
			if err := store.Save(res.Tokens); err != nil {
				return clierr.Wrapf("auth login", err, "save tokens")
			}
			fmt.Fprintf(os.Stderr, "[biu] login ok — tokens saved to %s\n",
				clierr.DisplayPath(store.Path()))
			return nil
		},
	}
	c.Flags().BoolVar(&manual, "manual", false,
		"manual paste flow (for SSH / sandboxed environments)")
	return c
}

func newAuthLogoutCmd(_ *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Delete cached OAuth tokens from ~/.biu/auth.json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := oauth.Open("")
			if err != nil {
				return err
			}
			if err := store.Delete(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "[biu] logout ok")
			return nil
		},
	}
}

func newAuthStatusCmd(_ *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether biu has cached OAuth tokens",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := oauth.Open("")
			if err != nil {
				return err
			}
			t, err := store.Load()
			if err != nil {
				return err
			}
			if t.AccessToken == "" {
				fmt.Println("not logged in")
				fmt.Printf("backend      : %s (%s)\n", store.Backend(), store.Path())
				return nil
			}
			fmt.Printf("backend      : %s (%s)\n", store.Backend(), store.Path())
			fmt.Printf("access_token : %s…\n", redact(t.AccessToken))
			fmt.Printf("refresh_token: %s\n", presentString(t.RefreshToken != ""))
			fmt.Printf("scope        : %s\n", t.Scope)
			fmt.Printf("expires_at   : %s\n", t.ExpiresAt.Format(time.RFC3339))
			fmt.Printf("provider     : %s\n", t.Provider)
			fmt.Printf("expired      : %v\n", t.Expired())
			return nil
		},
	}
}

// runManualLogin walks the user through the SSH-friendly flow: print
// the URL, ask for the redirected URL, extract `code` + `state`, swap
// for tokens. The PKCE verifier generated here is threaded into
// oauth.Login via ManualVerifier so the exchange uses the matching
// challenge.
func runManualLogin(ctx context.Context, oc oauth.Config, store *oauth.Store) error {
	verifier, challenge, err := oauth.GeneratePKCE()
	if err != nil {
		return err
	}
	state, err := oauth.RandomState()
	if err != nil {
		return err
	}
	l := oauth.Login{Config: oc}
	authURL := l.AuthorizeURL(challenge, state, 0)
	fmt.Fprintln(os.Stderr, "[biu] open this URL in any browser:")
	fmt.Fprintln(os.Stderr, "      "+authURL)
	fmt.Fprintln(os.Stderr, "After approving, paste the FULL redirected URL below:")
	fmt.Fprint(os.Stderr, "> ")

	r := bufio.NewReader(os.Stdin)
	pasted, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	code, gotState, err := extractCodeFromURL(strings.TrimSpace(pasted))
	if err != nil {
		return err
	}
	if gotState != state {
		return fmt.Errorf("state mismatch: got %q, expected %q (CSRF guard)", gotState, state)
	}
	exchange := oauth.Login{
		Config:         oc,
		ManualCode:     code,
		ManualVerifier: verifier,
	}
	res, err := exchange.Run(ctx)
	if err != nil {
		return clierr.WithHint(
			clierr.Wrapf("auth login", err, "manual code exchange"),
			"the pasted URL may be expired — re-run `biu auth login --manual`")
	}
	if err := store.Save(res.Tokens); err != nil {
		return clierr.Wrapf("auth login", err, "save tokens")
	}
	fmt.Fprintf(os.Stderr, "[biu] manual login ok — tokens saved to %s\n",
		clierr.DisplayPath(store.Path()))
	return nil
}

// extractCodeFromURL pulls `code` + `state` from a redirected URL the
// user pasted. Tolerant of trailing whitespace / surrounding quotes.
func extractCodeFromURL(s string) (string, string, error) {
	s = strings.Trim(s, "\"' ")
	u, err := url.Parse(s)
	if err != nil {
		return "", "", fmt.Errorf("bad URL: %w", err)
	}
	q := u.Query()
	code := q.Get("code")
	state := q.Get("state")
	if code == "" {
		return "", "", fmt.Errorf("URL has no `code` parameter")
	}
	return code, state, nil
}

// openBrowser tries the OS-specific "open URL" command. Failures are
// logged but not fatal — the URL was already printed.
func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func presentString(b bool) string {
	if b {
		return "(present)"
	}
	return "(absent)"
}

func redact(tok string) string {
	if len(tok) <= 8 {
		return "***"
	}
	return tok[:4] + "…" + tok[len(tok)-4:]
}
