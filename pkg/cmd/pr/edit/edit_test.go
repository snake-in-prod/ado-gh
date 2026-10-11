package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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

const fieldsRepoPath = "/My%20Proj/_apis/git/repositories/web"

type fieldsEnv struct {
	org    string
	reg    *httpmock.Registry
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	ios    *iostreams.IOStreams
	// patches are the decoded bodies of every PATCH request.
	patches []map[string]any
}

// newFieldsEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newFieldsEnv(t *testing.T) *fieldsEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	org := "predit-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	return &fieldsEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
}

func (e *fieldsEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdEdit(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// stubFieldsPR stubs the SDK locations and GET of pull request 12; fork makes it come from a
// fork.
func (e *fieldsEnv) stubFieldsPR(fork bool) {
	pr := map[string]any{
		"pullRequestId": 12, "title": "Old title", "description": "Old body", "status": "active",
		"sourceRefName": "refs/heads/feat", "targetRefName": "refs/heads/main",
		"repository": map[string]any{"name": "web"},
	}
	if fork {
		pr["forkSource"] = map[string]any{"name": "refs/heads/feat", "repository": map[string]any{"name": "web-fork"}}
	}
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(pr))
}

// stubFieldsRefs answers the lookup of refs/heads/<branch>, which exists when exists is true.
func (e *fieldsEnv) stubFieldsRefs(branch string, exists bool) {
	refs := []map[string]any{}
	if exists {
		refs = append(refs, map[string]any{"name": "refs/heads/" + branch, "objectId": "abc"})
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+fieldsRepoPath+"/refs"),
		httpmock.JSONResponse(map[string]any{"count": len(refs), "value": refs}))
}

// stubFieldsPatch stubs the PATCH of pull request 12, recording its body and answering with
// targetRefName.
func (e *fieldsEnv) stubFieldsPatch(targetRefName string) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+fieldsRepoPath+"/pullRequests/12"),
		func(req *http.Request) (*http.Response, error) {
			if err := e.record(req); err != nil {
				return nil, err
			}
			return httpmock.JSONResponse(map[string]any{"pullRequestId": 12, "targetRefName": targetRefName})(req)
		})
}

// record appends the decoded JSON object body of req to e.patches.
func (e *fieldsEnv) record(req *http.Request) error {
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return err
	}
	e.patches = append(e.patches, body)
	return nil
}

// stubLabelPost stubs one POST of a label to pull request 12, recording its body.
func (e *fieldsEnv) stubLabelPost() {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+fieldsRepoPath+"/pullRequests/12/labels"),
		func(req *http.Request) (*http.Response, error) {
			if err := e.record(req); err != nil {
				return nil, err
			}
			return httpmock.JSONResponse(map[string]any{"name": "x"})(req)
		})
}

const fieldsURL = "https://dev.azure.com/ORG/My%20Proj/_git/web/pullrequest/12\n"

