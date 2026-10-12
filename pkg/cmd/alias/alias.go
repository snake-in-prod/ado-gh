// Package alias implements `ado alias set/list/delete/import`.
package alias

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/alias/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// NewCmdAlias returns the `alias` command group.
func NewCmdAlias(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias <command>",
		Short: "Create command shortcuts",
		Long: `Aliases are shortcuts for ado commands, stored under "aliases:" in config.yml.

An alias expands once: it can't call another alias or an extension. Built-in commands
always win over an alias of the same name, and an alias wins over an extension.`,
		GroupID: "additional",
	}
	cmd.AddCommand(newCmdSet(f), newCmdList(f), newCmdDelete(f), newCmdImport(f))
	return cmd
}

func aliasConfig(f *cmdutil.Factory) (shared.Config, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	return shared.AliasConfig(cfg)
}

// checkName refuses names that aren't one top-level word or that belong to a built-in
// command, and reports whether the name shadows an extension.
func checkName(root *cobra.Command, name string) (shadowsExtension bool, err error) {
	if name == "" || strings.ContainsFunc(name, unicode.IsSpace) {
		return false, errors.New("alias names must be a single word")
	}
	if strings.HasPrefix(name, "-") {
		return false, errors.New(`alias names must not start with "-"`)
	}
	taken := fmt.Errorf("%q is already an ado command", name)
	if name == "help" { // cobra adds its help command only at Execute time
		return false, taken
	}
	for _, c := range root.Commands() {
		if c.Name() != name && !c.HasAlias(name) {
			continue
		}
		switch c.GroupID {
		case shared.AliasGroupID: // an existing alias; --clobber decides
		case shared.ExtensionGroupID:
			shadowsExtension = true
		default:
			return false, taken
		}
	}
	return shadowsExtension, nil
}

// validate checks one new alias against the command tree and the existing aliases.
func validate(root *cobra.Command, existing map[string]string, name, expansion string, clobber bool) (bool, error) {
	shadowsExtension, err := checkName(root, name)
	if err != nil {
		return false, err
	}
	if _, ok := existing[name]; ok && !clobber {
		return false, fmt.Errorf("alias %q already exists; use --clobber to overwrite it", name)
	}
	return shadowsExtension, shared.ValidateExpansion(root, expansion)
}

