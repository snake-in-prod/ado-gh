package checkout

import (
	"bytes"
	"fmt"
	"maps"
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

const coForkID = "8b6f0a8e-0000-0000-0000-000000000002"

func coPR(branch string, fork map[string]any) map[string]any {
	pr := map[string]any{
		"pullRequestId": 7, "status": "active", "sourceRefName": "refs/heads/" + branch,
		"repository": map[string]any{"name": "web"},
	}
	if fork != nil {
		pr["forkSource"] = map[string]any{"name": "refs/heads/" + branch, "repository": fork}
	}
	return pr
}

const coOrigin = "origin\thttps://dev.azure.com/ORG/proj/_git/web (fetch)\norigin\thttps://dev.azure.com/ORG/proj/_git/web (push)\n"

func TestCheckout(t *testing.T) {
	forkWithURL := map[string]any{"id": coForkID, "name": "webfork", "remoteUrl": "https://ORG@dev.azure.com/ORG/other/_git/webfork"}
	const forkURL = "https://ORG@dev.azure.com/ORG/other/_git/webfork"
	baseRepo := func(reg *httpmock.Registry, org string) {
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/web"),
			httpmock.JSONResponse(map[string]any{"name": "web", "defaultBranch": "refs/heads/main"}))
	}
	tests := []struct {
		name    string
		args    []string
		pr      map[string]any // nil: the pull request is not found
		stub    func(reg *httpmock.Registry, org string)
		replies map[string]string
		fails   map[string]error
		tty     bool
		want    []string
		wantErr string
		wantOut string
	}{
		{
			name:    "same repo, new branch",
			pr:      coPR("feature/x", nil),
			replies: map[string]string{"remote -v": coOrigin},
			tty:     true,
			want: []string{
				"fetch origin +refs/heads/feature/x:refs/remotes/origin/feature/x --no-tags",
				"checkout -b feature/x --track origin/feature/x",
			},
			wantOut: "Checked out branch feature/x for pull request !7\n",
		},
		{
			name: "same repo, existing branch fast-forwards",
			pr:   coPR("feat", nil),
			replies: map[string]string{"remote -v": coOrigin,
				"rev-parse --verify --quiet refs/heads/feat": "abc\n"},
			want: []string{
				"fetch origin +refs/heads/feat:refs/remotes/origin/feat --no-tags",
				"checkout feat",
				"merge --ff-only refs/remotes/origin/feat",
			},
		},
		{
			name: "same repo, existing branch with --force resets",
			args: []string{"-f"},
			pr:   coPR("feat", nil),
			replies: map[string]string{"remote -v": coOrigin,
				"rev-parse --verify --quiet refs/heads/feat": "abc\n"},
			want: []string{
				"fetch origin +refs/heads/feat:refs/remotes/origin/feat --no-tags",
				"checkout feat",
				"reset --hard refs/remotes/origin/feat",
			},
		},
		{
			name:    "--branch overrides the local name",
			args:    []string{"--branch", "mine"},
			pr:      coPR("feat", nil),
			replies: map[string]string{"remote -v": coOrigin},
			want: []string{
				"fetch origin +refs/heads/feat:refs/remotes/origin/feat --no-tags",
				"checkout -b mine --track origin/feat",
			},
		},
		{
			name: "fork with a matching local remote uses it, no prefix",
			pr:   coPR("main", forkWithURL),
			replies: map[string]string{"remote -v": coOrigin +
				"fork\tgit@ssh.dev.azure.com:v3/ORG/Other/webfork (fetch)\n"},
			want: []string{
				"fetch fork +refs/heads/main:refs/remotes/fork/main --no-tags",
				"checkout -b main --track fork/main",
			},
		},
		{
			name:    "fork without a remote fetches by URL and sets branch config",
			pr:      coPR("feat", forkWithURL),
			stub:    baseRepo,
			replies: map[string]string{"remote -v": coOrigin},
			want: []string{
				"fetch " + forkURL + " refs/heads/feat:refs/heads/feat --no-tags",
				"checkout feat",
				"config branch.feat.remote " + forkURL,
				"config branch.feat.pushRemote " + forkURL,
				"config branch.feat.merge refs/heads/feat",
			},
		},
		{
			name: "fork default branch name is prefixed, URL looked up by ID",
			pr:   coPR("main", map[string]any{"id": coForkID}),
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/git/repositories/"+coForkID),
					httpmock.JSONResponse(map[string]any{"name": "webfork", "remoteUrl": "https://dev.azure.com/" + org + "/other/_git/webfork"}))
				baseRepo(reg, org)
			},
			replies: map[string]string{"remote -v": coOrigin},
			want: []string{
				"fetch https://dev.azure.com/ORG/other/_git/webfork refs/heads/main:refs/heads/webfork/main --no-tags",
				"checkout webfork/main",
				"config branch.webfork/main.remote https://dev.azure.com/ORG/other/_git/webfork",
				"config branch.webfork/main.pushRemote https://dev.azure.com/ORG/other/_git/webfork",
				"config branch.webfork/main.merge refs/heads/main",
			},
		},
		{
			name: "fork, already on the branch, --force resets to FETCH_HEAD, config kept",
			args: []string{"-f", "-b", "mine"},
			pr:   coPR("feat", forkWithURL),
			replies: map[string]string{"remote -v": coOrigin, "symbolic-ref --quiet HEAD": "refs/heads/mine\n",
				"config --get branch.mine.merge": "refs/heads/feat\n"},
			want: []string{
				"fetch " + forkURL + " refs/heads/feat --no-tags",
				"reset --hard FETCH_HEAD",
			},
		},
		{
			name:    "fork, already on the branch, fast-forwards",
			args:    []string{"-b", "mine"},
			pr:      coPR("feat", forkWithURL),
			replies: map[string]string{"remote -v": coOrigin, "symbolic-ref --quiet HEAD": "refs/heads/mine\n"},
			want: []string{
				"fetch " + forkURL + " refs/heads/feat --no-tags",
				"merge --ff-only FETCH_HEAD",
				"config branch.mine.remote " + forkURL,
				"config branch.mine.pushRemote " + forkURL,
				"config branch.mine.merge refs/heads/feat",
			},
		},
		{
			name: "fork into another existing branch with --force",
			args: []string{"-f", "-b", "mine"},
			pr:   coPR("feat", forkWithURL),
			replies: map[string]string{"remote -v": coOrigin,
				"config --get branch.mine.merge": "refs/heads/feat\n"},
			want: []string{
				"fetch " + forkURL + " refs/heads/feat:refs/heads/mine --no-tags --force",
				"checkout mine",
			},
		},
		{
			name:    "same repo without a local remote fetches the repo URL",
			pr:      coPR("main", nil),
			replies: map[string]string{"remote -v": ""},
			want: []string{
				"fetch https://dev.azure.com/ORG/proj/_git/web refs/heads/main:refs/heads/main --no-tags",
				"checkout main",
				"config branch.main.remote https://dev.azure.com/ORG/proj/_git/web",
				"config branch.main.pushRemote https://dev.azure.com/ORG/proj/_git/web",
				"config branch.main.merge refs/heads/main",
			},
		},
		{
			name: "unreadable fork (lookup fails)",
			pr:   coPR("feat", map[string]any{"id": coForkID}),
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/git/repositories/"+coForkID),
					httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: not found"}))
			},
			wantErr: "cannot read the fork of pull request !7; you need read access to the fork repository: TF401019: not found (HTTP 404)",
		},
		{
			name:    "unreadable fork (fetch fails)",
			args:    []string{"-b", "x"},
			pr:      coPR("feat", forkWithURL),
			replies: map[string]string{"remote -v": coOrigin},
			fails: map[string]error{"fetch " + forkURL + " refs/heads/feat:refs/heads/x --no-tags": &git.Error{
				ExitCode: 128, Stderr: "fatal: repository not found"}},
			wantErr: "cannot read the fork of pull request !7; you need read access to the fork repository: fatal: repository not found",
		},
		{
			name:    "fork with a non-ADO URL",
			pr:      coPR("feat", map[string]any{"remoteUrl": "-x"}),
			wantErr: "pull request !7 has an unsupported fork URL",
		},
		{
			name:    "git failure in a later step",
			pr:      coPR("feat", nil),
			replies: map[string]string{"remote -v": coOrigin},
			fails:   map[string]error{"checkout -b feat --track origin/feat": &git.Error{ExitCode: 1, Stderr: "error: local changes"}},
			wantErr: "error: local changes",
		},
		{
			name:    "remotes fail",
			pr:      coPR("feat", nil),
			fails:   map[string]error{"remote -v": &git.Error{ExitCode: 128, Stderr: "fatal: not a git repository"}},
			wantErr: "fatal: not a git repository",
		},
		{
			name: "base repository lookup fails",
			pr:   coPR("main", forkWithURL),
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/web"),
					httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
			},
			replies: map[string]string{"remote -v": coOrigin},
			wantErr: "boom (HTTP 500)",
		},
		{
			name:    "source branch starting with - is rejected",
			pr:      coPR("-oops", nil),
			wantErr: `invalid source branch "-oops" in pull request !7`,
		},
		{name: "--branch starting with - is rejected", args: []string{"-b", "-x"}, wantErr: `invalid branch name "-x"`},
		{name: "pull request not found", wantErr: "pull request !7 not found in proj/web"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := fmt.Sprintf("coorg%d", i)
			sub := func(s string) string { return strings.ReplaceAll(s, "ORG", org) }
			reg := &httpmock.Registry{}
			reg.StubLocations(org, httpmock.GitPRLocations...)
			prResp := httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"})
			if tt.pr != nil {
				pr := maps.Clone(tt.pr)
				if fs, ok := pr["forkSource"].(map[string]any); ok {
					repo := maps.Clone(fs["repository"].(map[string]any))
					if u, ok := repo["remoteUrl"].(string); ok {
						repo["remoteUrl"] = sub(u)
					}
					pr["forkSource"] = map[string]any{"name": fs["name"], "repository": repo}
				}
				prResp = httpmock.JSONResponse(pr)
			}
			reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/pullRequests/7"), prResp)
			if tt.stub != nil {
				tt.stub(reg, org)
			}
			g := &gittest.Runner{Replies: map[string]string{}, Fails: map[string]error{}}
			for k, v := range tt.replies {
				g.Replies[sub(k)] = sub(v)
			}
			for k, v := range tt.fails {
				g.Fails[sub(k)] = v
			}

			t.Setenv("ADO_CONFIG_DIR", t.TempDir())
			t.Setenv("ADO_TOKEN", "pat")
			t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
			keyring.MockInit()
			cfg, err := config.Load()
			require.NoError(t, err)
			ios, _, _, errOut := iostreams.Test()
			ios.SetStdoutTTY(tt.tty)
			f := &cmdutil.Factory{
				IOStreams:  ios,
				Config:     func() (cmdutil.Config, error) { return cfg, nil },
				HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
				BaseRepo: func() (adocontext.Repo, error) {
					return adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "proj", Name: "web"}, nil
				},
				GitClient: &git.Client{Run: g.Run},
			}
			cmd := NewCmdCheckout(f)
			cmd.SetArgs(append(tt.args, "7"))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			err = cmd.Execute()

			for _, c := range g.Calls {
				assert.NotContains(t, c, "refs/pull/", "never fetch pull request refs")
			}
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, sub(tt.wantErr), err.Error())
				return
			}
			require.NoError(t, err)
			want := make([]string, len(tt.want))
			for i, w := range tt.want {
				want[i] = sub(w)
			}
			assert.Equal(t, want, g.Mutations())
			assert.Equal(t, tt.wantOut, errOut.String())
		})
	}
}

func TestCheckoutOnlyBranchAndForceFlags(t *testing.T) {
	cmd := NewCmdCheckout(&cmdutil.Factory{})
	assert.Equal(t, "b", cmd.Flags().Lookup("branch").Shorthand)
	assert.Equal(t, "f", cmd.Flags().Lookup("force").Shorthand)
	for _, gh := range []string{"detach", "recurse-submodules", "worktree"} {
		assert.Nil(t, cmd.Flags().Lookup(gh), gh)
	}
	assert.Error(t, cmd.Args(cmd, nil), "a pull request argument is required")
}
