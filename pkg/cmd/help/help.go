// Package help implements `ado help` and `ado help reference`.
package help

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/build"
	aliasShared "github.com/snake-in-prod/ado-gh/pkg/cmd/alias/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// NewCmdHelp returns the help command, cobra's default plus a `reference` topic. Install it
// with SetHelpCommand.
func NewCmdHelp(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long: "Help provides help for any command in the application.\n" +
			"Simply type ado help [path to command] for full details.",
		GroupID: "additional",
		Args:    cobra.ArbitraryArgs,
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			target, _, err := c.Root().Find(args)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var comps []cobra.Completion
			for _, sub := range target.Commands() {
				if (sub.IsAvailableCommand() || sub.Name() == "help") && strings.HasPrefix(sub.Name(), toComplete) {
					comps = append(comps, cobra.CompletionWithDesc(sub.Name(), sub.Short))
				}
			}
			return comps, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(c *cobra.Command, args []string) error {
			target, _, err := c.Root().Find(args)
			if target == nil || err != nil {
				c.Printf("Unknown help topic %#q\n", args)
				return c.Root().Usage()
			}
			target.InitDefaultHelpFlag()    // show --help in the flag list
			target.InitDefaultVersionFlag() // and --version on root
			return target.Help()
		},
	}
	cmd.AddCommand(newCmdReference(f))
	return cmd
}

func newCmdReference(f *cmdutil.Factory) *cobra.Command {
	var dir, man string
	cmd := &cobra.Command{
		Use:   "reference",
		Short: "A comprehensive reference of all ado commands",
		Long: "Print the reference of every ado command as Markdown.\n\n" +
			"With --dir, write one Markdown page per command plus index.md; with --man, write\n" +
			"section 1 manpages. Aliases and extensions are left out.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			root := c.Root()
			prepare(root)
			if dir == "" && man == "" {
				_, err := io.WriteString(f.IOStreams.Out, markdown(root))
				return err
			}
			if dir != "" {
				if err := writeMarkdown(root, dir); err != nil {
					return err
				}
			}
			if man != "" {
				if err := os.MkdirAll(man, 0o755); err != nil {
					return err
				}
				date := manDate(build.Date)
				return doc.GenManTree(root, &doc.GenManHeader{Section: "1", Date: &date, Source: "ado", Manual: "ado manual"}, man)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Write one Markdown page per command into `path`")
	cmd.Flags().StringVar(&man, "man", "", "Write section 1 manpages into `dir`")
	return cmd
}

// prepare hides aliases and extensions and turns off cobra's dated autogen tag on every
// command (man pages check each command's own flag), so the output is deterministic.
func prepare(root *cobra.Command) {
	for _, c := range root.Commands() {
		if c.GroupID == aliasShared.AliasGroupID || c.GroupID == aliasShared.ExtensionGroupID {
			c.Hidden = true
		}
	}
	var noTag func(*cobra.Command)
	noTag = func(c *cobra.Command) {
		c.DisableAutoGenTag = true
		for _, sub := range c.Commands() {
			noTag(sub)
		}
	}
	noTag(root)
}

// walk calls fn for every documented descendant of c, depth first, with its depth below c.
// It uses cobra/doc's filter, which also drops the help command.
func walk(c *cobra.Command, depth int, fn func(*cobra.Command, int)) {
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() && !sub.IsAdditionalHelpTopicCommand() {
			fn(sub, depth+1)
			walk(sub, depth+1, fn)
		}
	}
}

// markdown renders the whole tree as one gh-style document: a heading per command with its
// usage, short description, local flags and aliases.
func markdown(root *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s reference\n\n", root.Name())
	walk(root, 1, func(c *cobra.Command, depth int) {
		flags := c.LocalFlags().FlagUsages() // merges inherited flags first, so UseLine is stable
		fmt.Fprintf(&b, "%s `%s`\n\n%s\n\n", strings.Repeat("#", depth), c.UseLine(), c.Short)
		if flags != "" {
			fmt.Fprintf(&b, "```\n%s```\n\n", dedent(flags))
		}
		if len(c.Aliases) > 0 {
			fmt.Fprintf(&b, "Aliases: `%s`\n\n", strings.Join(c.Aliases, "`, `"))
		}
	})
	return b.String()
}

// writeMarkdown writes cobra/doc's page per command into dir plus an index.md listing them.
func writeMarkdown(root *cobra.Command, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := doc.GenMarkdownTree(root, dir); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s reference\n\n", root.Name())
	entry := func(c *cobra.Command, _ int) {
		fmt.Fprintf(&b, "- [%s](%s.md): %s\n", c.CommandPath(), strings.ReplaceAll(c.CommandPath(), " ", "_"), c.Short)
	}
	entry(root, 0)
	walk(root, 0, entry)
	return os.WriteFile(filepath.Join(dir, "index.md"), []byte(b.String()), 0o644)
}

// manDate is the manpage date: the build's commit date, or a fixed date for builds without one.
func manDate(commitDate string) time.Time {
	t, err := time.Parse(time.RFC3339, commitDate)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return t.UTC()
}

// dedent removes the indentation common to all non-empty lines.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	indent := -1
	for _, l := range lines {
		if t := strings.TrimLeft(l, " "); t != "" && (indent < 0 || len(l)-len(t) < indent) {
			indent = len(l) - len(t)
		}
	}
	for i, l := range lines {
		lines[i] = l[min(max(indent, 0), len(l)):]
	}
	return strings.Join(lines, "\n")
}
