// Package view implements `ado pr view`.
package view

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

var jsonFields = append(slices.Clone(shared.JSONFields), "description", "policies", "comments", "threads")

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
	Description *string      `json:"description"`
	Policies    []policyJSON `json:"policies"`
	// Comments is an alias of Threads.
	Comments []threadJSON `json:"comments"`
	Threads  []threadJSON `json:"threads"`
}

// threadJSON is the `--json threads` shape of a comment thread; Line is the start line.
type threadJSON struct {
	ID       *int                     `json:"id"`
	Status   *git.CommentThreadStatus `json:"status"`
	FilePath *string                  `json:"filePath"`
	Line     *int                     `json:"line"`
	Comments []commentJSON            `json:"comments"`
}

type commentJSON struct {
	ID            *int                `json:"id"`
	Author        *webapi.IdentityRef `json:"author"`
	Content       *string             `json:"content"`
	PublishedDate *azuredevops.Time   `json:"publishedDate"`
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
	var threads []git.GitPullRequestCommentThread
	if wants("comments") || wants("threads") || o.exporter == nil && o.comments {
		all, err := c.Threads(ctx, *pr.PullRequestId)
		if err != nil {
			return err
		}
		threads = shared.VisibleThreads(all)
		data.Threads = threadsJSON(threads)
		data.Comments = data.Threads
	}

	switch {
	case o.exporter != nil:
		return o.exporter.Write(ios, data)
	case rawComments:
		printRawComments(ios.Out, threads)
	case ios.IsStdoutTTY():
		printTTY(ios, data, threads, o.comments)
	default:
		printRaw(ios.Out, data)
	}
	return nil
}

