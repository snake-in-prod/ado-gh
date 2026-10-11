package list

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	annID = "11111111-1111-1111-1111-111111111111"
	bobID = "22222222-2222-2222-2222-222222222222"
)

type listEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *browsertest.Browser
}

// newListEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newListEnv(t *testing.T, tty bool) *listEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetTerminalWidth(80)
	org := "prlist-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	return &listEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
}

func (e *listEnv) run(t *testing.T, args ...string) error {
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
		Browser: e.browser,
	}
	cmd := NewCmdList(f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// stubLocations stubs OPTIONS on the org for the Git and ResourceAreas locations; withIMS adds
// ResourceAreas and the OPTIONS call on the vssps host used by the identity resolver.
func (e *listEnv) stubLocations(withIMS bool) {
	e.reg.StubLocations(e.org, append([]httpmock.Location{httpmock.ResourceAreasLocation}, httpmock.GitPRLocations...)...)
	if withIMS {
		e.reg.StubIMS(e.org)
	}
}

func (e *listEnv) stubIdentity(value, id string) {
	e.reg.Register(func(req *http.Request) bool {
		return req.URL.Host == "vssps.dev.azure.com" && req.URL.Query().Get("filterValue") == value
	}, httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"id": id, "providerDisplayName": value}}}))
}

func (e *listEnv) stubList(prs ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests"),
		httpmock.JSONResponse(map[string]any{"count": len(prs), "value": prs}))
}

// listQuery returns the query of the n-th pull request list request.
func (e *listEnv) listQuery(t *testing.T, n int) map[string]string {
	t.Helper()
	var got []map[string]string
	for _, r := range e.reg.Requests {
		if strings.HasSuffix(r.URL.Path, "/pullRequests") {
			q := map[string]string{}
			for k, v := range r.URL.Query() {
				q[k] = v[0]
			}
			got = append(got, q)
		}
	}
	require.Greater(t, len(got), n)
	return got[n]
}

var created = time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)

func listPR(id int, title, status string, draft bool, branch string) map[string]any {
	return map[string]any{
		"pullRequestId": id, "title": title, "status": status, "isDraft": draft,
		"sourceRefName": "refs/heads/" + branch, "targetRefName": "refs/heads/main",
		"creationDate": created.Format(time.RFC3339), "mergeStatus": "succeeded",
		"createdBy": map[string]any{"id": annID, "displayName": "Ann Lee", "uniqueName": "ann@contoso.com"},
		"reviewers": []map[string]any{{"id": bobID, "displayName": "Bob", "vote": 10}},
	}
}

func TestListFiltersMapToSearchCriteria(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(true)
	e.stubIdentity("ann@contoso.com", annID)
	e.stubList(listPR(1, "Fix", "completed", false, "feat"))
	e.stubList()

	err := e.run(t, "-s", "completed", "-A", "ann@contoso.com", "--reviewer", bobID, "-B", "main", "-H", "refs/heads/feat", "-L", "5")
	require.NoError(t, err)
	e.reg.Verify(t)
	q := e.listQuery(t, 0)
	assert.Equal(t, "completed", q["searchCriteria.status"])
	assert.Equal(t, annID, q["searchCriteria.creatorId"])
	assert.Equal(t, bobID, q["searchCriteria.reviewerId"])
	assert.Equal(t, "refs/heads/main", q["searchCriteria.targetRefName"])
	assert.Equal(t, "refs/heads/feat", q["searchCriteria.sourceRefName"])
	assert.Equal(t, "5", q["$top"])
	assert.Equal(t, "0", q["$skip"])
}

func TestListDefaults(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(false)
	e.stubList()

	require.NoError(t, e.run(t))
	q := e.listQuery(t, 0)
	assert.Equal(t, "active", q["searchCriteria.status"])
	assert.Equal(t, "30", q["$top"])
	for _, k := range []string{"searchCriteria.creatorId", "searchCriteria.reviewerId", "searchCriteria.sourceRefName", "searchCriteria.targetRefName"} {
		assert.NotContains(t, q, k)
	}
}

func TestListLimitStopsPaging(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(false)
	e.stubList(listPR(3, "c", "active", false, "c"), listPR(2, "b", "active", false, "b"))

	require.NoError(t, e.run(t, "--limit", "2"))
	e.reg.Verify(t)
	assert.Equal(t, 2, strings.Count(e.out.String(), "\n"))
}

func TestListDraftFiltersClientSide(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(false)
	e.stubList(listPR(3, "Draft one", "active", true, "d"), listPR(2, "Ready", "active", false, "r"))
	e.stubList()

	require.NoError(t, e.run(t, "--draft"))
	assert.Equal(t, "100", e.listQuery(t, 0)["$top"])
	assert.Equal(t, "3\tDraft one\td\tdraft\t"+created.Format(time.RFC3339)+"\n", e.out.String())
}

