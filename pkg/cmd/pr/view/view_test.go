package view

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const viewProjectID = "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c"

type viewBrowser struct{ urls []string }

func (b *viewBrowser) Browse(u string) error {
	b.urls = append(b.urls, u)
	return nil
}

type viewEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *viewBrowser
}

// newViewEnv returns an env for its own org (the SDK caches locations per base URL for the
// whole process) with the OPTIONS location stub registered.
func newViewEnv(t *testing.T, tty bool) *viewEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetTerminalWidth(80)
	org := "prview-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	return &viewEnv{org: org, reg: reg, ios: ios, out: out, errOut: errOut, browser: &viewBrowser{}}
}

func (e *viewEnv) run(t *testing.T, args ...string) error {
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
		GitClient: &git.Client{Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("refs/heads/feature/x\n"), nil
		}},
		Browser: e.browser,
	}
	cmd := NewCmdView(f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *viewEnv) stubPR(pr map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(pr))
}

func (e *viewEnv) stubLabels() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12/labels"),
		httpmock.JSONResponse(map[string]any{"count": 2, "value": []map[string]any{
			{"id": "6f1c2a5e-0000-4000-8000-000000000001", "name": "bug", "active": true},
			{"id": "6f1c2a5e-0000-4000-8000-000000000002", "name": "smoke", "active": true},
		}}))
}

// stubPolicies also stubs labels: the plain view fetches both.
func (e *viewEnv) stubPolicies() {
	e.stubLabels()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+viewProjectID+"/_apis/policy/evaluations"),
		httpmock.JSONResponse(map[string]any{"count": 4, "value": []map[string]any{
			evaluation("Minimum number of reviewers", "approved", true),
			evaluation("Build", "running", true),
			evaluation("Comment requirements", "rejected", false),
			evaluation("Work item linking", "notApplicable", true),
		}}))
}

func (e *viewEnv) stubThreads() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12/threads"),
		httpmock.JSONResponse(map[string]any{"count": 6, "value": []map[string]any{
			{"id": 1, "status": "active", "threadContext": map[string]any{
				"filePath":       "/src/app.go",
				"rightFileStart": map[string]any{"line": 10, "offset": 1},
				"rightFileEnd":   map[string]any{"line": 12, "offset": 1},
			}, "comments": []map[string]any{
				withID(comment("Bob", "Looks good", "text", false), 1, 0),
				withID(comment("Ann Lee", "Thanks\nwill do", "text", false), 2, 1),
			}},
			{"id": 2, "comments": []map[string]any{comment("Microsoft.VisualStudio.Services.TFS", "Bob voted 10", "system", false)}},
			{"id": 3, "status": "active", "isDeleted": true, "comments": []map[string]any{comment("Eve", "gone", "text", false)}},
			{"id": 4, "status": "fixed", "comments": []map[string]any{
				withID(comment("Carol", "Please rename", "text", false), 1, 0),
				withID(comment("Carol", "deleted reply", "text", true), 2, 1),
			}},
			{"id": 5, "properties": map[string]any{"CodeReviewThreadType": map[string]any{"$type": "System.String", "$value": "VoteUpdate"}},
				"comments": []map[string]any{comment("Bob", "Bob voted", "text", false)}},
			{"id": 6, "status": "active", "threadContext": map[string]any{
				"filePath": "/old.go", "leftFileStart": map[string]any{"line": 7, "offset": 1},
			}, "comments": []map[string]any{withID(comment("Dan", "Why removed?", "text", false), 1, 0)}},
		}}))
}

// withID sets a fixture comment's id and parentCommentId (0 = root comment).
func withID(c map[string]any, id, parent int) map[string]any {
	c["id"], c["parentCommentId"] = id, parent
	return c
}

func (e *viewEnv) requested(path string) bool {
	for _, r := range e.reg.Requests {
		if strings.HasSuffix(r.URL.Path, path) {
			return true
		}
	}
	return false
}

var (
	viewCreated   = time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	viewPublished = time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
)

const viewAuthorID = "6f2b7c3e-0d4e-4c55-9a43-0e1f6a1b2c3d"

