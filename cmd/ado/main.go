// Command ado is a gh-style CLI for Azure DevOps.
package main

import (
	"os"

	"github.com/snake-in-prod/ado-gh/pkg/cmd/root"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// version is injected at build time: -ldflags "-X main.version=v1.2.3".
var version = "DEV"

func main() {
	os.Exit(int(run(os.Args[1:], iostreams.System())))
}

func run(args []string, ios *iostreams.IOStreams) cmdutil.ExitCode {
	rootCmd := root.NewCmdRoot(cmdutil.NewFactory(version, ios))
	rootCmd.SetArgs(args)
	cmd, err := rootCmd.ExecuteC()
	return cmdutil.HandleError(ios.ErrOut, err, cmd.UsageString())
}
