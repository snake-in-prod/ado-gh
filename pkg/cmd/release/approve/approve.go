// Package approve implements `ado release approve` and `ado release reject`.
package approve

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/api"
	pshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Scopes the release approvals API lists per operation, named in 401/403 errors.
const (
	queryScopes  = `"Release (Read)" (vso.release)`
	updateScopes = `"Release (Read, write, execute & manage)" (vso.release_manage)`
)

type options struct {
	ref         string
	comment     string
	environment string
	all         bool
	status      release.ApprovalStatus
	// verb is "approve" or "reject".
	verb string
}

// NewCmdApprove returns the `release approve` command.
func NewCmdApprove(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, "approve", release.ApprovalStatusValues.Approved,
		`  ado release approve 45 -c "ship it"
  ado release approve 45 --environment Prod`)
}

// NewCmdReject returns the `release reject` command.
func NewCmdReject(f *cmdutil.Factory) *cobra.Command {
	return newCmd(f, "reject", release.ApprovalStatusValues.Rejected,
		`  ado release reject 45 -c "not today"
  ado release reject 45 --all`)
}

func newCmd(f *cmdutil.Factory, verb string, status release.ApprovalStatus, example string) *cobra.Command {
	o := &options{verb: verb, status: status}
	title := strings.ToUpper(verb[:1]) + verb[1:]
	cmd := &cobra.Command{
		Use:   verb + " <release>",
		Short: title + " a release's pending approvals",
		Long: title + ` the pending pre- or post-deployment approvals of a release's environments.

<release> is a release ID or URL. With one pending approval, ado acts on it. With several, a
terminal asks which one; otherwise pass --environment or --all.

The server decides whether you may act: when an environment has one approval per approver, ado
tries each in turn, and when the server refuses them all, the error names the approvers.`,
		Example: example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ref = args[0]
			if o.all && o.environment != "" {
				return cmdutil.FlagErrorf("specify only one of --environment or --all")
			}
			return runApprove(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVarP(&o.comment, "comment", "c", "", "Comment to record with the decision")
	cmd.Flags().StringVar(&o.environment, "environment", "", "Act on the approval of the environment `name`")
	cmd.Flags().BoolVar(&o.all, "all", false, "Act on every pending approval")
	return cmd
}

// pending is the pending approval of one environment and type, with one record per approver.
type pending struct {
	env, kind string
	approvals []release.ReleaseApproval
}

// label is e.g. "Prod pre-deployment".
func (p pending) label() string { return p.env + " " + p.kind }

func runApprove(ctx context.Context, f *cmdutil.Factory, o *options) error {
	id, project, err := shared.ParseReleaseRef(o.ref)
	if err != nil {
		return err
	}
	if project.Project == "" {
		if project, err = f.BaseProject(); err != nil {
			return err
		}
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
		return fmt.Errorf("no pending approvals for release %d", id)
	}
	sel, err := choose(f, o, all)
	if err != nil {
		return err
	}
	cs := f.IOStreams.ColorScheme()
	verbed := map[string]string{"approve": "Approved", "reject": "Rejected"}[o.verb]
	for _, p := range sel {
		if err := update(ctx, c, o, p); err != nil {
			return err
		}
		fmt.Fprintf(f.IOStreams.ErrOut, "%s %s %s\n", cs.Green("✓"), verbed, p.label())
	}
	return nil
}

// pendingApprovals lists the manual approvals of release id still pending, grouped by
// environment and type in the order the server created them.
func pendingApprovals(ctx context.Context, c *api.ReleaseClient, id int) ([]pending, error) {
	status, typ, order := release.ApprovalStatusValues.Pending, release.ApprovalTypeValues.All, release.ReleaseQueryOrderValues.Ascending
	got, err := c.Approvals(ctx, release.GetApprovalsArgs{
		ReleaseIdsFilter: &[]int{id}, StatusFilter: &status, TypeFilter: &typ, QueryOrder: &order,
	}, 0)
	if err != nil {
		return nil, pshared.ApprovalError(err, fmt.Sprintf("could not read the approvals of release %d", id), queryScopes, nil)
	}
	var out []pending
	for _, a := range got {
		if prshared.Deref(a.Status) != status || prshared.Deref(a.IsAutomated) {
			continue
		}
		env := ""
		if a.ReleaseEnvironment != nil {
			env = prshared.Deref(a.ReleaseEnvironment.Name)
		}
		kind := strings.TrimSuffix(string(prshared.Deref(a.ApprovalType)), "Deploy") + "-deployment"
		i := slices.IndexFunc(out, func(p pending) bool { return p.env == env && p.kind == kind })
		if i < 0 {
			out = append(out, pending{env: env, kind: kind})
			i = len(out) - 1
		}
		out[i].approvals = append(out[i].approvals, a)
	}
	return out, nil
}

// choose picks the approvals to act on: --all, --environment, the only one, or a terminal prompt.
func choose(f *cmdutil.Factory, o *options, all []pending) ([]pending, error) {
	switch {
	case o.all:
		return all, nil
	case o.environment != "":
		sel := slices.DeleteFunc(slices.Clone(all), func(p pending) bool { return !strings.EqualFold(p.env, o.environment) })
		if len(sel) == 0 {
			return nil, fmt.Errorf("no pending approval for environment %q; pending: %s", o.environment, labels(all))
		}
		return sel, nil
	case len(all) == 1:
		return all, nil
	case !f.IOStreams.CanPrompt():
		return nil, cmdutil.FlagErrorf("several approvals are pending (%s); pass --environment or --all", labels(all))
	}
	opts := make([]string, len(all))
	for i, p := range all {
		opts[i] = fmt.Sprintf("%s (approvers: %s)", p.label(), strings.Join(approvers(p), ", "))
	}
	i, err := f.Prompter.Select("Which approval do you want to "+o.verb+"?", opts)
	if err != nil {
		return nil, err
	}
	return all[i : i+1], nil
}

// update sends the decision for each approval record of p in turn until the server accepts one;
// a refusal (4xx) moves on to the next record, anything else stops. With every record refused,
// it returns the first refusal, naming the approvers.
func update(ctx context.Context, c *api.ReleaseClient, o *options, p pending) error {
	var first error
	for _, a := range p.approvals {
		_, err := c.UpdateApproval(ctx, prshared.Deref(a.Id), o.status, o.comment)
		if err == nil {
			return nil
		}
		if first == nil {
			first = err
		}
		var he *api.HTTPError
		if !errors.As(err, &he) || he.StatusCode < 400 || he.StatusCode >= 500 {
			first = err
			break
		}
	}
	return pshared.ApprovalError(first, fmt.Sprintf("could not %s %s", o.verb, p.label()), updateScopes, approvers(p))
}

// labels lists the labels of ps.
func labels(ps []pending) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.label()
	}
	return strings.Join(out, ", ")
}

// approvers names the approvers (users or groups) of p's records.
func approvers(p pending) []string {
	var out []string
	for _, a := range p.approvals {
		if a.Approver != nil {
			if n := prshared.Deref(a.Approver.DisplayName); n != "" && !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}
