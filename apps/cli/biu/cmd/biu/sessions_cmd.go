// `biu sessions` — list / show / export saved session logs. The
// JSONL log files live under ~/.biumind/sessions/<id>.jsonl; the
// REPL writes one record per Event when [no-log] is unset.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
	"github.com/spf13/cobra"
)

// newSessionsCmd registers `biu sessions list/show` for listing and
// replaying past conversation logs.
func newSessionsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sessions",
		Short: "List or inspect saved session logs",
	}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Print every saved session, newest first",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.SessionsDir()
			if err != nil {
				return err
			}
			rows, err := session.ListSessions(dir)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Println("(no saved sessions)")
				return nil
			}
			for _, r := range rows {
				fmt.Printf("%s  %5d msgs  %4d KB  [%s]  %s\n",
					r.ID, r.MessageCount, r.BytesOnDisk/1024, r.ProjectHash, r.FirstPrompt)
			}
			return nil
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "show <id>",
		Short: "Print every event from a session as JSONL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.SessionsDir()
			if err != nil {
				return err
			}
			s, ok := session.FindByID(dir, args[0])
			if !ok {
				return clierr.WithHint(
					clierr.Newf("sessions show", "no session with id %q", args[0]),
					"run `biu sessions list` for available ids")
			}
			body, err := os.ReadFile(s.Path)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(body)
			return err
		},
	})
	c.AddCommand(newSessionsExportCmd())
	return c
}

// newSessionsExportCmd registers `biu sessions export <id>` — turns
// the raw JSONL into one of three human / tool-friendly formats:
// markdown (default), json, anthropic-replay.
func newSessionsExportCmd() *cobra.Command {
	var (
		format             string
		includeToolOutput  bool
		excludeSystem      bool
		maxToolOutputBytes int
		outputPath         string
	)
	c := &cobra.Command{
		Use:   "export <id>",
		Short: "Export a session as markdown, json, or anthropic-replay",
		Long: `Export a session in a human- or tool-friendly format.

Formats:
  markdown          — chat transcript with tool-call boxes (default)
  json              — turn-consolidated structured dump
  anthropic-replay  — messages payload you can POST to /v1/messages

Secrets (api_key / token / refresh_token / virtual_key field values
plus free-text Bearer / sk-ant-… patterns) are auto-redacted before
any format runs. Use --include-tool-output=false to drop tool outputs
entirely (e.g. when sharing a session that touched secret files).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := config.SessionsDir()
			if err != nil {
				return err
			}
			s, ok := session.FindByID(dir, args[0])
			if !ok {
				return clierr.WithHint(
					clierr.Newf("sessions export", "no session with id %q", args[0]),
					"run `biu sessions list` for available ids")
			}
			f := session.ExportFormat(strings.ToLower(format))
			switch f {
			case session.FormatMarkdown, session.FormatJSON, session.FormatAnthropicReplay:
				// ok
			default:
				return clierr.WithHint(
					clierr.Newf("sessions export", "unknown format %q", format),
					"valid: markdown, json, anthropic-replay")
			}
			out := io.Writer(os.Stdout)
			if outputPath != "" {
				file, err := os.Create(outputPath)
				if err != nil {
					return clierr.Wrapf("sessions export", err, "create %s",
						clierr.DisplayPath(outputPath))
				}
				defer file.Close()
				out = file
			}
			if _, err := session.Export(s.Path, out, session.ExportOptions{
				Format:             f,
				IncludeToolOutput:  includeToolOutput,
				ExcludeSystem:      excludeSystem,
				MaxToolOutputBytes: maxToolOutputBytes,
			}); err != nil {
				return clierr.Wrapf("sessions export", err, "render %s", format)
			}
			if outputPath != "" {
				fmt.Fprintf(os.Stderr, "[biu] exported %s → %s\n",
					s.ID, clierr.DisplayPath(outputPath))
			}
			return nil
		},
	}
	c.Flags().StringVarP(&format, "format", "f", "markdown",
		"output format: markdown | json | anthropic-replay")
	c.Flags().BoolVar(&includeToolOutput, "include-tool-output", true,
		"render tool_result payloads (set false to drop them)")
	c.Flags().BoolVar(&excludeSystem, "exclude-system", false,
		"drop system_* events (permission rejections, hook blocks)")
	c.Flags().IntVar(&maxToolOutputBytes, "max-tool-output-bytes", 4096,
		"truncate each tool_result to N bytes (0 = no cap)")
	c.Flags().StringVarP(&outputPath, "output", "o", "",
		"write to file instead of stdout")
	return c
}
