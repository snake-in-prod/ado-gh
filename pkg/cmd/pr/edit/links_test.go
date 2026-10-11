package edit

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lnkProject  = "aaaaaaaa-0000-0000-0000-000000000001"
	lnkRepo     = "bbbbbbbb-0000-0000-0000-000000000002"
	lnkBugLabel = "cccccccc-0000-0000-0000-000000000003"
	lnkArtifact = "vstfs:///Git/PullRequestId/" + lnkProject + "%2F" + lnkRepo + "%2F12"
)

// stubLnkPR stubs the SDK locations and GET of pull request 12 with its repository IDs; noIDs
// leaves them out. Like the real API, the GET has no labels (see stubLabels).
func (e *fieldsEnv) stubLnkPR(noIDs bool) {
	repo := map[string]any{"name": "web", "id": lnkRepo, "project": map[string]any{"id": lnkProject}}
	if noIDs {
		repo = map[string]any{"name": "web"}
	}
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 12, "title": "T", "status": "active",
		"sourceRefName": "refs/heads/feat", "targetRefName": "refs/heads/main",
		"repository": repo,
	}))
}

// stubLabels answers GET of the labels of pull request 12 with labels, as id/name pairs.
func (e *fieldsEnv) stubLabels(labels ...string) {
	var value []map[string]any
	for i := 0; i+1 < len(labels); i += 2 {
		value = append(value, map[string]any{"id": labels[i], "name": labels[i+1]})
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+fieldsRepoPath+"/pullRequests/12/labels"),
		httpmock.JSONResponse(map[string]any{"count": len(value), "value": value}))
}

// stubLnkPatch stubs PATCH of work item id, recording the json-patch document.
func (e *fieldsEnv) stubLnkPatch(id string, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/_apis/wit/workitems/"+id), func(req *http.Request) (*http.Response, error) {
		var ops []map[string]any
		if err := json.NewDecoder(req.Body).Decode(&ops); err != nil {
			return nil, err
		}
		e.patches = append(e.patches, ops...)
		return resp(req)
	})
}

// stubLnkGet answers GET of work item id with its rev and relation URLs.
func (e *fieldsEnv) stubLnkGet(id string, urls ...string) {
	rels := []map[string]any{}
	for _, u := range urls {
		rels = append(rels, map[string]any{"rel": "ArtifactLink", "url": u})
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/wit/workitems/"+id),
		httpmock.JSONResponse(map[string]any{"rev": 3, "relations": rels}))
}

