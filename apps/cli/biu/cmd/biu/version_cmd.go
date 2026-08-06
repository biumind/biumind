// `biu version` — prints version + commit + build date + go runtime.
// Build metadata vars (version, commit, buildDate) live in main.go
// because they're set via -ldflags by GoReleaser.

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			short, _ := cmd.Flags().GetBool("short")
			if short {
				fmt.Println(version)
				return
			}
			fmt.Printf("biu %s\n  commit:     %s\n  build date: %s\n  go:         %s\n  os/arch:    %s/%s\n",
				version, commit, buildDate,
				runtime.Version(),
				runtime.GOOS, runtime.GOARCH)
		},
	}
	c.Flags().Bool("short", false, "print version only")
	return c
}
