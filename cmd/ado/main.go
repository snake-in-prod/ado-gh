// Command ado is a gh-style CLI for Azure DevOps.
package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/internal/update"
	aliasShared "github.com/snake-in-prod/ado-gh/pkg/cmd/alias/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/root"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func main() {
	os.Exit(int(run(os.Args[1:], iostreams.System())))
}

// newCmdRoot builds the command tree; tests add commands to it.
var newCmdRoot = root.NewCmdRoot

// updateTransport carries the update check's GitHub requests; nil is http.DefaultTransport.
var updateTransport http.RoundTripper

func run(args []string, ios *iostreams.IOStreams) cmdutil.ExitCode {
	// The update check runs alongside the command; every path, aliases and extensions
	// included, comes back here, so the notice is printed once, after the command.
	ctx, cancel := context.WithTimeout(context.Background(), update.Timeout)
	defer cancel()
	updates := update.Start(ctx, ios, build.Version, updateTransport)

	f := newFactory(ios)
	rootCmd := newCmdRoot(f)
	args = pathExtensionArgs(rootCmd, f, args)
	rootCmd.SetArgs(dispatchArgs(rootCmd, args))
	cmd, err := rootCmd.ExecuteC()
	code := cmdutil.HandleError(ios.ErrOut, err, cmd.UsageString())
	if code == cmdutil.ExitOK {
		update.Print(ctx, ios, updates, build.Version, f.Debug) // f.Debug is final after ExecuteC
	} else if updates != nil {
		cancel() // no notice after a failure: stop the check and wait for it
		<-updates
	}
	return code
}

// dispatchArgs applies the global flags leading the command name to root and drops them from
// args when the name is an alias or extension. Those commands get their arguments unparsed,
// so cobra would hand them -R/--debug as arguments and leave the Factory unset.
func dispatchArgs(root *cobra.Command, args []string) []string {
	n := globalFlagsLen(args)
	if n == 0 || n == len(args) {
		return args
	}
	c, _, err := root.Find(args[n : n+1])
	if err != nil || !c.DisableFlagParsing {
		return args // a built-in parses its own flags
	}
	if err := root.PersistentFlags().Parse(args[:n]); err != nil {
		return args // cobra reports it
	}
	return args[n:]
}

// pathExtensionArgs registers ado-<name> from PATH when the command name in args is no built-in,
// alias or installed extension. Only that one name is looked up, so a normal run never scans
// PATH. `help <ext>` becomes `<ext> --help`, so the extension prints its own help, unless
// <ext> is a topic of the help command itself.
func pathExtensionArgs(rootCmd *cobra.Command, f *cmdutil.Factory, args []string) []string {
	n := globalFlagsLen(args)
	if n >= len(args) {
		return args
	}
	if args[n] != "help" || len(args) != n+2 {
		root.AddPathExtension(rootCmd, f, args[n])
		return args
	}
	if c, _, err := rootCmd.Find(args[n:]); err == nil && c.Name() != "help" {
		return args // the help command's own topics, such as `help reference`, beat extensions
	}
	root.AddPathExtension(rootCmd, f, args[n+1])
	if c, _, err := rootCmd.Find(args[n+1:]); err == nil && c.GroupID == aliasShared.ExtensionGroupID {
		return append(args[:n:n], args[n+1], "--help")
	}
	return args
}

// globalFlagsLen returns how many leading args are --debug and -R/--repo flags with values.
func globalFlagsLen(args []string) int {
	i := 0
	for i < len(args) {
		switch a := args[i]; {
		case a == "-R" || a == "--repo":
			i += 2
		case a == "--debug" || strings.HasPrefix(a, "--debug=") || strings.HasPrefix(a, "--repo=") || strings.HasPrefix(a, "-R"):
			i++
		default:
			return i
		}
	}
	return min(i, len(args))
}

// newFactory wires the api transport into the Factory here because cmdutil can't import api.
func newFactory(ios *iostreams.IOStreams) *cmdutil.Factory {
	f := cmdutil.NewFactory(build.Version, ios)
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