func warnShadow(ios *iostreams.IOStreams, name string) {
	fmt.Fprintf(ios.ErrOut, "! alias %q shadows the extension of the same name; run the extension with `ado extension exec %s`\n", name, name)
}

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	var shell, clobber bool
	cmd := &cobra.Command{
		Use:   "set <name> <expansion>",
		Short: "Create a shortcut for an ado command",
		Long: `Create a shortcut for an ado command. Use "-" as the expansion to read it from stdin.

$1, $2, … in the expansion are replaced by the alias arguments; arguments no placeholder
uses are appended. A non-shell expansion must start with an ado command.

With --shell the expansion runs through a shell ("alias_shell" config, default sh) and
$1, $2, … are its positional parameters. It is stored with a leading "!".`,
		Example: `  ado alias set prs 'pr list -a @me'
  ado alias set wiv 'workitem view $1 --web'
  ado alias set --shell igrep 'ado workitem list --json id,title -q ".[].title" | grep "$1"'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ios := f.IOStreams
			ac, err := aliasConfig(f)
			if err != nil {
				return err
			}
			name, expansion := args[0], args[1]
			if expansion == "-" {
				b, err := io.ReadAll(ios.In)
				if err != nil {
					return fmt.Errorf("reading the expansion: %w", err)
				}
				expansion = strings.TrimRight(string(b), "\r\n")
			}
			if shell && !shared.IsShell(expansion) {
				expansion = "!" + expansion
			}
			existing := ac.Aliases()
			shadowsExtension, err := validate(cmd.Root(), existing, name, expansion, clobber)
			if err != nil {
				return err
			}
			if shadowsExtension {
				warnShadow(ios, name)
			}
			ac.SetAlias(name, expansion)
			if err := ac.Write(); err != nil {
				return err
			}
			if ios.IsStderrTTY() {
				verb := "Added"
				if _, ok := existing[name]; ok {
					verb = "Changed"
				}
				fmt.Fprintf(ios.ErrOut, "%s %s alias %s\n", ios.ColorScheme().Green("✓"), verb, name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&shell, "shell", "s", false, "Run the expansion through a shell")
	cmd.Flags().BoolVar(&clobber, "clobber", false, "Overwrite an existing alias of the same name")
	return cmd
}

type aliasJSON struct {
	Name      string `json:"name"`
	Expansion string `json:"expansion"`
	Shell     bool   `json:"shell"`
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var exporter *cmdutil.Exporter
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your aliases",
		Long: `List your aliases: a table on a terminal, YAML when piped. The YAML can be fed to
"ado alias import" on another machine.`,
		Example: `  ado alias list
  ado alias list > aliases.yml
  ado alias list --json name,expansion,shell`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ios := f.IOStreams
			ac, err := aliasConfig(f)
			if err != nil {
				return err
			}
			aliases := ac.Aliases()
			names := slices.Sorted(maps.Keys(aliases))
			if exporter != nil {
				rows := make([]aliasJSON, 0, len(names))
				for _, n := range names {
					e := aliases[n]
					rows = append(rows, aliasJSON{Name: n, Expansion: strings.TrimPrefix(e, "!"), Shell: shared.IsShell(e)})
				}
				return exporter.Write(ios, rows)
			}
			if len(aliases) == 0 {
				if ios.IsStdoutTTY() {
					fmt.Fprintln(ios.ErrOut, "no aliases configured")
				}
				return nil
			}
			if !ios.IsStdoutTTY() {
				b, err := yaml.Marshal(aliases)
				if err != nil {
					return err
				}
				_, err = ios.Out.Write(b)
				return err
			}
			tp := tableprinter.New(ios, "name", "expansion")
			for _, n := range names {
				tp.AddField(n, tableprinter.WithColor(ios.ColorScheme().Bold))
				tp.AddField(tableprinter.OneLine(aliases[n]))
				tp.EndRow()
			}
			return tp.Render()
		},
	}
	cmdutil.AddJSONFlags(cmd, &exporter, []string{"name", "expansion", "shell"})
	return cmd
}

func newCmdDelete(f *cmdutil.Factory) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "delete {<name> | --all}",
		Short: "Delete aliases",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if (len(args) == 1) == all {
				return cmdutil.FlagErrorf("specify either an alias name or --all")
			}
			ios := f.IOStreams
			ac, err := aliasConfig(f)
			if err != nil {
				return err
			}
			aliases := ac.Aliases()
			names := args
			if all {
				if len(aliases) == 0 {
					return errors.New("no aliases configured")
				}
				names = slices.Sorted(maps.Keys(aliases))
			} else if _, ok := aliases[args[0]]; !ok {
				return fmt.Errorf("no such alias %q", args[0])
			}
			for _, n := range names {
				ac.DeleteAlias(n)
			}
			if err := ac.Write(); err != nil {
				return err
			}
			if ios.IsStderrTTY() {
				for _, n := range names {
					fmt.Fprintf(ios.ErrOut, "%s Deleted alias %s; was %s\n", ios.ColorScheme().Red("✓"), n, aliases[n])
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Delete all aliases")
	return cmd
}

func newCmdImport(f *cmdutil.Factory) *cobra.Command {
	var clobber bool
	cmd := &cobra.Command{
		Use:   "import [<file> | -]",
		Short: "Import aliases from a YAML file",
		Long: `Import aliases from a YAML map of names to expansions, as printed by "ado alias list"
when piped. Without a file, or with "-", the YAML is read from stdin.

Every entry is checked like "ado alias set"; invalid entries are skipped and reported.
Shell aliases need their leading "!".`,
		Example: `  ado alias import aliases.yml
  ado alias list | ssh other-host ado alias import`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ios := f.IOStreams
			path := "-"
			if len(args) == 1 {
				path = args[0]
			} else if ios.IsStdinTTY() {
				return cmdutil.FlagErrorf("no file given and nothing on stdin")
			}
			ac, err := aliasConfig(f)
			if err != nil {
				return err
			}
			var b []byte
			if path == "-" {
				b, err = io.ReadAll(ios.In)
			} else {
				b, err = os.ReadFile(path)
			}
			if err != nil {
				return fmt.Errorf("reading aliases: %w", err)
			}
			var entries map[string]string
			if err := yaml.Unmarshal(b, &entries); err != nil {
				return fmt.Errorf("invalid alias file: %w", err)
			}

			cs := ios.ColorScheme()
			existing := ac.Aliases()
			root := cmd.Root()
			imported := 0
			for _, name := range slices.Sorted(maps.Keys(entries)) {
				shadowsExtension, err := validate(root, existing, name, entries[name], clobber)
				if err != nil {
					fmt.Fprintf(ios.ErrOut, "%s Skipped %q: %v\n", cs.Red("X"), name, err)
					continue
				}
				if shadowsExtension {
					warnShadow(ios, name)
				}
				verb := "Imported"
				if _, ok := existing[name]; ok {
					verb = "Changed"
				}
				ac.SetAlias(name, entries[name])
				imported++
				fmt.Fprintf(ios.ErrOut, "%s %s %s\n", cs.Green("✓"), verb, name)
			}
			if imported == 0 {
				return nil
			}
			return ac.Write()
		},
	}
	cmd.Flags().BoolVar(&clobber, "clobber", false, "Overwrite existing aliases of the same name")
	return cmd
}
