package status

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	stMeID      = "33333333-3333-3333-3333-333333333333"
	stAnnID     = "11111111-1111-1111-1111-111111111111"
	stTeamID    = "aaaaaaaa-0000-0000-0000-000000000001"
	stReqTeamID = "aaaaaaaa-0000-0000-0000-000000000002"
	stOtherID   = "aaaaaaaa-0000-0000-0000-000000000003"
	stProjectID = "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c"
	stReqPolicy = "fd2167ab-b0be-447a-8ec8-39368250530e"
	stMinPolicy = "fa4e907d-c16b-4a4c-9dfa-4906e5d171dd"
)

type statusEnv struct {
	org      string
	reg      *httpmock.Registry
	ios      *iostreams.IOStreams
	out      *bytes.Buffer
	git      *gittest.Runner
	override string
}

var envSeq atomic.Int64

// newStatusEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newStatusEnv(t *testing.T, tty bool) *statusEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, _ := iostreams.Test()
	ios.SetStdoutTTY(tty)
	// The sequence number keeps orgs unique under -count>1.
	org := fmt.Sprintf("prstatus-%s-%d", strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name())), envSeq.Add(1))
	e := &statusEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, git: &gittest.Runner{}}
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

func (e *statusEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
		GitClient:    &git.Client{Run: e.git.Run},
		RepoOverride: e.override,
	}
	cmd := NewCmdStatus(f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(&bytes.Buffer{})
	return cmd.Execute()
}

// stubBase stubs the org's locations and the signed-in user; withIMS adds the identity
// discovery used to expand group reviewers.
func (e *statusEnv) stubBase(withIMS bool) {
	e.reg.StubLocations(e.org, append([]httpmock.Location{httpmock.ResourceAreasLocation}, httpmock.GitPRLocations...)...)
	if withIMS {
		e.reg.StubIMS(e.org)
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": stMeID}}))
}

func (e *statusEnv) repoPath() string {
	return e.org + "/My%20Proj/_apis/git/repositories/web/pullRequests"
}
func (e *statusEnv) projectPath() string { return e.org + "/My%20Proj/_apis/git/pullrequests" }

func isCurrent(q url.Values) bool { return q.Get("searchCriteria.sourceRefName") != "" }

// isCreated matches the active pull requests created by the signed-in user.
func isCreated(q url.Values) bool {
	return !isCurrent(q) && strings.EqualFold(q.Get("searchCriteria.creatorId"), stMeID) && q.Get("searchCriteria.status") == "active"
}

// isReview matches the active pull requests of anyone.
func isReview(q url.Values) bool {
	return !isCurrent(q) && q.Get("searchCriteria.creatorId") == "" && q.Get("searchCriteria.status") == "active"
}

// stubPRs answers the first page of the list at path whose query matches, and an empty
// second page when some but fewer than limit pull requests are returned.
func (e *statusEnv) stubPRs(path string, match func(url.Values) bool, limit int, prs ...map[string]any) {
	page := func(first bool) httpmock.Matcher {
		return func(req *http.Request) bool {
			return httpmock.REST(http.MethodGet, path)(req) && match(req.URL.Query()) && (req.URL.Query().Get("$skip") == "0") == first
		}
	}
	e.reg.Register(page(true), httpmock.JSONResponse(map[string]any{"count": len(prs), "value": prs}))
	if len(prs) > 0 && len(prs) < limit {
		e.reg.Register(page(false), httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
	}
}

func (e *statusEnv) stubStatuses(repo string, id int, statuses ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, fmt.Sprintf("%s/My%%20Proj/_apis/git/repositories/%s/pullRequests/%d/statuses", e.org, repo, id)),
		httpmock.JSONResponse(map[string]any{"count": len(statuses), "value": statuses}))
}

func (e *statusEnv) stubEvaluations(id int, evals ...map[string]any) {
	e.reg.Register(func(req *http.Request) bool {
		return httpmock.REST(http.MethodGet, e.org+"/"+stProjectID+"/_apis/policy/evaluations")(req) &&
			strings.HasSuffix(req.URL.Query().Get("artifactId"), fmt.Sprintf("/%d", id))
	}, httpmock.JSONResponse(map[string]any{"count": len(evals), "value": evals}))
}

