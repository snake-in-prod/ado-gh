package create

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// relCreateDef is release definition 1 as a single GET returns it (trimmed): live, greeting
// allows override and locked doesn't.
const relCreateDef = `{"id":1,"name":"ado-live-release",
 "artifacts":[{"alias":"_ado-live-classic","type":"Build","isPrimary":true}],
 "variables":{"greeting":{"value":"hi","allowOverride":true},"locked":{"value":"x"}}}`

type relCreateEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	browser     *browsertest.Browser
	// posted is the decoded body of the create POST, nil until sent.
	posted map[string]any
}

// newRelCreateEnv returns an env for its own org (the SDK caches discovery per base URL).
func newRelCreateEnv(t *testing.T, tty bool) *relCreateEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "relcreate-" + strings.NewReplacer("/", "-", " ", "-", "_", "-", "=", "-", ",", "-").Replace(strings.ToLower(t.Name()))
	e := &relCreateEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	e.reg.StubLocations(org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(org)
	return e
}

// stubDefinition answers the GET of release definition 1.
func (e *relCreateEnv) stubDefinition() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/definitions/1"), httpmock.StringResponse(relCreateDef))
}

// stubCreate answers the create POST with release.json (Release-1, artifact version 3).
func (e *relCreateEnv) stubCreate(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/proj/_apis/release/releases"), func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &e.posted))
		return httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "release.json")))(r)
	})
}

func (e *relCreateEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Browser:    e.browser,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "proj", Name: "ado-gh"}, nil
		},
	}
	cmd := NewCmdCreate(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

const relCreateURL = "https://dev.azure.com/%s/proj/_release?releaseId=1&_a=release-summary\n"

// TestReleaseCreateDefaults: without --artifact no artifacts are sent and the server's pick is
// printed from the returned release; the definition is found by name.
func TestReleaseCreateDefaults(t *testing.T) {
	e := newRelCreateEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/definitions"), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "ado-live-release", r.URL.Query().Get("searchText"))
		return httpmock.StringResponse(`{"count":1,"value":[{"id":1,"name":"ado-live-release","path":"\\"}]}`)(r)
	})
	e.stubDefinition()
	e.stubCreate(t)

	require.NoError(t, e.run(t, "ado-live-release"))
	assert.Equal(t, map[string]any{"definitionId": float64(1), "isDraft": false}, e.posted)
	assert.Equal(t, strings.Replace(relCreateURL, "%s", e.org, 1), e.out.String())
	assert.Equal(t, "  _ado-live-classic: 3\n", e.errOut.String())
	e.reg.Verify(t)
}

// TestReleaseCreateOverrides: --artifact, -v and --description are sent; aliases and variable
// names match without regard to case and are sent as the definition spells them.
func TestReleaseCreateOverrides(t *testing.T) {
	e := newRelCreateEnv(t, true)
	e.stubDefinition()
	e.stubCreate(t)

	require.NoError(t, e.run(t, "1", "--artifact", "_ADO-live-classic=5", "-v", "Greeting=hello", "--description", "hotfix"))
	assert.Equal(t, map[string]any{
		"definitionId": float64(1), "isDraft": false, "description": "hotfix",
		"artifacts": []any{map[string]any{"alias": "_ado-live-classic", "instanceReference": map[string]any{"id": "5"}}},
		"variables": map[string]any{"greeting": map[string]any{"value": "hello"}},
	}, e.posted)
	assert.Equal(t, "✓ Created Release-1 of ado-live-release\n  _ado-live-classic: 3\n\nTo see the release's progress, try: ado release watch 1\n", e.errOut.String())
	e.reg.Verify(t)
}

func TestReleaseCreateRejected(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		err  string
	}{
		{"not overridable", []string{"-v", "locked=y"}, `variable "locked" of release definition "ado-live-release" is not settable at release time; settable: greeting`},
		{"unknown variable", []string{"-v", "nope=y"}, `release definition "ado-live-release" has no release variable "nope"; settable at release time: greeting`},
		{"unknown artifact", []string{"--artifact", "other=1"}, `release definition "ado-live-release" has no artifact "other"; its artifacts: _ado-live-classic`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelCreateEnv(t, false)
			e.stubDefinition()
			err := e.run(t, append([]string{"1"}, tt.args...)...)
			assert.EqualError(t, err, tt.err)
			assert.Nil(t, e.posted)
			e.reg.Verify(t)
		})
	}
}

func TestReleaseCreateFlagErrors(t *testing.T) {
	for _, tt := range []struct {
		args []string
		err  string
	}{
		{[]string{"1", "--artifact", "a"}, `invalid --artifact "a": expected alias=version`},
		{[]string{"1", "--artifact", "a="}, `invalid --artifact "a=": expected alias=version`},
		{[]string{"1", "-v", "=x"}, `invalid --variable "=x": expected key=value`},
		{[]string{"1", "--watch", "-w"}, "if any flags in the group [watch web] are set none of the others can be; [watch web] were all set"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e := newRelCreateEnv(t, false)
			assert.EqualError(t, e.run(t, tt.args...), tt.err)
		})
	}
}

func TestReleaseCreateWeb(t *testing.T) {
	e := newRelCreateEnv(t, false)
	e.stubDefinition()
	e.stubCreate(t)

	require.NoError(t, e.run(t, "1", "-w"))
	assert.Equal(t, []string{strings.TrimSuffix(strings.Replace(relCreateURL, "%s", e.org, 1), "\n")}, e.browser.URLs)
	assert.Empty(t, e.out.String())
}

// TestReleaseCreateWatch: --watch follows the created release until its deployments complete.
func TestReleaseCreateWatch(t *testing.T) {
	e := newRelCreateEnv(t, false)
	e.stubDefinition()
	e.stubCreate(t)
	var r map[string]any
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, "release.json"), &r))
	for _, env := range r["environments"].([]any) {
		env.(map[string]any)["status"] = "succeeded"
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/1"), httpmock.JSONResponse(r))

	require.NoError(t, e.run(t, "1", "--watch"))
	assert.Contains(t, e.out.String(), strings.Replace(relCreateURL, "%s", e.org, 1))
	assert.Contains(t, e.out.String(), "Release-1 ado-live-release · succeeded · ID 1")
	e.reg.Verify(t)
}

func TestReleaseCreateErrors(t *testing.T) {
	t.Run("definition", func(t *testing.T) {
		e := newRelCreateEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/definitions/1"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "VS402856: definition not found"}))
		assert.ErrorContains(t, e.run(t, "1"), "VS402856")
	})
	t.Run("create", func(t *testing.T) {
		e := newRelCreateEnv(t, false)
		e.stubDefinition()
		e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/proj/_apis/release/releases"),
			httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "VS402881: no artifact version"}))
		assert.ErrorContains(t, e.run(t, "1"), "VS402881")
	})
	t.Run("unknown name", func(t *testing.T) {
		e := newRelCreateEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/definitions"), httpmock.StringResponse(`{"count":0,"value":[]}`))
		assert.ErrorContains(t, e.run(t, "nope"), "no release definition with that name")
	})
	t.Run("no project", func(t *testing.T) {
		e := newRelCreateEnv(t, false)
		f := &cmdutil.Factory{IOStreams: e.ios, BaseRepo: func() (adocontext.Repo, error) { return adocontext.Repo{}, assert.AnError }}
		cmd := NewCmdCreate(f)
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		cmd.SetArgs([]string{"1"})
		assert.Error(t, cmd.Execute())
	})
}