func evaluation(name, status string, blocking bool) map[string]any {
	return map[string]any{"status": status, "configuration": map[string]any{
		"isBlocking": blocking, "type": map[string]any{"displayName": name},
	}}
}

func comment(author, content, kind string, deleted bool) map[string]any {
	return map[string]any{
		"author": map[string]any{"displayName": author, "id": viewAuthorID}, "content": content, "commentType": kind, "isDeleted": deleted,
		"publishedDate": viewPublished.Format(time.RFC3339),
	}
}

func viewPR() map[string]any {
	return map[string]any{
		"pullRequestId": 12, "title": "Add feature", "description": "Adds the feature.", "status": "active", "isDraft": false,
		"sourceRefName": "refs/heads/feature/x", "targetRefName": "refs/heads/main",
		"creationDate": viewCreated.Format(time.RFC3339), "mergeStatus": "succeeded",
		"createdBy":  map[string]any{"displayName": "Ann Lee", "uniqueName": "ann@contoso.com"},
		"repository": map[string]any{"name": "web", "project": map[string]any{"id": viewProjectID, "name": "My Proj"}},
		"reviewers": []map[string]any{
			{"displayName": "Bob", "vote": 10, "votedFor": []map[string]any{{"displayName": `[My Proj]\Web Team`, "isContainer": true}}},
			{"displayName": "Carol", "vote": -5, "isRequired": true},
			{"displayName": "Dan", "vote": 5},
			{"displayName": "Eve", "vote": -10},
			{"displayName": "Fay", "vote": 0},
			{"displayName": `[My Proj]\Web Team`, "vote": 10, "isContainer": true, "isRequired": true},
		},
	}
}

const wantReviewers = `Bob (Approved, for [My Proj]\Web Team), Carol (Waiting for author, required), ` +
	`Dan (Approved with suggestions), Eve (Rejected), Fay (No vote), [My Proj]\Web Team (team, required)`

func (e *viewEnv) prURL() string {
	return "https://dev.azure.com/" + e.org + "/My%20Proj/_git/web/pullrequest/12"
}

func TestViewTTY(t *testing.T) {
	e := newViewEnv(t, true)
	e.stubPR(viewPR())
	e.stubPolicies()

	require.NoError(t, e.run(t, "12"))
	e.reg.Verify(t)
	assert.Equal(t, ""+
		"Add feature !12\n"+
		"Active • Ann Lee wants to merge into main from feature/x • about 3 hours ago\n"+
		"Reviewers: "+wantReviewers+"\n"+
		"Labels: bug, smoke\n"+
		"Policies: 1 rejected, 1 running, 1 approved\n"+
		"\n"+
		"Adds the feature.\n"+
		"\n"+
		"View this pull request on Azure DevOps: "+e.prURL()+"\n", e.out.String())
	assert.False(t, e.requested("/threads"))
}

func TestViewTTYDraftWithoutDescriptionOrPolicies(t *testing.T) {
	e := newViewEnv(t, true)
	pr := viewPR()
	pr["isDraft"], pr["description"], pr["reviewers"] = true, "", nil
	e.stubPR(pr)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+viewProjectID+"/_apis/policy/evaluations"),
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12/labels"),
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))

	require.NoError(t, e.run(t, "!12"))
	assert.Equal(t, ""+
		"Add feature !12\n"+
		"Draft • Ann Lee wants to merge into main from feature/x • about 3 hours ago\n"+
		"\n"+
		"No description provided\n"+
		"\n"+
		"View this pull request on Azure DevOps: "+e.prURL()+"\n", e.out.String())
}

func TestViewCurrentBranchDefault(t *testing.T) {
	e := newViewEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests"),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []any{viewPR()}}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests"),
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
	e.stubPolicies()

	require.NoError(t, e.run(t))
	e.reg.Verify(t)
	assert.Equal(t, "refs/heads/feature/x", e.reg.Requests[1].URL.Query().Get("searchCriteria.sourceRefName"))
	assert.Equal(t, ""+
		"title:\tAdd feature\n"+
		"state:\tactive\n"+
		"draft:\tfalse\n"+
		"author:\tAnn Lee\n"+
		"reviewers:\t"+wantReviewers+"\n"+
		"labels:\tbug, smoke\n"+
		"policies:\t1 rejected, 1 running, 1 approved\n"+
		"id:\t12\n"+
		"url:\t"+e.prURL()+"\n"+
		"source:\tfeature/x\n"+
		"target:\tmain\n"+
		"--\n"+
		"Adds the feature.\n", e.out.String())
}

