package close

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
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type closeEnv struct {
	org, repoOverride string
	reg               *httpmock.Registry
	ios               *iostreams.IOStreams
	in, out, errOut   *bytes.Buffer
	git               *gittest.Runner
	// calls records the HTTP writes in order: "thread", "patch", "delete <oldObjectId>".
	calls   []string
	patched map[string]any
}

// newCloseEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newCloseEnv(t *testing.T, tty bool) *closeEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "prclose-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	return &closeEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, git: &gittest.Runner{}}
}

func (e *closeEnv) run(t *testing.T, args ...string) error {
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
		RepoOverride: e.repoOverride,
		Prompter:     &cmdutil.Prompter{IO: e.ios, Accessible: true},
		GitClient:    &git.Client{Run: e.git.Run},
	}
	cmd := NewCmdClose(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

const closeRepoPath = "/My%20Proj/_apis/git/repositories/web"

// stubPR stubs the SDK locations and GET of pull request 12 with the given extra fields.
func (e *closeEnv) stubPR(extra map[string]any) {
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

func (e *closeEnv) stubThread() {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+closeRepoPath+"/pullRequests/12/threads"),
		func(r *http.Request) (*http.Response, error) {
			e.calls = append(e.calls, "thread")
			return httpmock.JSONResponse(map[string]any{"id": 1})(r)
		})
}

func (e *closeEnv) stubAbandon() {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+closeRepoPath+"/pullRequests/12"),
		func(r *http.Request) (*http.Response, error) {
			e.calls = append(e.calls, "patch")
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &e.patched)
			return httpmock.JSONResponse(map[string]any{"pullRequestId": 12, "status": "abandoned"})(r)
		})
}

// stubRefs answers the lookup of refs/heads/feat with tip ("" = absent).
func (e *closeEnv) stubRefs(tip string) {
	var refs []map[string]any
	if tip != "" {
		refs = []map[string]any{{"name": "refs/heads/feat-x", "objectId": "other"}, {"name": "refs/heads/feat", "objectId": tip}}
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+closeRepoPath+"/refs"),
		httpmock.JSONResponse(map[string]any{"count": len(refs), "value": refs}))
}

// stubDeleteRef answers the refs update with updateStatus.
func (e *closeEnv) stubDeleteRef(t *testing.T, updateStatus string) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+closeRepoPath+"/refs"),
		func(r *http.Request) (*http.Response, error) {
			var body []map[string]any
			b, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(b, &body))
			require.Len(t, body, 1)
			assert.Equal(t, "refs/heads/feat", body[0]["name"])
			assert.Equal(t, strings.Repeat("0", 40), body[0]["newObjectId"])
			e.calls = append(e.calls, "delete "+body[0]["oldObjectId"].(string))
			ok := strings.HasPrefix(updateStatus, "succeeded")
			return httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{
				{"name": "refs/heads/feat", "success": ok, "updateStatus": updateStatus},
			}})(r)
		})
}

func (e *closeEnv) stubDefaultBranch() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+closeRepoPath),
		httpmock.JSONResponse(map[string]any{"name": "web", "defaultBranch": "refs/heads/main"}))
}

func TestCloseAbandons(t *testing.T) {
	e := newCloseEnv(t, false)
	e.stubPR(nil)
	e.stubAbandon()

	require.NoError(t, e.run(t, "12", "--yes"))
	e.reg.Verify(t)
	assert.Equal(t, map[string]any{"status": "abandoned"}, e.patched)
	assert.Empty(t, e.errOut.String())
	assert.Empty(t, e.git.Calls, "no local work without -d")
}

func TestCloseCommentsBeforeAbandoning(t *testing.T) {
	e := newCloseEnv(t, false)
	e.stubPR(nil)
	e.stubThread()
	e.stubAbandon()

	require.NoError(t, e.run(t, "12", "-y", "-c", "Superseded"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"thread", "patch"}, e.calls)
}

func TestCloseConfirmation(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		e := newCloseEnv(t, true)
		e.stubPR(nil)
		e.stubAbandon()
		e.in.WriteString("y\n")

		require.NoError(t, e.run(t, "12"))
		e.reg.Verify(t)
		assert.Contains(t, e.out.String(), `Abandon pull request !12 "Add feature"?`)
		assert.Equal(t, "✓ Abandoned pull request !12 (Add feature)\n", e.errOut.String())
	})
	t.Run("declined", func(t *testing.T) {
		e := newCloseEnv(t, true)
		e.stubPR(nil)
		e.in.WriteString("n\n")

		err := e.run(t, "12")
		require.ErrorIs(t, err, cmdutil.CancelError)
		assert.Equal(t, cmdutil.ExitCancel, cmdutil.HandleError(io.Discard, err, ""))
		e.reg.Verify(t)
		assert.Nil(t, e.patched)
	})
	t.Run("--yes skips the prompt", func(t *testing.T) {
		e := newCloseEnv(t, true)
		e.stubPR(nil)
		e.stubAbandon()

		require.NoError(t, e.run(t, "12", "-y"))
		assert.Empty(t, e.out.String())
		assert.Equal(t, "✓ Abandoned pull request !12 (Add feature)\n", e.errOut.String())
	})
}