func stPR(id int, title string, mods ...func(map[string]any)) map[string]any {
	pr := map[string]any{
		"pullRequestId": id, "title": title, "status": "active", "isDraft": false,
		"sourceRefName": fmt.Sprintf("refs/heads/b%d", id), "targetRefName": "refs/heads/main",
		"mergeStatus": "succeeded", "createdBy": map[string]any{"id": stAnnID, "displayName": "Ann"},
		"reviewers": []map[string]any{},
	}
	for _, m := range mods {
		m(pr)
	}
	return pr
}

func reviewer(id string, vote int, extra ...string) func(map[string]any) {
	return func(pr map[string]any) {
		rv := map[string]any{"id": id, "vote": vote}
		for _, x := range extra {
			rv[x] = true
		}
		pr["reviewers"] = append(pr["reviewers"].([]map[string]any), rv)
	}
}

func set(k string, v any) func(map[string]any) {
	return func(pr map[string]any) { pr[k] = v }
}

var inProject = set("repository", map[string]any{"name": "web", "project": map[string]any{"id": stProjectID, "name": "My Proj"}})

func stStatus(state string) map[string]any {
	return map[string]any{"id": 1, "state": state, "context": map[string]any{"genre": "ci", "name": state}}
}

func reqPolicy(status, group string) map[string]any {
	return map[string]any{"status": status, "configuration": map[string]any{
		"isBlocking": true, "type": map[string]any{"id": stReqPolicy, "displayName": "Required reviewers"},
		"settings": map[string]any{"requiredReviewerIds": []string{strings.ToUpper(group)}},
	}}
}

func TestStatusNeedsReviewFilter(t *testing.T) {
	e := newStatusEnv(t, false)
	e.stubBase(true)
	e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
	e.stubPRs(e.repoPath(), isReview, reviewCandidates,
		stPR(1, "mine", set("createdBy", map[string]any{"id": stMeID}), reviewer(stMeID, 0)),
		stPR(2, "direct", reviewer(strings.ToUpper(stMeID), 0), reviewer(stTeamID, 10, "isContainer")),
		stPR(3, "voted", reviewer(stMeID, 10)),
		stPR(4, "declined", reviewer(stMeID, 0, "hasDeclined")),
		stPR(5, "team draft", set("isDraft", true), reviewer(stTeamID, 0, "isContainer")),
		stPR(6, "team voted", reviewer(stTeamID, 10, "isContainer")),
		stPR(7, "required team, policy pending", inProject, reviewer(stReqTeamID, 10, "isContainer", "isRequired")),
		stPR(8, "required team, policy passed", inProject, reviewer(stReqTeamID, 10, "isContainer", "isRequired")),
		stPR(9, "required team approved, no policy", inProject, reviewer(stReqTeamID, 5, "isContainer", "isRequired")),
		stPR(10, "required team waiting, no policy", inProject, reviewer(stReqTeamID, -5, "isContainer", "isRequired")),
		stPR(11, "other team", reviewer(stOtherID, 0, "isContainer")),
		stPR(12, "team declined", reviewer(stTeamID, 0, "isContainer", "hasDeclined")),
	)
	e.reg.StubMemberGroups(e.org, stMeID, []string{"d;team", "d;req"},
		map[string]string{stTeamID: "d;TEAM", stReqTeamID: "d;req", stOtherID: "d;other"})
	e.stubEvaluations(7, reqPolicy("approved", stOtherID), reqPolicy("queued", stReqTeamID))
	e.stubEvaluations(8, reqPolicy("approved", stReqTeamID))
	e.stubEvaluations(9)
	e.stubEvaluations(10)

	require.NoError(t, e.run(t, "--json", "id"))
	assert.Equal(t, `{"createdBy":[],"currentBranch":null,"needsReview":[{"id":2},{"id":5},{"id":7},{"id":10}]}`+"\n", e.out.String())
	assert.Equal(t, []string{"symbolic-ref --quiet HEAD"}, e.git.Calls)
}

