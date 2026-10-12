package approve

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// Approval IDs: liveID is the Checkpoint.Approval identifier in live_timeline_pending_approval.json
// (stage Deploy); deployID and prodID are those of plApproveTwoStages.
const (
	liveID   = "00000000-0000-0000-0000-000000000930"
	deployID = "00000000-0000-0000-0000-00000000a001"
	prodID   = "00000000-0000-0000-0000-00000000a002"
)

// plApproveTwoStages is a timeline whose stages Deploy and Prod both wait for an approval.
const plApproveTwoStages = `{"records": [
 {"id":"00000000-0000-0000-0000-00000000b001","type":"Stage","name":"Deploy","identifier":"Deploy","state":"pending","order":1},
 {"id":"00000000-0000-0000-0000-00000000b002","parentId":"00000000-0000-0000-0000-00000000b001","type":"Checkpoint","name":"Checkpoint","identifier":"Checkpoint.Deploy","state":"inProgress"},
 {"id":"` + deployID + `","parentId":"00000000-0000-0000-0000-00000000b002","type":"Checkpoint.Approval","name":"Checkpoint.Approval","identifier":"` + deployID + `","state":"inProgress"},
 {"id":"00000000-0000-0000-0000-00000000b003","type":"Stage","name":"Prod","identifier":"Prod","state":"pending","order":2},
 {"id":"00000000-0000-0000-0000-00000000b004","parentId":"00000000-0000-0000-0000-00000000b003","type":"Checkpoint","name":"Checkpoint","identifier":"Checkpoint.Prod","state":"inProgress"},
 {"id":"` + prodID + `","parentId":"00000000-0000-0000-0000-00000000b004","type":"Checkpoint.Approval","name":"Checkpoint.Approval","identifier":"` + prodID + `","state":"inProgress"}
]}`

type plApproveEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// queries are the query strings of the approvals GETs; patches the PATCH bodies.
	queries []url.Values
	patches []string
}

// newPLApproveEnv returns an env for its own org (the SDK caches discovery per base URL).
func newPLApproveEnv(t *testing.T, tty bool) *plApproveEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	org := "plapprove-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &plApproveEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.PipelineLocations...)
	return e
}

func (e *plApproveEnv) run(t *testing.T, reject bool, args ...string) error {
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
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdApprove(f)
	if reject {
		cmd = NewCmdReject(f)
	}
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func (e *plApproveEnv) stubTimeline(body string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds/812/timeline"), httpmock.StringResponse(body))
}

// stubQuery answers the approvals GET with approvals, recording its query.
func (e *plApproveEnv) stubQuery(approvals ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/pipelines/approvals"), func(r *http.Request) (*http.Response, error) {
		e.queries = append(e.queries, r.URL.Query())
		return httpmock.JSONResponse(map[string]any{"count": len(approvals), "value": approvals})(r)
	})
}

// stubPatch answers the approvals PATCH with resp, recording its body.
func (e *plApproveEnv) stubPatch(resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/pipelines/approvals"), func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		e.patches = append(e.patches, string(b))
		return resp(r)
	})
}

// plApproval is a pending approval id with one pending step per approver.
func plApproval(id string, minRequired int, approvers ...string) map[string]any {
	steps := make([]any, len(approvers))
	for i, a := range approvers {
		steps[i] = map[string]any{"assignedApprover": map[string]any{"displayName": a}, "status": "pending"}
	}
	return map[string]any{"id": id, "status": "pending", "minRequiredApprovers": minRequired, "executionOrder": "anyOrder", "steps": steps}
}

// plUpdated is a PATCH response with each id in status, as the live server answers (no steps).
func plUpdated(status string, ids ...string) httpmock.Responder {
	vals := make([]any, len(ids))
	for i, id := range ids {
		vals[i] = map[string]any{"id": id, "status": status}
	}
	return httpmock.JSONResponse(map[string]any{"count": len(vals), "value": vals})
}

func TestPLApproveSingle(t *testing.T) {
	e := newPLApproveEnv(t, false)
	e.stubTimeline(string(pipelinefixtures.Read(t, "live_timeline_pending_approval.json")))
	e.stubQuery(plApproval(liveID, 1, "Filip"))
	e.stubPatch(plUpdated("approved", liveID))

	require.NoError(t, e.run(t, false, "812", "-c", "ship it"))
	e.reg.Verify(t)
	require.Len(t, e.queries, 1)
	assert.Equal(t, liveID, e.queries[0].Get("approvalIds"))
	assert.Contains(t, e.queries[0].Get("$expand"), "steps")
	require.Len(t, e.patches, 1)
	assert.JSONEq(t, `[{"approvalId":"`+liveID+`","status":"approved","comment":"ship it"}]`, e.patches[0])
	assert.Equal(t, "✓ Approved Deploy\n", e.errOut.String())
}