func TestCloseNonInteractiveNeedsYes(t *testing.T) {
	e := newCloseEnv(t, false)
	err := e.run(t, "12")
	require.EqualError(t, err, "--yes required when not running interactively")
	var stderr bytes.Buffer
	assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(&stderr, err, "Usage: close"))
	assert.Contains(t, stderr.String(), "Usage: close", "usage is printed like pr merge")
	e.reg.Verify(t)
}

func TestCloseStates(t *testing.T) {
	t.Run("already abandoned warns", func(t *testing.T) {
		e := newCloseEnv(t, false)
		e.stubPR(map[string]any{"status": "abandoned"})

		require.NoError(t, e.run(t, "12", "-y", "-d"))
		e.reg.Verify(t)
		assert.Equal(t, "! Pull request !12 (Add feature) is already abandoned\n", e.errOut.String())
		assert.Empty(t, e.git.Calls, "no branch deletion")
	})
	t.Run("already abandoned does not prompt", func(t *testing.T) {
		e := newCloseEnv(t, true)
		e.stubPR(map[string]any{"status": "abandoned"})

		require.NoError(t, e.run(t, "12"))
		assert.Equal(t, "! Pull request !12 (Add feature) is already abandoned\n", e.errOut.String())
	})
	t.Run("completed fails", func(t *testing.T) {
		e := newCloseEnv(t, false)
		e.stubPR(map[string]any{"status": "completed"})

		err := e.run(t, "12", "-y")
		require.EqualError(t, err, "pull request !12 (Add feature) is completed")
		assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(io.Discard, err, ""))
		e.reg.Verify(t)
	})
	t.Run("not found", func(t *testing.T) {
		e := newCloseEnv(t, false)
		e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "nope"}))

		require.EqualError(t, e.run(t, "12", "-y"), "pull request !12 not found in My Proj/web")
	})
}

const closeTracking = "branch.main.merge refs/heads/main\nbranch.feat.merge refs/heads/feat\n" +
	"branch.mine.merge refs/heads/feat\nbranch.web/feat.merge refs/heads/feat\nbranch.feat-x.merge refs/heads/feat-x\n"

const closeGetRegexp = `config --get-regexp ^branch\..*\.merge$`

