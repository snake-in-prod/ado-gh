// Package approve implements `ado pipeline approve` and `ado pipeline reject`.
package approve

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelinesapproval"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Scopes the approvals API lists per operation, named in 401/403 errors.
const (
	queryScopes  = `"Build (Read)" (vso.build)`
	updateScopes = `"Build (Read & execute)" (vso.build_execute) or "Pipeline Resources (Use)" (vso.pipelineresources_use)`
)

type options struct {
	run     string
	comment string
	stage   string
	all     bool
	status  pipelinesapproval.ApprovalStatus
	// verb is "approve" or "reject".
	verb string
}

// NewCmdApprove returns the `pipeline approve` command.
func NewCmdApprove(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, "approve", pipelinesapproval.ApprovalStatusValues.Approved,
		`  ado pipeline approve 812 -c "ship it"
  ado run approve 812 --stage Deploy`)
}

// NewCmdReject returns the `pipeline reject` command.
func NewCmdReject(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, "reject", pipelinesapproval.ApprovalStatusValues.Rejected,
		`  ado pipeline reject 812 -c "not today"
  ado run reject 812 --all`)
}

func newCmd(f *cmdutil.Factory, verb string, status pipelinesapproval.ApprovalStatus, example string) *cobra.Command {
	o := &options{verb: verb, status: status}
	title := strings.ToUpper(verb[:1]) + verb[1:]
	cmd := &cobra.Command{
		Use:   verb + " <run>",
		Short: title + " a run's pending approvals",
		Long: title + ` the pending approvals of a run's stages.

<run> is a run ID or URL. With one pending approval, ado acts on it. With several, a terminal
asks which one; otherwise pass --stage or --all (--all acts on each in turn and reports
those the server refused).

The server decides whether you may act: when it refuses, the error names the approvers.
Other checks (business hours, Azure functions, ...) show in "ado pipeline view" but cannot be
acted on here.`,
		Example: example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.run = args[0]
			if o.all && o.stage != "" {
				return cmdutil.FlagErrorf("specify only one of --stage or --all")
			}
			return runApprove(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVarP(&o.comment, "comment", "c", "", "Comment to record with the decision")
	cmd.Flags().StringVar(&o.stage, "stage", "", "Act on the approval of the stage `name`")
	cmd.Flags().BoolVar(&o.all, "all", false, "Act on every pending approval")
	return cmd
}

// pending is a pending approval with the stage waiting for it.
type pending struct {
	stage    string
	approval pipelinesapproval.Approval
}

func (p pending) label() string { return cmp.Or(p.stage, "run") }

func runApprove(ctx context.Context, f *cmdutil.Factory, o *options) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	id, p, err := shared.ParseRunRef(o.run)
	if err != nil {
		return err
	}
	if p.Project != "" {
		project = p
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	all, err := pendingApprovals(ctx, c, id)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return fmt.Errorf("no pending approvals for run %d", id)
	}
	sel, err := choose(f, o, all)
	if err != nil {
		return err
	}

	// One request per approval: the server may refuse some (stages the caller can't approve)
	// while accepting others, so each outcome is reported on its own.
	var errs []error
	for _, s := range sel {
		one := []pending{s}
		updated, err := c.UpdateApprovals(ctx, []uuid.UUID{*s.approval.Id}, o.status, o.comment)
		if err != nil {
			errs = append(errs, shared.ApprovalError(err, fmt.Sprintf("could not %s %s", o.verb, s.label()), updateScopes, approvers(one)))
			continue
		}
		report(f, o, one, updated)
	}
	return errors.Join(errs...)
}

// pendingApprovals reads run id's timeline for its Checkpoint.Approval records (their identifier
// is the approval ID) and returns those the approvals API reports pending, in timeline order.
func pendingApprovals(ctx context.Context, c *api.PipelineClient, id int) ([]pending, error) {
	tl, err := c.Timeline(ctx, id)
	if err != nil {
		return nil, err
	}
	type rec struct {
		id    uuid.UUID
		stage string
	}
	var recs []rec
	if tl != nil && tl.Records != nil {
		for _, p := range shared.NewTimeline(*tl.Records).Pending {
			if aid, err := uuid.Parse(p.ID); err == nil && p.IsApproval() {
				recs = append(recs, rec{aid, p.Stage})
			}
		}
	}
	if len(recs) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(recs))
	for i, r := range recs {
		ids[i] = r.id
	}
	got, err := c.QueryApprovals(ctx, ids)
	if err != nil {
		return nil, shared.ApprovalError(err, fmt.Sprintf("could not read the approvals of run %d", id), queryScopes, nil)
	}
	var out []pending
	for _, r := range recs {
		i := slices.IndexFunc(got, func(a pipelinesapproval.Approval) bool { return a.Id != nil && *a.Id == r.id })
		if i >= 0 && prshared.Deref(got[i].Status) == pipelinesapproval.ApprovalStatusValues.Pending {
			out = append(out, pending{r.stage, got[i]})
		}
	}
	return out, nil
}

