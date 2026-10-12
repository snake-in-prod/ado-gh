// Package watch implements `ado release watch`.
package watch

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	pshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

// idlePolls is how many polls in a row with nothing deploying or deployed end a watch: enough
// for an automated trigger to start its deployment.
const idlePolls = 3

type options struct {
	ref, environment string
	interval         int
}

// NewCmdWatch returns the `release watch` command.
func NewCmdWatch(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "watch <release>",
		Short: "Watch a release until its deployments complete",
		Long: `Watch the environments of a Classic Release until none is deploying and at least one
has been deployed, then print a summary. Environments never deployed (e.g. manual
ones) don't hold the watch; --environment watches only one. When no watched environment
starts deploying within 3 polls, the watch stops with exit status 1.

<release> is a release ID or URL.

On a terminal, the environments are redrawn every --interval seconds; otherwise a
line is printed per environment or job state change. Pressing Ctrl+C stops watching;
the deployments go on.

The exit status is 0 if every deployed environment succeeded, 1 if any was rejected
(a failed deployment or a rejected approval; the summary tells which), failed or
partially succeeded, and 2 if any was canceled or watching was stopped.`,
		Example: `  ado release watch 45
  ado release watch 45 --environment Prod -i 10 && echo "deployed"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.interval < 1 {
				return cmdutil.FlagErrorf("--interval must be at least 1 second")
			}
			o.ref = args[0]
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().IntVarP(&o.interval, "interval", "i", 3, "Refresh interval in seconds")
	cmd.Flags().StringVar(&o.environment, "environment", "", "Only watch the environment with this name or ID")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
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
	return Release(ctx, f.IOStreams, c, project, id, o.environment, time.Duration(o.interval)*time.Second)
}

// Release watches release id of project through c every interval, all its environments or
// only the one environment names (when not ""), and prints the summary; the error is the
// exit status, as for `release watch`.
func Release(ctx context.Context, ios *iostreams.IOStreams, c *api.ReleaseClient, project adocontext.Repo, id int, environment string, interval time.Duration) error {
	var (
		r    *release.Release
		envs []shared.Environment
		err  error
		// idle counts the polls in a row with no watched environment deploying or deployed.
		idle int
	)
	w := &pshared.Watch{
		Interval: interval,
		Header:   fmt.Sprintf("Release %d", id),
		Poll: func(ctx context.Context) (pshared.Timeline, error) {
			if r, err = c.ReleaseByID(ctx, id); err != nil {
				return pshared.Timeline{}, err
			}
			tree := shared.NewTree(*r)
			nodes := tree.Nodes
			envs = tree.Environments
			if environment != "" {
				i, err := tree.EnvironmentIndex(environment, id)
				if err != nil {
					return pshared.Timeline{}, err
				}
				envs, nodes = envs[i:i+1], nodes[i:i+1]
			}
			return pshared.Timeline{Nodes: nodes, Pending: shared.PendingApprovals(envs)}, nil
		},
		Done: func(pshared.Timeline) bool {
			deployed := false
			for _, e := range envs {
				res, active := outcome(e, stepStatus(*r, e))
				if active {
					idle = 0
					return false
				}
				deployed = deployed || res != ""
			}
			if deployed {
				return true
			}
			// Nothing deploying and nothing deployed: an automated trigger may not have started
			// yet, so give it a few polls before giving up.
			idle++
			return idle >= idlePolls
		},
		Summary: func(tl pshared.Timeline) error {
			if idle >= idlePolls {
				fmt.Fprintf(ios.ErrOut, "no deployment started; deploy one with: ado release deploy %d --environment %s\n",
					id, cmp.Or(environment, "<name>"))
				return cmdutil.SilentError
			}
			return printSummary(ios, project, *r, envs, tl)
		},
	}
	return w.Run(ctx, ios)
}

// stepStatus is the status of e's latest deploy step in r, "" before its first.
func stepStatus(r release.Release, e shared.Environment) string {
	for _, re := range prshared.Deref(r.Environments) {
		if prshared.Deref(re.Id) != e.ID {
			continue
		}
		for _, s := range prshared.Deref(re.DeploySteps) {
			if prshared.Deref(s.Attempt) == e.Attempt {
				return string(prshared.Deref(s.Status))
			}
		}
	}
	return ""
}

// outcome is e's final status ("" while deploying or never deployed) and whether it is
// deploying. EnvironmentStatus has no failed, and when it isn't telling (undefined,
// notStarted, ...) the latest deploy step's status and operationStatus are read instead.
func outcome(e shared.Environment, step string) (result string, active bool) {
	switch e.Status {
	case "succeeded", "rejected", "partiallySucceeded", "canceled":
		return e.Status, false
	case "inProgress", "queued", "scheduled":
		return "", true
	}
	switch {
	case strings.EqualFold(e.OperationStatus, "canceled"):
		return "canceled", false
	case strings.EqualFold(e.OperationStatus, "rejected"):
		return "rejected", false
	case step == "succeeded", step == "failed", step == "partiallySucceeded":
		return step, false
	}
	return "", step == "inProgress"
}

// printSummary prints the watched environments envs of release r, a line telling a failed
// deployment from a rejected approval per rejected one, their issues and the release URL, and
// returns the exit error: any failure wins over a cancel.
func printSummary(ios *iostreams.IOStreams, project adocontext.Repo, r release.Release, envs []shared.Environment, tl pshared.Timeline) error {
	cs := ios.ColorScheme()
	w := ios.Out
	result := "succeeded"
	var rejected []string
	for _, e := range envs {
		res, _ := outcome(e, stepStatus(r, e))
		switch res {
		case "rejected", "failed", "partiallySucceeded":
			if result == "succeeded" || result == "canceled" {
				result = res
			}
		case "canceled":
			if result == "succeeded" {
				result = res
			}
		}
		if res != "rejected" {
			continue
		}
		why := "an approval was rejected"
		if op := e.OperationStatus; !strings.EqualFold(op, "rejected") {
			why = "the deployment failed"
			if op != "" {
				why += " (" + op + ")"
			}
		}
		rejected = append(rejected, fmt.Sprintf("%s %s rejected: %s", cs.Red("X"), e.Name, why))
	}

	id := prshared.Deref(r.Id)
	var def string
	if d := r.ReleaseDefinition; d != nil {
		def = " " + prshared.Deref(d.Name)
	}
	mark, color := pshared.StatusMark(cs, result, "")
	fmt.Fprintf(w, "%s %s%s%s\n", color(mark), cs.Bold(prshared.Deref(r.Name)), def, cs.Gray(fmt.Sprintf(" · %s · ID %d", result, id)))
	if len(tl.Nodes) > 0 {
		fmt.Fprintf(w, "\n%s\n", cs.Bold("ENVIRONMENTS"))
		pshared.PrintTree(w, cs, tl.Nodes, false)
	}
	if len(rejected) > 0 {
		fmt.Fprintf(w, "\n%s\n", strings.Join(rejected, "\n"))
	}
	if anns := pshared.Annotations(tl.Nodes); len(anns) > 0 {
		fmt.Fprintf(w, "\n%s\n", cs.Bold("ANNOTATIONS"))
		pshared.PrintAnnotations(w, cs, anns)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, cs.Gray("View this release on Azure DevOps: "+adocontext.ReleaseURL(project, id)))
	return pshared.ResultExitError(result)
}
