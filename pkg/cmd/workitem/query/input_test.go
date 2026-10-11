package query

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

const wiQueryInputID = "0a1b2c3d-0000-4000-8000-00000000000a"

type wiQueryInputEnv struct {
	org string
	reg *httpmock.Registry
	ios *iostreams.IOStreams
	in  *bytes.Buffer
	out *bytes.Buffer
	cfg *config.Config
	// wiqls and tops are the WIQL bodies and $top values posted.
	wiqls, tops []string
	// expands are the $expand values of GetQuery calls.
	expands []string
}

// newWIQueryInputEnv returns an env for its own org. The SDK caches discovery per base URL, so
// the OPTIONS location lookup is stubbed per test when sendsHTTP.
func newWIQueryInputEnv(t *testing.T, sendsHTTP bool) *wiQueryInputEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, _ := iostreams.Test()
	cfg, err := config.Load()
	require.NoError(t, err)
	org := "wiqueryin-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &wiQueryInputEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, cfg: cfg}
	t.Cleanup(func() { e.reg.Verify(t) })
	if sendsHTTP {
		e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	}
	return e
}

// stubWIQL answers a WIQL POST on path (relative to the org) with ids after recording it.
func (e *wiQueryInputEnv) stubWIQL(t *testing.T, path string, ids ...int) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/"+path), func(r *http.Request) (*http.Response, error) {
		var body struct{ Query string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.wiqls, e.tops = append(e.wiqls, body.Query), append(e.tops, r.URL.Query().Get("$top"))
		return httpmock.JSONResponse(wiQueryInputFlat(ids))(r)
	})
}

// stubSaved answers GetQuery for the escaped idOrPath with item after recording $expand.
func (e *wiQueryInputEnv) stubSaved(idOrPath string, item map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/queries/"+idOrPath), func(r *http.Request) (*http.Response, error) {
		e.expands = append(e.expands, r.URL.Query().Get("$expand"))
		return httpmock.JSONResponse(item)(r)
	})
}

// stubRunByID answers QueryById on path (relative to the org) with ids after recording $top.
func (e *wiQueryInputEnv) stubRunByID(path string, ids ...int) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+path), func(r *http.Request) (*http.Response, error) {
		e.tops = append(e.tops, r.URL.Query().Get("$top"))
		return httpmock.JSONResponse(wiQueryInputFlat(ids))(r)
	})
}

func (e *wiQueryInputEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return e.cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
	}
	cmd := NewCmdQuery(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func wiQueryInputFlat(ids []int) map[string]any {
	items := []map[string]any{}
	for _, id := range ids {
		items = append(items, map[string]any{"id": id})
	}
	return map[string]any{"queryType": "flat", "queryResultType": "workItem", "workItems": items}
}

func wiQueryInputSaved(path string) map[string]any {
	return map[string]any{"id": wiQueryInputID, "name": filepath.Base(path), "path": path, "isFolder": false,
		"queryType": "flat", "wiql": "SELECT [System.Id] FROM WorkItems WHERE [System.AssignedTo] = @Me"}
}

func TestWIQueryInputWIQL(t *testing.T) {
	for i, tt := range []struct{ wiql, top string }{
		{"SELECT [System.Id] FROM WorkItems WHERE [System.AssignedTo] = @Me AND [System.CreatedDate] > @Today - 7", "200"},
		{"  select [System.Id] from WorkItems where [System.IterationPath] = @CurrentIteration", "200"},
		{"Select [System.Id] FROM WorkItemLinks WHERE [System.AreaPath] IN @TeamAreas", ""},
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			e := newWIQueryInputEnv(t, true)
			e.stubWIQL(t, "My%20Proj/_apis/wit/wiql", 7, 8)
			require.NoError(t, e.run(t, tt.wiql))
			assert.Equal(t, []string{tt.wiql}, e.wiqls, "macros passed through unchanged")
			assert.Equal(t, []string{tt.top}, e.tops, "link queries send no $top")
			assert.Equal(t, "7\n8\n", e.out.String())
		})
	}
}

