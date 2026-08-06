// `biu bridge --listen :8088` — exposes the agent over HTTP/SSE so
// IDEs / remote operator UIs can drive it. Each request gets a fresh
// agent built via buildSDKAgent (defined in main.go) so multiple
// clients don't share state.

package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/biumind/biumind/apps/cli/biu/internal/bridge"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/spf13/cobra"
)

func newBridgeCmd(f *rootFlags) *cobra.Command {
	var addr, authToken string
	c := &cobra.Command{
		Use:   "bridge",
		Short: "Expose the agent over HTTP/SSE so an IDE or remote UI can drive it",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}
			model := firstNonEmpty(f.model, cfg.Default.Model)
			factory := func(extras bridge.AgentExtras) (*biumindkit.Agent, error) {
				// extras.PermissionPolicy 由 bridge 在 session create 时填好；
				// 让它路由到 buildSDKAgent，里面 wire 进 biumindkit.Options。
				return buildSDKAgent(cfg, f, model, extras.PermissionPolicy)
			}
			// Smoke test the factory once at startup so we fail fast
			// on bad config rather than at first POST.
			if probe, err := factory(bridge.AgentExtras{}); err != nil {
				return fmt.Errorf("bridge: agent factory: %w", err)
			} else {
				_ = probe.Close()
			}
			srv, err := bridge.NewServer(bridge.Options{
				AgentFactory: factory, AuthToken: authToken,
			})
			if err != nil {
				return err
			}
			ln, handler, err := srv.Listen(addr)
			if err != nil {
				return err
			}
			// Print the RESOLVED address so callers passing --listen=:0
			// can scrape the actual port. Tests rely on this.
			fmt.Fprintf(os.Stderr, "[biu] bridge listening on %s (auth=%v)\n",
				ln.Addr().String(), authToken != "")
			return http.Serve(ln, handler)
		},
	}
	c.Flags().StringVar(&addr, "listen", ":8088", "address to listen on")
	c.Flags().StringVar(&authToken, "auth-token", "",
		"bearer token required on every request (empty = no auth, dev only)")
	return c
}
