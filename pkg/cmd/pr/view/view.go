// Package view implements `ado pr view`.
package view

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

var jsonFields = append(slices.Clone(shared.JSONFields), "description", "policies", "comments")

var voteLabels = map[int]string{
	10: "Approved", 5: "Approved with suggestions", 0: "No vote", -5: "Waiting for author", -10: "Rejected",
}

// policyOrder is the summary order of policy statuses; notApplicable is left out.
var policyOrder = []policy.PolicyEvaluationStatus{"rejected", "broken", "running", "queued", "approved"}

type options struct {
	ref           string
	comments, web bool
	exporter      *cmdutil.Exporter
}

// NewCmdView returns the `pr view` command.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "view [<number> | <url> | <branch>]",
		Short: "View a pull request",
		Long: `Display the title, status, author, branches, reviewers with their votes, branch policy
summary and description of a pull request.

Without an argument, the pull request of the current branch is shown.`,
		Example: `  ado pr view
  ado pr view 123 --comments
  ado pr view feature/x --json id,reviewers,policies`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVarP(&o.comments, "comments", "c", false, "View pull request comment threads")
	cmd.Flags().BoolVarP(&o.web, "web", "w", false, "Open the pull request in the web browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

// viewJSON is the `--json` shape of a viewed pull request.
type viewJSON struct {
	shared.PRJSON
	Description *string                           `json:"description"`
	Policies    []policyJSON                      `json:"policies"`
	Comments    []git.GitPullRequestCommentThread `json:"comments"`
}

type policyJSON struct {
	Name       string                         `json:"name"`
	Status     *policy.PolicyEvaluationStatus `json:"status"`
	IsBlocking bool                           `json:"isBlocking"`
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, repo, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	data := viewJSON{PRJSON: shared.ToJSON(*pr, shared.RepoURL(repo)), Description: pr.Description}

	if o.web {
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "Opening %s in your browser.\n", data.URL)
		}
		return f.Browser.Browse(data.URL)
	}

	wants := func(field string) bool { return o.exporter != nil && slices.Contains(o.exporter.Fields, field) }
	rawComments := o.exporter == nil && o.comments && !ios.IsStdoutTTY()
	if wants("policies") || o.exporter == nil && !rawComments {
		if data.Policies, err = policies(ctx, c, pr); err != nil {
			return err
		}
	}
	if wants("comments") || o.exporter == nil && o.comments {
		if data.Comments, err = threads(ctx, c, *pr.PullRequestId); err != nil {
			return err
		}
	}

	switch {
	case o.exporter != nil:
		return o.exporter.Write(ios, data)
	case rawComments:
		printRawComments(ios.Out, data.Comments)
	case ios.IsStdoutTTY():
		printTTY(ios, data, o.comments)
	default:
		printRaw(ios.Out, data)
	}
	return nil
}