func TestStatusTTY(t *testing.T) {
	e := newStatusEnv(t, true)
	e.git.Replies = map[string]string{"symbolic-ref --quiet HEAD": "refs/heads/feat\n"}
	e.stubBase(false)
	e.stubPRs(e.repoPath(), isCurrent, 30,
		stPR(7, "Current   work", set("sourceRefName", "refs/heads/feat"), reviewer(stAnnID, 10), reviewer(stTeamID, 10, "isContainer")))
	e.stubStatuses("web", 7, stStatus("succeeded"), stStatus("notApplicable"))

	created := []map[string]any{stPR(20, "Draft thing", set("isDraft", true), reviewer(stAnnID, 0, "isRequired"))}
	e.stubStatuses("web", 20, stStatus("failed"))
	for id := 21; id <= 30; id++ {
		created = append(created, stPR(id, fmt.Sprintf("PR %d", id)))
		if id < 30 {
			e.stubStatuses("web", id)
		}
	}
	e.stubPRs(e.repoPath(), isCreated, sectionLimit+1, created...)
	e.stubPRs(e.repoPath(), isReview, reviewCandidates,
		stPR(40, "Needs eyes on a title that is far longer than fifty characters", reviewer(stMeID, 0), reviewer(stAnnID, -10)),
		stPR(41, "Mixed", reviewer(stMeID, 0)),
	)
	e.stubStatuses("web", 40, stStatus("pending"))
	e.stubStatuses("web", 41, stStatus("failed"), map[string]any{"id": 2, "state": "succeeded", "context": map[string]any{"name": "other"}})

	require.NoError(t, e.run(t))
	want := `
Relevant pull requests in My Proj/web

Current branch
  #7  Current work [feat]
  ✓ Checks passing ✓ 1 Approved

Created by you
  #20  Draft thing [b20] (draft)
  × All checks failing - Review required
  #21  PR 21 [b21]
  #22  PR 22 [b22]
  #23  PR 23 [b23]
  #24  PR 24 [b24]
  #25  PR 25 [b25]
  #26  PR 26 [b26]
  #27  PR 27 [b27]
  #28  PR 28 [b28]
  #29  PR 29 [b29]
  And more

Requesting a code review from you
  #40  Needs eyes on a title that is far longer than f... [b40]
  - Checks pending + Changes requested
  #41  Mixed [b41]
  × 1/2 checks failing

`
	assert.Equal(t, want, e.out.String())
}

func TestStatusEmpty(t *testing.T) {
	e := newStatusEnv(t, false)
	e.stubBase(false)
	e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
	e.stubPRs(e.repoPath(), isReview, reviewCandidates)

	require.NoError(t, e.run(t))
	assert.Equal(t, `
Relevant pull requests in My Proj/web

Current branch
  There is no current branch

Created by you
  You have no open pull requests

Requesting a code review from you
  You have no pull requests to review

`, e.out.String())
}

func TestStatusNoPRForBranchAndConflictStatus(t *testing.T) {
	e := newStatusEnv(t, false)
	e.git.Replies = map[string]string{"symbolic-ref --quiet HEAD": "refs/heads/feat\n"}
	e.stubBase(false)
	e.stubPRs(e.repoPath(), isCurrent, 30)
	e.stubPRs(e.repoPath(), isCreated, sectionLimit+1,
		stPR(1, "ok"), stPR(2, "conflicted", set("mergeStatus", "conflicts")), stPR(3, "queued", set("mergeStatus", "queued")))
	e.stubPRs(e.repoPath(), isReview, reviewCandidates)
	for id := 1; id <= 3; id++ {
		e.stubStatuses("web", id)
	}

	require.NoError(t, e.run(t, "-c"))
	assert.Contains(t, e.out.String(), "Current branch\n  There is no pull request associated with [feat]\n\n")
	assert.Contains(t, e.out.String(), `Created by you
  #1  ok [b1]
  ✓ No merge conflicts
  #2  conflicted [b2]
  × Merge conflicts
  #3  queued [b3]
  ! Merge conflict status unknown
`)
}

func TestStatusCurrentBranchClosed(t *testing.T) {
	e := newStatusEnv(t, false)
	e.git.Replies = map[string]string{"symbolic-ref --quiet HEAD": "refs/heads/feat\n"}
	e.stubBase(false)
	e.stubPRs(e.repoPath(), isCurrent, 30, stPR(3, "Old", set("status", "completed"), set("sourceRefName", "refs/heads/feat")))
	e.stubStatuses("web", 3)
	e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
	e.stubPRs(e.repoPath(), isReview, reviewCandidates)

	require.NoError(t, e.run(t))
	assert.Contains(t, e.out.String(), "Current branch\n  #3  Old [feat] - Completed\n\n")
}

