package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const host = "dev.azure.com"

func newStore(t *testing.T) (*Store, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", dir)
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	cfg, err := config.Load()
	require.NoError(t, err)
	var stderr bytes.Buffer
	return &Store{Config: cfg, ErrOut: &stderr}, &stderr, filepath.Join(dir, "config.yml")
}

func reload(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg
}

func TestServiceKeys(t *testing.T) {
	assert.Equal(t, "ado:dev.azure.com/contoso", service(host, "contoso"))
	assert.Equal(t, "ado:dev.azure.com", service(host, ""))
	assert.Equal(t, "ado:fabrikam.visualstudio.com/fabrikam", service("fabrikam.visualstudio.com", "fabrikam"))
}

func TestKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()
	s, stderr, path := newStore(t)

	require.NoError(t, s.Set(host, "contoso", "pat-1", false))
	got, err := keyring.Get("ado:dev.azure.com/contoso", "")
	require.NoError(t, err)
	assert.Equal(t, "pat-1", got)
	assert.Empty(t, stderr.String())
	assert.NoFileExists(t, path, "no plaintext written when the keyring works")

	tok, src := s.Token(host, "contoso")
	assert.Equal(t, "pat-1", tok)
	assert.Equal(t, SourceKeyring, src)

	require.NoError(t, s.Delete(host, "contoso"))
	_, err = keyring.Get("ado:dev.azure.com/contoso", "")
	assert.ErrorIs(t, err, keyring.ErrNotFound)
	tok, src = s.Token(host, "contoso")
	assert.Empty(t, tok)
	assert.Empty(t, src)
}

func TestTokenPerOrgWithHostFallback(t *testing.T) {
	keyring.MockInit()
	s, _, _ := newStore(t)
	require.NoError(t, s.Set(host, "", "entra-token", false))
	require.NoError(t, s.Set(host, "contoso", "contoso-pat", false))

	tok, _ := s.Token(host, "contoso")
	assert.Equal(t, "contoso-pat", tok, "per-org PAT wins")
	tok, src := s.Token(host, "other")
	assert.Equal(t, "entra-token", tok, "per-host token is the fallback")
	assert.Equal(t, SourceKeyring, src)
	tok, _ = s.Token("fabrikam.visualstudio.com", "contoso")
	assert.Empty(t, tok, "hosts are isolated")
}

func TestTokenEnvPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantTok string
		wantSrc string
	}{
		{"ADO_TOKEN wins", map[string]string{"ADO_TOKEN": "a", "AZURE_DEVOPS_EXT_PAT": "b"}, "a", "ADO_TOKEN"},
		{"AZURE_DEVOPS_EXT_PAT", map[string]string{"AZURE_DEVOPS_EXT_PAT": "b"}, "b", "AZURE_DEVOPS_EXT_PAT"},
		{"stored token without env", nil, "stored", SourceKeyring},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			s, _, _ := newStore(t)
			require.NoError(t, s.Set(host, "contoso", "stored", false))
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			tok, src := s.Token(host, "contoso")
			assert.Equal(t, tt.wantTok, tok)
			assert.Equal(t, tt.wantSrc, src)
		})
	}
}

func assertPlaintext(t *testing.T, stderr *bytes.Buffer, path, org, want string) {
	t.Helper()
	assert.Contains(t, stderr.String(), "Authentication credentials saved in plain text")
	assert.Equal(t, want, reload(t).Get(host, tokenKey(org)))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestSetFallsBackToConfigOnKeyringError(t *testing.T) {
	for _, keyErr := range []error{keyring.ErrSetDataTooBig, errors.New("dbus: couldn't determine address of session bus")} {
		t.Run(keyErr.Error(), func(t *testing.T) {
			keyring.MockInitWithError(keyErr)
			s, stderr, path := newStore(t)

			require.NoError(t, s.Set(host, "contoso", "pat-1", false))
			assertPlaintext(t, stderr, path, "contoso", "pat-1")

			// Get errors (other than not-found) mean the keyring is unusable: read the config.
			tok, src := s.Token(host, "contoso")
			assert.Equal(t, "pat-1", tok)
			assert.Equal(t, SourceConfig, src)
		})
	}
}

func TestSetFallsBackOnKeyringTimeout(t *testing.T) {
	keyring.MockInit()
	s, stderr, path := newStore(t)
	stubKeyring(t, 10*time.Millisecond)

	require.NoError(t, s.Set(host, "contoso", "pat-1", false))
	assertPlaintext(t, stderr, path, "contoso", "pat-1")
	tok, src := s.Token(host, "contoso")
	assert.Equal(t, "pat-1", tok)
	assert.Equal(t, SourceConfig, src)
}

func TestInsecureStorageSkipsKeyring(t *testing.T) {
	keyring.MockInit()
	s, stderr, path := newStore(t)

	require.NoError(t, s.Set(host, "contoso", "pat-1", true))
	assertPlaintext(t, stderr, path, "contoso", "pat-1")
	_, err := keyring.Get("ado:dev.azure.com/contoso", "")
	assert.ErrorIs(t, err, keyring.ErrNotFound)
	_, src := s.Token(host, "contoso")
	assert.Equal(t, SourceConfig, src)
}

func TestKeyringSetRemovesPlaintext(t *testing.T) {
	keyring.MockInit()
	s, _, _ := newStore(t)
	require.NoError(t, s.Set(host, "contoso", "old", true))

	require.NoError(t, s.Set(host, "contoso", "new", false))
	assert.Empty(t, reload(t).Get(host, tokenKey("contoso")))
	tok, src := s.Token(host, "contoso")
	assert.Equal(t, "new", tok)
	assert.Equal(t, SourceKeyring, src)
}

func TestDeleteClearsConfigAndReportsKeyringErrors(t *testing.T) {
	keyring.MockInit()
	s, _, _ := newStore(t)
	require.NoError(t, s.Set(host, "contoso", "pat-1", true))
	require.NoError(t, s.Delete(host, "contoso"), "not-found in the keyring is not an error")
	assert.Empty(t, reload(t).Get(host, tokenKey("contoso")))

	boom := errors.New("boom")
	keyring.MockInitWithError(boom)
	assert.ErrorIs(t, s.Delete(host, "contoso"), boom)
}

func TestKeyringTimeoutWrapper(t *testing.T) {
	keyring.MockInit()
	stubKeyring(t, 10*time.Millisecond)

	var te *TimeoutError
	assert.ErrorAs(t, keyringSet("svc", "secret"), &te)
	_, err := keyringGet("svc")
	assert.ErrorAs(t, err, &te)
	assert.ErrorAs(t, keyringDelete("svc"), &te)
	assert.Contains(t, te.Error(), "timed out")
}

func TestDefaultKeyringTimeout(t *testing.T) {
	assert.Equal(t, 60*time.Second, keyringTimeout)
}

// stubKeyring shortens the timeout and makes every keyring call hang forever.
func stubKeyring(t *testing.T, timeout time.Duration) {
	t.Helper()
	oldTimeout, oldHook := keyringTimeout, beforeKeyringCall
	keyringTimeout, beforeKeyringCall = timeout, func() { select {} }
	t.Cleanup(func() { keyringTimeout, beforeKeyringCall = oldTimeout, oldHook })
}
