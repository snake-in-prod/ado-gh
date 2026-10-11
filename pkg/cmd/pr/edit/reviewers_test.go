package edit

import (
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	revAnn  = "11111111-1111-1111-1111-111111111111" // not a reviewer
	revTeam = "22222222-2222-2222-2222-222222222222" // not a reviewer
	revOpt  = "33333333-3333-3333-3333-333333333333" // optional, no vote
	revVote = "44444444-4444-4444-4444-444444444444" // optional, approved
	revReq  = "55555555-5555-5555-5555-555555555555" // required (by policy)
	revGrp  = "66666666-6666-6666-6666-666666666666" // optional team
)

// stubRevPR stubs the SDK locations and GET of pull request 12 with its reviewers.
func (e *fieldsEnv) stubRevPR() {
	e.reg.StubLocations(e.org, append([]httpmock.Location{httpmock.ResourceAreasLocation}, httpmock.GitPRLocations...)...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 12, "title": "T", "status": "active",
		"sourceRefName": "refs/heads/feat", "targetRefName": "refs/heads/main",
		"repository": map[string]any{"name": "web"},
		"reviewers": []map[string]any{
			{"id": revOpt, "vote": 0},
			{"id": revVote, "vote": 10},
			{"id": revReq, "vote": 0, "isRequired": true},
			{"id": revGrp, "vote": 0, "isContainer": true},
		},
	}))
}

// stubRevIdentity answers the identity search for value with id; container marks a team.
// Register the identity client's discovery (StubIMS) once before the first one.
func (e *fieldsEnv) stubRevIdentity(value, id string, container bool) {
	e.reg.Register(func(req *http.Request) bool {
		return req.URL.Host == "vssps.dev.azure.com" && req.URL.Query().Get("filterValue") == value
	}, httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{
		{"id": id, "providerDisplayName": value, "isContainer": container},
	}}))
}

// stubRevPut stubs PUT of reviewer id, recording the body and answering with vote.
func (e *fieldsEnv) stubRevPut(id string, vote int) {
	e.reg.Register(httpmock.REST(http.MethodPut, e.org+fieldsRepoPath+"/pullRequests/12/reviewers/"+id),
		func(req *http.Request) (*http.Response, error) {
			if err := e.record(req); err != nil {
				return nil, err
			}
			return httpmock.JSONResponse(map[string]any{"id": id, "vote": vote})(req)
		})
}

func (e *fieldsEnv) stubRevDelete(id string, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodDelete, e.org+fieldsRepoPath+"/pullRequests/12/reviewers/"+id), resp)
}