// policies returns the policy evaluations of pr; none when its project ID is unknown.
func policies(ctx context.Context, c *api.PRClient, pr *git.GitPullRequest) ([]policyJSON, error) {
	out := []policyJSON{}
	if pr.Repository == nil || pr.Repository.Project == nil || pr.Repository.Project.Id == nil {
		return out, nil
	}
	evals, err := c.PolicyEvaluations(ctx, pr.Repository.Project.Id.String(), *pr.PullRequestId)
	if err != nil {
		return nil, err
	}
	for _, e := range evals {
		p := policyJSON{Status: e.Status}
		if cfg := e.Configuration; cfg != nil {
			p.IsBlocking = cfg.IsBlocking != nil && *cfg.IsBlocking
			if cfg.Type != nil {
				p.Name = deref(cfg.Type.DisplayName)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// threads returns the comment threads of pull request id without system comments
// (votes, pushes) and deleted comments; threads left empty are dropped.
func threads(ctx context.Context, c *api.PRClient, id int) ([]git.GitPullRequestCommentThread, error) {
	all, err := c.Threads(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []git.GitPullRequestCommentThread{}
	for _, t := range all {
		if deref(t.IsDeleted) || t.Comments == nil {
			continue
		}
		comments := slices.DeleteFunc(*t.Comments, func(c git.Comment) bool {
			return deref(c.IsDeleted) || c.CommentType != nil && *c.CommentType == git.CommentTypeValues.System
		})
		if len(comments) > 0 {
			t.Comments = &comments
			out = append(out, t)
		}
	}
	return out, nil
}

func printTTY(ios *iostreams.IOStreams, d viewJSON, comments bool) {
	cs := ios.ColorScheme()
	w := ios.Out
	state, color := "Active", cs.Green
	switch {
	case deref(d.IsDraft) && deref(d.Status) == git.PullRequestStatusValues.Active:
		state, color = "Draft", cs.Gray
	case deref(d.Status) == git.PullRequestStatusValues.Completed:
		state, color = "Completed", cs.Magenta
	case deref(d.Status) == git.PullRequestStatusValues.Abandoned:
		state, color = "Abandoned", cs.Red
	}
	fmt.Fprintf(w, "%s %s\n", cs.Bold(deref(d.Title)), cs.Gray(fmt.Sprintf("!%d", deref(d.ID))))
	fmt.Fprintf(w, "%s • %s wants to merge into %s from %s • %s\n", color(state), author(d),
		cs.Cyan(branch(d.TargetRefName)), cs.Cyan(branch(d.SourceRefName)), ago(d.CreatedDate))
	if r := reviewers(d.Reviewers); r != "" {
		fmt.Fprintf(w, "%s %s\n", cs.Bold("Reviewers:"), r)
	}
	if p := policySummary(d.Policies); p != "" {
		fmt.Fprintf(w, "%s %s\n", cs.Bold("Policies:"), p)
	}

	desc := strings.TrimSpace(deref(d.Description))
	if desc == "" {
		desc = cs.Gray("No description provided")
	}
	fmt.Fprintf(w, "\n%s\n\n", desc)

	if comments {
		if len(d.Comments) == 0 {
			fmt.Fprint(w, cs.Gray("No comments")+"\n\n")
		} else {
			fmt.Fprintf(w, "%s\n\n", cs.Bold(fmt.Sprintf("Comments (%d threads)", len(d.Comments))))
		}
		for _, t := range d.Comments {
			head := []string{string(deref(t.Status))}
			if file := filePath(t); file != "" {
				head = append(head, file)
			}
			fmt.Fprintln(w, strings.Join(head, " • "))
			for _, c := range *t.Comments {
				fmt.Fprintf(w, "  %s • %s\n", cs.Bold(commentAuthor(c)), ago(c.PublishedDate))
				fmt.Fprintf(w, "  %s\n", strings.ReplaceAll(deref(c.Content), "\n", "\n  "))
			}
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w, cs.Gray("View this pull request on Azure DevOps: "+d.URL))
}

func printRaw(w io.Writer, d viewJSON) {
	fmt.Fprintf(w, "title:\t%s\n", deref(d.Title))
	fmt.Fprintf(w, "state:\t%s\n", deref(d.Status))
	fmt.Fprintf(w, "draft:\t%t\n", deref(d.IsDraft))
	fmt.Fprintf(w, "author:\t%s\n", author(d))
	fmt.Fprintf(w, "reviewers:\t%s\n", reviewers(d.Reviewers))
	fmt.Fprintf(w, "policies:\t%s\n", policySummary(d.Policies))
	fmt.Fprintf(w, "id:\t%d\n", deref(d.ID))
	fmt.Fprintf(w, "url:\t%s\n", d.URL)
	fmt.Fprintf(w, "source:\t%s\n", branch(d.SourceRefName))
	fmt.Fprintf(w, "target:\t%s\n", branch(d.TargetRefName))
	fmt.Fprintf(w, "--\n%s\n", deref(d.Description))
}

func printRawComments(w io.Writer, threads []git.GitPullRequestCommentThread) {
	for _, t := range threads {
		for _, c := range *t.Comments {
			fmt.Fprintf(w, "author:\t%s\nthread:\t%d\nstatus:\t%s\nfile:\t%s\n--\n%s\n--\n",
				commentAuthor(c), deref(t.Id), deref(t.Status), filePath(t), deref(c.Content))
		}
	}
}

// reviewers lists reviewers with their vote. Teams cannot vote: they are marked as teams, and
// members show which teams their vote counts for (votedFor).
func reviewers(rs []git.IdentityRefWithVote) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		var notes []string
		if deref(r.IsContainer) {
			notes = append(notes, "team")
		} else {
			notes = append(notes, voteLabels[deref(r.Vote)])
		}
		if deref(r.IsRequired) {
			notes = append(notes, "required")
		}
		if r.VotedFor != nil && len(*r.VotedFor) > 0 {
			teams := make([]string, len(*r.VotedFor))
			for i, t := range *r.VotedFor {
				teams[i] = deref(t.DisplayName)
			}
			notes = append(notes, "for "+strings.Join(teams, ", "))
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", deref(r.DisplayName), strings.Join(notes, ", ")))
	}
	return strings.Join(parts, ", ")
}

func policySummary(ps []policyJSON) string {
	counts := map[policy.PolicyEvaluationStatus]int{}
	for _, p := range ps {
		counts[deref(p.Status)]++
	}
	var parts []string
	for _, s := range policyOrder {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	return strings.Join(parts, ", ")
}

func author(d viewJSON) string {
	if d.Author == nil {
		return ""
	}
	return deref(d.Author.DisplayName)
}

func commentAuthor(c git.Comment) string {
	if c.Author == nil {
		return ""
	}
	return deref(c.Author.DisplayName)
}

func filePath(t git.GitPullRequestCommentThread) string {
	if t.ThreadContext == nil {
		return ""
	}
	return deref(t.ThreadContext.FilePath)
}

func branch(ref *string) string {
	return strings.TrimPrefix(deref(ref), "refs/heads/")
}

func ago(t *azuredevops.Time) string {
	if t == nil {
		return ""
	}
	return text.RelativeTimeAgo(time.Now(), t.Time)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