func TestCloseDeleteBranch(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name         string
		tty          bool
		pr           map[string]any
		repoOverride string
		replies      map[string]string
		fails        map[string]error
		stub         func(t *testing.T, e *closeEnv)
		wantCalls    []string
		wantGit      []string
		wantErr      string
		wantErrOut   string
	}{
		{
			name: "deletes remote at its tip and every tracking local branch after leaving the current one",
			tty:  true,
			replies: map[string]string{closeGetRegexp: closeTracking,
				"symbolic-ref --quiet HEAD": "refs/heads/mine\n"},
			stub: func(t *testing.T, e *closeEnv) {
				e.stubDefaultBranch()
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeeded")
			},
			wantCalls: []string{"patch", "delete tip999"},
			wantGit:   []string{"branch -D feat", "checkout main", "branch -D mine", "branch -D web/feat"},
			wantErrOut: "✓ Abandoned pull request !12 (Add feature)\n" +
				"✓ Deleted local branch feat\n" +
				"✓ Deleted local branch mine and switched to branch main\n" +
				"✓ Deleted local branch web/feat\n" +
				"✓ Deleted remote branch feat\n",
		},
		{
			name:    "no tracking local branch",
			replies: map[string]string{closeGetRegexp: "branch.main.merge refs/heads/main\n"},
			stub: func(t *testing.T, e *closeEnv) {
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeeded")
			},
			wantCalls: []string{"patch", "delete tip999"},
		},
		{
			// Live bug: a same-name local branch without an upstream survived close -d.
			name:  "same-name local branch without upstream is deleted like gh",
			fails: map[string]error{closeGetRegexp: &git.Error{ExitCode: 1}},
			replies: map[string]string{"rev-parse --verify --quiet refs/heads/feat": "abc\n",
				"symbolic-ref --quiet HEAD": "refs/heads/main\n"},
			stub: func(t *testing.T, e *closeEnv) {
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeeded")
			},
			wantCalls: []string{"patch", "delete tip999"},
			wantGit:   []string{"branch -D feat"},
		},
		{
			name:  "no upstream configured at all",
			fails: map[string]error{closeGetRegexp: &git.Error{ExitCode: 1}},
			stub: func(t *testing.T, e *closeEnv) {
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeeded")
			},
			wantCalls: []string{"patch", "delete tip999"},
		},
		{
			name: "checkout of the default branch fails",
			replies: map[string]string{closeGetRegexp: "branch.feat.merge refs/heads/feat\n",
				"symbolic-ref --quiet HEAD": "refs/heads/feat\n"},
			fails: map[string]error{"checkout main": &git.Error{ExitCode: 1, Stderr: "error: your local changes would be overwritten"}},
			stub: func(t *testing.T, e *closeEnv) {
				e.stubDefaultBranch()
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeeded")
			},
			wantCalls: []string{"patch", "delete tip999"},
			wantGit:   []string{"checkout main"},
			wantErr: "pull request !12 was abandoned, but cannot check out main to delete local branch feat: " +
				"error: your local changes would be overwritten",
		},
		{
			name:    "local branch delete fails",
			replies: map[string]string{closeGetRegexp: "branch.feat.merge refs/heads/feat\n"},
			fails:   map[string]error{"branch -D feat": boom},
			stub: func(t *testing.T, e *closeEnv) {
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeeded")
			},
			wantCalls: []string{"patch", "delete tip999"},
			wantGit:   []string{"branch -D feat"},
			wantErr:   "pull request !12 was abandoned, but cannot delete local branch feat: boom",
		},
		{
			name:       "listing local branches fails",
			fails:      map[string]error{closeGetRegexp: boom},
			stub:       func(_ *testing.T, e *closeEnv) { e.stubRefs("") },
			wantCalls:  []string{"patch"},
			wantErr:    "pull request !12 was abandoned, but boom",
			wantErrOut: "! Remote branch feat was already deleted\n",
		},
		{
			name: "fork pull request skips the remote branch",
			pr: map[string]any{"forkSource": map[string]any{"name": "refs/heads/feat",
				"repository": map[string]any{"id": "8b6f0a8e-0000-0000-0000-000000000002"}}},
			replies:    map[string]string{closeGetRegexp: "branch.webfork/feat.merge refs/heads/feat\n"},
			wantCalls:  []string{"patch"},
			wantGit:    []string{"branch -D webfork/feat"},
			wantErrOut: "! Skipped deleting remote branch feat: it is in a fork\n",
		},
		{
			name: "remote delete refused keeps the pull request abandoned",
			stub: func(t *testing.T, e *closeEnv) {
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "writePermissionRequired")
			},
			wantCalls: []string{"patch", "delete tip999"},
			wantErr:   "pull request !12 was abandoned, but cannot delete branch feat: you need the Force push permission on it",
		},
		{
			name: "remote lookup fails",
			stub: func(_ *testing.T, e *closeEnv) {
				e.reg.Register(httpmock.REST(http.MethodGet, e.org+closeRepoPath+"/refs"),
					httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
			},
			wantCalls: []string{"patch"},
			wantErr:   "pull request !12 was abandoned, but cannot delete branch feat: denied (HTTP 403)",
		},
		{
			name:       "remote branch already gone",
			stub:       func(_ *testing.T, e *closeEnv) { e.stubRefs("") },
			wantCalls:  []string{"patch"},
			wantErrOut: "! Remote branch feat was already deleted\n",
		},
		{
			name: "remote branch deleted meanwhile",
			stub: func(t *testing.T, e *closeEnv) {
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeededNonExistentRef")
			},
			wantCalls:  []string{"patch", "delete tip999"},
			wantErrOut: "! Remote branch feat was already deleted\n",
		},
		{
			name:         "-R leaves local branches alone",
			repoOverride: "proj/web",
			replies:      map[string]string{closeGetRegexp: "branch.feat.merge refs/heads/feat\n"},
			stub: func(t *testing.T, e *closeEnv) {
				e.stubRefs("tip999")
				e.stubDeleteRef(t, "succeeded")
			},
			wantCalls: []string{"patch", "delete tip999"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCloseEnv(t, tt.tty)
			e.repoOverride = tt.repoOverride
			e.git.Replies, e.git.Fails = tt.replies, tt.fails
			e.stubPR(tt.pr)
			e.stubAbandon()
			if tt.stub != nil {
				tt.stub(t, e)
			}

			err := e.run(t, "12", "-y", "-d")
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			e.reg.Verify(t)
			assert.Equal(t, tt.wantCalls, e.calls)
			assert.Equal(t, tt.wantGit, e.git.Mutations())
			if tt.repoOverride != "" {
				assert.Empty(t, e.git.Calls)
			}
			assert.Equal(t, tt.wantErrOut, e.errOut.String())
		})
	}
}

func TestCloseFlags(t *testing.T) {
	cmd := NewCmdClose(&cmdutil.Factory{})
	for name, short := range map[string]string{"comment": "c", "delete-branch": "d", "yes": "y"} {
		require.NotNil(t, cmd.Flags().Lookup(name), name)
		assert.Equal(t, short, cmd.Flags().Lookup(name).Shorthand, name)
	}
	assert.Error(t, cmd.Args(cmd, []string{"1", "2"}))
}