func TestViewCommentsTTY(t *testing.T) {
	e := newViewEnv(t, true)
	e.stubPR(viewPR())
	e.stubPolicies()
	e.stubThreads()

	require.NoError(t, e.run(t, "12", "--comments"))
	assert.Contains(t, e.out.String(), ""+
		"Adds the feature.\n"+
		"\n"+
		"Comments (3 threads)\n"+
		"\n"+
		"#1 active • /src/app.go:10-12\n"+
		"  Bob • about 2 hours ago\n"+
		"  Looks good\n"+
		"    ↳ Ann Lee • about 2 hours ago\n"+
		"      Thanks\n"+
		"      will do\n"+
		"\n"+
		"#4 fixed\n"+
		"  Carol • about 2 hours ago\n"+
		"  Please rename\n"+
		"\n"+
		"#6 active • /old.go:7\n"+
		"  Dan • about 2 hours ago\n"+
		"  Why removed?\n"+
		"\n"+
		"View this pull request on Azure DevOps: ")
}

func TestViewCommentsNonTTY(t *testing.T) {
	e := newViewEnv(t, false)
	e.stubPR(viewPR())
	e.stubThreads()

	require.NoError(t, e.run(t, "12", "-c"))
	assert.Equal(t, ""+
		"author:\tBob\nthread:\t1\nstatus:\tactive\nfile:\t/src/app.go\nline:\t10-12\n--\nLooks good\n--\n"+
		"author:\tAnn Lee\nthread:\t1\nstatus:\tactive\nfile:\t/src/app.go\nline:\t10-12\n--\nThanks\nwill do\n--\n"+
		"author:\tCarol\nthread:\t4\nstatus:\tfixed\nfile:\t\nline:\t\n--\nPlease rename\n--\n"+
		"author:\tDan\nthread:\t6\nstatus:\tactive\nfile:\t/old.go\nline:\t7\n--\nWhy removed?\n--\n", e.out.String())
	assert.False(t, e.requested("/evaluations"))
}

func TestViewCommentsNone(t *testing.T) {
	e := newViewEnv(t, true)
	e.stubPR(viewPR())
	e.stubPolicies()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12/threads"),
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))

	require.NoError(t, e.run(t, "12", "-c"))
	assert.Contains(t, e.out.String(), "Adds the feature.\n\nNo comments\n\nView this pull request")
}

