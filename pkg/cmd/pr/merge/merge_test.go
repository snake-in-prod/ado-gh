package merge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const meID = "33333333-3333-3333-3333-333333333333"

type mergeEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	// patched is the decoded body of the PATCH request, if any.
	patched map[string]any
}

// newMergeEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newMergeEnv(t *testing.T, tty bool) *mergeEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "prmerge-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	return &mergeEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
}

func (e *mergeEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdMerge(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// stubPR stubs the SDK locations and GET of pull request 12 with the given extra fields.
func (e *mergeEnv) stubPR(extra map[string]any) {
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	pr := map[string]any{
		"pullRequestId": 12, "title": "Add feature", "status": "active",
		"sourceRefName": "refs/heads/feat", "targetRefName": "refs/heads/main",
		"repository":            map[string]any{"name": "web"},
		"lastMergeSourceCommit": map[string]any{"commitId": "abc123"},
	}
	for k, v := range extra {
		pr[k] = v
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(pr))
}

// stubPatch stubs the PATCH of pull request 12, recording its body, and answers with resp.
func (e *mergeEnv) stubPatch(resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12"),
		func(req *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(b, &e.patched)
			return resp(req)
		})
}

func (e *mergeEnv) completionOptions(t *testing.T) map[string]any {
	t.Helper()
	require.NotNil(t, e.patched, "no PATCH sent")
	opts, ok := e.patched["completionOptions"].(map[string]any)
	require.True(t, ok, "no completionOptions in %v", e.patched)
	return opts
}

var completedPR = httpmock.JSONResponse(map[string]any{"pullRequestId": 12, "status": "completed"})

func TestMergeStrategyFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		existing map[string]any
		want     string
	}{
		{name: "merge", args: []string{"--merge"}, want: "noFastForward"},
		{name: "squash", args: []string{"--squash"}, want: "squash"},
		{name: "rebase", args: []string{"--rebase"}, want: "rebase"},
		{name: "rebase merge", args: []string{"--rebase-merge"}, want: "rebaseMerge"},
		{name: "default", want: "noFastForward"},
		{name: "existing strategy kept", existing: map[string]any{"mergeStrategy": "rebase"}, want: "rebase"},
		{name: "flag overrides existing", args: []string{"--squash"}, existing: map[string]any{"mergeStrategy": "rebase"}, want: "squash"},
		{name: "legacy squashMerge dropped", existing: map[string]any{"squashMerge": true}, want: "noFastForward"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMergeEnv(t, false)
			e.stubPR(map[string]any{"completionOptions": tt.existing})
			e.stubPatch(completedPR)

			require.NoError(t, e.run(t, append([]string{"12", "--yes"}, tt.args...)...))
			e.reg.Verify(t)
			opts := e.completionOptions(t)
			assert.Equal(t, tt.want, opts["mergeStrategy"])
			assert.NotContains(t, opts, "squashMerge")
		})
	}
}

func TestMergeCompletesWithLastMergeSourceCommit(t *testing.T) {
	e := newMergeEnv(t, false)
	e.stubPR(map[string]any{"completionOptions": map[string]any{
		"transitionWorkItems": true, "autoCompleteIgnoreConfigIds": []int{7}, "mergeCommitMessage": "old",
	}})
	e.stubPatch(completedPR)

	require.NoError(t, e.run(t, "12", "-y", "--squash", "-d", "--subject", "Ship it", "--body", "Details"))
	assert.Equal(t, "completed", e.patched["status"])
	assert.Equal(t, map[string]any{"commitId": "abc123"}, e.patched["lastMergeSourceCommit"])
	assert.NotContains(t, e.patched, "autoCompleteSetBy")
	assert.Equal(t, map[string]any{
		"mergeStrategy": "squash", "deleteSourceBranch": true, "mergeCommitMessage": "Ship it\n\nDetails",
		"transitionWorkItems": true, "autoCompleteIgnoreConfigIds": []any{float64(7)},
	}, e.completionOptions(t))
	assert.Empty(t, e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestMergeMessage(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want any
	}{
		{name: "subject only", args: []string{"--subject", "S"}, want: "S"},
		{name: "body only uses default subject", args: []string{"--body", "B"}, want: "Merged PR 12: Add feature\n\nB"},
		{name: "none keeps server default", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMergeEnv(t, false)
			e.stubPR(nil)
			e.stubPatch(completedPR)

			require.NoError(t, e.run(t, append([]string{"12", "-y"}, tt.args...)...))
			assert.Equal(t, tt.want, e.completionOptions(t)["mergeCommitMessage"])
		})
	}
}

