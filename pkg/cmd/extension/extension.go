// Package extension implements `ado extension install/upgrade/search/list/remove/exec/create/browse`.
package extension

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/extension/browse"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/extension/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/extension/install"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/extension/search"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/extension/upgrade"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
)

// NewCmdExtension returns the `extension` command group (alias `ext`).
func NewCmdExtension(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "extension <command>",
		Aliases: []string{"ext"},
		Short:   "Manage ado extensions",
		Long: `Extensions are ado-<name> executables run as "ado <name>".

Built-in commands win over an alias of the same name, an alias wins over an installed
extension, and an installed extension wins over an ado-<name> executable on PATH.
"ado extension exec <name>" runs an installed extension even when an alias shadows it.`,
		GroupID: "additional",
	}
	cmd.AddCommand(install.NewCmdInstall(f), upgrade.NewCmdUpgrade(f), search.NewCmdSearch(f), newCmdList(f), newCmdRemove(f),
		newCmdExec(f), create.NewCmdCreate(f), browse.NewCmdBrowse(f))
	return cmd
}

type extensionJSON struct {
	Name         string      `json:"name"`
	Source       string      `json:"source"`
	Kind         extpkg.Kind `json:"kind"`
	Version      string      `json:"version"`
	Pinned       bool        `json:"pinned"`
	TokenConsent bool        `json:"tokenConsent"`
	Path         string      `json:"path"`
	commit       bool
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var exporter *cmdutil.Exporter
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed extensions",
		Example: `  ado extension list
  ado extension list --json name,version,pinned`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ios := f.IOStreams
			root := extpkg.Dir()
			exts, err := extpkg.List(root)
			if err != nil {
				return err
			}
			rows := make([]extensionJSON, 0, len(exts))
			for _, e := range exts {
				rec, err := extpkg.ReadRecord(root, e.Name)
				if err != nil {
					rec = &extpkg.Record{} // unreadable record: shown without details, no consent
				}
				r := extensionJSON{Name: e.Name, Source: rec.Source, Kind: e.Kind, Version: rec.Version,
					Pinned: rec.Pinned, TokenConsent: rec.TokenConsent, Path: e.Dir}
				if r.Version == "" {
					r.Version = rec.Tag
				}
				if r.Version == "" {
					r.Version, r.commit = rec.Commit, true
				}
				if r.Source == "" && e.Kind == extpkg.KindLocal {
					r.Source = e.Dir
				}
				rows = append(rows, r)
			}
			if exporter != nil {
				return exporter.Write(ios, rows)
			}
			if len(rows) == 0 {
				if ios.IsStdoutTTY() {
					fmt.Fprintln(ios.ErrOut, "no installed extensions found")
				}
				return nil
			}
			tty := ios.IsStdoutTTY()
			mark := func(b bool) string {
				if tty {
					return map[bool]string{true: "✓"}[b]
				}
				return strconv.FormatBool(b)
			}
			tp := tableprinter.New(ios, "name", "source", "kind", "version", "pinned", "token")
			for _, r := range rows {
				version := r.Version
				if tty && r.commit && len(version) > 8 {
					version = version[:8]
				}
				tp.AddField(r.Name, tableprinter.WithColor(ios.ColorScheme().Bold))
				tp.AddField(tableprinter.OneLine(r.Source))
				tp.AddField(string(r.Kind))
				tp.AddField(tableprinter.OneLine(version))
				tp.AddField(mark(r.Pinned))
				tp.AddField(mark(r.TokenConsent))
				tp.EndRow()
			}
			return tp.Render()
		},
	}
	cmdutil.AddJSONFlags(cmd, &exporter, []string{"name", "source", "kind", "version", "pinned", "tokenConsent", "path"})
	return cmd
}

func newCmdRemove(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed extension",
		Long: `Remove an installed extension and its install record. For a local install only the
link to the directory is removed, never the directory itself.`,
		Example: `  ado extension remove lint`,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			err := extpkg.Remove(extpkg.Dir(), name)
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("extension %q is not installed", name)
			}
			if err != nil {
				return err
			}
			if ios := f.IOStreams; ios.IsStderrTTY() {
				fmt.Fprintf(ios.ErrOut, "%s Removed extension %s\n", ios.ColorScheme().Green("✓"), name)
			}
			return nil
		},
	}
}

func newCmdExec(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <name> [args...]",
		Short: "Run an installed extension",
		Long: `Run an installed extension, even when an alias of the same name shadows it.
Everything after the name is passed to the extension.`,
		Example: `  ado extension exec lint --fix`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ext, err := extpkg.Lookup(extpkg.Dir(), args[0])
			if err != nil {
				return err
			}
			return extpkg.Run(f, ext, args[1:])
		},
	}
	cmd.Flags().SetInterspersed(false) // flags after the name belong to the extension
	return cmd
}
