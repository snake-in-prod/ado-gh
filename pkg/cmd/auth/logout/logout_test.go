package logout

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type env struct {
	f      *cmdutil.Factory
	cfg    *config.Config
	dir    string
	errOut *bytes.Buffer
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", dir)
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ENTRA_CLIENT_ID", "client-id")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, _, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		Config:    func() (cmdutil.Config, error) { return cfg, nil },
		BaseRepo:  func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("no repo") },
	}
	return &env{f: f, cfg: cfg, dir: dir, errOut: errOut}
}

func (e *env) run(args ...string) error {
	cmd := NewCmdLogout(e.f)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true // as under the root command
	cmd.SetOut(&bytes.Buffer{})
	return cmd.Execute()
}

// reload reads the config file back, to check what logout wrote.
func reload(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg
}

type fakeMSAL struct {
	accounts []public.Account
	removed  []public.Account
}

func (m *fakeMSAL) Accounts(context.Context) ([]public.Account, error) { return m.accounts, nil }
func (m *fakeMSAL) Silent(context.Context, public.Account, string) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}
func (m *fakeMSAL) Interactive(context.Context, string, func(string) error) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}
func (m *fakeMSAL) DeviceCode(context.Context, string, func(string)) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}
func (m *fakeMSAL) RemoveAccount(_ context.Context, a public.Account) error {
	m.removed = append(m.removed, a)
	return nil
}

func TestLogoutPAT(t *testing.T) {
	e := setup(t)
	require.NoError(t, keyring.Set("ado:dev.azure.com/contoso", "", "pat"))
	e.cfg.Set("dev.azure.com", "token:contoso", "plain-pat")
	e.cfg.Set("dev.azure.com", "token:other", "keep")
	require.NoError(t, e.cfg.Write())

	require.NoError(t, e.run("--org", "contoso"))
	_, err := keyring.Get("ado:dev.azure.com/contoso", "")
	assert.ErrorIs(t, err, keyring.ErrNotFound)
	cfg := reload(t)
	assert.Empty(t, cfg.Get("dev.azure.com", "token:contoso"))
	assert.Equal(t, "keep", cfg.Get("dev.azure.com", "token:other"), "other orgs stay logged in")
	assert.Contains(t, e.errOut.String(), "Logged out of dev.azure.com/contoso")
}

func TestLogoutEntra(t *testing.T) {
	e := setup(t)
	e.f.BaseRepo = func() (adocontext.Repo, error) { return adocontext.Repo{Host: "dev.azure.com", Org: "contoso"}, nil }
	e.cfg.Set("dev.azure.com", auth.HomeAccountIDKey, "uid.tid")
	require.NoError(t, e.cfg.Write())
	mine := public.Account{HomeAccountID: "uid.tid"}
	m := &fakeMSAL{accounts: []public.Account{{HomeAccountID: "other"}, mine}}
	old := shared.NewMSAL
	shared.NewMSAL = func(id string, c cache.ExportReplace) (auth.MSAL, error) {
		assert.Equal(t, "client-id", id)
		assert.Equal(t, e.dir, c.(*auth.MSALCache).Dir)
		return m, nil
	}
	t.Cleanup(func() { shared.NewMSAL = old })
	for _, name := range []string{"msal_cache.bin", "msal_cache.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(e.dir, name), []byte("x"), 0o600))
	}
	require.NoError(t, keyring.Set("ado", "msal-cache-key", "key"))

	require.NoError(t, e.run())
	assert.Equal(t, []public.Account{mine}, m.removed)
	assert.NoFileExists(t, filepath.Join(e.dir, "msal_cache.bin"))
	assert.NoFileExists(t, filepath.Join(e.dir, "msal_cache.json"))
	_, err := keyring.Get("ado", "msal-cache-key")
	assert.ErrorIs(t, err, keyring.ErrNotFound)
	assert.Empty(t, reload(t).Get("dev.azure.com", auth.HomeAccountIDKey))
	assert.Contains(t, e.errOut.String(), "Logged out of dev.azure.com/contoso")
}

func TestLogoutEntraWithoutClientID(t *testing.T) {
	e := setup(t)
	t.Setenv("ADO_ENTRA_CLIENT_ID", "")
	e.cfg.Set("dev.azure.com", auth.HomeAccountIDKey, "uid.tid")
	require.NoError(t, os.WriteFile(filepath.Join(e.dir, "msal_cache.bin"), []byte("x"), 0o600))

	require.NoError(t, e.run("--org", "contoso"), "the cache is cleared even without an MSAL client")
	assert.NoFileExists(t, filepath.Join(e.dir, "msal_cache.bin"))
}

func TestLogoutEnvTokenWarning(t *testing.T) {
	e := setup(t)
	t.Setenv("ADO_TOKEN", "env")
	require.NoError(t, e.run("--org", "contoso"))
	assert.Contains(t, e.errOut.String(), "! ADO_TOKEN is set and still authenticates ado")
}

func TestLogoutErrors(t *testing.T) {
	e := setup(t)
	assert.EqualError(t, e.run(), shared.NoOrg)
	assert.EqualError(t, e.run("--org", "contoso"), "not logged in to dev.azure.com/contoso")

	boom := errors.New("bad config")
	e.f.Config = func() (cmdutil.Config, error) { return nil, boom }
	assert.ErrorIs(t, e.run("--org", "contoso"), boom)
}

func TestLogoutKeyringError(t *testing.T) {
	e := setup(t)
	e.cfg.Set("dev.azure.com", "token:contoso", "plain-pat")
	keyring.MockInitWithError(errors.New("keyring locked"))
	assert.ErrorContains(t, e.run("--org", "contoso"), "keyring locked")
}
