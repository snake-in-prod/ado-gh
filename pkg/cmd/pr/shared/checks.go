package shared

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/snake-in-prod/ado-gh/api"
)

// CheckJSONFields are the public `--json` fields of Check.
var CheckJSONFields = []string{
	"name", "state", "bucket", "type", "isRequired", "description", "link", "startedAt", "completedAt",
}

// Check is one policy evaluation or PR status of a pull request.
type Check struct {
	Name string `json:"name"`
	// State is the raw policy or status state, "expired" for an expired build.
	State string `json:"state"`
	// Bucket is pass, fail, pending or skipping.
	Bucket string `json:"bucket"`
	// Type is build, statusPolicy, minimumReviewers, requiredReviewers, workItemLinking,
	// commentResolution, mergeStrategy, policy (other policy types) or status (a PR status).
	Type        string     `json:"type"`
	IsRequired  bool       `json:"isRequired"`
	Description string     `json:"description"`
	Link        string     `json:"link"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
}

// Gating reports whether c counts toward the exit code: only build and status policies and
// PR statuses do; reviewer, work item, comment and other policies are listed only.
func (c Check) Gating() bool {
	return c.Type == "build" || c.Type == "statusPolicy" || c.Type == "status"
}

// CheckCounts counts checks per bucket.
type CheckCounts struct{ Pass, Fail, Pending, Skipping int }

// TallyChecks counts the gating checks per bucket.
func TallyChecks(checks []Check) CheckCounts {
	var n CheckCounts
	for _, c := range checks {
		if !c.Gating() {
			continue
		}
		switch c.Bucket {
		case "pass":
			n.Pass++
		case "fail":
			n.Fail++
		case "skipping":
			n.Skipping++
		default:
			n.Pending++
		}
	}
	return n
}

// policyTypes maps policy type GUIDs to Check types; display names aren't stable.
var policyTypes = map[string]string{
	"0609b952-1397-4640-95ec-e00a01b2c241": "build",
	"fa4e907d-c16b-4a4c-9dfa-4906e5d171dd": "minimumReviewers",
	"fd2167ab-b0be-447a-8ec8-39368250530e": "requiredReviewers",
	"40e92b44-2fe1-4dd6-b3d8-74a9c21d0c6e": "workItemLinking",
	"c6a1889d-b943-4856-b76f-9e46bb6b0df2": "commentResolution",
	"fa4e907d-c16b-4a4c-9dfa-4916e5d171ab": "mergeStrategy",
	"cbdc66da-9728-4af8-aada-9a5a32e4a226": "statusPolicy",
}

// PolicyType returns the Check type of policy evaluation e from its policy type GUID, or
// "policy" for other or unknown types.
func PolicyType(e policy.PolicyEvaluationRecord) string {
	if cfg := e.Configuration; cfg != nil && cfg.Type != nil && cfg.Type.Id != nil {
		if t, ok := policyTypes[cfg.Type.Id.String()]; ok {
			return t
		}
	}
	return "policy"
}

// checkBucket maps a policy evaluation or PR status state to its bucket; queued, running,
// notSet, pending, expired and unknown states are pending.
func checkBucket(state string) string {
	switch state {
	case "approved", "succeeded":
		return "pass"
	case "rejected", "broken", "failed", "error":
		return "fail"
	case "notApplicable":
		return "skipping"
	}
	return "pending"
}

// Checks returns the policy evaluations and the latest PR statuses of pr, a pull request of
// the repository at repoURL. Statuses matching a status policy are folded into its row.
func Checks(ctx context.Context, c *api.PRClient, pr *git.GitPullRequest, repoURL string) ([]Check, error) {
	evals, err := Evaluations(ctx, c, pr)
	if err != nil {
		return nil, err
	}
	statuses, err := c.Statuses(ctx, Deref(pr.PullRequestId))
	if err != nil {
		return nil, err
	}
	return aggregateChecks(evals, statuses, repoURL, PRURL(repoURL, *pr)), nil
}

// PolicyChecks returns only the policy evaluations of pr, a pull request of the repository
// at repoURL.
func PolicyChecks(ctx context.Context, c *api.PRClient, pr *git.GitPullRequest, repoURL string) ([]Check, error) {
	evals, err := Evaluations(ctx, c, pr)
	if err != nil {
		return nil, err
	}
	return aggregateChecks(evals, nil, repoURL, PRURL(repoURL, *pr)), nil
}

// Evaluations fetches the policy evaluations of pr (notApplicable ones aren't requested);
// none when its project ID is unknown.
func Evaluations(ctx context.Context, c *api.PRClient, pr *git.GitPullRequest) ([]policy.PolicyEvaluationRecord, error) {
	if pr.Repository == nil || pr.Repository.Project == nil || pr.Repository.Project.Id == nil {
		return nil, nil
	}
	return c.PolicyEvaluations(ctx, pr.Repository.Project.Id.String(), Deref(pr.PullRequestId))
}

func aggregateChecks(evals []policy.PolicyEvaluationRecord, statuses []git.GitPullRequestStatus, repoURL, prURL string) []Check {
	keys, latest := latestStatuses(statuses)
	folded := map[string]bool{}
	out := []Check{}
	for _, e := range evals {
		ch := Check{State: string(Deref(e.Status)), Link: prURL, StartedAt: timeOf(e.StartedDate), CompletedAt: timeOf(e.CompletedDate)}
		var settings map[string]any
		typeName := ""
		if cfg := e.Configuration; cfg != nil {
			ch.IsRequired = Deref(cfg.IsBlocking)
			settings, _ = cfg.Settings.(map[string]any)
			if cfg.Type != nil {
				typeName = Deref(cfg.Type.DisplayName)
			}
		}
		ch.Type, ch.Name = PolicyType(e), typeName
		switch ch.Type {
		case "build":
			ctx, _ := e.Context.(map[string]any)
			if expired, _ := ctx["isExpired"].(bool); expired || ch.State == "expired" {
				ch.State = "expired"
			}
			if id, ok := ctx["buildId"].(float64); ok {
				base, _, _ := strings.Cut(repoURL, "/_git/")
				ch.Link = fmt.Sprintf("%s/_build/results?buildId=%d", base, int64(id))
			}
			ch.Name = cmp.Or(str(settings, "displayName"), typeName)
		case "statusPolicy":
			genre, name := str(settings, "statusGenre"), str(settings, "statusName")
			ch.Name = cmp.Or(str(settings, "defaultDisplayName"), contextName(genre, name))
			key := statusKey(genre, name)
			if s, ok := latest[key]; ok {
				folded[key] = true
				ch.Description = Deref(s.Description)
				ch.Link = cmp.Or(Deref(s.TargetUrl), prURL)
			}
		}
		ch.Bucket = checkBucket(ch.State)
		out = append(out, ch)
	}
	for _, key := range keys {
		if folded[key] {
			continue
		}
		s := latest[key]
		var genre, name string
		if s.Context != nil {
			genre, name = Deref(s.Context.Genre), Deref(s.Context.Name)
		}
		ch := Check{
			Name: contextName(genre, name), State: string(Deref(s.State)), Type: "status",
			Description: Deref(s.Description), Link: cmp.Or(Deref(s.TargetUrl), prURL), StartedAt: timeOf(s.CreationDate),
		}
		ch.Bucket = checkBucket(ch.State)
		if ch.Bucket != "pending" {
			ch.CompletedAt = timeOf(s.UpdatedDate)
		}
		out = append(out, ch)
	}
	return out
}

// latestStatuses keeps the latest status per context (genre, name): the highest ID, then
// the latest updatedDate. keys lists the contexts in order of first appearance.
func latestStatuses(all []git.GitPullRequestStatus) (keys []string, latest map[string]git.GitPullRequestStatus) {
	latest = map[string]git.GitPullRequestStatus{}
	for _, s := range all {
		var key string
		if s.Context != nil {
			key = statusKey(Deref(s.Context.Genre), Deref(s.Context.Name))
		}
		cur, seen := latest[key]
		if !seen {
			keys = append(keys, key)
		}
		if !seen || Deref(s.Id) > Deref(cur.Id) ||
			Deref(s.Id) == Deref(cur.Id) && timeAfter(s.UpdatedDate, cur.UpdatedDate) {
			latest[key] = s
		}
	}
	return keys, latest
}

func statusKey(genre, name string) string {
	return strings.ToLower(genre) + "/" + strings.ToLower(name)
}

// contextName is "genre/name", or name alone without a genre.
func contextName(genre, name string) string {
	if genre == "" {
		return name
	}
	return genre + "/" + name
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func timeOf(t *azuredevops.Time) *time.Time {
	if t == nil {
		return nil
	}
	return &t.Time
}

func timeAfter(a, b *azuredevops.Time) bool {
	return a != nil && (b == nil || a.Time.After(b.Time))
}
