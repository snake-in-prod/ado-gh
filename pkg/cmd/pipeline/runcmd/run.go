// Package runcmd implements `ado pipeline run`.
package runcmd

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelines"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

// watchInterval is how often `run --watch` polls, as `pipeline watch` does by default.
const watchInterval = 3 * time.Second

type options struct {
	pipeline, branch, commit string
	params, vars             []string
	watch, web               bool
}

// NewCmdRun returns the `pipeline run` command.
func NewCmdRun(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "run [<pipeline>]",
		Short: "Run a pipeline",
		Long: `Queue a run of a pipeline and print its URL.

<pipeline> is an ID, a name, a folder path (\Folder\Name or Folder/Name) or a URL. Without
it, the current repository's pipeline is used.

The run uses --branch, or else the pipeline repository's default branch. --parameter sets
YAML template parameters and is rejected for classic pipelines; --variable sets queue-time
variables, which the pipeline must allow to be set at queue time.`,
		Example: `  ado pipeline run
  ado pipeline run deploy -b main -p env=prod --watch
  ado pipeline run 12 -v foo=bar --commit 0123abc`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.pipeline = args[0]
			}
			return runRun(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.branch, "branch", "b", "", "Branch to run (default: the pipeline repository's default branch)")
	fl.StringVar(&o.commit, "commit", "", "Commit `SHA` to run")
	fl.StringArrayVarP(&o.params, "parameter", "p", nil, "YAML template parameter in `key=value` format")
	fl.StringArrayVarP(&o.vars, "variable", "v", nil, "Queue-time variable in `key=value` format")
	fl.BoolVar(&o.watch, "watch", false, "Watch the run until it completes and exit with its result")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the queued run in the web browser")
	cmd.MarkFlagsMutuallyExclusive("watch", "web")
	return cmd
}

func runRun(ctx context.Context, f *cmdutil.Factory, o *options) error {
	params, err := parsePairs("parameter", o.params)
	if err != nil {
		return err
	}
	vars, err := parsePairs("variable", o.vars)
	if err != nil {
		return err
	}
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	var ref shared.PipelineRef
	if o.pipeline != "" {
		if ref, err = shared.ParsePipelineRef(o.pipeline); err != nil {
			return err
		}
		if ref.Project.Project != "" {
			project = ref.Project
		}
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	var defID int
	if o.pipeline == "" {
		d, err := shared.DefaultDefinition(ctx, f, c, project.Name)
		if err != nil {
			return err
		}
		defID = prshared.Deref(d.Id)
	} else if defID, err = shared.ResolvePipeline(ctx, c, ref); err != nil {
		return err
	}
	def, err := c.Definition(ctx, defID)
	if err != nil {
		return err
	}
	name := prshared.Deref(def.Name)
	yaml := isYAML(def)
	if len(params) > 0 && !yaml {
		return cmdutil.FlagErrorf("--parameter is supported only for YAML pipelines; pipeline %q is classic, use --variable", name)
	}

	ios := f.IOStreams
	cs := ios.ColorScheme()
	switch prshared.Deref(def.QueueStatus) {
	case build.DefinitionQueueStatusValues.Disabled:
		return fmt.Errorf("pipeline %q is disabled; run 'ado pipeline enable %d'", name, defID)
	case build.DefinitionQueueStatusValues.Paused:
		fmt.Fprintf(ios.ErrOut, "%s pipeline %q is paused: the run is queued but won't start until it is enabled ('ado pipeline enable %d')\n", cs.Yellow("!"), name, defID)
	}

	branch, err := runBranch(ctx, c, def, o.branch)
	if err != nil {
		return err
	}
	var id int
	if yaml {
		id, err = runYAML(ctx, c, defID, branch, o.commit, params, vars)
	} else {
		id, err = queueClassic(ctx, c, defID, branch, o.commit, vars)
	}
	if err != nil {
		return err
	}

	url := adocontext.RunURL(project, id)
	if ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "%s Queued run %d of %s on %s\n", cs.Green("✓"), id, name, cmp.Or(strings.TrimPrefix(branch, "refs/heads/"), "the default branch"))
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, url)
	}
	fmt.Fprintln(ios.Out, url)
	if !o.watch {
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "\nTo see the run's progress, try: ado pipeline watch %d\n", id)
		}
		return nil
	}
	return shared.BuildWatch(c, id, watchInterval, func(b *build.Build, tl shared.Timeline) {
		printSummary(ios, b, tl)
	}).Run(ctx, ios)
}

