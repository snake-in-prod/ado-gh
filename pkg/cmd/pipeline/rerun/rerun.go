// Package rerun implements `ado pipeline rerun`.
package rerun

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelines"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	run    string
	failed bool
}

// NewCmdRerun returns the `pipeline rerun` command.
func NewCmdRerun(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "rerun [<run>]",
		Short: "Run a pipeline again, or retry the failed parts of a run",
		Long: `Queue a new run of the same pipeline, branch, commit, template parameters and
queue-time variables as <run>, and print its URL. Secret variables are not copied.

With --failed, retry the failed and canceled stages of a completed YAML run in place (only
their failed jobs run again), or the failed jobs of a classic run.

<run> is a run ID or URL. Without it, you are asked to pick one of the recent runs of
the current repository's pipeline on the current branch (or of the project).`,
		Example: `  ado pipeline rerun 812
  ado run rerun 812 --failed`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.run = args[0]
			}
			return runRerun(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVar(&o.failed, "failed", false, "Retry only the failed and canceled stages of the run")
	return cmd
}

func runRerun(ctx context.Context, f *cmdutil.Factory, o *options) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	var id int
	if o.run != "" {
		var p adocontext.Repo
		if id, p, err = shared.ParseRunRef(o.run); err != nil {
			return err
		}
		if p.Project != "" {
			project = p
		}
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	if id == 0 {
		if id, err = selectRun(ctx, f, c, project); err != nil {
			return err
		}
	}

	b, err := c.BuildByID(ctx, id)
	if err != nil {
		return err
	}
	if o.failed && prshared.Deref(b.Status) != build.BuildStatusValues.Completed {
		return errors.New("run is still in progress")
	}
	var defID int
	var defName string
	if b.Definition != nil {
		defID, defName = prshared.Deref(b.Definition.Id), prshared.Deref(b.Definition.Name)
	}
	def, err := c.Definition(ctx, defID)
	if err != nil {
		return err
	}
	yaml := isYAML(def)

	ios := f.IOStreams
	status := func(format string, args ...any) {
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "%s %s\n", ios.ColorScheme().Green("✓"), fmt.Sprintf(format, args...))
		}
	}
	newID := id
	switch {
	case o.failed && yaml:
		if err := retryStages(ctx, c, id, status); err != nil {
			return err
		}
	case o.failed:
		if _, err := c.RetryBuild(ctx, id); err != nil {
			return err
		}
		status("Retrying the failed jobs of run %d", id)
	default:
		if newID, err = queueCopy(ctx, c, b, defID, yaml); err != nil {
			return err
		}
		status("Queued run %d of %s", newID, defName)
	}
	fmt.Fprintln(ios.Out, adocontext.RunURL(project, newID))
	return nil
}

// queueCopy queues a new run of b's definition with b's branch, commit, template parameters and
// queue-time variables, through the Pipelines API for YAML and the Build API for classic.
func queueCopy(ctx context.Context, c *api.PipelineClient, b *build.Build, defID int, yaml bool) (int, error) {
	if !yaml {
		queued, err := c.QueueBuild(ctx, &build.Build{
			Definition:   &build.DefinitionReference{Id: &defID},
			SourceBranch: b.SourceBranch, SourceVersion: b.SourceVersion, Parameters: b.Parameters,
		})
		if err != nil {
			return 0, err
		}
		return prshared.Deref(queued.Id), nil
	}
	params := pipelines.RunPipelineParameters{
		TemplateParameters: b.TemplateParameters,
		Resources: &pipelines.RunResourcesParameters{Repositories: &map[string]pipelines.RepositoryResourceParameters{
			"self": {RefName: b.SourceBranch, Version: b.SourceVersion},
		}},
	}
	if p := prshared.Deref(b.Parameters); p != "" {
		var vars map[string]string
		if err := json.Unmarshal([]byte(p), &vars); err != nil {
			return 0, fmt.Errorf("could not read the queue-time variables of run %d: %w", prshared.Deref(b.Id), err)
		}
		vs := make(map[string]pipelines.Variable, len(vars))
		for k, v := range vars {
			vs[k] = pipelines.Variable{Value: &v}
		}
		params.Variables = &vs
	}
	run, err := c.RunPipeline(ctx, defID, params)
	if err != nil {
		return 0, err
	}
	return prshared.Deref(run.Id), nil
}

