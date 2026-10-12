package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRefTestClient returns a pipeline client for project "proj" of org (one org per test: the
// SDK caches location discovery per base URL).
func newRefTestClient(t *testing.T, org string) (*api.PipelineClient, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.PipelineLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	return api.NewPipelineClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj"), reg
}

var refProj = adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "proj"}

func TestNormalizeFolder(t *testing.T) {
	for in, want := range map[string]string{
		"": `\`, `\`: `\`, "/": `\`, "A": `\A`, "A/B": `\A\B`, `\A\B`: `\A\B`, `/A/B/`: `\A\B`, `A\B/C`: `\A\B\C`,
	} {
		assert.Equal(t, want, NormalizeFolder(in), in)
	}
}

func TestParsePipelineRef(t *testing.T) {
	tests := []struct {
		ref  string
		want PipelineRef
	}{
		{"12", PipelineRef{ID: 12}},
		{"deploy", PipelineRef{Name: "deploy"}},
		{"my pipeline", PipelineRef{Name: "my pipeline"}},
		{`\deploy`, PipelineRef{Name: "deploy", Folder: `\`}},
		{"/deploy", PipelineRef{Name: "deploy", Folder: `\`}},
		{`\Apps\Web\deploy`, PipelineRef{Name: "deploy", Folder: `\Apps\Web`}},
		{"Apps/Web/deploy", PipelineRef{Name: "deploy", Folder: `\Apps\Web`}},
		{`Apps/Web\deploy`, PipelineRef{Name: "deploy", Folder: `\Apps\Web`}},
		{"https://dev.azure.com/org/proj/_build?definitionId=5", PipelineRef{ID: 5, Project: refProj}},
		{"https://dev.azure.com/org/proj/_build/index?DefinitionID=5&_a=summary", PipelineRef{ID: 5, Project: refProj}},
		{"https://dev.azure.com/org/proj/_build/definition?definitionId=5", PipelineRef{ID: 5, Project: refProj}},
		{"https://dev.azure.com/org/proj/_apis/build/definitions/5", PipelineRef{ID: 5, Project: refProj}},
		{"https://dev.azure.com/org/proj/_apis/build/Definitions/5?revision=3", PipelineRef{ID: 5, Project: refProj}},
		{"https://dev.azure.com/org/My%20Proj/_build?definitionId=5",
			PipelineRef{ID: 5, Project: adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "My Proj"}}},
		{"https://Fabrikam.VisualStudio.com/DefaultCollection/proj/_build?definitionId=5",
			PipelineRef{ID: 5, Project: adocontext.Repo{Host: "fabrikam.visualstudio.com", Org: "fabrikam", Project: "proj"}}},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := ParsePipelineRef(tt.ref)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParsePipelineRefInvalid(t *testing.T) {
	for _, ref := range []string{
		"", " ", `\`, "/", "Apps/ ",
		"http://dev.azure.com/org/proj/_build?definitionId=5",
		"https://example.com/org/proj/_build?definitionId=5",
		"https://dev.azure.com/org/_build?definitionId=5",
		"https://dev.azure.com/org/proj/_build/results?buildId=5",
		"https://dev.azure.com/org/proj/_build?definitionId=x",
		"https://dev.azure.com/org/proj/_build?definitionId=0",
		"https://dev.azure.com/org/proj/_apis/build/builds/5",
		"https://dev.azure.com/org/proj/_git/repo",
		"https://user:secret@dev.azure.com/org/proj/_build/results?buildId=5",
		"https://user:secret@dev.azure.com/org/proj/_build?definitionId=%zz",
	} {
		t.Run(ref, func(t *testing.T) {
			_, err := ParsePipelineRef(ref)
			require.ErrorIs(t, err, errInvalidPipeline)
			assert.NotContains(t, err.Error(), "secret", "a ref is never echoed")
		})
	}
}

func TestParseRunRef(t *testing.T) {
	tests := []struct {
		ref     string
		id      int
		project adocontext.Repo
	}{
		{"812", 812, adocontext.Repo{}},
		{"https://dev.azure.com/org/proj/_build/results?buildId=812", 812, refProj},
		{"https://dev.azure.com/org/proj/_build/results?buildId=812&view=logs&j=abc&t=def", 812, refProj},
		{"https://dev.azure.com/org/proj/_build/results?view=results&BUILDID=812", 812, refProj},
		{"https://dev.azure.com/org/proj/_build/index?buildid=812", 812, refProj},
		{"https://dev.azure.com/org/proj/_build/index?definitionId=5&buildId=812", 812, refProj},
		{"https://dev.azure.com/org/proj/_apis/build/builds/812", 812, refProj},
		{"https://dev.azure.com/org/00000000-0000-0000-0000-000000000001/_apis/pipelines/5/runs/812", 812,
			adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "00000000-0000-0000-0000-000000000001"}},
		{"https://fabrikam.visualstudio.com/proj/_build/results?buildId=812", 812,
			adocontext.Repo{Host: "fabrikam.visualstudio.com", Org: "fabrikam", Project: "proj"}},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			id, project, err := ParseRunRef(tt.ref)
			require.NoError(t, err)
			assert.Equal(t, tt.id, id)
			assert.Equal(t, tt.project, project)
		})
	}
}

func TestParseRunRefInvalid(t *testing.T) {
	for _, ref := range []string{
		"", "0", "-1", "+5", "#5", "abc", "12a",
		"https://dev.azure.com/org/proj/_build?definitionId=5",
		"https://dev.azure.com/org/proj/_build/results",
		"https://dev.azure.com/org/proj/_build/results?buildId=-1",
		"https://dev.azure.com/org/proj/_apis/build/definitions/5",
		"https://dev.azure.com/org/proj/_apis/pipelines/5",
		"https://dev.azure.com/org/proj/_apis/build/builds/812/timeline",
		"https://dev.azure.com/org/_apis/build/builds/812",
		"https://user:secret@dev.azure.com/org/proj/_workitems/edit/1",
		"https://user:secret@github.com/org/proj/_build/results?buildId=1",
	} {
		t.Run(ref, func(t *testing.T) {
			_, _, err := ParseRunRef(ref)
			require.ErrorIs(t, err, errInvalidRun)
			assert.NotContains(t, err.Error(), "secret", "a ref is never echoed")
		})
	}
}

func TestResolvePipeline(t *testing.T) {
	tests := []struct {
		name, org string
		ref       PipelineRef
		want      int
		wantErr   string
	}{
		{name: "ID needs no request", ref: PipelineRef{ID: 7}, want: 7},
		{name: "unique name, any case", org: "refname", ref: PipelineRef{Name: "NIGHTLY"}, want: 2},
		{name: "root folder", org: "refroot", ref: PipelineRef{Name: "ado-gh", Folder: `\`}, want: 1},
		{name: "name in folder", org: "reffolder", ref: PipelineRef{Name: "deploy", Folder: `\apps\web`}, want: 4},
		{name: "ambiguous name lists matches", org: "refambig", ref: PipelineRef{Name: "deploy"},
			wantErr: "several pipelines in project proj have that name; pass an ID or a folder path:\n  \\Infra\\deploy (ID 3)\n  \\Apps\\Web\\deploy (ID 4)"},
		{name: "wrong folder", org: "refwrongfolder", ref: PipelineRef{Name: "deploy", Folder: `\`},
			wantErr: "no pipeline with that name in project proj; list them with: ado pipeline list -d"},
		{name: "server pre-filter is not exact", org: "refprefix", ref: PipelineRef{Name: "deplo"},
			wantErr: "no pipeline with that name in project proj; list them with: ado pipeline list -d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c *api.PipelineClient
			if tt.org != "" {
				var reg *httpmock.Registry
				c, reg = newRefTestClient(t, tt.org)
				reg.Register(func(r *http.Request) bool {
					return httpmock.REST(http.MethodGet, tt.org+"/proj/_apis/build/definitions")(r) && r.URL.Query().Get("name") == tt.ref.Name
				}, httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "definitions.json"))))
			}
			got, err := ResolvePipeline(context.Background(), c, tt.ref)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolvePipelineServerError(t *testing.T) {
	c, reg := newRefTestClient(t, "referr")
	reg.Register(httpmock.REST(http.MethodGet, "referr/proj/_apis/build/definitions"),
		httpmock.StatusJSONResponse(http.StatusUnauthorized, map[string]string{"message": "no access"}))
	_, err := ResolvePipeline(context.Background(), c, PipelineRef{Name: "x"})
	require.ErrorContains(t, err, "authentication failed for dev.azure.com (HTTP 401)")
}

const refRunsJSON = `{"count": 2, "value": [
 {"id": 812, "buildNumber": "20261012.2", "status": "inProgress", "sourceBranch": "refs/heads/main", "definition": {"id": 1, "name": "ado-gh"}},
 {"id": 811, "buildNumber": "20261012.1", "status": "completed", "result": "failed", "sourceBranch": "refs/heads/feature/x", "definition": {"id": 1, "name": "ado-gh"}}
]}`

// newRefPromptFactory returns a Factory whose terminal state is tty, answering prompts from the
// returned stdin buffer in accessible mode.
func newRefPromptFactory(tty bool) (*cmdutil.Factory, *iostreams.IOStreams, func(string)) {
	ios, in, _, _ := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	return &cmdutil.Factory{IOStreams: ios, Prompter: &cmdutil.Prompter{IO: ios, Accessible: true}}, ios, func(s string) { in.WriteString(s) }
}

func TestSelectRunPrompts(t *testing.T) {
	c, reg := newRefTestClient(t, "refselect")
	var query map[string][]string
	reg.Register(httpmock.REST(http.MethodGet, "refselect/proj/_apis/build/builds"), func(r *http.Request) (*http.Response, error) {
		query = r.URL.Query()
		return httpmock.StringResponse(refRunsJSON)(r)
	})
	f, ios, answer := newRefPromptFactory(true)
	answer("2\n")
	def, branch := []int{1}, "refs/heads/main"
	id, err := SelectRun(context.Background(), f, c, build.GetBuildsArgs{Definitions: &def, BranchName: &branch})
	require.NoError(t, err)
	assert.Equal(t, 811, id)
	assert.Equal(t, []string{"10"}, query["$top"])
	assert.Equal(t, []string{"1"}, query["definitions"])
	assert.Equal(t, []string{"refs/heads/main"}, query["branchName"])
	assert.Equal(t, []string{"queueTimeDescending"}, query["queryOrder"])
	out := ios.Out.(interface{ String() string }).String()
	assert.Contains(t, out, "● ado-gh 20261012.2 [main] ID 812")
	assert.Contains(t, out, "X ado-gh 20261012.1 [feature/x] ID 811")
}

func TestSelectRunNoRuns(t *testing.T) {
	c, reg := newRefTestClient(t, "refnoruns")
	reg.Register(httpmock.REST(http.MethodGet, "refnoruns/proj/_apis/build/builds"), httpmock.StringResponse(`{"count":0,"value":[]}`))
	f, _, _ := newRefPromptFactory(true)
	_, err := SelectRun(context.Background(), f, c, build.GetBuildsArgs{})
	assert.EqualError(t, err, "no recent runs found; pass a run ID")
}

func TestSelectRunServerError(t *testing.T) {
	c, reg := newRefTestClient(t, "refrunserr")
	reg.Register(httpmock.REST(http.MethodGet, "refrunserr/proj/_apis/build/builds"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]string{"message": "boom"}))
	f, _, _ := newRefPromptFactory(true)
	_, err := SelectRun(context.Background(), f, c, build.GetBuildsArgs{})
	require.EqualError(t, err, "boom (HTTP 500)")
}

func TestSelectRunNonTTY(t *testing.T) {
	f, _, _ := newRefPromptFactory(false)
	_, err := SelectRun(context.Background(), f, nil, build.GetBuildsArgs{})
	var flagErr *cmdutil.FlagError
	require.ErrorAs(t, err, &flagErr)
	assert.EqualError(t, err, "<run> argument required when not running interactively")
}

// TestSelectActiveRunMerges: in-progress and queued runs are offered together, newest queued
// first, at most selectRunLimit of them.
func TestSelectActiveRunMerges(t *testing.T) {
	c, reg := newRefTestClient(t, "refactive")
	runs := func(status string, ids ...int) string {
		var vals []string
		for _, id := range ids {
			vals = append(vals, fmt.Sprintf(`{"id":%d,"status":%q,"buildNumber":"b%d","queueTime":"2026-10-01T10:%02d:00Z","definition":{"name":"ado-gh"}}`, id, status, id, id))
		}
		return fmt.Sprintf(`{"count":%d,"value":[%s]}`, len(ids), strings.Join(vals, ","))
	}
	var statuses []string
	for _, body := range []string{runs("inProgress", 11, 9, 7, 5, 3, 1), runs("notStarted", 12, 10, 8, 6, 4, 2)} {
		reg.Register(httpmock.REST(http.MethodGet, "refactive/proj/_apis/build/builds"), func(r *http.Request) (*http.Response, error) {
			statuses = append(statuses, r.URL.Query().Get("statusFilter"))
			return httpmock.StringResponse(body)(r)
		})
	}
	f, ios, answer := newRefPromptFactory(true)
	answer("1\n")
	id, err := SelectActiveRun(context.Background(), f, c)
	require.NoError(t, err)
	assert.Equal(t, 12, id, "the newest queued run comes first")
	assert.Equal(t, []string{"inProgress", "notStarted"}, statuses)
	out := ios.Out.(interface{ String() string }).String()
	assert.Equal(t, selectRunLimit, strings.Count(out, " ID "), "capped at the prompt limit")
	assert.Contains(t, out, "⏸ ado-gh b12 [] ID 12")
	assert.NotContains(t, out, "ID 2\n", "the oldest runs are cut")
}