func TestMergeAutoSetsAutoComplete(t *testing.T) {
	e := newMergeEnv(t, true)
	e.stubPR(nil)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": meID}}))
	e.stubPatch(httpmock.JSONResponse(map[string]any{"pullRequestId": 12, "status": "active"}))

	require.NoError(t, e.run(t, "12", "--auto", "--rebase", "-y"))
	e.reg.Verify(t)
	assert.Equal(t, map[string]any{"id": meID}, e.patched["autoCompleteSetBy"])
	assert.NotContains(t, e.patched, "status")
	assert.NotContains(t, e.patched, "lastMergeSourceCommit")
	assert.Equal(t, "rebase", e.completionOptions(t)["mergeStrategy"])
	assert.Equal(t, "✓ Pull request !12 will be completed automatically when all policies pass\n", e.errOut.String())
}

func TestMergeBypassPolicy(t *testing.T) {
	e := newMergeEnv(t, false)
	e.stubPR(nil)
	e.stubPatch(completedPR)

	require.NoError(t, e.run(t, "12", "-y", "--bypass-policy", "--bypass-reason", "hotfix"))
	opts := e.completionOptions(t)
	assert.Equal(t, true, opts["bypassPolicy"])
	assert.Equal(t, "hotfix", opts["bypassReason"])
}

func TestMergeFlagErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "bypass without reason", args: []string{"--bypass-policy"}, wantErr: "--bypass-reason is required with --bypass-policy"},
		{name: "reason without bypass", args: []string{"--bypass-reason", "x"}, wantErr: "--bypass-reason requires --bypass-policy"},
		{name: "two strategies", args: []string{"--squash", "--rebase"}, wantErr: "[rebase squash] were all set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMergeEnv(t, false)
			err := e.run(t, append([]string{"12", "-y"}, tt.args...)...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, e.reg.Requests)
		})
	}
}

func TestMergeBlockedByPolicy(t *testing.T) {
	e := newMergeEnv(t, false)
	e.stubPR(nil)
	e.stubPatch(httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{
		"message": "TF401181: The pull request cannot be completed because one or more required policies are not satisfied.",
	}))

	err := e.run(t, "12", "-y")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required policies are not satisfied")
	assert.Contains(t, err.Error(), "use --auto to complete it when policies pass, or --bypass-policy --bypass-reason")
}

func TestMergeOtherErrorHasNoHint(t *testing.T) {
	e := newMergeEnv(t, false)
	e.stubPR(nil)
	e.stubPatch(httpmock.StatusJSONResponse(http.StatusConflict, map[string]any{"message": "merge conflicts"}))

	err := e.run(t, "12", "-y")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "merge conflicts")
	assert.NotContains(t, err.Error(), "--auto")
}

func TestMergeNotActive(t *testing.T) {
	e := newMergeEnv(t, false)
	e.stubPR(map[string]any{"status": "completed"})

	err := e.run(t, "12", "-y")
	require.EqualError(t, err, "pull request !12 (Add feature) is completed")
}

func TestMergeTTYConfirm(t *testing.T) {
	e := newMergeEnv(t, true)
	e.stubPR(nil)
	e.stubPatch(completedPR)
	e.in.WriteString("y\n")

	require.NoError(t, e.run(t, "12", "--squash"))
	e.reg.Verify(t)
	assert.Contains(t, e.out.String(), "Complete pull request !12 (Add feature) into main with squash?")
	assert.Equal(t, "✓ Completed pull request !12 (Add feature)\n", e.errOut.String())
}

func TestMergeTTYCancel(t *testing.T) {
	e := newMergeEnv(t, true)
	e.stubPR(nil)
	e.in.WriteString("n\n")

	err := e.run(t, "12")
	require.ErrorIs(t, err, cmdutil.CancelError)
	assert.Equal(t, cmdutil.ExitCancel, cmdutil.HandleError(io.Discard, err, ""))
	assert.Nil(t, e.patched)
}

func TestMergeNonTTYRequiresYes(t *testing.T) {
	e := newMergeEnv(t, false)

	err := e.run(t, "12")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes required when not running interactively")
	assert.Empty(t, e.reg.Requests)
}

func TestMergeCompleteAlias(t *testing.T) {
	assert.Contains(t, NewCmdMerge(&cmdutil.Factory{}).Aliases, "complete")
}