func TestViewJSON(t *testing.T) {
	e := newViewEnv(t, false)
	e.stubPR(viewPR())
	e.stubPolicies()
	e.stubThreads()

	require.NoError(t, e.run(t, "12", "--json", "id,url,description,reviewers,policies,comments"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	assert.EqualValues(t, 12, got["id"])
	assert.Equal(t, e.prURL(), got["url"])
	assert.Equal(t, "Adds the feature.", got["description"])
	assert.Len(t, got["reviewers"], 6)
	assert.Equal(t, []any{
		map[string]any{"name": "Minimum number of reviewers", "status": "approved", "isBlocking": true},
		map[string]any{"name": "Build", "status": "running", "isBlocking": true},
		map[string]any{"name": "Comment requirements", "status": "rejected", "isBlocking": false},
		map[string]any{"name": "Work item linking", "status": "notApplicable", "isBlocking": true},
	}, got["policies"])
	want := []any{
		map[string]any{"id": 1.0, "status": "active", "filePath": "/src/app.go", "line": 10.0, "comments": []any{
			viewCommentJSON(1, "Bob", "Looks good"), viewCommentJSON(2, "Ann Lee", "Thanks\nwill do"),
		}},
		map[string]any{"id": 4.0, "status": "fixed", "filePath": nil, "line": nil, "comments": []any{
			viewCommentJSON(1, "Carol", "Please rename"),
		}},
		map[string]any{"id": 6.0, "status": "active", "filePath": "/old.go", "line": 7.0, "comments": []any{
			viewCommentJSON(1, "Dan", "Why removed?"),
		}},
	}
	assert.Equal(t, want, got["comments"], "comments is an alias of the threads shape")
}

func TestViewJSONThreads(t *testing.T) {
	e := newViewEnv(t, false)
	e.stubPR(viewPR())
	e.stubThreads()

	require.NoError(t, e.run(t, "12", "--json", "threads"))
	var got map[string][]map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	require.Len(t, got, 1)
	require.Len(t, got["threads"], 3)
	assert.EqualValues(t, 1, got["threads"][0]["id"])
	assert.EqualValues(t, 10, got["threads"][0]["line"])
	assert.Len(t, got["threads"][0]["comments"], 2)
	assert.False(t, e.requested("/evaluations"))
}

// viewCommentJSON is the decoded `--json threads` shape of a fixture comment.
func viewCommentJSON(id int, author, content string) map[string]any {
	return map[string]any{
		"id": float64(id), "content": content, "publishedDate": viewPublished.Format(time.RFC3339),
		"author": map[string]any{"displayName": author, "id": viewAuthorID},
	}
}

func TestViewJSONSkipsUnrequestedCalls(t *testing.T) {
	e := newViewEnv(t, false)
	e.stubPR(viewPR())

	require.NoError(t, e.run(t, "12", "--json", "id,title", "-c"))
	assert.JSONEq(t, `{"id":12,"title":"Add feature"}`, e.out.String())
	assert.False(t, e.requested("/evaluations"))
	assert.False(t, e.requested("/threads"))
}

func TestViewJSONEmptyLists(t *testing.T) {
	e := newViewEnv(t, false)
	pr := viewPR()
	pr["repository"] = map[string]any{"name": "web"}
	e.stubPR(pr)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12/threads"),
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))

	require.NoError(t, e.run(t, "12", "--json", "policies,comments,threads"))
	assert.JSONEq(t, `{"policies":[],"comments":[],"threads":[]}`, e.out.String())
}

func TestViewWeb(t *testing.T) {
	e := newViewEnv(t, true)
	e.stubPR(viewPR())

	require.NoError(t, e.run(t, "12", "--web"))
	assert.Equal(t, []string{e.prURL()}, e.browser.urls)
	assert.Equal(t, "Opening "+e.prURL()+" in your browser.\n", e.errOut.String())
	assert.Empty(t, e.out.String())
	assert.False(t, e.requested("/evaluations"))
}

func TestViewErrors(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		e := newViewEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401180"}))
		assert.EqualError(t, e.run(t, "12"), "pull request !12 not found in My Proj/web")
	})
	t.Run("policies", func(t *testing.T) {
		e := newViewEnv(t, false)
		e.stubPR(viewPR())
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+viewProjectID+"/_apis/policy/evaluations"),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
		assert.EqualError(t, e.run(t, "12"), "boom (HTTP 500)")
	})
	t.Run("threads", func(t *testing.T) {
		e := newViewEnv(t, false)
		e.stubPR(viewPR())
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12/threads"),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
		assert.EqualError(t, e.run(t, "12", "-c"), "boom (HTTP 500)")
	})
	t.Run("web with json", func(t *testing.T) {
		e := newViewEnv(t, false)
		var fe *cmdutil.FlagError
		assert.ErrorAs(t, e.run(t, "12", "--web", "--json", "id"), &fe)
	})
	t.Run("too many args", func(t *testing.T) {
		e := newViewEnv(t, false)
		assert.Error(t, e.run(t, "1", "2"))
	})
}

func TestLabelNames(t *testing.T) {
	assert.Equal(t, "", labelNames(nil))
	assert.Equal(t, "bug, smoke", labelNames([]shared.LabelJSON{{Name: "bug"}, {Name: "smoke"}}))
}

func TestViewJSONLabels(t *testing.T) {
	e := newViewEnv(t, false)
	e.stubPR(viewPR())
	e.stubLabels()
	require.NoError(t, e.run(t, "12", "--json", "labels", "--jq", ".labels[].name"))
	e.reg.Verify(t)
	assert.Equal(t, "bug\nsmoke\n", e.out.String())
}