func TestPLRejectRunURL(t *testing.T) {
	e := newPLApproveEnv(t, false)
	e.stubTimeline(string(pipelinefixtures.Read(t, "live_timeline_pending_approval.json")))
	e.stubQuery(plApproval(liveID, 1, "Filip"))
	e.stubPatch(plUpdated("rejected", liveID))

	require.NoError(t, e.run(t, true, "https://dev.azure.com/"+e.org+"/My%20Proj/_build/results?buildId=812"))
	e.reg.Verify(t)
	assert.JSONEq(t, `[{"approvalId":"`+liveID+`","status":"rejected"}]`, e.patches[0])
	assert.Equal(t, "✓ Rejected Deploy\n", e.errOut.String())
}

func TestPLApproveSeveral(t *testing.T) {
	two := []map[string]any{plApproval(deployID, 1, "Alice"), plApproval(prodID, 1, "Bob")}
	tests := []struct {
		name    string
		tty     bool
		args    []string
		input   string
		ids     []string
		wantErr string
		flagErr bool
	}{
		{name: "non-TTY needs a flag", args: []string{"812"},
			wantErr: "several approvals are pending (Deploy, Prod); pass --stage or --all", flagErr: true},
		{name: "stage ignores case", args: []string{"812", "--stage", "prod"}, ids: []string{prodID}},
		{name: "all one PATCH each", args: []string{"812", "--all"}, ids: []string{deployID, prodID}},
		{name: "TTY select", tty: true, args: []string{"812"}, input: "2\n", ids: []string{prodID}},
		{name: "unknown stage", args: []string{"812", "--stage", "QA"},
			wantErr: `no pending approval for stage "QA"; pending: Deploy, Prod`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLApproveEnv(t, tt.tty)
			e.stubTimeline(plApproveTwoStages)
			e.stubQuery(two...)
			for _, id := range tt.ids {
				e.stubPatch(plUpdated("approved", id))
			}
			e.in.WriteString(tt.input)

			err := e.run(t, false, tt.args...)
			e.reg.Verify(t)
			assert.Equal(t, deployID+","+prodID, e.queries[0].Get("approvalIds"))
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				var fe *cmdutil.FlagError
				assert.Equal(t, tt.flagErr, errors.As(err, &fe))
				assert.Empty(t, e.patches)
				return
			}
			require.NoError(t, err)
			require.Len(t, e.patches, len(tt.ids))
			var got []string
			for _, p := range e.patches {
				var body []map[string]any
				require.NoError(t, json.Unmarshal([]byte(p), &body))
				require.Len(t, body, 1, "one approval per PATCH")
				got = append(got, body[0]["approvalId"].(string))
			}
			assert.Equal(t, tt.ids, got)
		})
	}
}

func TestPLApproveAllPartialFailure(t *testing.T) {
	// The caller approves Deploy but is not an approver of Prod: Deploy is still approved and
	// reported, and the error names only Prod's approver.
	e := newPLApproveEnv(t, false)
	e.stubTimeline(plApproveTwoStages)
	e.stubQuery(plApproval(deployID, 1, "Alice"), plApproval(prodID, 1, "Bob"))
	e.stubPatch(plUpdated("approved", deployID))
	e.stubPatch(httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "not allowed"}))

	err := e.run(t, false, "812", "--all")
	e.reg.Verify(t)
	require.EqualError(t, err, "could not approve Prod: not allowed (HTTP 400); approvers: Bob")
	assert.Equal(t, "✓ Approved Deploy\n", e.errOut.String())
	assert.Len(t, e.patches, 2)
}

func TestPLApproveStageAndAll(t *testing.T) {
	e := newPLApproveEnv(t, false)
	err := e.run(t, false, "812", "--stage", "Prod", "--all")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.EqualError(t, err, "specify only one of --stage or --all")
}

func TestPLApproveNonePending(t *testing.T) {
	t.Run("no approval records", func(t *testing.T) {
		e := newPLApproveEnv(t, false)
		e.stubTimeline(string(pipelinefixtures.Read(t, "live_timeline_failed.json")))
		err := e.run(t, false, "812")
		require.EqualError(t, err, "no pending approvals for run 812")
		assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(io.Discard, err, ""))
		e.reg.Verify(t)
	})
	t.Run("already decided", func(t *testing.T) {
		e := newPLApproveEnv(t, false)
		e.stubTimeline(plApproveTwoStages)
		a := plApproval(deployID, 1, "Alice")
		a["status"] = "approved"
		e.stubQuery(a)
		require.EqualError(t, e.run(t, false, "812"), "no pending approvals for run 812")
		e.reg.Verify(t)
	})
}

