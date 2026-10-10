// Command ado is a gh-style CLI for Azure DevOps.
package main

import (
	"net/http"
	"os"
	"sync"

	"github.com/snake-in-prod/ado-gh/api"
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
	rootCmd := root.NewCmdRoot(newFactory(ios))
	rootCmd.SetArgs(args)
	cmd, err := rootCmd.ExecuteC()
	return cmdutil.HandleError(ios.ErrOut, err, cmd.UsageString())
}

// newFactory wires the api transport into the Factory here because cmdutil can't import api.
func newFactory(ios *iostreams.IOStreams) *cmdutil.Factory {
	f := cmdutil.NewFactory(version, ios)
	f.HttpClient = sync.OnceValues(func() (*http.Client, error) {
		// Built lazily: f.Debug is final only after flag parsing.
		// ponytail: no Token provider yet; the credential store plugs in here.
		opts := api.ClientOptions{AppVersion: f.AppVersion}
		if f.Debug {
			opts.Log = ios.ErrOut
		}
		return api.NewHTTPClient(opts), nil
	})
	return f
}
