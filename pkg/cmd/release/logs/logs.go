// Package logs implements `ado release logs`.
package logs

import (
	"context"
	"fmt"

	pshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref, environment, output string
	failed                   bool
}

// NewCmdLogs returns the `release logs` command.
func NewCmdLogs(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "logs <release>",
		Short: "Print or save the task logs of a release",
		Long: `Print the logs of the completed tasks of each environment's latest deployment, each
line prefixed with its job and task names, or write them to
<dir>/<environment>/<job>/<NN>_<task>.log with --output.

<release> is a release ID or URL. Agentless tasks have no logs.`,
		Example: `  ado release logs 45
  ado release logs 45 --environment Prod --failed
  ado release logs 45 -o out/`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ref = args[0]
			return runLogs(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.environment, "environment", "", "Only the logs of the environment with this name or ID")
	fl.BoolVar(&o.failed, "failed", false, "Only the logs of failed tasks")
	fl.StringVarP(&o.output, "output", "o", "", "Write the logs to files under this directory")
	return cmd
}

func runLogs(ctx context.Context, f *cmdutil.Factory, o *options) error {
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
	r, err := c.ReleaseByID(ctx, id)
	if err != nil {
		return err
	}
	tree := shared.NewTree(*r)
	nodes := tree.Nodes
	if o.environment != "" {
		i, err := tree.EnvironmentIndex(o.environment, id)
		if err != nil {
			return err
		}
		nodes = nodes[i : i+1]
	}

	ios := f.IOStreams
	logs := pshared.TaskLogs(nodes, pshared.LogFilter{Failed: o.failed})
	open := tree.LogOpener(c, id)
	switch {
	case len(logs) == 0:
		fmt.Fprintln(ios.ErrOut, "no task logs found")
	case o.output != "":
		if err := pshared.WriteLogs(ctx, o.output, open, logs); err != nil {
			return err
		}
		fmt.Fprintf(ios.ErrOut, "%s Wrote %d logs to %s\n", ios.ColorScheme().Green("✓"), len(logs), o.output)
	default:
		return pshared.PrintLogs(ctx, ios.Out, open, logs)
	}
	return nil
}