func TestEditLinks(t *testing.T) {
	ok := httpmock.JSONResponse(map[string]any{"id": 1})
	branchLink := "vstfs:///Git/Ref/" + lnkProject + "%2F" + lnkRepo + "%2FGBfeat"
	tests := []struct {
		name     string
		args     []string
		noIDs    bool
		stub     func(e *fieldsEnv)
		ops      []map[string]any // json-patch operations sent, in order
		labels   []map[string]any // label POST bodies
		wantErr  string
		wantWarn string
	}{
		{
			name: "link work items from repeated and comma lists",
			args: []string{"--add-work-item", "5,6", "--add-work-item", "7"},
			stub: func(e *fieldsEnv) {
				e.stubLnkPatch("5", ok)
				e.stubLnkPatch("6", httpmock.StatusJSONResponse(http.StatusBadRequest,
					map[string]any{"message": "TF201035: Relation already exists."}))
				e.stubLnkPatch("7", ok)
			},
			ops: func() []map[string]any {
				op := map[string]any{"op": "add", "path": "/relations/-", "value": map[string]any{
					"rel": "ArtifactLink", "url": lnkArtifact, "attributes": map[string]any{"name": "Pull Request"},
				}}
				return []map[string]any{op, op, op}
			}(),
		},
		{
			name: "unlink keeps branch links",
			args: []string{"--remove-work-item", "5"},
			stub: func(e *fieldsEnv) {
				e.stubLnkGet("5", branchLink, strings.ToUpper(lnkArtifact))
				e.stubLnkPatch("5", ok)
			},
			ops: []map[string]any{
				{"op": "test", "path": "/rev", "value": float64(3)},
				{"op": "remove", "path": "/relations/1"},
			},
		},
		{
			name:     "unlinking an unlinked work item warns",
			args:     []string{"--remove-work-item", "5"},
			stub:     func(e *fieldsEnv) { e.stubLnkGet("5", branchLink) },
			wantWarn: "! work item #5 is not linked to pull request !12\n",
		},
		{
			name: "add labels creating tags on demand and skipping existing ones",
			args: []string{"--add-label", "needs docs,BUG", "--add-label", "ui"},
			stub: func(e *fieldsEnv) {
				e.stubLabels(lnkBugLabel, "bug")
				for range 2 {
					e.stubLabelPost()
				}
			},
			labels: []map[string]any{{"name": "needs docs"}, {"name": "ui"}},
		},
		{
			name: "remove labels by ID and warn on missing ones",
			args: []string{"--remove-label", "Bug,nope"},
			stub: func(e *fieldsEnv) {
				e.stubLabels(lnkBugLabel, "bug")
				e.reg.Register(httpmock.REST(http.MethodDelete, e.org+fieldsRepoPath+"/pullRequests/12/labels/"+lnkBugLabel),
					httpmock.StringResponse(""))
			},
			wantWarn: "! label nope not found on pull request !12\n",
		},
		{
			name: "repeated label names are applied once",
			args: []string{"--add-label", "ui,UI", "--remove-label", "bug", "--remove-label", "BUG"},
			stub: func(e *fieldsEnv) {
				e.stubLabels(lnkBugLabel, "bug")
				e.stubLabelPost()
				e.reg.Register(httpmock.REST(http.MethodDelete, e.org+fieldsRepoPath+"/pullRequests/12/labels/"+lnkBugLabel),
					httpmock.StringResponse(""))
			},
			labels: []map[string]any{{"name": "ui"}},
		},
		{
			name: "failures are reported with the applied edits",
			args: []string{"--add-work-item", "5,9", "--remove-work-item", "8", "--add-label", "ui", "--remove-label", "bug"},
			stub: func(e *fieldsEnv) {
				e.stubLnkPatch("5", ok)
				e.stubLnkPatch("9", httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "no 9"}))
				e.stubLabels(lnkBugLabel, "bug")
				e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/wit/workitems/8"),
					httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "no 8"}))
				e.reg.Register(httpmock.REST(http.MethodPost, e.org+fieldsRepoPath+"/pullRequests/12/labels"),
					httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "too many tags"}))
				e.reg.Register(httpmock.REST(http.MethodDelete, e.org+fieldsRepoPath+"/pullRequests/12/labels/"+lnkBugLabel),
					httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
			},
			ops: func() []map[string]any {
				op := map[string]any{"op": "add", "path": "/relations/-", "value": map[string]any{
					"rel": "ArtifactLink", "url": lnkArtifact, "attributes": map[string]any{"name": "Pull Request"},
				}}
				return []map[string]any{op, op}
			}(),
			wantErr: "failed to link work item #9: no 9 (HTTP 404)\n" +
				"failed to unlink work item #8: no 8 (HTTP 404)\n" +
				"failed to add label ui: too many tags (HTTP 400)\n" +
				"failed to remove label bug: denied (HTTP 403)\n" +
				"applied: linked work item #5",
		},
		{
			name:    "unknown repository IDs fail before any edit",
			args:    []string{"-t", "New", "--add-work-item", "5"},
			noIDs:   true,
			wantErr: "cannot link work items: the repository and project IDs of pull request !12 are unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newFieldsEnv(t)
			e.stubLnkPR(tt.noIDs)
			if tt.stub != nil {
				tt.stub(e)
			}

			err := e.run(t, append([]string{"12"}, tt.args...)...)
			e.reg.Verify(t)
			want := slices.Concat(tt.ops, tt.labels)
			if want == nil {
				assert.Empty(t, e.patches)
			} else {
				assert.Equal(t, want, e.patches)
			}
			url := strings.Replace(fieldsURL, "ORG", e.org, 1)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.True(t, strings.HasPrefix(e.errOut.String(), tt.wantWarn), e.errOut.String())
				if tt.noIDs {
					assert.NotContains(t, e.out.String(), "pullrequest/12")
				} else {
					assert.True(t, strings.HasPrefix(e.out.String(), url), "the URL is printed on partial failure")
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantWarn, e.errOut.String())
			assert.Equal(t, url, e.out.String())
		})
	}
}

func TestEditLabelsLookupFailure(t *testing.T) {
	for _, args := range [][]string{{"--remove-label", "bug"}, {"-t", "New", "--add-label", "ui"}} {
		t.Run(args[0], func(t *testing.T) {
			e := newFieldsEnv(t)
			e.stubLnkPR(false)
			e.reg.Register(httpmock.REST(http.MethodGet, e.org+fieldsRepoPath+"/pullRequests/12/labels"),
				httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))

			err := e.run(t, append([]string{"12"}, args...)...)
			require.EqualError(t, err, "denied (HTTP 403)")
			e.reg.Verify(t)
			assert.Empty(t, e.patches, "nothing is edited")
		})
	}
}
