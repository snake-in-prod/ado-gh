package ready

import (
	"bytes"
	"encoding/json"
	"errors"
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

type readyEnv struct {
	org    string
	reg    *httpmock.Registry
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	ios    *iostreams.IOStreams
	// patches are the decoded bodies of every PATCH request.
	patches []map[string]any
}

// newReadyEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newReadyEnv(t *testing.T, tty bool) *readyEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "prready-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	return &readyEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
}

func (e *readyEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdReady(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// stubReadyPR stubs the SDK locations and GET of pull request 12 with the given fields.
func (e *readyEnv) stubReadyPR(status string, draft bool, votes ...int) {
	reviewers := []map[string]any{}
	for _, v := range votes {
		reviewers = append(reviewers, map[string]any{"id": "r", "vote": v})
	}
	e.stubReadyPRReviewers(status, draft, reviewers)
}

// stubReadyPRReviewers stubs the SDK locations and GET of pull request 12 with reviewers.
func (e *readyEnv) stubReadyPRReviewers(status string, draft bool, reviewers []map[string]any) {
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 12, "title": "Add feature", "status": status, "isDraft": draft,
		"sourceRefName": "refs/heads/feat", "targetRefName": "refs/heads/main",
		"repository": map[string]any{"name": "web"}, "reviewers": reviewers,
	}))
}

// stubReadyPatch stubs the PATCH of pull request 12, recording its body.
func (e *readyEnv) stubReadyPatch() {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12"),
		func(req *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			e.patches = append(e.patches, body)
			return httpmock.JSONResponse(map[string]any{"pullRequestId": 12})(req)
		})
}

func TestReady(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		status    string
		draft     bool
		votes     []int
		wantPatch map[string]any
		wantErr   string
		wantOut   string
	}{
		{name: "publish draft", args: []string{"12"}, status: "active", draft: true,
			wantPatch: map[string]any{"isDraft": false}, wantOut: "✓ Pull request !12 (Add feature) is marked as ready for review\n"},
		{name: "undo", args: []string{"12", "--undo"}, status: "active",
			wantPatch: map[string]any{"isDraft": true}, wantOut: "✓ Pull request !12 (Add feature) is converted to draft\n"},
		{name: "undo with zero votes does not prompt", args: []string{"12", "--undo"}, status: "active", votes: []int{0},
			wantPatch: map[string]any{"isDraft": true}, wantOut: "✓ Pull request !12 (Add feature) is converted to draft\n"},
		{name: "already ready", args: []string{"12"}, status: "active",
			wantOut: "! Pull request !12 (Add feature) is already ready for review\n"},
		{name: "already draft", args: []string{"12", "--undo"}, status: "active", draft: true, votes: []int{10},
			wantOut: "! Pull request !12 (Add feature) is already a draft\n"},
		{name: "abandoned", args: []string{"12"}, status: "abandoned", draft: true,
			wantErr: "pull request !12 (Add feature) is abandoned"},
		{name: "completed undo", args: []string{"12", "--undo"}, status: "completed", votes: []int{10},
			wantErr: "pull request !12 (Add feature) is completed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newReadyEnv(t, true)
			e.stubReadyPR(tt.status, tt.draft, tt.votes...)
			if tt.wantPatch != nil {
				e.stubReadyPatch()
			}
			err := e.run(t, tt.args...)
			e.reg.Verify(t)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(io.Discard, err, ""))
				assert.Empty(t, e.patches)
				return
			}
			require.NoError(t, err)
			if tt.wantPatch != nil {
				assert.Equal(t, []map[string]any{tt.wantPatch}, e.patches, "isDraft is sent alone in one PATCH")
			} else {
				assert.Empty(t, e.patches)
			}
			assert.Equal(t, tt.wantOut, e.errOut.String())
			assert.Empty(t, e.out.String())
		})
	}
}

func TestReadyNonTTYIsQuiet(t *testing.T) {
	e := newReadyEnv(t, false)
	e.stubReadyPR("active", true)
	e.stubReadyPatch()
	require.NoError(t, e.run(t, "12"))
	assert.Empty(t, e.errOut.String())
}

func TestReadyUndoWithVotes(t *testing.T) {
	const question = "Converting pull request !12 (Add feature) to draft removes its 2 votes. Continue?"
	tests := []struct {
		name      string
		tty       bool
		args      []string
		input     string
		wantPatch bool
		wantErr   string // "cancel" or "flag"
	}{
		{name: "tty accept", tty: true, input: "y\n", wantPatch: true},
		{name: "tty decline", tty: true, input: "n\n", wantErr: "cancel"},
		{name: "yes skips prompt", tty: true, args: []string{"-y"}, wantPatch: true},
		{name: "non-tty yes", args: []string{"--yes"}, wantPatch: true},
		{name: "non-tty refuses", wantErr: "flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newReadyEnv(t, tt.tty)
			e.stubReadyPR("active", false, 10, 0, -5)
			if tt.wantPatch {
				e.stubReadyPatch()
			}
			e.in.WriteString(tt.input)

			err := e.run(t, append([]string{"12", "--undo"}, tt.args...)...)
			e.reg.Verify(t)
			prompted := strings.Contains(e.out.String(), question)
			assert.Equal(t, tt.tty && len(tt.args) == 0, prompted, "prompt shown")
			switch tt.wantErr {
			case "":
				require.NoError(t, err)
				assert.Equal(t, []map[string]any{{"isDraft": true}}, e.patches)
			case "flag":
				var fe *cmdutil.FlagError
				require.True(t, errors.As(err, &fe), "want FlagError, got %v", err)
				assert.EqualError(t, err, "--yes required when not running interactively")
				assert.Empty(t, e.patches)
			default:
				require.ErrorIs(t, err, cmdutil.CancelError)
				assert.Equal(t, cmdutil.ExitCancel, cmdutil.HandleError(io.Discard, err, ""))
				assert.Empty(t, e.patches)
			}
		})
	}
}

func TestReadyUndoCountsGroupVotesOnce(t *testing.T) {
	tests := []struct {
		name      string
		reviewers []map[string]any
		question  string
	}{
		{"a vote for a group counts for its voter", []map[string]any{
			{"id": "p", "vote": 10, "votedFor": []map[string]any{{"id": "g"}}},
			{"id": "g", "vote": 10, "isContainer": true},
			{"id": "q", "vote": -5},
		}, "removes its 2 votes."},
		{"group votes alone count once", []map[string]any{
			{"id": "g", "vote": 10, "isContainer": true},
			{"id": "h", "vote": 5, "isContainer": true},
		}, "removes its 1 vote."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newReadyEnv(t, true)
			e.stubReadyPRReviewers("active", false, tt.reviewers)
			e.in.WriteString("n\n")

			require.ErrorIs(t, e.run(t, "12", "--undo"), cmdutil.CancelError)
			assert.Contains(t, e.out.String(), tt.question)
		})
	}
}