// runBranch returns the ref to run: branch with "refs/heads/" added when it has no "refs/"
// prefix, else the definition repository's default branch, else (for an Azure Repos Git
// repository) that repository's default branch; "" leaves the choice to the server.
func runBranch(ctx context.Context, c *api.PipelineClient, def *build.BuildDefinition, branch string) (string, error) {
	if branch == "" && def.Repository != nil {
		branch = prshared.Deref(def.Repository.DefaultBranch)
		if id := prshared.Deref(def.Repository.Id); branch == "" && id != "" && prshared.Deref(def.Repository.Type) == "TfsGit" {
			repos := &api.RepoClient{Git: &git.ClientImpl{Client: *c.SDK}, SDK: c.SDK, Project: c.Project}
			repo, err := repos.Repository(ctx, id)
			if err != nil {
				return "", err
			}
			branch = repo.DefaultBranch
		}
	}
	if branch != "" && !strings.HasPrefix(branch, "refs/") {
		branch = "refs/heads/" + branch
	}
	return branch, nil
}

// runYAML runs YAML pipeline defID through the Pipelines API, the branch and commit as the
// self repository resource, and returns the run (build) ID.
func runYAML(ctx context.Context, c *api.PipelineClient, defID int, branch, commit string, params, vars map[string]string) (int, error) {
	var p pipelines.RunPipelineParameters
	if branch != "" || commit != "" {
		self := pipelines.RepositoryResourceParameters{}
		if branch != "" {
			self.RefName = &branch
		}
		if commit != "" {
			self.Version = &commit
		}
		p.Resources = &pipelines.RunResourcesParameters{Repositories: &map[string]pipelines.RepositoryResourceParameters{"self": self}}
	}
	if len(params) > 0 {
		p.TemplateParameters = &params
	}
	if len(vars) > 0 {
		vs := make(map[string]pipelines.Variable, len(vars))
		for k, v := range vars {
			vs[k] = pipelines.Variable{Value: &v}
		}
		p.Variables = &vs
	}
	run, err := c.RunPipeline(ctx, defID, p)
	if err != nil {
		return 0, err
	}
	return prshared.Deref(run.Id), nil
}

// queueClassic queues classic pipeline defID through the Build API, the variables as the
// JSON-encoded parameters object, and returns the build ID.
func queueClassic(ctx context.Context, c *api.PipelineClient, defID int, branch, commit string, vars map[string]string) (int, error) {
	b := &build.Build{Definition: &build.DefinitionReference{Id: &defID}}
	if branch != "" {
		b.SourceBranch = &branch
	}
	if commit != "" {
		b.SourceVersion = &commit
	}
	if len(vars) > 0 {
		enc, err := json.Marshal(vars)
		if err != nil {
			return 0, err
		}
		s := string(enc)
		b.Parameters = &s
	}
	queued, err := c.QueueBuild(ctx, b)
	if err != nil {
		return 0, err
	}
	return prshared.Deref(queued.Id), nil
}

// printSummary prints the watched run's outcome, after its tree on a terminal.
func printSummary(ios *iostreams.IOStreams, b *build.Build, tl shared.Timeline) {
	cs := ios.ColorScheme()
	if ios.IsStdoutTTY() {
		shared.PrintTree(ios.Out, cs, tl.Nodes, false)
		fmt.Fprintln(ios.Out)
	}
	result := string(prshared.Deref(b.Result))
	mark, color := shared.StatusMark(cs, string(prshared.Deref(b.Status)), result)
	fmt.Fprintf(ios.Out, "%s Run %s (ID %d) completed: %s\n", color(mark), prshared.Deref(b.BuildNumber), prshared.Deref(b.Id), cmp.Or(result, "none"))
}

// parsePairs parses repeated key=value flag values; a later key wins.
func parsePairs(flag string, pairs []string) (map[string]string, error) {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, cmdutil.FlagErrorf("invalid --%s %q: expected key=value", flag, p)
		}
		out[k] = v
	}
	return out, nil
}

// isYAML reports whether d is a YAML pipeline (process type 2) rather than a classic one.
func isYAML(d *build.BuildDefinition) bool {
	p, _ := d.Process.(map[string]any)
	t, _ := p["type"].(float64)
	return t == 2
}
