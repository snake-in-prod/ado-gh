// Package list implements `ado repo list`.
package list

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// jsonFields are the public `--json` fields; parentRepository is left out because the list
// API doesn't return it.
var jsonFields = []string{"id", "name", "project", "defaultBranch", "size", "isDisabled", "isFork", "remoteUrl", "sshUrl", "webUrl"}

type options struct {
	limit                int
	disabled, noDisabled bool
	fork, source         bool
	match                string
	exporter             *cmdutil.Exporter
}

// NewCmdList returns the `repo list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:     "list [<org>[/<project>]]",
		Aliases: []string{"ls"},
		Short:   "List repositories",
		Long: `List the repositories of the current project, of every project in <org>, or of
<org>/<project>. Results are sorted by name.`,
		Example: `  ado repo list
  ado repo list myorg --match 'svc-*' --source
  ado repo list myorg/myproject --json name,webUrl`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.limit < 1 {
				return cmdutil.FlagErrorf("invalid value for --limit: %d", o.limit)
			}
			if o.disabled && o.noDisabled {
				return cmdutil.FlagErrorf("specify only one of `--disabled` or `--no-disabled`")
			}
			if _, err := path.Match(o.match, ""); err != nil {
				return cmdutil.FlagErrorf("invalid value for --match: %q", o.match)
			}
			scope, err := scopeFromArgs(f, args)
			if err != nil {
				return err
			}
			return run(cmd.Context(), f, o, scope)
		},
	}
	fl := cmd.Flags()
	fl.IntVarP(&o.limit, "limit", "L", 30, "Maximum number of repositories to list")
	fl.BoolVar(&o.disabled, "disabled", false, "Show only disabled repositories")
	fl.BoolVar(&o.noDisabled, "no-disabled", false, "Omit disabled repositories")
	fl.BoolVar(&o.disabled, "archived", false, "Alias for --disabled")
	fl.BoolVar(&o.noDisabled, "no-archived", false, "Alias for --no-disabled")
	fl.Lookup("archived").Hidden = true
	fl.Lookup("no-archived").Hidden = true
	fl.BoolVar(&o.fork, "fork", false, "Show only forks")
	fl.BoolVar(&o.source, "source", false, "Show only non-forks")
	cmd.MarkFlagsMutuallyExclusive("fork", "source")
	fl.StringVar(&o.match, "match", "", "Show only repositories whose name matches a `glob` (case-insensitive)")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

// scopeFromArgs returns the organization and project to list (Project "" = whole
// organization). Without an argument it is the current repository's project.
func scopeFromArgs(f *cmdutil.Factory, args []string) (adocontext.Repo, error) {
	if len(args) == 0 {
		base, err := f.BaseRepo()
		if err != nil {
			return adocontext.Repo{}, err
		}
		return adocontext.Repo{Host: base.Host, Org: base.Org, Project: base.Project}, nil
	}
	segs := strings.Split(args[0], "/")
	if len(segs) > 2 || slices.ContainsFunc(segs, func(s string) bool { return s == "" || strings.ContainsRune(s, '\\') }) {
		return adocontext.Repo{}, cmdutil.FlagErrorf("invalid argument %q: expected <org> or <org>/<project>", args[0])
	}
	org := segs[0]
	scope := adocontext.Repo{Host: "dev.azure.com", Org: org}
	if len(segs) == 2 {
		scope.Project = segs[1]
	}
	// Keep the current repository's host (e.g. org.visualstudio.com) for the same organization,
	// so its stored credential applies.
	if base, err := f.BaseRepo(); err == nil && strings.EqualFold(base.Org, org) {
		scope.Host = base.Host
	}
	return scope, nil
}

func run(ctx context.Context, f *cmdutil.Factory, o *options, scope adocontext.Repo) error {
	client, err := shared.NewClient(ctx, f, scope)
	if err != nil {
		return err
	}
	all, err := client.ListRepositories(ctx)
	if err != nil {
		return err
	}
	repos := filter(all, o)
	slices.SortStableFunc(repos, func(a, b api.Repository) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(strings.ToLower(a.Project), strings.ToLower(b.Project)))
	})
	total := len(repos)
	repos = repos[:min(total, o.limit)]

	ios := f.IOStreams
	if o.exporter != nil {
		return o.exporter.Write(ios, repos)
	}
	if len(repos) == 0 {
		fmt.Fprintln(ios.ErrOut, "no repositories match your search")
		return nil
	}

	orgWide := scope.Project == ""
	tty := ios.IsStdoutTTY()
	if tty {
		where := scope.Org
		if !orgWide {
			where += "/" + scope.Project
		}
		fmt.Fprintf(ios.Out, "\nShowing %d of %d repositories in %s\n\n", len(repos), total, where)
	}
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "name", "default branch", "info", "size")
	for _, r := range repos {
		name := r.Name
		if orgWide {
			name = r.Project + "/" + r.Name
		}
		tp.AddField(name, tableprinter.WithColor(cs.Bold))
		tp.AddField(r.DefaultBranch, tableprinter.WithColor(cs.Cyan))
		tp.AddField(info(r), tableprinter.WithColor(cs.Gray))
		size := strconv.FormatUint(r.Size, 10)
		if tty {
			size = shared.HumanSize(r.Size)
		}
		tp.AddField(size)
		tp.EndRow()
	}
	return tp.Render()
}

func filter(repos []api.Repository, o *options) []api.Repository {
	match := strings.ToLower(o.match)
	return slices.DeleteFunc(slices.Clone(repos), func(r api.Repository) bool {
		switch {
		case o.disabled && !r.IsDisabled, o.noDisabled && r.IsDisabled,
			o.fork && !r.IsFork, o.source && r.IsFork:
			return true
		case match != "":
			ok, err := path.Match(match, strings.ToLower(r.Name)) // pattern validated in RunE
			return err != nil || !ok
		}
		return false
	})
}

// info lists a repository's markers: fork, disabled and empty (no default branch).
func info(r api.Repository) string {
	var tags []string
	if r.IsFork {
		tags = append(tags, "fork")
	}
	if r.IsDisabled {
		tags = append(tags, "disabled")
	}
	if r.DefaultBranch == "" {
		tags = append(tags, "empty")
	}
	return strings.Join(tags, ", ")
}
