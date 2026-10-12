// Package view implements `ado release view`.
package view

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/text"
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

var jsonFields = []string{
	"id", "name", "definition", "status", "description", "reason", "createdBy", "createdOn", "environments", "url",
}

type options struct {
	ref                 string
	log, logFailed, web bool
	exporter            *cmdutil.Exporter
}

// NewCmdView returns the `release view` command.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "view <release>",
		Short: "View a release",
		Long: `Display a Classic Release: its environments with the deploy phases, jobs and tasks of
their latest deployment, their manual approvals, and the issues reported (Annotations).

<release> is a release ID or URL.

--log and --log-failed print task logs, each line prefixed with its job and task.
In --json, the jobs of each environment use the build timeline's state and result
values (a task "failure" is state completed, result failed).`,
		Example: `  ado release view 45
  ado release view 45 --log-failed
  ado release view 45 --json environments
  ado release view 45 --web`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ref = args[0]
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&o.log, "log", false, "Print the logs of all tasks")
	fl.BoolVar(&o.logFailed, "log-failed", false, "Print the logs of failed tasks")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the release in the web browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	cmd.MarkFlagsMutuallyExclusive("web", "json", "log", "log-failed")
	return cmd
}

// viewJSON is the `--json` shape of a viewed release.
type viewJSON struct {
	ID           int                   `json:"id"`
	Name         string                `json:"name"`
	Definition   *pshared.PipelineJSON `json:"definition"`
	Status       string                `json:"status"`
	Description  string                `json:"description"`
	Reason       string                `json:"reason"`
	CreatedBy    *pshared.UserJSON     `json:"createdBy"`
	CreatedOn    *time.Time            `json:"createdOn"`
	Environments []shared.Environment  `json:"environments"`
	URL          string                `json:"url"`
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	id, p, err := shared.ParseReleaseRef(o.ref)
	if err != nil {
		return err
	}
	project := p
	if p.Project == "" {
		if project, err = f.BaseProject(); err != nil {
			return err
		}
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, adocontext.ReleaseURL(project, id))
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
	data := viewJSON{
		ID: id, Name: prshared.Deref(r.Name), Status: string(prshared.Deref(r.Status)),
		Description: prshared.Deref(r.Description), Reason: string(prshared.Deref(r.Reason)),
		CreatedOn: api.TimeOf(r.CreatedOn), Environments: tree.Environments, URL: adocontext.ReleaseURL(project, id),
	}
	if d := r.ReleaseDefinition; d != nil {
		data.Definition = &pshared.PipelineJSON{ID: prshared.Deref(d.Id), Name: prshared.Deref(d.Name), Path: prshared.Deref(d.Path)}
	}
	if u := r.CreatedBy; u != nil {
		data.CreatedBy = &pshared.UserJSON{ID: prshared.Deref(u.Id), DisplayName: prshared.Deref(u.DisplayName), UniqueName: prshared.Deref(u.UniqueName)}
	}

	ios := f.IOStreams
	switch {
	case o.exporter != nil:
		return o.exporter.Write(ios, data)
	case o.log || o.logFailed:
		return printLogs(ctx, ios.Out, c, *r, tree, o.logFailed)
	}
	printView(ios, data, tree)
	return nil
}

func printLogs(ctx context.Context, w io.Writer, c *api.ReleaseClient, r release.Release, tree shared.Tree, failed bool) error {
	id := prshared.Deref(r.Id)
	logs := pshared.TaskLogs(tree.Nodes, pshared.LogFilter{Failed: failed})
	if len(logs) == 0 {
		what := "task"
		if failed {
			what = "failed task"
		}
		return fmt.Errorf("no %s logs found in release %d", what, id)
	}
	return pshared.PrintLogs(ctx, w, tree.LogOpener(c, id), logs)
}

func printView(ios *iostreams.IOStreams, d viewJSON, tree shared.Tree) {
	cs := ios.ColorScheme()
	w := ios.Out
	tty := ios.IsStdoutTTY()
	var def, by string
	if d.Definition != nil {
		def = d.Definition.Name
	}
	if d.CreatedBy != nil {
		by = d.CreatedBy.DisplayName
	}

	if tty {
		title := cs.Bold(d.Name)
		if def != "" {
			title += " " + def
		}
		fmt.Fprintf(w, "%s %s\n", title, cs.Gray(fmt.Sprintf("· %s · ID %d", d.Status, d.ID)))
		if d.Description != "" {
			fmt.Fprintln(w, d.Description)
		}
		var created []string
		if d.CreatedOn != nil {
			created = append(created, "Created "+text.RelativeTimeAgo(time.Now(), *d.CreatedOn)+" by "+by)
		}
		if d.Reason != "" {
			created = append(created, d.Reason)
		}
		if len(created) > 0 {
			fmt.Fprintln(w, cs.Gray(strings.Join(created, " · ")))
		}
	} else {
		var createdOn string
		if d.CreatedOn != nil {
			createdOn = d.CreatedOn.UTC().Format(time.RFC3339)
		}
		for _, kv := range [][2]string{
			{"name", d.Name}, {"id", strconv.Itoa(d.ID)}, {"definition", def}, {"status", d.Status},
			{"description", d.Description}, {"reason", d.Reason}, {"created by", by}, {"created", createdOn}, {"url", d.URL},
		} {
			fmt.Fprintf(w, "%s:\t%s\n", kv[0], kv[1])
		}
	}

	if len(tree.Nodes) > 0 {
		fmt.Fprintf(w, "\n%s\n", cs.Bold("ENVIRONMENTS"))
		pshared.PrintTree(w, cs, tree.Nodes, true)
	}
	var approvals []string
	for _, e := range tree.Environments {
		for _, a := range e.Approvals {
			if !a.Automated {
				approvals = append(approvals, approvalLine(cs, e.Name, a))
			}
		}
	}
	if len(approvals) > 0 {
		fmt.Fprintf(w, "\n%s\n%s\n", cs.Bold("APPROVALS"), strings.Join(approvals, "\n"))
	}
	if anns := pshared.Annotations(tree.Nodes); len(anns) > 0 {
		fmt.Fprintf(w, "\n%s\n", cs.Bold("ANNOTATIONS"))
		pshared.PrintAnnotations(w, cs, anns)
	}
	if !tty {
		return
	}
	fmt.Fprintln(w)
	if len(pshared.TaskLogs(tree.Nodes, pshared.LogFilter{Failed: true})) > 0 {
		fmt.Fprintf(w, "To see what failed, try: ado release view %d --log-failed\n", d.ID)
	}
	fmt.Fprintln(w, cs.Gray("View this release on Azure DevOps: "+d.URL))
}

// approvalLine is e.g. "⏸ Prod: pre-deployment approval pending · Ann Lee", with the comment
// last when there is one.
func approvalLine(cs *iostreams.ColorScheme, env string, a shared.Approval) string {
	status := a.Status
	if status == "approved" {
		status = "succeeded"
	}
	mark, color := pshared.StatusMark(cs, status, "")
	kind := strings.TrimSuffix(a.Type, "Deploy") + "-deployment"
	line := fmt.Sprintf("%s %s: %s approval %s", color(mark), env, kind, a.Status)
	if a.Approver != nil {
		line += " · " + a.Approver.DisplayName
	}
	if a.Comments != "" {
		line += " · " + a.Comments
	}
	return line
}
