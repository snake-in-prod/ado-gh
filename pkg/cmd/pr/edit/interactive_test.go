package edit

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const iaWipLabel = "dddddddd-0000-0000-0000-000000000004"

// iaTTY makes e a terminal whose prompts read answers one byte at a time (each accessible
// prompt starts a new scanner).
func (e *fieldsEnv) iaTTY(answers string) {
	e.ios.SetStdinTTY(true)
	e.ios.SetStdoutTTY(true)
	e.in.WriteString(answers)
	e.ios.In = iotest.OneByteReader(e.in)
}

// stubIaPR stubs the SDK locations and GET of pull request 12 with reviewers, labels and the
// repository IDs work item links need.
func (e *fieldsEnv) stubIaPR() {
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 12, "title": "T", "status": "active",
		"sourceRefName": "refs/heads/feat", "targetRefName": "refs/heads/main",
		"repository": map[string]any{"name": "web", "id": lnkRepo, "project": map[string]any{"id": lnkProject}},
		"reviewers": []map[string]any{
			{"id": revOpt, "displayName": "Opal", "vote": 0},
			{"id": revReq, "displayName": "Rex", "vote": 0, "isRequired": true},
		},
		"labels": []map[string]any{{"id": lnkBugLabel, "name": "bug"}, {"id": iaWipLabel, "name": "wip"}},
	}))
}

func TestEditInteractiveFields(t *testing.T) {
	e := newFieldsEnv(t)
	// Title, Description, Base branch; new title, keep the description, new base.
	e.iaTTY("1\n2\n3\n0\nNew title\n\ndevelop\n")
	e.stubFieldsPR()
	e.stubFieldsRefs("develop", true)
	e.stubFieldsPatch("refs/heads/develop")

	err := e.run(t, "12")
	require.NoError(t, err)
	e.reg.Verify(t)
	assert.Equal(t, []map[string]any{{"title": "New title", "targetRefName": "refs/heads/develop"}}, e.patches,
		"the description kept its prefilled value, so it isn't sent")
	out := e.out.String()
	assert.Contains(t, out, "What would you like to edit?")
	assert.True(t, strings.HasSuffix(out, strings.Replace(fieldsURL, "ORG", e.org, 1)), out)
}

func TestEditInteractiveLists(t *testing.T) {
	e := newFieldsEnv(t)
	e.iaTTY("4\n5\n6\n0\n" + // Reviewers, Work items, Labels
		"1\n0\n" + revAnn + "\n" + // unselect Opal, add Ann
		"2\n0\n44\n" + // unselect #43, add #44
		"2\n0\ndocs, ui\n") // unselect wip, add docs and ui
	e.stubIaPR()
	e.stubRevPut(revAnn, 0)
	e.stubRevDelete(revOpt, httpmock.StringResponse(""))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+fieldsRepoPath+"/pullRequests/12/workitems"),
		httpmock.JSONResponse(map[string]any{"count": 2, "value": []map[string]any{{"id": "42"}, {"id": "43"}}}))
	ok := httpmock.JSONResponse(map[string]any{"id": 1})
	e.stubLnkPatch("44", ok)
	e.stubLnkGet("43", lnkArtifact)
	e.stubLnkPatch("43", ok)
	e.reg.Register(httpmock.REST(http.MethodDelete, e.org+fieldsRepoPath+"/pullRequests/12/labels/"+iaWipLabel),
		httpmock.StringResponse(""))
	for range 2 {
		e.reg.Register(httpmock.REST(http.MethodPost, e.org+fieldsRepoPath+"/pullRequests/12/labels"),
			func(req *http.Request) (*http.Response, error) {
				e.patches = append(e.patches, revReqBody(req))
				return httpmock.JSONResponse(map[string]any{"name": "x"})(req)
			})
	}

	err := e.run(t, "12")
	require.NoError(t, err)
	// Verify fails on unused stubs; RoundTrip fails on unexpected requests (e.g. removing Rex).
	e.reg.Verify(t)
	assert.Contains(t, e.patches, map[string]any{"name": "docs"})
	assert.Contains(t, e.patches, map[string]any{"name": "ui"})
	assert.Empty(t, e.errOut.String())
	out := e.out.String()
	for _, want := range []string{"Opal", "Rex", "#42", "#43", "bug", "wip"} {
		assert.Contains(t, out, want, "current values are listed")
	}
}

func TestEditInteractiveErrors(t *testing.T) {
	tests := []struct {
		name    string
		answers string
		wantErr string
	}{
		{name: "nothing selected", answers: "0\n", wantErr: "nothing to edit"},
		{name: "invalid work item", answers: "5\n0\n0\nabc\n", wantErr: `invalid work item ID "abc"`},
		{name: "description too long", answers: "2\n0\n" + strings.Repeat("x", 4001) + "\n",
			wantErr: "the description is 4001 characters long; the maximum is 4000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newFieldsEnv(t)
			e.iaTTY(tt.answers)
			e.stubIaPR()
			if strings.HasPrefix(tt.answers, "5") {
				e.reg.Register(httpmock.REST(http.MethodGet, e.org+fieldsRepoPath+"/pullRequests/12/workitems"),
					httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"id": "42"}}}))
			}

			err := e.run(t, "12")
			require.EqualError(t, err, tt.wantErr)
			e.reg.Verify(t)
			assert.Empty(t, e.patches)
		})
	}
}

func TestEditNonTTYWithoutFlags(t *testing.T) {
	e := newFieldsEnv(t)
	err := e.run(t, "12")
	var fe *cmdutil.FlagError
	require.True(t, errors.As(err, &fe), "%v", err)
	for _, flag := range []string{"--title", "--body", "--base", "--add-reviewer", "--add-work-item", "--remove-label"} {
		assert.Contains(t, err.Error(), flag)
	}
	e.reg.Verify(t)
}

func TestEditPatchFailureIsPartial(t *testing.T) {
	e := newFieldsEnv(t)
	e.stubRevPR()
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+fieldsRepoPath+"/pullRequests/12"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
	e.stubRevPut(revAnn, 0)

	err := e.run(t, "12", "-t", "New", "-b", "Body", "--add-reviewer", revAnn)
	e.reg.Verify(t)
	require.EqualError(t, err, "failed to update title, description: denied (HTTP 403)\napplied: added reviewer "+revAnn)
	// cobra prints the usage on Out after the URL.
	assert.True(t, strings.HasPrefix(e.out.String(), strings.Replace(fieldsURL, "ORG", e.org, 1)), e.out.String())
}
