package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

const projects = "_apis/projects"

type result struct {
	out, errOut string
	err         error
	reqs        []*http.Request
	bodies      []string
}

type setup struct {
	repo   *adocontext.Repo
	stdin  string
	noAuth bool
	ios    func(*iostreams.IOStreams)
}

func run(t *testing.T, s setup, stubs func(*httpmock.Registry), args ...string) result {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	if s.noAuth {
		t.Setenv("ADO_TOKEN", "")
	}
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ORG", "")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, in, out, errOut := iostreams.Test()
	in.WriteString(s.stdin)
	if s.ios != nil {
		s.ios(ios)
	}
	reg := &httpmock.Registry{}
	if stubs != nil {
		stubs(reg)
	}
	repo := adocontext.Repo{Host: "dev.azure.com", Org: "contoso", Project: "Fabrikam Fiber", Name: "web"}
	if s.repo != nil {
		repo = *s.repo
	}
	f := &cmdutil.Factory{
		AppVersion: "1.0.0",
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			if repo.Org == "" {
				return repo, errors.New("not an Azure DevOps repository")
			}
			return repo, nil
		},
	}
	cmd := NewCmdAPI(f)
	cmd.SetArgs(args)
	cmd.SetIn(in)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err = cmd.Execute()
	if stubs != nil && err == nil {
		reg.Verify(t)
	}
	r := result{out: out.String(), errOut: errOut.String(), err: err, reqs: reg.Requests}
	for _, req := range reg.Requests {
		b := ""
		if req.Body != nil {
			raw, _ := io.ReadAll(req.Body)
			b = string(raw)
		}
		r.bodies = append(r.bodies, b)
	}
	return r
}

func ok(body string) func(*httpmock.Registry) {
	return func(reg *httpmock.Registry) { reg.Register(httpmock.MatchAny, httpmock.StringResponse(body)) }
}

func TestURL(t *testing.T) {
	tests := []struct {
		name string
		args []string
		repo *adocontext.Repo
		want string
	}{
		{
			name: "relative with placeholders and default api-version",
			args: []string{"{project}/_apis/git/repositories/{repo}"},
			want: "https://dev.azure.com/contoso/Fabrikam%20Fiber/_apis/git/repositories/web?api-version=7.1",
		},
		{
			name: "leading slash, user api-version kept",
			args: []string{"/_apis/projects?api-version=7.1-preview.4"},
			want: "https://dev.azure.com/contoso/_apis/projects?api-version=7.1-preview.4",
		},
		{
			name: "--api-version",
			args: []string{"_apis/projects", "--api-version", "6.0"},
			want: "https://dev.azure.com/contoso/_apis/projects?api-version=6.0",
		},
		{
			name: "visualstudio.com remote",
			args: []string{"_apis/projects"},
			repo: &adocontext.Repo{Host: "contoso.visualstudio.com", Org: "contoso"},
			want: "https://contoso.visualstudio.com/_apis/projects?api-version=7.1",
		},
		{
			name: "absolute URL with {org}",
			args: []string{"https://vssps.dev.azure.com/{org}/_apis/graph/users"},
			want: "https://vssps.dev.azure.com/contoso/_apis/graph/users?api-version=7.1",
		},
		{
			name: "absolute visualstudio.com URL",
			args: []string{"https://fabrikam.vsrm.visualstudio.com/_apis/release/releases"},
			repo: &adocontext.Repo{},
			want: "https://fabrikam.vsrm.visualstudio.com/_apis/release/releases?api-version=7.1",
		},
		{
			name: "absolute URL needs no context",
			args: []string{"https://dev.azure.com/fabrikam/_apis/projects"},
			repo: &adocontext.Repo{},
			want: "https://dev.azure.com/fabrikam/_apis/projects?api-version=7.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := run(t, setup{repo: tt.repo}, ok("{}"), tt.args...)
			require.NoError(t, r.err)
			require.Len(t, r.reqs, 1)
			assert.Equal(t, tt.want, r.reqs[0].URL.String())
			assert.Equal(t, "GET", r.reqs[0].Method)
			assert.Equal(t, "Basic OnBhdA==", r.reqs[0].Header.Get("Authorization"))
			assert.Equal(t, "{}", r.out)
		})
	}
}

func TestURLErrors(t *testing.T) {
	r := run(t, setup{repo: &adocontext.Repo{}}, nil, "_apis/projects")
	assert.ErrorContains(t, r.err, "no organization")

	r = run(t, setup{repo: &adocontext.Repo{}}, nil, "{project}/_apis/git/repositories")
	assert.ErrorContains(t, r.err, "not an Azure DevOps repository")

	for _, u := range []string{"https://evil.example/_apis", "http://dev.azure.com/contoso/_apis"} {
		r = run(t, setup{}, nil, u)
		assert.ErrorContains(t, r.err, "not an Azure DevOps URL", u)
		assert.Empty(t, r.reqs)
	}
}

func TestMethodAndHeaders(t *testing.T) {
	r := run(t, setup{}, ok(""), projects, "-X", "delete", "-H", "Accept: text/plain", "--header", "X-Custom:a:b")
	require.NoError(t, r.err)
	req := r.reqs[0]
	assert.Equal(t, "DELETE", req.Method)
	assert.Equal(t, "text/plain", req.Header.Get("Accept"))
	assert.Equal(t, "a:b", req.Header.Get("X-Custom"))
	assert.Empty(t, r.out)

	r = run(t, setup{}, nil, projects, "-H", "bad")
	var flagErr *cmdutil.FlagError
	assert.ErrorAs(t, r.err, &flagErr)
}

