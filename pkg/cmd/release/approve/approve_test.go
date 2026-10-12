package approve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

type relApproveEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// queries are the query strings of the approvals GETs; patches "id body" per PATCH.
	queries []url.Values
	patches []string
}

// newRelApproveEnv returns an env for its own org (the SDK caches discovery per base URL).
func newRelApproveEnv(t *testing.T, tty bool) *relApproveEnv {
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
	org := "relapprove-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &relApproveEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(org)
	return e
}

func (e *relApproveEnv) run(t *testing.T, reject bool, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "proj", Name: "web"}, nil
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

// stubList answers the approvals GET with body, recording its query.
func (e *relApproveEnv) stubList(body string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/approvals"), func(r *http.Request) (*http.Response, error) {
		e.queries = append(e.queries, r.URL.Query())
		return httpmock.StringResponse(body)(r)
	})
}

// stubApprovals answers the approvals GET with approvals.
func (e *relApproveEnv) stubApprovals(approvals ...map[string]any) {
	b, _ := json.Marshal(map[string]any{"count": len(approvals), "value": approvals})
	e.stubList(string(b))
}

// stubPatch answers the PATCH of approval id with resp (nil: the approval in the sent status).
func (e *relApproveEnv) stubPatch(id int, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodPatch, fmt.Sprintf("%s/proj/_apis/release/approvals/%d", e.org, id)),
		func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			e.patches = append(e.patches, fmt.Sprintf("%d %s", id, b))
			if resp == nil {
				var a map[string]any
				_ = json.Unmarshal(b, &a)
				a["id"] = id
				return httpmock.JSONResponse(a)(r)
			}
			return resp(r)
		})
}

// relApproval is pending approval id of type typ ("preDeploy"/"postDeploy") for environment env
// of release 1, assigned to approver.
func relApproval(id int, env string, envID int, typ, approver string) map[string]any {
	return map[string]any{
		"id": id, "status": "pending", "approvalType": typ, "isAutomated": false,
		"approver":           map[string]any{"displayName": approver},
		"release":            map[string]any{"id": 1, "name": "Release-1"},
		"releaseEnvironment": map[string]any{"id": envID, "name": env},
	}
}

func TestRelApproveSingle(t *testing.T) {
	e := newRelApproveEnv(t, false)
	e.stubList(string(pipelinefixtures.Read(t, "release_approvals.json")))
	e.stubPatch(4, nil)

	require.NoError(t, e.run(t, false, "1", "-c", "ship it"))
	e.reg.Verify(t)
	require.Len(t, e.queries, 1)
	assert.Equal(t, "1", e.queries[0].Get("releaseIdsFilter"))
	assert.Equal(t, "pending", e.queries[0].Get("statusFilter"))
	assert.Equal(t, "all", e.queries[0].Get("typeFilter"))
	require.Len(t, e.patches, 1)
	id, body, _ := strings.Cut(e.patches[0], " ")
	assert.Equal(t, "4", id)
	assert.JSONEq(t, `{"status":"approved","comments":"ship it"}`, body)
	assert.Equal(t, "✓ Approved Prod pre-deployment\n", e.errOut.String())
}

func TestRelRejectPostDeployURL(t *testing.T) {
	e := newRelApproveEnv(t, false)
	e.stubApprovals(relApproval(13, "Dev", 1, "postDeploy", "Test User"))
	e.stubPatch(13, nil)

	require.NoError(t, e.run(t, true, "https://dev.azure.com/"+e.org+"/proj/_release?releaseId=1"))
	e.reg.Verify(t)
	assert.Equal(t, []string{`13 {"status":"rejected"}`}, e.patches)
	assert.Equal(t, "✓ Rejected Dev post-deployment\n", e.errOut.String())
}

func TestRelApproveSeveral(t *testing.T) {
	two := []map[string]any{relApproval(13, "Dev", 1, "postDeploy", "Alice"), relApproval(4, "Prod", 2, "preDeploy", "Bob")}
	tests := []struct {
		name    string
		tty     bool
		args    []string
		input   string
		ids     []int
		wantErr string
		flagErr bool
	}{
		{name: "non-TTY needs a flag", args: []string{"1"},
			wantErr: "several approvals are pending (Dev post-deployment, Prod pre-deployment); pass --environment or --all", flagErr: true},
		{name: "environment ignores case", args: []string{"1", "--environment", "prod"}, ids: []int{4}},
		{name: "environment by ID", args: []string{"1", "--environment", "2"}, ids: []int{4}},
		{name: "all", args: []string{"1", "--all"}, ids: []int{13, 4}},
		{name: "TTY select", tty: true, args: []string{"1"}, input: "2\n", ids: []int{4}},
		{name: "unknown environment", args: []string{"1", "--environment", "QA"},
			wantErr: `no pending approval for environment "QA"; pending: Dev post-deployment, Prod pre-deployment`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelApproveEnv(t, tt.tty)
			e.stubApprovals(two...)
			for _, id := range tt.ids {
				e.stubPatch(id, nil)
			}
			e.in.WriteString(tt.input)

			err := e.run(t, false, tt.args...)
			e.reg.Verify(t)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				var fe *cmdutil.FlagError
				assert.Equal(t, tt.flagErr, errors.As(err, &fe))
				assert.Empty(t, e.patches)
				return
			}
			require.NoError(t, err)
			var got []int
			for _, p := range e.patches {
				var id int
				_, err := fmt.Sscan(p, &id)
				require.NoError(t, err)
				got = append(got, id)
			}
			assert.Equal(t, tt.ids, got)
		})
	}
}

