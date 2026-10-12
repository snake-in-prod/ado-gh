package shared

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestOrgFromContext(t *testing.T) {
	repo := func() (adocontext.Repo, error) {
		return adocontext.Repo{Host: "contoso.visualstudio.com", Org: "contoso"}, nil
	}
	noRepo := func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("no repo") }
	tests := []struct {
		name, flag, env string
		repo            func() (adocontext.Repo, error)
		host, org       string
	}{
		{name: "flag wins", flag: "fab", env: "env", repo: repo, host: "dev.azure.com", org: "fab"},
		{name: "remote keeps host", env: "env", repo: repo, host: "contoso.visualstudio.com", org: "contoso"},
		{name: "env", env: "env", repo: noRepo, host: "dev.azure.com", org: "env"},
		{name: "nil resolver", env: "env", host: "dev.azure.com", org: "env"},
		{name: "none", repo: noRepo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ADO_ORG", tt.env)
			host, org := OrgFromContext(tt.flag, tt.repo)
			assert.Equal(t, tt.host, host)
			assert.Equal(t, tt.org, org)
		})
	}
}

type fakeMSAL struct {
	accounts []public.Account
	err      error
}

func (m *fakeMSAL) Accounts(context.Context) ([]public.Account, error) { return m.accounts, nil }
func (m *fakeMSAL) Silent(_ context.Context, _ public.Account, tenant string) (public.AuthResult, error) {
	return public.AuthResult{AccessToken: "at-" + tenant}, m.err
}
func (m *fakeMSAL) Interactive(context.Context, string, func(string) error) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}
func (m *fakeMSAL) DeviceCode(context.Context, string, func(string)) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}
func (m *fakeMSAL) RemoveAccount(context.Context, public.Account) error { return nil }

func newFactory(t *testing.T, reg *httpmock.Registry) (*cmdutil.Factory, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", dir)
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_TOKEN_TYPE", "")
	t.Setenv("ADO_ENTRA_CLIENT_ID", "client-id")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
	}, cfg
}

func stubMSAL(t *testing.T, m auth.MSAL) {
	t.Helper()
	old := NewMSAL
	NewMSAL = func(clientID string, _ cache.ExportReplace) (auth.MSAL, error) {
		assert.Equal(t, "client-id", clientID)
		return m, nil
	}
	t.Cleanup(func() { NewMSAL = old })
}

func TestLookupPAT(t *testing.T) {
	f, _ := newFactory(t, &httpmock.Registry{})
	require.NoError(t, keyring.Set("ado:dev.azure.com/contoso", "", "pat"))

	got, err := Lookup(context.Background(), f, "dev.azure.com", "contoso")
	require.NoError(t, err)
	assert.Equal(t, Credential{Token: api.Token{Value: "pat"}, Method: MethodPAT, Source: auth.SourceKeyring}, got)

	t.Setenv("ADO_TOKEN", "env-pat")
	got, err = Lookup(context.Background(), f, "dev.azure.com", "contoso")
	require.NoError(t, err)
	assert.Equal(t, Credential{Token: api.Token{Value: "env-pat"}, Method: MethodPAT, Source: "ADO_TOKEN"}, got)
}