// retryStages retries the stages of run id whose latest attempt failed or was canceled, in
// timeline order, reporting each. A failure doesn't stop the later stages; all are returned.
// ponytail: a failed stage depending on another failed one is retried too, after it; whether
// a 7.1 stage retry already brings back dependents is unverified, which would make its PATCH
// fail harmlessly. Skip stages whose ancestors are retried once dependsOn is known.
func retryStages(ctx context.Context, c *api.PipelineClient, id int, status func(string, ...any)) error {
	tl, err := c.Timeline(ctx, id)
	if err != nil {
		return err
	}
	var records []build.TimelineRecord
	if tl != nil {
		records = prshared.Deref(tl.Records)
	}
	stages := retryableStages(records)
	if len(stages) == 0 {
		return fmt.Errorf("run %d has no failed or canceled stages to retry", id)
	}
	var errs []error
	for _, s := range stages {
		if err := c.RetryStage(ctx, id, s); err != nil {
			errs = append(errs, fmt.Errorf("could not retry stage %s: %w", s, err))
			continue
		}
		status("Retrying stage %s of run %d", s, id)
	}
	return errors.Join(errs...)
}

// retryableStages returns the identifiers of the Stage records whose latest attempt failed or
// was canceled, in timeline order. Raw records are read, not the tree, so the implicit
// __default stage of a stageless YAML pipeline counts.
func retryableStages(records []build.TimelineRecord) []string {
	latest := map[string]build.TimelineRecord{}
	for _, r := range records {
		key := prshared.Deref(r.Identifier)
		if prshared.Deref(r.Type) != "Stage" || key == "" {
			continue
		}
		if l, ok := latest[key]; !ok || prshared.Deref(r.Attempt) > prshared.Deref(l.Attempt) {
			latest[key] = r
		}
	}
	var stages []build.TimelineRecord
	for _, r := range latest {
		if res := prshared.Deref(r.Result); res == build.TaskResultValues.Failed || res == build.TaskResultValues.Canceled {
			stages = append(stages, r)
		}
	}
	slices.SortFunc(stages, func(a, b build.TimelineRecord) int {
		return cmp.Compare(prshared.Deref(a.Order), prshared.Deref(b.Order))
	})
	out := make([]string, len(stages))
	for i, s := range stages {
		out[i] = prshared.Deref(s.Identifier)
	}
	return out
}

// isYAML reports whether d is a YAML pipeline (process type 2) rather than a classic one.
func isYAML(d *build.BuildDefinition) bool {
	p, _ := d.Process.(map[string]any)
	t, _ := p["type"].(float64)
	return t == 2
}

// selectRun asks which recent run to use: one of the current repository's pipeline on the
// current branch when the clone has one, otherwise one of the project's.
func selectRun(ctx context.Context, f *cmdutil.Factory, c *api.PipelineClient, project adocontext.Repo) (int, error) {
	var args build.GetBuildsArgs
	if f.IOStreams.CanPrompt() && project.Name != "" && f.BaseRepoFromGit() {
		if def, err := shared.DefaultDefinition(ctx, f, c, project.Name); err == nil {
			args.Definitions = &[]int{prshared.Deref(def.Id)}
			if branch, err := f.GitClient.CurrentBranch(ctx); err == nil {
				ref := "refs/heads/" + branch
				args.BranchName = &ref
			}
		}
	}
	return shared.SelectRun(ctx, f, c, args)
}