func TestListTTYTable(t *testing.T) {
	e := newListEnv(t, true)
	e.stubLocations(false)
	e.stubList(listPR(12, "Add feature", "active", false, "feature/x"), listPR(9, "WIP", "active", true, "wip"))
	e.stubList()

	require.NoError(t, e.run(t))
	assert.Equal(t, ""+
		"\nShowing 2 active pull requests in My Proj/web\n\n"+
		"ID   TITLE        BRANCH     CREATED\n"+
		"!12  Add feature  feature/x  about 3 hours ago\n"+
		"!9   WIP          wip        about 3 hours ago\n", e.out.String())
}

func TestListNonTTYTSV(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(false)
	e.stubList(listPR(12, "Add feature", "abandoned", false, "feature/x"))
	e.stubList()

	require.NoError(t, e.run(t, "-s", "all"))
	assert.Equal(t, "12\tAdd feature\tfeature/x\tabandoned\t"+created.Format(time.RFC3339)+"\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestListJSON(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(false)
	e.stubList(listPR(12, "Add feature", "active", false, "feature/x"))
	e.stubList()

	require.NoError(t, e.run(t, "--json", "id,title,status,isDraft,author,sourceRefName,targetRefName,createdDate,url,reviewers,mergeStatus"))
	var got []map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	require.Len(t, got, 1)
	pr := got[0]
	assert.EqualValues(t, 12, pr["id"])
	assert.Equal(t, "Add feature", pr["title"])
	assert.Equal(t, "active", pr["status"])
	assert.Equal(t, false, pr["isDraft"])
	assert.Equal(t, "ann@contoso.com", pr["author"].(map[string]any)["uniqueName"])
	assert.Equal(t, "refs/heads/feature/x", pr["sourceRefName"])
	assert.Equal(t, "refs/heads/main", pr["targetRefName"])
	assert.Equal(t, created.Format(time.RFC3339), pr["createdDate"])
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_git/web/pullrequest/12", pr["url"])
	assert.EqualValues(t, 10, pr["reviewers"].([]any)[0].(map[string]any)["vote"])
	assert.Equal(t, "succeeded", pr["mergeStatus"])
}

func TestListJSONEmptyIsArray(t *testing.T) {
	e := newListEnv(t, true)
	e.stubLocations(false)
	e.stubList()

	require.NoError(t, e.run(t, "--json", "id"))
	assert.Equal(t, "[]\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestListWeb(t *testing.T) {
	e := newListEnv(t, true)
	require.NoError(t, e.run(t, "--web", "-s", "completed"))
	assert.Equal(t, []string{"https://dev.azure.com/" + e.org + "/My%20Proj/_git/web/pullrequests?_a=completed"}, e.browser.URLs)
	assert.Contains(t, e.errOut.String(), "Opening https://dev.azure.com/"+e.org+"/My%20Proj/_git/web/pullrequests?_a=completed in your browser.")
	assert.Empty(t, e.reg.Requests)
}

func TestListEmpty(t *testing.T) {
	for _, tt := range []struct {
		name, state, want string
		tty               bool
	}{
		{name: "tty active", tty: true, state: "active", want: "No active pull requests match your search in My Proj/web\n"},
		{name: "tty all", tty: true, state: "all", want: "No pull requests match your search in My Proj/web\n"},
		{name: "non-tty", state: "active"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newListEnv(t, tt.tty)
			e.stubLocations(false)
			e.stubList()
			require.NoError(t, e.run(t, "-s", tt.state))
			assert.Empty(t, e.out.String())
			assert.Equal(t, tt.want, e.errOut.String())
		})
	}
}

func TestListFlagErrors(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-s", "open"}, `invalid value for --state: "open"`},
		{[]string{"-L", "0"}, "invalid value for --limit: 0"},
		{[]string{"--web", "--json", "id"}, "cannot use `--web` with `--json`"},
	} {
		e := newListEnv(t, false)
		err := e.run(t, tt.args...)
		var fe *cmdutil.FlagError
		require.ErrorAs(t, err, &fe, tt.args)
		assert.Contains(t, err.Error(), tt.want)
	}
}

func TestListIdentityError(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(true)
	e.reg.Register(func(req *http.Request) bool { return req.URL.Host == "vssps.dev.azure.com" },
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
	e.reg.Register(func(req *http.Request) bool { return req.URL.Host == "vssps.dev.azure.com" },
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
	e.reg.Register(func(req *http.Request) bool { return req.URL.Host == "vssps.dev.azure.com" },
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))

	err := e.run(t, "--reviewer", "ghost@contoso.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not resolve ghost@contoso.com")
}

func TestListAPIError(t *testing.T) {
	e := newListEnv(t, false)
	e.stubLocations(false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: repository web not found"}))

	err := e.run(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TF401019")
}

func TestListNotLoggedIn(t *testing.T) {
	e := newListEnv(t, false)
	t.Setenv("ADO_TOKEN", "")
	var authErr *cmdutil.AuthError
	assert.ErrorAs(t, e.run(t), &authErr)
}