func TestEditReviewers(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		ims      bool // names are looked up, so the identity client is discovered
		stub     func(e *fieldsEnv)
		bodies   []map[string]any // PUT bodies, in order
		wantErr  string
		local    bool // fails before any edit
		wantWarn string
	}{
		{
			name: "add users and teams from repeated and comma lists",
			args: []string{"--add-reviewer", "ann@contoso.com", "--add-reviewer", `[My Proj]\Team,` + revAnn},
			ims:  true,
			stub: func(e *fieldsEnv) {
				e.stubRevIdentity("ann@contoso.com", revAnn, false)
				e.stubRevIdentity(`[My Proj]\Team`, revTeam, true)
				e.stubRevPut(revAnn, 0)
				e.stubRevPut(revTeam, 0)
			},
			bodies: []map[string]any{{"id": revAnn}, {"id": revTeam}},
		},
		{
			name: "adding an existing reviewer is a no-op",
			args: []string{"--add-reviewer", revOpt + "," + revReq},
		},
		{
			name:   "add required reviewer",
			args:   []string{"--add-required-reviewer", revAnn},
			stub:   func(e *fieldsEnv) { e.stubRevPut(revAnn, 0) },
			bodies: []map[string]any{{"id": revAnn, "isRequired": true}},
		},
		{
			name: "promote optional reviewers keeping the vote",
			args: []string{"--add-required-reviewer", revOpt + "," + revVote},
			stub: func(e *fieldsEnv) {
				e.stubRevPut(revOpt, 0)
				e.stubRevPut(revVote, 10)
			},
			bodies: []map[string]any{{"id": revOpt, "isRequired": true}, {"id": revVote, "isRequired": true}},
		},
		{
			name:     "promotion that resets the vote warns",
			args:     []string{"--add-required-reviewer", revVote},
			stub:     func(e *fieldsEnv) { e.stubRevPut(revVote, 0) },
			bodies:   []map[string]any{{"id": revVote, "isRequired": true}},
			wantWarn: "! Promoting " + revVote + " to required reset their vote\n",
		},
		{
			name: "already required is a no-op",
			args: []string{"--add-required-reviewer", revReq},
		},
		{
			name: "remove users and teams",
			args: []string{"--remove-reviewer", revOpt, "--remove-reviewer", `[My Proj]\Reviewers`},
			ims:  true,
			stub: func(e *fieldsEnv) {
				e.stubRevIdentity(`[My Proj]\Reviewers`, revGrp, true)
				e.stubRevDelete(revOpt, httpmock.StringResponse(""))
				e.stubRevDelete(revGrp, httpmock.StringResponse(""))
			},
		},
		{
			name:     "remove a non-reviewer warns locally",
			args:     []string{"--remove-reviewer", "ann@contoso.com"},
			ims:      true,
			stub:     func(e *fieldsEnv) { e.stubRevIdentity("ann@contoso.com", revAnn, false) },
			wantWarn: "! ann@contoso.com is not a reviewer of pull request !12\n",
		},
		{
			name: "policy-required removal is a partial failure",
			args: []string{"--remove-reviewer", revReq + "," + revOpt, "--add-reviewer", revAnn},
			stub: func(e *fieldsEnv) {
				e.stubRevPut(revAnn, 0)
				e.stubRevDelete(revReq, httpmock.StatusJSONResponse(http.StatusBadRequest,
					map[string]any{"message": "The reviewer is required by policy"}))
				e.stubRevDelete(revOpt, httpmock.StringResponse(""))
			},
			bodies: []map[string]any{{"id": revAnn}},
			wantErr: "failed to remove reviewer " + revReq + ": The reviewer is required by policy (HTTP 400)\n" +
				"applied: added reviewer " + revAnn + ", removed reviewer " + revOpt,
		},
		{
			name: "unknown identity fails before any edit",
			args: []string{"-t", "New", "--add-reviewer", revAnn + ",bob"},
			ims:  true,
			stub: func(e *fieldsEnv) {
				for range 3 { // General, DirectoryAlias, AccountName
					e.reg.Register(func(req *http.Request) bool {
						return req.URL.Host == "vssps.dev.azure.com" && req.URL.Query().Get("filterValue") == "bob"
					}, httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
				}
			},
			wantErr: "could not resolve bob: user must be added to the organization",
			local:   true,
		},
		{
			name:    "same identity added and removed fails before any edit",
			args:    []string{"-t", "New", "--add-required-reviewer", revOpt, "--remove-reviewer", revOpt},
			wantErr: revOpt + " is both added and removed as a reviewer",
			local:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newFieldsEnv(t)
			e.stubRevPR()
			if tt.ims {
				e.reg.StubIMS(e.org)
			}
			if tt.stub != nil {
				tt.stub(e)
			}

			err := e.run(t, append([]string{"12"}, tt.args...)...)
			// Verify fails on unused stubs; RoundTrip fails on unexpected requests, e.g. a PUT
			// for an existing reviewer or a DELETE for a non-reviewer.
			e.reg.Verify(t)
			if tt.bodies == nil {
				assert.Empty(t, e.patches)
			} else {
				assert.Equal(t, tt.bodies, e.patches, "PUT bodies never carry a vote")
			}
			url := strings.Replace(fieldsURL, "ORG", e.org, 1)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.True(t, strings.HasPrefix(err.Error(), tt.wantErr), err.Error())
				// cobra prints the error after the warnings.
				assert.True(t, strings.HasPrefix(e.errOut.String(), tt.wantWarn), e.errOut.String())
				// cobra prints the usage on Out after the URL.
				if tt.local {
					assert.NotContains(t, e.out.String(), "pullrequest/12")
				} else {
					assert.EqualError(t, err, tt.wantErr)
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

func TestEditReviewersAfterFieldEdit(t *testing.T) {
	e := newFieldsEnv(t)
	e.stubRevPR()
	e.stubFieldsPatch("refs/heads/main")
	e.stubRevDelete(revReq, httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "required"}))

	err := e.run(t, "12", "-t", "New", "--remove-reviewer", revReq)
	e.reg.Verify(t)
	require.EqualError(t, err, "failed to remove reviewer "+revReq+": required (HTTP 400)\napplied: title")
}