func TestRelApproveEnvironmentAndAll(t *testing.T) {
	e := newRelApproveEnv(t, false)
	err := e.run(t, false, "1", "--environment", "Prod", "--all")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.EqualError(t, err, "specify only one of --environment or --all")
}

func TestRelApproveNonePending(t *testing.T) {
	e := newRelApproveEnv(t, false)
	automated := relApproval(5, "Dev", 1, "preDeploy", "")
	automated["isAutomated"] = true
	decided := relApproval(6, "Prod", 2, "preDeploy", "Bob")
	decided["status"] = "approved"
	e.stubApprovals(automated, decided)
	err := e.run(t, false, "1")
	require.EqualError(t, err, "no pending approvals for release 1")
	assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(io.Discard, err, ""))
	e.reg.Verify(t)
}

// TestRelApproveSeveralApprovers covers an environment with one approval per approver: ado
// sends the PATCH for each in turn until the server accepts one.
func TestRelApproveSeveralApprovers(t *testing.T) {
	refused := httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "not yours"})
	alice, bob := relApproval(7, "Prod", 2, "preDeploy", "Alice"), relApproval(8, "Prod", 2, "preDeploy", "Bob")

	t.Run("second accepted", func(t *testing.T) {
		e := newRelApproveEnv(t, false)
		e.stubApprovals(alice, bob)
		e.stubPatch(7, refused)
		e.stubPatch(8, nil)
		require.NoError(t, e.run(t, false, "1"))
		e.reg.Verify(t)
		assert.Len(t, e.patches, 2)
		assert.Equal(t, "✓ Approved Prod pre-deployment\n", e.errOut.String())
	})
	t.Run("not an approver", func(t *testing.T) {
		e := newRelApproveEnv(t, false)
		e.stubApprovals(alice, bob)
		e.stubPatch(7, refused)
		e.stubPatch(8, httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "nope"}))
		err := e.run(t, false, "1")
		require.EqualError(t, err, "could not approve Prod pre-deployment: not yours (HTTP 400); approvers: Alice, Bob")
		e.reg.Verify(t)
		assert.Empty(t, e.errOut.String())
	})
}

func TestRelApproveErrors(t *testing.T) {
	tests := []struct {
		name    string
		list    httpmock.Responder
		patch   httpmock.Responder
		wantErr []string
		auth    bool
	}{
		{name: "update forbidden",
			patch:   httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}),
			wantErr: []string{"could not approve Prod pre-deployment: denied (HTTP 403); approvers: Test User", "vso.release_manage"},
			auth:    true},
		{name: "server error has no approvers",
			patch:   httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}),
			wantErr: []string{"could not approve Prod pre-deployment: boom (HTTP 500)"}},
		{name: "list forbidden",
			list:    httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}),
			wantErr: []string{"could not read the approvals of release 1: denied (HTTP 403)", `"Release (Read)" (vso.release) scope`},
			auth:    true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelApproveEnv(t, false)
			if tt.list != nil {
				e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/approvals"), tt.list)
			} else {
				e.stubApprovals(relApproval(4, "Prod", 2, "preDeploy", "Test User"))
				e.stubPatch(4, tt.patch)
			}
			err := e.run(t, false, "1")
			require.Error(t, err)
			for _, w := range tt.wantErr {
				assert.Contains(t, err.Error(), w)
			}
			var ae *cmdutil.AuthError
			assert.Equal(t, tt.auth, errors.As(err, &ae))
			assert.NotContains(t, err.Error(), "approvers: Test User; approvers")
			e.reg.Verify(t)
		})
	}
}

func TestRelApproveBadRef(t *testing.T) {
	e := newRelApproveEnv(t, false)
	require.ErrorContains(t, e.run(t, false, "abc"), "invalid release")
	require.Error(t, e.run(t, false))
}
