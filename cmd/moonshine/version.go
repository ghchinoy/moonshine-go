package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version is the CLI's own build version -- distinct from
// moonshine.HeaderVersion / moonshine.Version(), which describe libmoonshine
// compatibility (see "moonshine doctor"). Set via -ldflags at build time
// (see Makefile's `build` target); defaults to "dev" for `go run`/plain `go
// build` invocations that don't pass -ldflags.
var version = "dev"

func init() {
	rootCmd.Version = version
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:     "version",
	GroupID: "config",
	Short:   "Print the moonshine CLI's build version",
	Long: `Prints this CLI's own build version (semver tag + git commit, or a
bare commit hash if untagged) plus the Go toolchain/OS/arch it was built
with. This is about the moonshine-go binary itself -- for libmoonshine's
version and compatibility, see "moonshine doctor".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("moonshine version %s\n", version)
		fmt.Printf("  go:      %s\n", runtime.Version())
		fmt.Printf("  os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}