func TestStatusRepoOverrideHidesCurrentBranch(t *testing.T) {
	for name, set := range map[string]func(t *testing.T, e *statusEnv){
		"-R":       func(_ *testing.T, e *statusEnv) { e.override = "My Proj/web" },
		"ADO_REPO": func(t *testing.T, _ *statusEnv) { t.Setenv("ADO_REPO", "My Proj/web") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newStatusEnv(t, false)
			set(t, e)
			e.stubBase(false)
			e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
			e.stubPRs(e.repoPath(), isReview, reviewCandidates)

			require.NoError(t, e.run(t))
			assert.NotContains(t, e.out.String(), "Current branch")
			assert.Empty(t, e.git.Calls)
		})
	}
}

func TestStatusProject(t *testing.T) {
	apiRepo := set("repository", map[string]any{"name": "api", "project": map[string]any{"name": "My Proj"}})
	stub := func(e *statusEnv) {
		e.override = "My Proj/web"
		e.stubBase(false)
		e.stubPRs(e.projectPath(), isCreated, sectionLimit+1, stPR(5, "In api", apiRepo))
		e.stubPRs(e.projectPath(), isReview, reviewCandidates)
	}
	t.Run("tty", func(t *testing.T) {
		e := newStatusEnv(t, true)
		stub(e)
		e.stubStatuses("api", 5)
		require.NoError(t, e.run(t, "--project"))
		assert.Contains(t, e.out.String(), "Relevant pull requests in My Proj\n")
		assert.Contains(t, e.out.String(), "  api#5  In api [b5]\n")
	})
	t.Run("without -R the current branch stays repo-scoped and other repos are prefixed", func(t *testing.T) {
		e := newStatusEnv(t, true)
		e.git.Replies = map[string]string{"symbolic-ref --quiet HEAD": "refs/heads/feat\n"}
		e.stubBase(false)
		e.stubPRs(e.repoPath(), isCurrent, 30, stPR(7, "Current", set("sourceRefName", "refs/heads/feat")))
		e.stubStatuses("web", 7)
		e.stubPRs(e.projectPath(), isCreated, sectionLimit+1)
		e.stubPRs(e.projectPath(), isReview, reviewCandidates, stPR(9, "Review api", apiRepo, reviewer(stMeID, 0)))
		e.stubStatuses("api", 9)

		require.NoError(t, e.run(t, "--project"))
		assert.Contains(t, e.out.String(), "Current branch\n  #7  Current [feat]\n")
		assert.Contains(t, e.out.String(), "Requesting a code review from you\n  api#9  Review api [b9]\n")
	})
	t.Run("json", func(t *testing.T) {
		e := newStatusEnv(t, false)
		stub(e)
		require.NoError(t, e.run(t, "--project", "--json", "id,url"))
		assert.Equal(t, `{"createdBy":[{"id":5,"url":"https://dev.azure.com/`+e.org+`/My%20Proj/_git/api/pullrequest/5"}],"currentBranch":null,"needsReview":[]}`+"\n", e.out.String())
	})
}

func TestStatusJSONChecksAndReviewDecision(t *testing.T) {
	e := newStatusEnv(t, false)
	e.git.Replies = map[string]string{"symbolic-ref --quiet HEAD": "refs/heads/feat\n"}
	e.stubBase(false)
	e.stubPRs(e.repoPath(), isCurrent, 30, stPR(7, "Current", inProject, reviewer(stAnnID, 10)))
	e.stubEvaluations(7, map[string]any{"status": "queued", "configuration": map[string]any{
		"isBlocking": true, "type": map[string]any{"id": stMinPolicy, "displayName": "Minimum number of reviewers"},
	}})
	e.stubStatuses("web", 7)
	e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
	e.stubPRs(e.repoPath(), isReview, reviewCandidates)

	require.NoError(t, e.run(t, "--json", "id,reviewDecision,checks", "--jq", ".currentBranch | [.id, .reviewDecision, .checks[0].type, .checks[0].bucket]"))
	assert.Equal(t, `[7,"reviewRequired","minimumReviewers","pending"]`+"\n", e.out.String())
}

