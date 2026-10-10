package status

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type env struct {
	f      *cmdutil.Factory
	cfg    *config.Config
	reg    *httpmock.Registry
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

func setup(t *testing.T) *env {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ENTRA_CLIENT_ID", "client-id")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, out, errOut := iostreams.Test()
	reg := &httpmock.Registry{}
	f := &cmdutil.Factory{
		AppVersion: "1.0",
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
		BaseRepo:   func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("no repo") },
	}
	return &env{f: f, cfg: cfg, reg: reg, out: out, errOut: errOut}
}

func (e *env) run(args ...string) error {
	cmd := NewCmdStatus(e.f)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true // as under the root command
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func connectionData(id string) httpmock.Responder {
	return httpmock.JSONResponse(map[string]any{
		"authenticatedUser": map[string]any{"id": id, "providerDisplayName": "Mona Lisa"},
	})
}

type fakeMSAL struct{ public.Account }

func (m *fakeMSAL) Accounts(context.Context) ([]public.Account, error) {
	return []public.Account{m.Account}, nil
}
func (m *fakeMSAL) Silent(context.Context, public.Account, string) (public.AuthResult, error) {
	return public.AuthResult{AccessToken: "eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.entra"}, nil
}
func (m *fakeMSAL) Interactive(context.Context, string, func(string) error) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}
func (m *fakeMSAL) DeviceCode(context.Context, string, func(string)) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}
func (m *fakeMSAL) RemoveAccount(context.Context, public.Account) error { return nil }

func TestStatusPAT(t *testing.T) {
	e := setup(t)
	require.NoError(t, keyring.Set("ado:dev.azure.com/contoso", "", "abcdefghijklmnop"))
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run("--org", "contoso"))
	e.reg.Verify(t)
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte(":abcdefghijklmnop")),
		e.reg.Requests[0].Header.Get("Authorization"))
	assert.Equal(t, `dev.azure.com/contoso
  ✓ Logged in as Mona Lisa
  - Method: pat
  - Token source: keyring
  - Token: abcd****************
`, e.out.String())
	assert.NotContains(t, e.out.String()+e.errOut.String(), "efghijklmnop", "token is masked")
}

func TestStatusEnvTokenFromRemote(t *testing.T) {
	e := setup(t)
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "short")
	e.f.BaseRepo = func() (adocontext.Repo, error) {
		return adocontext.Repo{Host: "contoso.visualstudio.com", Org: "contoso"}, nil
	}
	e.reg.Register(httpmock.REST("GET", "_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run())
	assert.Equal(t, "contoso.visualstudio.com", e.reg.Requests[0].URL.Host)
	assert.Contains(t, e.out.String(), "contoso.visualstudio.com/contoso\n")
	assert.Contains(t, e.out.String(), "Token source: AZURE_DEVOPS_EXT_PAT\n")
	assert.Contains(t, e.out.String(), "Token: *****\n")
}

func TestStatusEntra(t *testing.T) {
	e := setup(t)
	e.cfg.Set("dev.azure.com", auth.HomeAccountIDKey, "uid.tid")
	e.cfg.Set("dev.azure.com", auth.TenantKey("contoso"), "tid")
	old := shared.NewMSAL
	shared.NewMSAL = func(string, cache.ExportReplace) (auth.MSAL, error) {
		return &fakeMSAL{public.Account{HomeAccountID: "uid.tid"}}, nil
	}
	t.Cleanup(func() { shared.NewMSAL = old })
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run("--org", "contoso"))
	assert.True(t, strings.HasPrefix(e.reg.Requests[0].Header.Get("Authorization"), "Bearer eyJ0"))
	assert.Contains(t, e.out.String(), "Method: entra\n")
	assert.Contains(t, e.out.String(), "Token: eyJ0****************\n")
}

func TestStatusNotLoggedIn(t *testing.T) {
	e := setup(t)
	err := e.run("--org", "contoso")
	var authErr *cmdutil.AuthError
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, cmdutil.ExitAuth, cmdutil.HandleError(e.errOut, err, ""))
	assert.Empty(t, e.reg.Requests)
}

func TestStatusInvalidToken(t *testing.T) {
	e := setup(t)
	require.NoError(t, keyring.Set("ado:dev.azure.com/contoso", "", "revoked-token"))
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), httpmock.StatusStringResponse(401, ""))

	err := e.run("--org", "contoso")
	var authErr *cmdutil.AuthError
	require.ErrorAs(t, err, &authErr, "invalid token exits 4")
	assert.Contains(t, e.out.String(), "X Token invalid\n")
	assert.Contains(t, e.out.String(), "Token source: keyring\n")
}

func TestStatusErrors(t *testing.T) {
	e := setup(t)
	err := e.run()
	assert.EqualError(t, err, shared.NoOrg)

	require.NoError(t, keyring.Set("ado:dev.azure.com/contoso", "", "pat"))
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), httpmock.StatusStringResponse(500, ""))
	err = e.run("--org", "contoso")
	assert.ErrorContains(t, err, "HTTP 500")
	assert.Empty(t, e.out.String())

	boom := errors.New("no client")
	e.f.HttpClient = func() (*http.Client, error) { return nil, boom }
	assert.ErrorIs(t, e.run("--org", "contoso"), boom)
}

func TestStatusDebugLog(t *testing.T) {
	e := setup(t)
	e.f.Debug = true
	require.NoError(t, keyring.Set("ado:dev.azure.com/contoso", "", "secret-pat"))
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run("--org", "contoso"))
	assert.Contains(t, e.errOut.String(), "connectionData")
	assert.NotContains(t, e.errOut.String(), "secret-pat")
}