func TestFields(t *testing.T) {
	file := filepath.Join(t.TempDir(), "desc.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))

	r := run(t, setup{stdin: "from stdin"}, ok("{}"), projects,
		"-f", "title=true", "-F", "draft=true", "-F", "off=false", "-F", "n=42", "-F", "none=null",
		"-F", "s=text", "-F", "desc=@"+file, "--field", "in=@-", "--raw-field", "eq=a=b")
	require.NoError(t, r.err)
	req := r.reqs[0]
	assert.Equal(t, "POST", req.Method, "fields imply POST")
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"title":"true","draft":true,"off":false,"n":42,"none":null,"s":"text",
		"desc":"from file","in":"from stdin","eq":"a=b"}`, r.bodies[0])

	r = run(t, setup{}, ok("{}"), projects, "-X", "PATCH", "-F", "n=1")
	require.NoError(t, r.err)
	assert.Equal(t, "PATCH", r.reqs[0].Method)

	r = run(t, setup{}, ok("{}"), projects, "-X", "GET", "-f", "$top=2", "-F", "n=null")
	require.NoError(t, r.err)
	assert.Equal(t, "2", r.reqs[0].URL.Query().Get("$top"), "GET fields go to the query")
	assert.True(t, r.reqs[0].URL.Query().Has("n"))
	assert.Empty(t, r.bodies[0])

	for _, bad := range [][]string{{"-f", "novalue"}, {"-F", "x=@/does/not/exist"}} {
		r = run(t, setup{}, nil, append([]string{projects}, bad...)...)
		assert.Error(t, r.err, bad)
	}
}

func TestInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "body.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"a":1}`), 0o600))

	r := run(t, setup{}, ok("{}"), projects, "--input", file, "-f", "q=1")
	require.NoError(t, r.err)
	assert.Equal(t, "POST", r.reqs[0].Method)
	assert.Equal(t, `{"a":1}`, r.bodies[0])
	assert.Equal(t, "1", r.reqs[0].URL.Query().Get("q"), "with --input, fields go to the query")
	assert.Equal(t, "application/json", r.reqs[0].Header.Get("Content-Type"))

	r = run(t, setup{stdin: `[{"op":"add"}]`}, ok("{}"), projects, "--input", "-", "-X", "PATCH",
		"-H", "Content-Type: application/json-patch+json")
	require.NoError(t, r.err)
	assert.Equal(t, `[{"op":"add"}]`, r.bodies[0])
	assert.Equal(t, "application/json-patch+json", r.reqs[0].Header.Get("Content-Type"))

	r = run(t, setup{}, nil, projects, "--input", "/does/not/exist")
	assert.Error(t, r.err)
}

func TestInclude(t *testing.T) {
	r := run(t, setup{}, func(reg *httpmock.Registry) {
		reg.Register(httpmock.MatchAny, httpmock.WithHeader(httpmock.JSONResponse(map[string]int{"count": 0}), "X-Trace", "1"))
	}, projects, "-i")
	require.NoError(t, r.err)
	assert.Equal(t, "HTTP/1.1 200 OK\nContent-Type: application/json\nX-Trace: 1\n\n{\"count\":0}", r.out)
}

func TestOutputFormats(t *testing.T) {
	body := `{"count":2,"value":[{"name":"a"},{"name":"b"}]}`
	r := run(t, setup{}, ok(body), projects, "--jq", ".value[].name")
	require.NoError(t, r.err)
	assert.Equal(t, "a\nb\n", r.out)

	r = run(t, setup{}, ok(body), projects, "-t", "{{range .value}}{{.name}};{{end}}")
	require.NoError(t, r.err)
	assert.Equal(t, "a;b;", r.out)

	r = run(t, setup{}, ok(body), projects, "-q", ".x", "-t", "y")
	assert.Error(t, r.err, "--jq and --template are exclusive")

	r = run(t, setup{}, ok("{"), projects, "-t", "{{.x}}")
	assert.Error(t, r.err)

	r = run(t, setup{}, ok(body), projects, "-t", "{{")
	assert.Error(t, r.err)

	colored := func(ios *iostreams.IOStreams) { ios.SetStdoutTTY(true); ios.SetColorEnabled(true) }
	r = run(t, setup{ios: colored}, func(reg *httpmock.Registry) {
		reg.Register(httpmock.MatchAny, httpmock.JSONResponse(map[string]int{"count": 0}))
	}, projects)
	require.NoError(t, r.err)
	assert.Contains(t, r.out, "\x1b[")
	assert.Contains(t, r.out, "\n  ")

	r = run(t, setup{ios: colored}, ok("plain text"), projects)
	require.NoError(t, r.err)
	assert.Equal(t, "plain text", r.out, "non-JSON is printed as-is")
}

func TestErrors(t *testing.T) {
	r := run(t, setup{}, func(reg *httpmock.Registry) {
		reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(404, `{"message":"nope"}`))
	}, projects, "--jq", ".value")
	require.Error(t, r.err)
	assert.Empty(t, r.out)
	assert.Equal(t, "{\"message\":\"nope\"}\n", r.errOut)
	assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(io.Discard, r.err, ""))
	assert.Contains(t, r.err.Error(), "HTTP 404")

	r = run(t, setup{}, func(reg *httpmock.Registry) {
		reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(401, ""))
	}, projects)
	var authErr *cmdutil.AuthError
	assert.ErrorAs(t, r.err, &authErr, "401 exits 4")

	r = run(t, setup{noAuth: true}, nil, projects)
	assert.ErrorAs(t, r.err, &authErr, "not logged in exits 4")
	assert.Empty(t, r.reqs)
}