func threadsJSON(threads []git.GitPullRequestCommentThread) []threadJSON {
	out := make([]threadJSON, len(threads))
	for i, t := range threads {
		out[i] = threadJSON{ID: t.Id, Status: t.Status}
		if file := shared.FilePath(t); file != "" {
			out[i].FilePath = &file
		}
		if line, _ := shared.ThreadLines(t); line > 0 {
			out[i].Line = &line
		}
		for _, c := range *t.Comments {
			out[i].Comments = append(out[i].Comments, commentJSON{
				ID: c.Id, Author: c.Author, Content: c.Content, PublishedDate: c.PublishedDate,
			})
		}
	}
	return out
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
				p.Name = shared.Deref(cfg.Type.DisplayName)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func printTTY(ios *iostreams.IOStreams, d viewJSON, threads []git.GitPullRequestCommentThread, comments bool) {
	cs := ios.ColorScheme()
	w := ios.Out
	state, color := "Active", cs.Green
	switch {
	case shared.Deref(d.IsDraft) && shared.Deref(d.Status) == git.PullRequestStatusValues.Active:
		state, color = "Draft", cs.Gray
	case shared.Deref(d.Status) == git.PullRequestStatusValues.Completed:
		state, color = "Completed", cs.Magenta
	case shared.Deref(d.Status) == git.PullRequestStatusValues.Abandoned:
		state, color = "Abandoned", cs.Red
	}
	fmt.Fprintf(w, "%s %s\n", cs.Bold(shared.Deref(d.Title)), cs.Gray(fmt.Sprintf("!%d", shared.Deref(d.ID))))
	fmt.Fprintf(w, "%s • %s wants to merge into %s from %s • %s\n", color(state), author(d),
		cs.Cyan(branch(d.TargetRefName)), cs.Cyan(branch(d.SourceRefName)), ago(d.CreatedDate))
	if r := reviewers(d.Reviewers); r != "" {
		fmt.Fprintf(w, "%s %s\n", cs.Bold("Reviewers:"), r)
	}
	if p := policySummary(d.Policies); p != "" {
		fmt.Fprintf(w, "%s %s\n", cs.Bold("Policies:"), p)
	}

	desc := strings.TrimSpace(shared.Deref(d.Description))
	if desc == "" {
		desc = cs.Gray("No description provided")
	}
	fmt.Fprintf(w, "\n%s\n\n", desc)

	if comments {
		if len(threads) == 0 {
			fmt.Fprint(w, cs.Gray("No comments")+"\n\n")
		} else {
			fmt.Fprintf(w, "%s\n\n", cs.Bold(fmt.Sprintf("Comments (%d threads)", len(threads))))
		}
		for _, t := range threads {
			head := []string{fmt.Sprintf("%s %s", cs.Bold(fmt.Sprintf("#%d", shared.Deref(t.Id))), shared.Deref(t.Status))}
			if file := shared.FilePath(t); file != "" {
				if lines := lineRange(t); lines != "" {
					file += ":" + lines
				}
				head = append(head, file)
			}
			fmt.Fprintln(w, strings.Join(head, " • "))
			// Every comment after the root one is a reply.
			for i, c := range *t.Comments {
				lead, indent := "  ", "  "
				if i > 0 {
					lead, indent = "    ↳ ", "      "
				}
				fmt.Fprintf(w, "%s%s • %s\n", lead, cs.Bold(commentAuthor(c)), ago(c.PublishedDate))
				fmt.Fprintf(w, "%s%s\n", indent, strings.ReplaceAll(shared.Deref(c.Content), "\n", "\n"+indent))
			}
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w, cs.Gray("View this pull request on Azure DevOps: "+d.URL))
}

func printRaw(w io.Writer, d viewJSON) {
	fmt.Fprintf(w, "title:\t%s\n", shared.Deref(d.Title))
	fmt.Fprintf(w, "state:\t%s\n", shared.Deref(d.Status))
	fmt.Fprintf(w, "draft:\t%t\n", shared.Deref(d.IsDraft))
	fmt.Fprintf(w, "author:\t%s\n", author(d))
	fmt.Fprintf(w, "reviewers:\t%s\n", reviewers(d.Reviewers))
	fmt.Fprintf(w, "policies:\t%s\n", policySummary(d.Policies))
	fmt.Fprintf(w, "id:\t%d\n", shared.Deref(d.ID))
	fmt.Fprintf(w, "url:\t%s\n", d.URL)
	fmt.Fprintf(w, "source:\t%s\n", branch(d.SourceRefName))
	fmt.Fprintf(w, "target:\t%s\n", branch(d.TargetRefName))
	fmt.Fprintf(w, "--\n%s\n", shared.Deref(d.Description))
}

func printRawComments(w io.Writer, threads []git.GitPullRequestCommentThread) {
	for _, t := range threads {
		for _, c := range *t.Comments {
			fmt.Fprintf(w, "author:\t%s\nthread:\t%d\nstatus:\t%s\nfile:\t%s\nline:\t%s\n--\n%s\n--\n",
				commentAuthor(c), shared.Deref(t.Id), shared.Deref(t.Status), shared.FilePath(t), lineRange(t),
				shared.Deref(c.Content))
		}
	}
}

// lineRange is a thread's anchor as "<line>" or "<line>-<endLine>", "" when it has none.
func lineRange(t git.GitPullRequestCommentThread) string {
	start, end := shared.ThreadLines(t)
	switch {
	case start == 0:
		return ""
	case end > start:
		return fmt.Sprintf("%d-%d", start, end)
	}
	return strconv.Itoa(start)
}

// reviewers lists reviewers with their vote. Teams cannot vote: they are marked as teams, and
// members show which teams their vote counts for (votedFor).
func reviewers(rs []git.IdentityRefWithVote) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		var notes []string
		if shared.Deref(r.IsContainer) {
			notes = append(notes, "team")
		} else {
			notes = append(notes, voteLabels[shared.Deref(r.Vote)])
		}
		if shared.Deref(r.IsRequired) {
			notes = append(notes, "required")
		}
		if r.VotedFor != nil && len(*r.VotedFor) > 0 {
			teams := make([]string, len(*r.VotedFor))
			for i, t := range *r.VotedFor {
				teams[i] = shared.Deref(t.DisplayName)
			}
			notes = append(notes, "for "+strings.Join(teams, ", "))
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", shared.Deref(r.DisplayName), strings.Join(notes, ", ")))
	}
	return strings.Join(parts, ", ")
}

func policySummary(ps []policyJSON) string {
	counts := map[policy.PolicyEvaluationStatus]int{}
	for _, p := range ps {
		counts[shared.Deref(p.Status)]++
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
	return shared.Deref(d.Author.DisplayName)
}

func commentAuthor(c git.Comment) string {
	if c.Author == nil {
		return ""
	}
	return shared.Deref(c.Author.DisplayName)
}

func branch(ref *string) string {
	return strings.TrimPrefix(shared.Deref(ref), "refs/heads/")
}

func ago(t *azuredevops.Time) string {
	if t == nil {
		return ""
	}
	return text.RelativeTimeAgo(time.Now(), t.Time)
}