// choose picks the approvals to act on: --all, --stage, the only one, or a terminal prompt.
func choose(f *cmdutil.Factory, o *options, all []pending) ([]pending, error) {
	switch {
	case o.all:
		return all, nil
	case o.stage != "":
		sel := slices.DeleteFunc(slices.Clone(all), func(p pending) bool { return !strings.EqualFold(p.stage, o.stage) })
		if len(sel) == 0 {
			return nil, fmt.Errorf("no pending approval for stage %q; pending: %s", o.stage, labels(all))
		}
		return sel, nil
	case len(all) == 1:
		return all, nil
	case !f.IOStreams.CanPrompt():
		return nil, cmdutil.FlagErrorf("several approvals are pending (%s); pass --stage or --all", labels(all))
	}
	opts := make([]string, len(all))
	for i, p := range all {
		opts[i] = fmt.Sprintf("%s (approvers: %s)", p.label(), strings.Join(approvers(all[i:i+1]), ", "))
	}
	i, err := f.Prompter.Select("Which approval do you want to "+o.verb+"?", opts)
	if err != nil {
		return nil, err
	}
	return all[i : i+1], nil
}

// report prints the outcome per approval. An approval that stays pending after an approve needs
// more approvers (minRequiredApprovers).
func report(f *cmdutil.Factory, o *options, sel []pending, updated []pipelinesapproval.Approval) {
	ios := f.IOStreams
	cs := ios.ColorScheme()
	verbed := map[string]string{"approve": "Approved", "reject": "Rejected"}[o.verb]
	for _, s := range sel {
		a := s.approval
		i := slices.IndexFunc(updated, func(u pipelinesapproval.Approval) bool { return u.Id != nil && *u.Id == *a.Id })
		if i >= 0 && prshared.Deref(updated[i].Status) == pipelinesapproval.ApprovalStatusValues.Pending {
			// ponytail: the PATCH response carries no steps, so the count is the queried
			// approved steps plus ours.
			done := 1
			for _, st := range prshared.Deref(a.Steps) {
				if prshared.Deref(st.Status) == pipelinesapproval.ApprovalStatusValues.Approved {
					done++
				}
			}
			fmt.Fprintf(ios.ErrOut, "%s %s %s (%d of %d required); still waiting\n", cs.Green("✓"), verbed,
				s.label(), done, prshared.Deref(a.MinRequiredApprovers))
			continue
		}
		fmt.Fprintf(ios.ErrOut, "%s %s %s\n", cs.Green("✓"), verbed, s.label())
	}
}

// labels lists the stages of ps.
func labels(ps []pending) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.label()
	}
	return strings.Join(out, ", ")
}

// approvers names who can still act on ps: the assigned approvers of pending steps (with
// inSequence, later steps are uninitiated and not yet up), or of every step when none is pending.
func approvers(ps []pending) []string {
	var out []string
	for _, p := range ps {
		steps := prshared.Deref(p.approval.Steps)
		up := slices.DeleteFunc(slices.Clone(steps), func(s pipelinesapproval.ApprovalStep) bool {
			return prshared.Deref(s.Status) != pipelinesapproval.ApprovalStatusValues.Pending
		})
		if len(up) == 0 {
			up = steps
		}
		for _, s := range up {
			if s.AssignedApprover != nil {
				if n := prshared.Deref(s.AssignedApprover.DisplayName); n != "" && !slices.Contains(out, n) {
					out = append(out, n)
				}
			}
		}
	}
	return out
}