func TestPLApproveInSequence(t *testing.T) {
	// Later steps of an inSequence approval are uninitiated; the approval itself is pending.
	inSeq := plApproval(liveID, 1, "Alice", "Bob")
	inSeq["executionOrder"] = "inSequence"
	inSeq["steps"].([]any)[1].(map[string]any)["status"] = "uninitiated"
	fixture := string(pipelinefixtures.Read(t, "live_timeline_pending_approval.json"))

	t.Run("acted on", func(t *testing.T) {
		e := newPLApproveEnv(t, false)
		e.stubTimeline(fixture)
		e.stubQuery(inSeq)
		e.stubPatch(plUpdated("approved", liveID))
		require.NoError(t, e.run(t, false, "812"))
		e.reg.Verify(t)
		assert.Len(t, e.patches, 1)
	})
	t.Run("refusal names the current approver", func(t *testing.T) {
		e := newPLApproveEnv(t, false)
		e.stubTimeline(fixture)
		e.stubQuery(inSeq)
		e.stubPatch(httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "You are not authorized."}))
		err := e.run(t, false, "812")
		require.EqualError(t, err, "could not approve Deploy: You are not authorized. (HTTP 400); approvers: Alice")
		e.reg.Verify(t)
	})
}

func TestPLApprovePartial(t *testing.T) {
	e := newPLApproveEnv(t, false)
	e.stubTimeline(string(pipelinefixtures.Read(t, "live_timeline_pending_approval.json")))
	e.stubQuery(plApproval(liveID, 2, "Alice", "Bob"))
	e.stubPatch(plUpdated("pending", liveID))

	require.NoError(t, e.run(t, false, "812"))
	e.reg.Verify(t)
	assert.Equal(t, "✓ Approved Deploy (1 of 2 required); still waiting\n", e.errOut.String())
}

func TestPLApproveErrors(t *testing.T) {
	fixture := string(pipelinefixtures.Read(t, "live_timeline_pending_approval.json"))
	tests := []struct {
		name    string
		query   httpmock.Responder
		patch   httpmock.Responder
		wantErr []string
		auth    bool
	}{
		{name: "not an approver",
			patch:   httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "not allowed"}),
			wantErr: []string{"could not approve Deploy: not allowed (HTTP 400); approvers: Alice, Bob"}},
		{name: "update forbidden",
			patch:   httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}),
			wantErr: []string{"could not approve Deploy: denied (HTTP 403); approvers: Alice, Bob", "vso.build_execute", "vso.pipelineresources_use"},
			auth:    true},
		{name: "update unauthorized",
			patch:   httpmock.StatusStringResponse(http.StatusUnauthorized, ""),
			wantErr: []string{"could not approve Deploy:", "approvers: Alice, Bob", "vso.build_execute"},
			auth:    true},
		{name: "server error has no approvers",
			patch:   httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}),
			wantErr: []string{"could not approve Deploy: boom (HTTP 500)"}},
		{name: "query forbidden",
			query:   httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}),
			wantErr: []string{"could not read the approvals of run 812: denied (HTTP 403)", `"Build (Read)" (vso.build) scope`},
			auth:    true},
		{name: "query failure",
			query:   httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}),
			wantErr: []string{"could not read the approvals of run 812: gone (HTTP 404)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLApproveEnv(t, false)
			e.stubTimeline(fixture)
			if tt.query != nil {
				e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/pipelines/approvals"), tt.query)
			} else {
				e.stubQuery(plApproval(liveID, 1, "Alice", "Bob"))
				e.stubPatch(tt.patch)
			}
			err := e.run(t, false, "812")
			require.Error(t, err)
			for _, w := range tt.wantErr {
				assert.Contains(t, err.Error(), w)
			}
			var ae *cmdutil.AuthError
			assert.Equal(t, tt.auth, errors.As(err, &ae))
			if tt.name == "update forbidden" || tt.name == "update unauthorized" {
				assert.NotContains(t, err.Error(), "vso.build)")
			}
			e.reg.Verify(t)
		})
	}
}

func TestPLApproveBadRef(t *testing.T) {
	e := newPLApproveEnv(t, false)
	require.ErrorContains(t, e.run(t, false, "abc"), "invalid run")
	require.Error(t, e.run(t, false))
}
