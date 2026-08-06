// `biu plan` — inspect & manage plan files written by ExitPlanMode.
//
// Files live under ~/.biu/plans/<session-id>.md. This command exposes
// the read-side surface so users can browse / share / clean up plans
// without poking at the filesystem.
//
// Subcommands:
//
//   biu plan list                     list newest first, with previews
//   biu plan show [<ref>|latest]      print one plan (default: latest)
//   biu plan rm <ref>                 delete one plan
//   biu plan rm --older-than 30d      bulk cleanup
//
// `<ref>` accepts the full session id, an unambiguous prefix, or the
// literal `latest`.

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/plans"
	"github.com/spf13/cobra"
)

func newPlanCmd(_ *rootFlags) *cobra.Command {
	c := &cobra.Command{
		Use:   "plan",
		Short: "Inspect & manage saved plans (output of ExitPlanMode)",
	}
	c.AddCommand(newPlanListCmd(), newPlanShowCmd(), newPlanRmCmd())
	return c
}

func newPlanListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved plans, newest first",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := plans.Dir()
			if err != nil {
				return clierr.Wrapf("plan list", err, "resolve dir")
			}
			rows, err := plans.ListPlans(dir)
			if err != nil {
				return clierr.Wrapf("plan list", err, "read %s", clierr.DisplayPath(dir))
			}
			if len(rows) == 0 {
				fmt.Println("(no plans yet — saved when ExitPlanMode runs)")
				return nil
			}
			for _, p := range rows {
				fmt.Printf("%s  %5d B  %s  %s\n",
					p.ID, p.Bytes,
					p.ModTime.Format("2006-01-02 15:04"),
					p.FirstLine)
			}
			return nil
		},
	}
}

func newPlanShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [<ref>|latest]",
		Short: "Print a saved plan (default: latest)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := "latest"
			if len(args) == 1 {
				ref = args[0]
			}
			dir, err := plans.Dir()
			if err != nil {
				return clierr.Wrapf("plan show", err, "resolve dir")
			}
			p, ok := plans.FindByID(dir, ref)
			if !ok {
				return clierr.WithHint(
					clierr.Newf("plan show", "no plan matching %q", ref),
					"run `biu plan list` to see saved plans")
			}
			body, err := plans.Read(p)
			if err != nil {
				return clierr.Wrapf("plan show", err, "read %s", clierr.DisplayPath(p.Path))
			}
			fmt.Fprintf(os.Stderr, "# %s  %s\n\n",
				p.ID, clierr.DisplayPath(p.Path))
			_, err = fmt.Print(body)
			return err
		},
	}
}

func newPlanRmCmd() *cobra.Command {
	var olderThan string
	c := &cobra.Command{
		Use:   "rm [<ref>]",
		Short: "Delete a saved plan, or bulk-delete with --older-than",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := plans.Dir()
			if err != nil {
				return clierr.Wrapf("plan rm", err, "resolve dir")
			}
			if olderThan != "" {
				dur, err := plans.ParseDuration(olderThan)
				if err != nil {
					return clierr.WithHint(
						clierr.Wrapf("plan rm", err, "parse --older-than"),
						"valid: 30d, 2w, 4h, 15m, or any Go duration string")
				}
				n, err := plans.RemoveOlderThan(dir, dur)
				if err != nil {
					return clierr.Wrapf("plan rm", err, "bulk delete")
				}
				fmt.Fprintf(os.Stderr, "[biu] removed %d plan(s) older than %s\n", n, dur)
				return nil
			}
			if len(args) != 1 {
				return clierr.WithHint(
					clierr.Newf("plan rm", "missing argument"),
					"pass <ref> or use --older-than")
			}
			ref := strings.TrimSpace(args[0])
			p, ok := plans.FindByID(dir, ref)
			if !ok {
				return clierr.WithHint(
					clierr.Newf("plan rm", "no plan matching %q", ref),
					"run `biu plan list` to see saved plans")
			}
			if err := plans.Remove(p); err != nil {
				return clierr.Wrapf("plan rm", err, "delete %s", clierr.DisplayPath(p.Path))
			}
			fmt.Fprintf(os.Stderr, "[biu] removed %s\n", p.ID)
			return nil
		},
	}
	c.Flags().StringVar(&olderThan, "older-than", "",
		"bulk-delete plans older than this (e.g. 30d, 2w, 4h)")
	return c
}
