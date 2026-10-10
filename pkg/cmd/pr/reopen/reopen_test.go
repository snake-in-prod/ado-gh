package reopen

import (
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

const (
	reopenPRPath  = "/My%20Proj/_apis/git/repositories/web/pullRequests/12"
	reopenForkID  = "8b6f0a8e-0000-0000-0000-000000000002"
	reopenRefsWeb = "/My%20Proj/_apis/git/repositories/web/refs"
)

// runReopen runs `pr reopen args` against reg in its own org (the SDK caches locations per
// base URL for the whole process) and returns stderr.
func runReopen(t *testing.T, reg *httpmock.Registry, org string, tty bool, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "My Proj", Name: "web"}, nil
		},
	}
	cmd := NewCmdReopen(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // the root command does this in production
	cmd.SetArgs(args)
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	err = cmd.Execute()
	return errOut.String(), err
}

func reopenOrg(t *testing.T) string {
	return "prreopen-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
}

func reopenRefs(names ...string) httpmock.Responder {
	refs := make([]map[string]any, len(names))
	for i, n := range names {
		refs[i] = map[string]any{"name": n, "objectId": "abc"}
	}
	return httpmock.JSONResponse(map[string]any{"count": len(refs), "value": refs})
}

func TestReopen(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		tty     bool
		status  string
		fork    bool
		refs    httpmock.Responder // nil: no GetRefs expected
		comment bool
		patch   bool
		wantErr string
		wantOut string
	}{
		{name: "abandoned to active", status: "abandoned", tty: true,
			refs: reopenRefs("refs/heads/feat-x", "refs/heads/feat"), patch: true,
			wantOut: "✓ Reopened pull request !12 (Add feature)\n"},
		{name: "non-TTY prints nothing", status: "abandoned", refs: reopenRefs("refs/heads/feat"), patch: true},
		{name: "comment posted before PATCH", args: []string{"-c", "back"}, status: "abandoned",
			refs: reopenRefs("refs/heads/feat"), comment: true, patch: true},
		{name: "already active", status: "active", wantOut: "! Pull request !12 (Add feature) is already active\n"},
		{name: "completed", status: "completed", wantErr: "pull request !12 (Add feature) is completed"},
		{name: "deleted source branch", status: "abandoned", refs: reopenRefs("refs/heads/feature"),
			wantErr: "source branch feat of pull request !12 was deleted; push it (git push origin feat) or restore it, then retry"},
		{name: "deleted source branch in fork", status: "abandoned", fork: true, refs: reopenRefs(),
			wantErr: "source branch feat of pull request !12 was deleted"},
		{name: "fork branch present", status: "abandoned", fork: true, refs: reopenRefs("refs/heads/feat"), patch: true},
		{name: "refs lookup fails", status: "abandoned",
			refs:    httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}),
			wantErr: "denied (HTTP 403)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := reopenOrg(t)
			reg := &httpmock.Registry{}
			reg.StubLocations(org, httpmock.GitPRLocations...)
			pr := map[string]any{
				"pullRequestId": 12, "title": "Add feature", "status": tt.status,
				"sourceRefName": "refs/heads/feat", "repository": map[string]any{"name": "web"},
			}
			refsPath := org + reopenRefsWeb
			if tt.fork {
				pr["forkSource"] = map[string]any{"name": "refs/heads/feat", "repository": map[string]any{
					"id": reopenForkID, "project": map[string]any{"name": "Fork Proj"},
				}}
				refsPath = org + "/Fork%20Proj/_apis/git/repositories/" + reopenForkID + "/refs"
			}
			reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(pr))
			var calls []string
			var filter string
			if tt.refs != nil {
				reg.Register(httpmock.REST(http.MethodGet, refsPath), func(r *http.Request) (*http.Response, error) {
					filter = r.URL.Query().Get("filter")
					return tt.refs(r)
				})
			}
			var thread, patched map[string]any
			if tt.comment {
				reg.Register(httpmock.REST(http.MethodPost, org+reopenPRPath+"/threads"), func(r *http.Request) (*http.Response, error) {
					calls = append(calls, "thread")
					b, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(b, &thread)
					return httpmock.JSONResponse(map[string]any{"id": 1})(r)
				})
			}
			if tt.patch {
				reg.Register(httpmock.REST(http.MethodPatch, org+reopenPRPath), func(r *http.Request) (*http.Response, error) {
					calls = append(calls, "patch")
					b, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(b, &patched)
					return httpmock.JSONResponse(map[string]any{"pullRequestId": 12, "status": "active"})(r)
				})
			}

			stderr, err := runReopen(t, reg, org, tt.tty, append([]string{"12"}, tt.args...)...)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			reg.Verify(t)
			if !tt.patch {
				for _, r := range reg.Requests {
					require.NotEqual(t, http.MethodPatch, r.Method, "unexpected PATCH")
				}
			}
			assert.Equal(t, tt.wantOut, stderr)
			if tt.refs != nil {
				assert.Equal(t, "heads/feat", filter)
			}
			if tt.patch {
				assert.Equal(t, map[string]any{"status": "active"}, patched)
			}
			if tt.comment {
				assert.Equal(t, []string{"thread", "patch"}, calls)
				assert.Equal(t, "back", thread["comments"].([]any)[0].(map[string]any)["content"])
			}
		})
	}
}

func TestReopenTooManyArgs(t *testing.T) {
	_, err := runReopen(t, &httpmock.Registry{}, reopenOrg(t), false, "1", "2")
	assert.EqualError(t, err, "accepts at most 1 arg(s), received 2")
}
