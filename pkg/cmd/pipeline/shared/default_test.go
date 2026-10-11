package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const defaultPipeRepoID = "00000000-0000-0000-0000-000000000011"

// defaultPipeEnv serves repository "ado-gh" and the definitions listed for it, keeping the
// definitions.json entries whose id is in ids.
type defaultPipeEnv struct {
	f    *cmdutil.Factory
	c    *api.PipelineClient
	in   *bytes.Buffer
	out  *bytes.Buffer
	reqs []*http.Request
}

func newDefaultPipeEnv(t *testing.T, org string, tty bool, ids ...int) *defaultPipeEnv {
	t.Helper()
	var all struct {
		Value []map[string]any `json:"value"`
	}
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, "definitions.json"), &all))
	defs := []map[string]any{}
	for _, d := range all.Value {
		if slices.Contains(ids, int(d["id"].(float64))) {
			defs = append(defs, d)
		}
	}

	e := &defaultPipeEnv{}
	reg := &httpmock.Registry{}
	reg.StubLocations(org, slices.Concat(httpmock.PipelineLocations, httpmock.GitRepoLocations)...)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/ado-gh"),
		httpmock.JSONResponse(map[string]any{"id": defaultPipeRepoID, "name": "ado-gh"}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), func(r *http.Request) (*http.Response, error) {
		e.reqs = append(e.reqs, r)
		return httpmock.JSONResponse(map[string]any{"count": len(defs), "value": defs})(r)
	})
	t.Cleanup(func() { reg.Verify(t) })

	ios, in, out, _ := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	e.in, e.out = in, out
	e.f = &cmdutil.Factory{IOStreams: ios, Prompter: &cmdutil.Prompter{IO: ios, Accessible: true}}
	e.c = api.NewPipelineClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj")
	return e
}

func TestDefaultDefinitionOne(t *testing.T) {
	// Definition 3 is disabled and skipped, so 1 is the only candidate.
	e := newDefaultPipeEnv(t, "defpipeoneorg", false, 1, 3)
	d, err := DefaultDefinition(context.Background(), e.f, e.c, "ado-gh")
	require.NoError(t, err)
	assert.Equal(t, 1, *d.Id)
	require.Len(t, e.reqs, 1)
	q := e.reqs[0].URL.Query()
	assert.Equal(t, defaultPipeRepoID, q.Get("repositoryId"))
	assert.Equal(t, "TfsGit", q.Get("repositoryType"))
}

func TestDefaultDefinitionPausedKept(t *testing.T) {
	e := newDefaultPipeEnv(t, "defpipepausedorg", false, 2, 3)
	d, err := DefaultDefinition(context.Background(), e.f, e.c, "ado-gh")
	require.NoError(t, err)
	assert.Equal(t, 2, *d.Id)
}

func TestDefaultDefinitionNone(t *testing.T) {
	tests := []struct {
		name, org string
		ids       []int
	}{
		{"no definitions", "defpipenoneorg", nil},
		{"only disabled", "defpipedisabledorg", []int{3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDefaultPipeEnv(t, tt.org, false, tt.ids...)
			_, err := DefaultDefinition(context.Background(), e.f, e.c, "ado-gh")
			assert.EqualError(t, err, "no pipeline uses repository ado-gh; pass a pipeline name or ID as an argument")
		})
	}
}

func TestDefaultDefinitionSeveralNonTTY(t *testing.T) {
	e := newDefaultPipeEnv(t, "defpipeseveralorg", false, 1, 2, 3, 4)
	_, err := DefaultDefinition(context.Background(), e.f, e.c, "ado-gh")
	assert.EqualError(t, err, "several pipelines use repository ado-gh; pass one as an argument:\n"+
		"  ado-gh\n  Infra\\nightly\n  Apps\\Web\\deploy")
}

func TestDefaultDefinitionSeveralTTY(t *testing.T) {
	e := newDefaultPipeEnv(t, "defpipeselectorg", true, 1, 2, 3, 4)
	e.in.WriteString("3\n")
	d, err := DefaultDefinition(context.Background(), e.f, e.c, "ado-gh")
	require.NoError(t, err)
	assert.Equal(t, 4, *d.Id)
	assert.Contains(t, e.out.String(), "Which pipeline?")
	assert.Contains(t, e.out.String(), `Infra\nightly`)
}

func TestDefaultDefinitionNoRepo(t *testing.T) {
	_, err := DefaultDefinition(context.Background(), nil, nil, "")
	assert.EqualError(t, err, "no current repository; pass a pipeline name or ID as an argument")
}

func TestDefaultDefinitionRepoError(t *testing.T) {
	org := "defpiperepoerrorg"
	reg := &httpmock.Registry{}
	reg.StubLocations(org, slices.Concat(httpmock.PipelineLocations, httpmock.GitRepoLocations)...)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/gone"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: repository gone not found"}))
	c := api.NewPipelineClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj")
	_, err := DefaultDefinition(context.Background(), &cmdutil.Factory{}, c, "gone")
	assert.ErrorContains(t, err, "TF401019")
}