func TestStatusNeedsReviewCapped(t *testing.T) {
	e := newStatusEnv(t, false)
	e.override = "My Proj/web"
	e.stubBase(false)
	e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
	var prs []map[string]any
	for id := 1; id <= 13; id++ {
		prs = append(prs, stPR(id, "x", reviewer(stMeID, 0)))
	}
	e.stubPRs(e.repoPath(), isReview, reviewCandidates, prs...)

	require.NoError(t, e.run(t, "--json", "id", "--jq", ".needsReview | map(.id) | join(\",\")"))
	assert.Equal(t, "1,2,3,4,5,6,7,8,9,10\n", e.out.String())
}

func TestStatusErrors(t *testing.T) {
	t.Run("current user", func(t *testing.T) {
		e := newStatusEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
		require.ErrorContains(t, e.run(t), "reading the current user")
		assert.Empty(t, e.git.Calls)
	})
	boom := httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"})
	t.Run("candidates list", func(t *testing.T) {
		e := newStatusEnv(t, false)
		e.override = "My Proj/web"
		e.stubBase(false)
		e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
		e.reg.Register(func(req *http.Request) bool {
			return httpmock.REST(http.MethodGet, e.repoPath())(req) && isReview(req.URL.Query())
		}, boom)
		require.EqualError(t, e.run(t), "boom (HTTP 500)")
		assert.NotContains(t, e.out.String(), "Relevant pull requests", "cobra prints only the usage")
	})
	t.Run("group memberships", func(t *testing.T) {
		e := newStatusEnv(t, false)
		e.override = "My Proj/web"
		e.stubBase(true)
		e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
		e.stubPRs(e.repoPath(), isReview, reviewCandidates, stPR(1, "team", reviewer(stTeamID, 0, "isContainer")))
		e.reg.Register(func(req *http.Request) bool { return req.URL.Host == "vssps.dev.azure.com" }, boom)
		require.ErrorContains(t, e.run(t), "boom")
		assert.NotContains(t, e.out.String(), "Relevant pull requests", "cobra prints only the usage")
	})
	t.Run("policy evaluations of a required group", func(t *testing.T) {
		e := newStatusEnv(t, false)
		e.override = "My Proj/web"
		e.stubBase(true)
		e.stubPRs(e.repoPath(), isCreated, sectionLimit+1)
		e.stubPRs(e.repoPath(), isReview, reviewCandidates,
			stPR(7, "required team", inProject, reviewer(stReqTeamID, 10, "isContainer", "isRequired")))
		e.reg.StubMemberGroups(e.org, stMeID, []string{"d;req"}, map[string]string{stReqTeamID: "d;req"})
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+stProjectID+"/_apis/policy/evaluations"), boom)
		require.EqualError(t, e.run(t), "boom (HTTP 500)")
		assert.NotContains(t, e.out.String(), "Relevant pull requests", "cobra prints only the usage")
	})
	t.Run("list", func(t *testing.T) {
		e := newStatusEnv(t, false)
		e.stubBase(false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()), httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: repo not found"}))
		require.EqualError(t, e.run(t), "TF401019: repo not found (HTTP 404)")
	})
	t.Run("git", func(t *testing.T) {
		e := newStatusEnv(t, false)
		e.git.Fails = map[string]error{"symbolic-ref --quiet HEAD": &git.Error{ExitCode: 128, Stderr: "not a git repository"}}
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
			httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": stMeID}}))
		var gitErr *git.Error
		require.ErrorAs(t, e.run(t), &gitErr)
		assert.Equal(t, 128, gitErr.ExitCode)
	})
}

func TestChecksSummary(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	cs := ios.ColorScheme()
	assert.Equal(t, "", checksSummary(cs, shared.CheckCounts{}))
	assert.Equal(t, "✓ Checks passing", checksSummary(cs, shared.CheckCounts{Pass: 1, Skipping: 1}))
	assert.Equal(t, "- Checks pending", checksSummary(cs, shared.CheckCounts{Pass: 1, Pending: 1}))
}