func TestEditFields(t *testing.T) {
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(bodyFile, []byte("From file\n"), 0o600))

	tests := []struct {
		name      string
		args      []string
		stdin     string
		local     bool   // fails before any request
		refs      string // branch looked up in refs ("" = no lookup)
		refExists bool
		target    string // targetRefName in the PATCH response
		wantPatch map[string]any
		wantErr   string
		partial   bool // the error is reported after the edits, so the URL is printed
		fork      bool // the pull request comes from a fork
	}{
		{name: "title", args: []string{"-t", "New title"},
			wantPatch: map[string]any{"title": "New title"}},
		{name: "body", args: []string{"-b", "New body"},
			wantPatch: map[string]any{"description": "New body"}},
		{name: "body file", args: []string{"-F", bodyFile},
			wantPatch: map[string]any{"description": "From file\n"}},
		{name: "body stdin", args: []string{"-F", "-"}, stdin: "From stdin",
			wantPatch: map[string]any{"description": "From stdin"}},
		{name: "empty body clears it", args: []string{"-b", ""},
			wantPatch: map[string]any{"description": ""}},
		{name: "title and body", args: []string{"--title", "T", "--body", "B"},
			wantPatch: map[string]any{"title": "T", "description": "B"}},
		{name: "unchanged title not sent", args: []string{"-t", "Old title", "-b", "B"},
			wantPatch: map[string]any{"description": "B"}},
		{name: "nothing changed sends no patch", args: []string{"-t", "Old title", "-B", "main"}},
		{name: "base", args: []string{"-B", "develop"}, refs: "develop", refExists: true, target: "refs/heads/develop",
			wantPatch: map[string]any{"targetRefName": "refs/heads/develop"}},
		{name: "base with refs prefix", args: []string{"--base", "refs/heads/develop"}, refs: "develop", refExists: true,
			target: "refs/heads/develop", wantPatch: map[string]any{"targetRefName": "refs/heads/develop"}},
		{name: "base and title", args: []string{"-B", "develop", "-t", "T"}, refs: "develop", refExists: true,
			target: "refs/heads/develop", wantPatch: map[string]any{"title": "T", "targetRefName": "refs/heads/develop"}},
		{name: "retargeting disabled", args: []string{"-B", "develop"}, refs: "develop", refExists: true, target: "refs/heads/main",
			wantPatch: map[string]any{"targetRefName": "refs/heads/develop"}, partial: true,
			wantErr: "base branch not changed to develop: pull request retargeting may be disabled in this organization"},
		{name: "retargeting disabled keeps the title", args: []string{"-B", "develop", "-t", "T"}, refs: "develop", refExists: true,
			target: "refs/heads/main", wantPatch: map[string]any{"title": "T", "targetRefName": "refs/heads/develop"}, partial: true,
			wantErr: "base branch not changed to develop: pull request retargeting may be disabled in this organization\napplied: title"},
		{name: "missing base branch", args: []string{"-B", "nope"}, refs: "nope",
			wantErr: "branch nope not found in My Proj/web"},
		{name: "base is source branch", args: []string{"-B", "feat"},
			wantErr: "the base branch cannot be the source branch feat"},
		{name: "fork source branch name is a valid base", args: []string{"-B", "feat"}, fork: true, refs: "feat", refExists: true,
			target: "refs/heads/feat", wantPatch: map[string]any{"targetRefName": "refs/heads/feat"}},
		{name: "body too long", args: []string{"-b", strings.Repeat("é", 4001)}, local: true,
			wantErr: "the description is 4001 characters long; the maximum is 4000"},
		{name: "body file too long", args: []string{"-F", "-"}, stdin: strings.Repeat("x", 4001), local: true,
			wantErr: "the description is 4001 characters long; the maximum is 4000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newFieldsEnv(t)
			e.in.WriteString(tt.stdin)
			if !tt.local {
				e.stubFieldsPR(tt.fork)
			}
			if tt.refs != "" {
				e.stubFieldsRefs(tt.refs, tt.refExists)
			}
			if tt.wantPatch != nil {
				e.stubFieldsPatch(tt.target)
			}

			err := e.run(t, append([]string{"12"}, tt.args...)...)
			// Verify fails on unused stubs and RoundTrip on unexpected requests, so no
			// reviewer/label/work item calls and no merge-status polling happened.
			e.reg.Verify(t)
			if tt.wantPatch != nil {
				assert.Equal(t, []map[string]any{tt.wantPatch}, e.patches, "only changed fields in one PATCH")
			} else {
				assert.Empty(t, e.patches)
			}
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				if tt.partial {
					assert.True(t, strings.HasPrefix(e.out.String(), strings.Replace(fieldsURL, "ORG", e.org, 1)), e.out.String())
				} else {
					assert.NotContains(t, e.out.String(), "pullrequest/12")
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, strings.Replace(fieldsURL, "ORG", e.org, 1), e.out.String())
		})
	}
}

func TestEditFieldsBodyFileMissing(t *testing.T) {
	e := newFieldsEnv(t)
	err := e.run(t, "12", "-F", filepath.Join(t.TempDir(), "missing.md"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the description")
	e.reg.Verify(t)
}

func TestEditFieldsFlagErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		flagErr bool // a *cmdutil.FlagError rather than cobra's plain usage error
		wantErr string
	}{
		{name: "no flags", args: []string{"12"}, flagErr: true,
			wantErr: "specify at least one of --title, --body, --body-file, --base, --add-reviewer, --add-required-reviewer, " +
				"--remove-reviewer, --add-work-item, --remove-work-item, --add-label or --remove-label"},
		{name: "empty body file path", args: []string{"12", "-F", ""}, flagErr: true,
			wantErr: `--body-file needs a file path, or "-" for stdin`},
		// cobra's mutually exclusive check is a plain error that HandleError treats as usage.
		{name: "body and body file", args: []string{"12", "-b", "x", "-F", "-"},
			wantErr: "if any flags in the group [body body-file] are set none of the others can be; [body body-file] were all set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newFieldsEnv(t)
			err := e.run(t, tt.args...)
			require.EqualError(t, err, tt.wantErr)
			var fe *cmdutil.FlagError
			assert.Equal(t, tt.flagErr, errors.As(err, &fe))
			e.reg.Verify(t)
		})
	}
}
