// Package create implements `ado repo create`.
package create

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/clone"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	name, project, gitignore string
	addReadme, clone         bool
}

// NewCmdCreate returns the `repo create` command.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "create [<name>]",
		Short: "Create a new repository",
		Long: `Create a Git repository in the current project or in --project.

--add-readme and --gitignore add a README.md and a .gitignore from one of the project's
gitignore templates in one initial commit on main. --clone clones the new repository into
./<name>.

Without <name> on a terminal, ado prompts for the name, project, README, .gitignore and clone.`,
		Example: `  ado repo create svc
  ado repo create svc --add-readme --gitignore Go --clone
  ado repo create svc -p myorg/myproject`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.name = args[0]
			} else if !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("repository name required when not running interactively")
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.project, "project", "p", "", "Create in `[ORG/]PROJECT` instead of the current project")
	fl.BoolVar(&o.addReadme, "add-readme", false, "Add a README.md")
	fl.StringVarP(&o.gitignore, "gitignore", "g", "", "Add a .gitignore from the `template` (e.g. Go, VisualStudio)")
	fl.BoolVar(&o.clone, "clone", false, "Clone the new repository into ./<name>")
	return cmd
}

// target resolves the repository name in project ("" = the current project).
func target(f *cmdutil.Factory, project, name string) (adocontext.Repo, error) {
	if name == "" || strings.ContainsAny(name, `/\:`) {
		return adocontext.Repo{}, cmdutil.FlagErrorf("invalid repository name %q", name)
	}
	if project != "" {
		name = strings.Trim(project, "/") + "/" + name
	}
	return shared.ParseRepo(f, name)
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	interactive := o.name == ""
	if interactive {
		if err := promptNameProject(f, o); err != nil {
			return err
		}
	}
	repo, err := target(f, o.project, o.name)
	if err != nil {
		return err
	}
	client, err := shared.NewClient(ctx, f, repo)
	if err != nil {
		return err
	}
	if interactive {
		if err := promptRest(ctx, f, client, o); err != nil {
			return err
		}
	} else if o.gitignore != "" {
		if o.gitignore, err = gitignoreTemplate(ctx, client, o.gitignore); err != nil {
			return err
		}
	}

	created, err := client.CreateRepository(ctx, repo.Name)
	if err != nil {
		var he *api.HTTPError
		if errors.As(err, &he) && strings.Contains(he.Message, "TF400948") {
			return fmt.Errorf("repository %s already exists in project %s; choose another name", repo.Name, repo.Project)
		}
		return err
	}
	ios := f.IOStreams
	repo.Name = cmp.Or(created.Name, repo.Name)
	webURL := cmp.Or(created.WebURL, adocontext.RepoURL(repo))

	var pushErr error
	if o.addReadme || o.gitignore != "" {
		readme := ""
		if o.addReadme {
			readme = "# " + repo.Name + "\n"
		}
		pushErr = client.InitialPush(ctx, created.ID, "Initial commit", readme, o.gitignore)
	}
	if ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "Created repository %s/%s\n", repo.Project, repo.Name)
	}
	if pushErr != nil {
		fmt.Fprintf(ios.ErrOut, "%s Created the repository, but adding the initial files failed: %v\n", ios.ColorScheme().Yellow("!"), pushErr)
	}
	fmt.Fprintln(ios.Out, webURL)
	if pushErr != nil {
		return cmdutil.SilentError
	}
	if !o.clone {
		return nil
	}
	return clone.Clone(ctx, f, clone.Options{Repo: repo})
}

// promptNameProject asks for the name and, without --project, the project.
func promptNameProject(f *cmdutil.Factory, o *options) error {
	name, err := f.Prompter.Input("Repository name", "")
	if err != nil {
		return err
	}
	if o.name = strings.TrimSpace(name); o.name == "" {
		return errors.New("a repository name is required")
	}
	if strings.ContainsAny(o.name, `/\:`) {
		return fmt.Errorf("invalid repository name %q", o.name)
	}
	if o.project != "" {
		return nil
	}
	def := ""
	if r, err := target(f, "", o.name); err == nil {
		def = r.Project
	}
	p, err := f.Prompter.Input("Project", def)
	o.project = strings.TrimSpace(p)
	return err
}

// promptRest asks for the README, .gitignore template and clone choices not set by flags.
func promptRest(ctx context.Context, f *cmdutil.Factory, client *api.RepoClient, o *options) error {
	p := f.Prompter
	var err error
	if !o.addReadme {
		if o.addReadme, err = p.Confirm("Add a README file?", false); err != nil {
			return err
		}
	}
	if o.gitignore != "" {
		o.gitignore, err = gitignoreTemplate(ctx, client, o.gitignore)
	} else {
		o.gitignore, err = selectGitignore(ctx, p, client)
	}
	if err != nil || o.clone {
		return err
	}
	o.clone, err = p.Confirm("Clone the new repository locally?", true)
	return err
}

func selectGitignore(ctx context.Context, p *cmdutil.Prompter, client *api.RepoClient) (string, error) {
	ok, err := p.Confirm("Add a .gitignore?", false)
	if err != nil || !ok {
		return "", err
	}
	names, err := client.GitignoreTemplates(ctx)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", errors.New("the project has no gitignore templates")
	}
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = templateLabel(n)
	}
	i, err := p.Select("Choose a .gitignore template", labels)
	if err != nil {
		return "", err
	}
	return names[i], nil
}

func templateLabel(name string) string {
	if len(name) >= len(".gitignore") && strings.EqualFold(name[len(name)-len(".gitignore"):], ".gitignore") {
		return name[:len(name)-len(".gitignore")]
	}
	return name
}

// gitignoreTemplate returns the template name matching want (with or without ".gitignore",
// case-insensitive), or an error listing close matches.
func gitignoreTemplate(ctx context.Context, client *api.RepoClient, want string) (string, error) {
	names, err := client.GitignoreTemplates(ctx)
	if err != nil {
		return "", err
	}
	w := strings.ToLower(templateLabel(want))
	var matches []string
	for _, n := range names {
		l := templateLabel(n)
		ll := strings.ToLower(l)
		if ll == w {
			return n, nil
		}
		if strings.Contains(ll, w) || cmdutil.Levenshtein(ll, w) <= 2 {
			matches = append(matches, l)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("unknown gitignore template %q; omit --gitignore on a terminal to choose from the list", want)
	}
	return "", fmt.Errorf("unknown gitignore template %q; did you mean: %s", want, strings.Join(matches, ", "))
}
