package diff

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	gitcmd "github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	cloneBaseSHA = strings.Repeat("b", 40)
	cloneSrcSHA  = strings.Repeat("5", 40)
	cloneTgtSHA  = strings.Repeat("7", 40)
	cloneMBSHA   = strings.Repeat("e", 40)
)

const (
	cloneOrigin  = "origin\thttps://dev.azure.com/ORG/proj/_git/web (fetch)\norigin\thttps://dev.azure.com/ORG/proj/_git/web (push)\n"
	cloneForkURL = "https://dev.azure.com/ORG/other/_git/webfork"
	cloneDiffOut = "diff --git a/a.go b/a.go\nindex 1234567..89abcde 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"
)

func cloneIteration(id int, base, src, tgt string) map[string]any {
	it := map[string]any{"id": id}
	for k, v := range map[string]string{"commonRefCommit": base, "sourceRefCommit": src, "targetRefCommit": tgt} {
		if v != "" {
			it[k] = map[string]any{"commitId": v}
		}
	}
	return it
}

func TestCloneDiff(t *testing.T) {
	forkRemote := "fork\thttps://dev.azure.com/ORG/other/_git/webfork (fetch)\n"
	diffCall := "-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false diff --no-color --no-ext-diff --no-textconv " + cloneBaseSHA + " " + cloneSrcSHA
	sameFetch := "fetch origin +refs/heads/feat:refs/remotes/origin/feat +refs/heads/main:refs/remotes/origin/main --no-tags"
	missing := &gitcmd.Error{ExitCode: 128, Stderr: "fatal: Not a valid object name"}
	tests := []struct {
		name       string
		fork       bool
		iterations []map[string]any
		iterStatus int
		nameOnly   bool
		patch      bool
		color      bool
		replies    map[string]string
		fails      map[string]error
		stalePR    bool // the PR's last merge commits differ from the latest iteration's
		wantCalls  []string
		wantOut    string
		wantErr    string
	}{
		{
			name: "same repo diffs the latest iteration's common commit to its source commit",
			// Oldest first, as Azure DevOps lists them; the PR's last merge commits are stale.
			iterations: []map[string]any{
				cloneIteration(1, strings.Repeat("0", 40), strings.Repeat("1", 40), cloneTgtSHA),
				cloneIteration(2, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA),
			},
			stalePR: true,
			replies: map[string]string{"remote -v": cloneOrigin, diffCall: cloneDiffOut},
			wantCalls: []string{
				"remote -v", sameFetch,
				"cat-file -e " + cloneSrcSHA + "^{commit}",
				"cat-file -e " + cloneBaseSHA + "^{commit}",
				diffCall,
			},
			wantOut: cloneDiffOut,
		},
		{
			name:       "color uses the ado colorizer",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			color:      true,
			replies:    map[string]string{"remote -v": cloneOrigin, diffCall: "--- a/a.go\n-old\n+new\n"},
			wantOut:    "\x1b[1;37m--- a/a.go\x1b[m\n\x1b[31m-old\x1b[m\n\x1b[32m+new\x1b[m\n",
		},
		{
			name:       "--name-only adds --name-only, wins over --patch and is never coloured",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			nameOnly:   true,
			patch:      true,
			color:      true,
			replies: map[string]string{"remote -v": cloneOrigin,
				"-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false diff --no-color --no-ext-diff --no-textconv --name-only " + cloneBaseSHA + " " + cloneSrcSHA: "a.go\n+b.go\n"},
			wantOut: "a.go\n+b.go\n",
		},
		{
			name:       "--patch uses format-patch target..source",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			patch:      true,
			color:      true,
			replies: map[string]string{"remote -v": cloneOrigin,
				"-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false format-patch --no-color --no-ext-diff --no-textconv --stdout " + cloneTgtSHA + ".." + cloneSrcSHA: "From abc\n---\n+x\n"},
			wantCalls: []string{
				"remote -v", sameFetch,
				"cat-file -e " + cloneSrcSHA + "^{commit}",
				"cat-file -e " + cloneTgtSHA + "^{commit}",
				"-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false format-patch --no-color --no-ext-diff --no-textconv --stdout " + cloneTgtSHA + ".." + cloneSrcSHA,
			},
			wantOut: "From abc\n\x1b[1;37m---\x1b[m\n\x1b[32m+x\x1b[m\n",
		},
		{
			name:       "missing base is fetched by SHA",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin, diffCall: cloneDiffOut},
			fails:      map[string]error{"cat-file -e " + cloneBaseSHA + "^{commit}": missing},
			wantCalls: []string{
				"remote -v", sameFetch,
				"cat-file -e " + cloneSrcSHA + "^{commit}",
				"cat-file -e " + cloneBaseSHA + "^{commit}",
				"fetch origin " + cloneBaseSHA + " --no-tags",
				diffCall,
			},
			wantOut: cloneDiffOut,
		},
		{
			name:       "base not fetchable falls back to git merge-base",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies: map[string]string{"remote -v": cloneOrigin,
				"merge-base " + cloneTgtSHA + " " + cloneSrcSHA: cloneMBSHA + "\n",
				"-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false diff --no-color --no-ext-diff --no-textconv " + cloneMBSHA + " " + cloneSrcSHA: cloneDiffOut},
			fails: map[string]error{
				"cat-file -e " + cloneBaseSHA + "^{commit}":   missing,
				"fetch origin " + cloneBaseSHA + " --no-tags": &gitcmd.Error{ExitCode: 128, Stderr: "fatal: not our ref"},
			},
			wantCalls: []string{
				"remote -v", sameFetch,
				"cat-file -e " + cloneSrcSHA + "^{commit}",
				"cat-file -e " + cloneBaseSHA + "^{commit}",
				"fetch origin " + cloneBaseSHA + " --no-tags",
				"cat-file -e " + cloneTgtSHA + "^{commit}",
				"merge-base " + cloneTgtSHA + " " + cloneSrcSHA,
				"-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false diff --no-color --no-ext-diff --no-textconv " + cloneMBSHA + " " + cloneSrcSHA,
			},
			wantOut: cloneDiffOut,
		},
		{
			name:       "merge-base failure is returned",
			iterations: []map[string]any{cloneIteration(1, "", cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin},
			fails:      map[string]error{"merge-base " + cloneTgtSHA + " " + cloneSrcSHA: &gitcmd.Error{ExitCode: 128, Stderr: "fatal: bad revision"}},
			wantErr:    "cannot find the merge base of pull request !7: fatal: bad revision",
		},
		{
			name:       "missing source commit that cannot be fetched is an error",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin},
			fails: map[string]error{
				sameFetch: &gitcmd.Error{ExitCode: 128, Stderr: "fatal: couldn't find remote ref refs/heads/feat"},
				"cat-file -e " + cloneSrcSHA + "^{commit}":   missing,
				"fetch origin " + cloneSrcSHA + " --no-tags": &gitcmd.Error{ExitCode: 128, Stderr: "fatal: not our ref"},
			},
			wantErr: "commit 5555555 of pull request !7 is not available locally or from the remote: fatal: not our ref",
		},
		{
			name:       "a failed branch fetch is tolerated when the commits are present",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin, diffCall: cloneDiffOut},
			fails:      map[string]error{sameFetch: &gitcmd.Error{ExitCode: 128, Stderr: "fatal: couldn't find remote ref"}},
			wantOut:    cloneDiffOut,
		},
		{
			name:       "fork with a local remote fetches each side from its remote",
			fork:       true,
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin + forkRemote, diffCall: cloneDiffOut},
			fails:      map[string]error{"cat-file -e " + cloneSrcSHA + "^{commit}": missing},
			wantCalls: []string{
				"remote -v",
				"fetch origin +refs/heads/main:refs/remotes/origin/main --no-tags",
				"fetch fork +refs/heads/feat:refs/remotes/fork/feat --no-tags",
				"cat-file -e " + cloneSrcSHA + "^{commit}",
				"fetch fork " + cloneSrcSHA + " --no-tags",
				"cat-file -e " + cloneBaseSHA + "^{commit}",
				diffCall,
			},
			wantOut: cloneDiffOut,
		},
		{
			name:       "fork without a remote fetches by URL",
			fork:       true,
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin, diffCall: cloneDiffOut},
			wantCalls: []string{
				"remote -v",
				"fetch origin +refs/heads/main:refs/remotes/origin/main --no-tags",
				"fetch " + cloneForkURL + " refs/heads/feat --no-tags",
				"cat-file -e " + cloneSrcSHA + "^{commit}",
				"cat-file -e " + cloneBaseSHA + "^{commit}",
				diffCall,
			},
			wantOut: cloneDiffOut,
		},
		{
			name:       "no matching remote fetches the repository URL",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": "", diffCall: cloneDiffOut},
			wantCalls: []string{
				"remote -v",
				"fetch https://dev.azure.com/ORG/proj/_git/web refs/heads/feat refs/heads/main --no-tags",
				"cat-file -e " + cloneSrcSHA + "^{commit}",
				"cat-file -e " + cloneBaseSHA + "^{commit}",
				diffCall,
			},
			wantOut: cloneDiffOut,
		},
		{
			name:       "no iterations falls back to the last merge commits and merge-base",
			iterations: []map[string]any{},
			replies: map[string]string{"remote -v": cloneOrigin,
				"merge-base " + cloneTgtSHA + " " + cloneSrcSHA: cloneMBSHA + "\n",
				"-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false diff --no-color --no-ext-diff --no-textconv " + cloneMBSHA + " " + cloneSrcSHA: cloneDiffOut},
			wantOut: cloneDiffOut,
		},
		{
			name:       "iterations error",
			iterStatus: http.StatusInternalServerError,
			wantErr:    "boom (HTTP 500)",
		},
		{
			name:       "invalid commit ID from the API is rejected",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, "--output=x", cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin},
			wantErr:    `invalid commit ID "--output=x" in pull request !7`,
			wantCalls:  []string{},
		},
		{
			name:       "remotes fail",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			fails:      map[string]error{"remote -v": &gitcmd.Error{ExitCode: 128, Stderr: "fatal: not a git repository"}},
			wantErr:    "fatal: not a git repository",
		},
		{
			name:       "git diff failure is returned",
			iterations: []map[string]any{cloneIteration(1, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)},
			replies:    map[string]string{"remote -v": cloneOrigin},
			fails:      map[string]error{diffCall: &gitcmd.Error{ExitCode: 128, Stderr: "fatal: bad object"}},
			wantErr:    "fatal: bad object",
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := fmt.Sprintf("cloneorg%d", i)
			sub := func(s string) string { return strings.ReplaceAll(s, "ORG", org) }
			reg := &httpmock.Registry{}
			reg.StubLocations(org, httpmock.GitPRLocations...)
			t.Cleanup(func() { reg.Verify(t) })
			iterResp := httpmock.JSONResponse(map[string]any{"count": len(tt.iterations), "value": tt.iterations})
			if tt.iterStatus != 0 {
				iterResp = httpmock.StatusJSONResponse(tt.iterStatus, map[string]any{"message": "boom"})
			}
			reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/web/pullRequests/7/iterations"), iterResp)

			g := &gittest.Runner{Replies: map[string]string{}, Fails: map[string]error{}}
			for k, v := range tt.replies {
				g.Replies[sub(k)] = sub(v)
			}
			for k, v := range tt.fails {
				g.Fails[sub(k)] = v
			}
			ios, _, out, _ := iostreams.Test()
			f := &cmdutil.Factory{IOStreams: ios, GitClient: &gitcmd.Client{Run: g.Run}}
			repo := adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "proj", Name: "web"}
			c := api.NewPRClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj", "web")

			id, src, tgt := 7, "refs/heads/feat", "refs/heads/main"
			pr := &git.GitPullRequest{
				PullRequestId: &id, SourceRefName: &src, TargetRefName: &tgt,
				LastMergeSourceCommit: &git.GitCommitRef{CommitId: &cloneSrcSHA},
				LastMergeTargetCommit: &git.GitCommitRef{CommitId: &cloneTgtSHA},
			}
			if tt.stalePR {
				stale := strings.Repeat("9", 40)
				pr.LastMergeSourceCommit, pr.LastMergeTargetCommit = &git.GitCommitRef{CommitId: &stale}, &git.GitCommitRef{CommitId: &stale}
			}
			if tt.fork {
				forkURL, forkID := sub(cloneForkURL), uuid.New()
				pr.ForkSource = &git.GitForkRef{Repository: &git.GitRepository{Id: &forkID, RemoteUrl: &forkURL}}
			}

			err := cloneDiff(context.Background(), f, c, repo, pr, tt.nameOnly, tt.patch, tt.color)
			if tt.wantCalls != nil {
				want := make([]string, len(tt.wantCalls))
				for i, w := range tt.wantCalls {
					want[i] = sub(w)
				}
				assert.Equal(t, want, append([]string{}, g.Calls...))
			}
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOut, out.String())
			for _, call := range g.Calls {
				if strings.Contains(call, " diff ") || strings.Contains(call, " format-patch ") {
					assert.True(t, strings.HasPrefix(call, "-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false "), "user diff config can't change the output: %s", call)
					assert.Contains(t, call, "--no-color --no-ext-diff --no-textconv", "git never colours or converts")
				}
			}
		})
	}
}