func TestWIQueryInputTeam(t *testing.T) {
	wiql := "SELECT [System.Id] FROM WorkItems WHERE [System.IterationPath] = @CurrentIteration"
	t.Run("flag", func(t *testing.T) {
		e := newWIQueryInputEnv(t, true)
		e.cfg.Set(config.ProjectScope(e.org, "My Proj"), "team", "Config Team")
		e.stubWIQL(t, "My%20Proj/ado-gh%20Team/_apis/wit/wiql", 1)
		require.NoError(t, e.run(t, wiql, "--team", "ado-gh Team", "-L", "5"))
		assert.Equal(t, []string{"5"}, e.tops)
	})
	t.Run("config", func(t *testing.T) {
		e := newWIQueryInputEnv(t, true)
		e.cfg.Set(config.ProjectScope(e.org, "My Proj"), "team", "Config Team")
		e.stubWIQL(t, "My%20Proj/Config%20Team/_apis/wit/wiql", 1)
		require.NoError(t, e.run(t, wiql))
	})
	t.Run("saved query by ID", func(t *testing.T) {
		e := newWIQueryInputEnv(t, true)
		e.stubSaved(wiQueryInputID, wiQueryInputSaved("Shared Queries/Mine"))
		e.stubRunByID("My%20Proj/ado-gh%20Team/_apis/wit/wiql/"+wiQueryInputID, 3)
		require.NoError(t, e.run(t, wiQueryInputID, "--team", "ado-gh Team"))
		assert.Equal(t, "3\n", e.out.String())
	})
}

func TestWIQueryInputSaved(t *testing.T) {
	tests := []struct{ arg, escaped, path string }{
		{wiQueryInputID, wiQueryInputID, "Shared Queries/Mine"},
		{"Shared Queries/Team/Active Bugs", "Shared%20Queries%2FTeam%2FActive%20Bugs", "Shared Queries/Team/Active Bugs"},
		{"/My Queries/Selection/", "My%20Queries%2FSelection", "My Queries/Selection"},
		{"SELECTED", "SELECTED", "Shared Queries/SELECTED"},
	}
	for i, tt := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			e := newWIQueryInputEnv(t, true)
			e.stubSaved(tt.escaped, wiQueryInputSaved(tt.path))
			e.stubRunByID("My%20Proj/_apis/wit/wiql/"+wiQueryInputID, 4, 5)
			require.NoError(t, e.run(t, tt.arg, "-L", "50"))
			assert.Equal(t, []string{"wiql"}, e.expands)
			assert.Equal(t, []string{"50"}, e.tops)
			assert.Equal(t, "4\n5\n", e.out.String())
		})
	}
}

func TestWIQueryInputFolder(t *testing.T) {
	e := newWIQueryInputEnv(t, true)
	e.stubSaved("Shared%20Queries%2FTeam", map[string]any{"id": wiQueryInputID, "name": "Team",
		"path": "Shared Queries/Team", "isFolder": true, "hasChildren": true})
	err := e.run(t, "Shared Queries/Team")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"Shared Queries/Team" is a query folder, not a query`)
}

func TestWIQueryInputMissing(t *testing.T) {
	e := newWIQueryInputEnv(t, true)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/queries/My%20Queries%2Fnope"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{
			"message": "TF401243: The query My Queries/nope does not exist, or you do not have permission to read it.",
		}))
	err := e.run(t, "My Queries/nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TF401243")
}

func TestWIQueryInputWIQLFile(t *testing.T) {
	wiql := "SELECT [System.Id]\nFROM WorkItems\nWHERE [System.ChangedDate] > @Today - 1\n"
	t.Run("file", func(t *testing.T) {
		e := newWIQueryInputEnv(t, true)
		p := filepath.Join(t.TempDir(), "q.wiql")
		require.NoError(t, os.WriteFile(p, []byte(wiql), 0o600))
		e.stubWIQL(t, "My%20Proj/_apis/wit/wiql", 9)
		require.NoError(t, e.run(t, "--wiql-file", p))
		assert.Equal(t, []string{wiql}, e.wiqls)
	})
	t.Run("stdin", func(t *testing.T) {
		e := newWIQueryInputEnv(t, true)
		e.in.WriteString(wiql)
		e.stubWIQL(t, "My%20Proj/_apis/wit/wiql", 9)
		require.NoError(t, e.run(t, "--wiql-file", "-"))
		assert.Equal(t, []string{wiql}, e.wiqls)
	})
	t.Run("missing file", func(t *testing.T) {
		e := newWIQueryInputEnv(t, false)
		err := e.run(t, "--wiql-file", filepath.Join(t.TempDir(), "none.wiql"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading the WIQL")
	})
	t.Run("empty stdin", func(t *testing.T) {
		e := newWIQueryInputEnv(t, false)
		e.in.WriteString(" \n")
		require.ErrorContains(t, e.run(t, "--wiql-file", "-"), "query is empty")
	})
}

func TestWIQueryInputArgErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"none":       {},
		"both":       {"SELECT 1", "--wiql-file", "-"},
		"blank":      {" / "},
		"zero limit": {"x", "-L", "0"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newWIQueryInputEnv(t, false)
			err := e.run(t, args...)
			var fe *cmdutil.FlagError
			require.ErrorAs(t, err, &fe)
		})
	}
}