// An extension with token consent gets an Entra token as ADO_TOKEN plus ADO_TOKEN_TYPE=bearer.
func TestLookupEnvTokenType(t *testing.T) {
	tests := []struct {
		env, typ string
		want     Credential
	}{
		{"ADO_TOKEN", "bearer", Credential{Token: api.Token{Value: "tok", Bearer: true}, Method: MethodEntra, Source: "ADO_TOKEN"}},
		{"ADO_TOKEN", "Bearer", Credential{Token: api.Token{Value: "tok", Bearer: true}, Method: MethodEntra, Source: "ADO_TOKEN"}},
		{"ADO_TOKEN", "", Credential{Token: api.Token{Value: "tok"}, Method: MethodPAT, Source: "ADO_TOKEN"}},
		{"ADO_TOKEN", "pat", Credential{Token: api.Token{Value: "tok"}, Method: MethodPAT, Source: "ADO_TOKEN"}},
		// The type describes ADO_TOKEN only; AZURE_DEVOPS_EXT_PAT is always a PAT.
		{"AZURE_DEVOPS_EXT_PAT", "bearer", Credential{Token: api.Token{Value: "tok"}, Method: MethodPAT, Source: "AZURE_DEVOPS_EXT_PAT"}},
	}
	for _, tt := range tests {
		t.Run(tt.env+"="+tt.typ, func(t *testing.T) {
			f, _ := newFactory(t, &httpmock.Registry{})
			t.Setenv(tt.env, "tok")
			t.Setenv("ADO_TOKEN_TYPE", tt.typ)
			got, err := Lookup(context.Background(), f, "dev.azure.com", "contoso")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLookupNotLoggedIn(t *testing.T) {
	f, _ := newFactory(t, &httpmock.Registry{})
	_, err := Lookup(context.Background(), f, "dev.azure.com", "contoso")
	var authErr *cmdutil.AuthError
	require.ErrorAs(t, err, &authErr)
	assert.EqualError(t, err, "not logged in to dev.azure.com/contoso")
}

func TestLookupEntra(t *testing.T) {
	reg := &httpmock.Registry{}
	f, cfg := newFactory(t, reg)
	cfg.Set("dev.azure.com", auth.HomeAccountIDKey, "uid.tid")
	reg.Register(httpmock.REST("HEAD", "contoso/"), func(req *http.Request) (*http.Response, error) {
		h := http.Header{"X-Vss-Resourcetenant": {"tid"}}
		return &http.Response{StatusCode: 401, Header: h, Body: http.NoBody, Request: req}, nil
	})
	stubMSAL(t, &fakeMSAL{accounts: []public.Account{{HomeAccountID: "uid.tid"}}})

	got, err := Lookup(context.Background(), f, "dev.azure.com", "contoso")
	require.NoError(t, err)
	reg.Verify(t)
	assert.Equal(t, Credential{Token: api.Token{Value: "at-tid", Bearer: true}, Method: MethodEntra, Source: auth.SourceKeyring}, got)

	require.NoError(t, os.WriteFile(filepath.Join(config.Dir(), "msal_cache.json"), []byte("{}"), 0o600))
	got, err = Lookup(context.Background(), f, "dev.azure.com", "contoso")
	require.NoError(t, err)
	assert.Equal(t, SourcePlaintextCache, got.Source)
}

func TestLookupEntraErrors(t *testing.T) {
	boom := errors.New("AADSTS700082")
	f, cfg := newFactory(t, &httpmock.Registry{})
	cfg.Set("dev.azure.com", auth.HomeAccountIDKey, "uid.tid")
	cfg.Set("dev.azure.com", auth.TenantKey("contoso"), "tid")
	stubMSAL(t, &fakeMSAL{accounts: []public.Account{{HomeAccountID: "uid.tid"}}, err: boom})

	_, err := Lookup(context.Background(), f, "dev.azure.com", "contoso")
	var authErr *cmdutil.AuthError
	require.ErrorAs(t, err, &authErr, "an expired sign-in exits 4")
	assert.ErrorIs(t, err, boom)

	t.Setenv("ADO_ENTRA_CLIENT_ID", "")
	oldID := auth.DefaultEntraClientID
	auth.DefaultEntraClientID = ""
	t.Cleanup(func() { auth.DefaultEntraClientID = oldID })
	_, err = Lookup(context.Background(), f, "dev.azure.com", "contoso")
	assert.ErrorContains(t, err, "client ID")
}

func TestStoreConfigErrors(t *testing.T) {
	boom := errors.New("bad yaml")
	_, err := StoreConfig(&cmdutil.Factory{Config: func() (cmdutil.Config, error) { return nil, boom }})
	assert.ErrorIs(t, err, boom)

	_, err = StoreConfig(&cmdutil.Factory{Config: func() (cmdutil.Config, error) { return readOnly{}, nil }})
	assert.ErrorContains(t, err, "storing credentials")
}

// readOnly is a cmdutil.Config without Unset.
type readOnly struct{}

func (readOnly) Get(string, string) string  { return "" }
func (readOnly) Set(string, string, string) {}
func (readOnly) Write() error               { return nil }
