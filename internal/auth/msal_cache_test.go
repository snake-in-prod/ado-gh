package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// blob is a cache.Marshaler and cache.Unmarshaler holding raw bytes.
type blob struct {
	data      []byte
	unmarshal int
	err       error
}

func (b *blob) Marshal() ([]byte, error) { return b.data, b.err }
func (b *blob) Unmarshal(d []byte) error {
	b.unmarshal++
	b.data = d
	return nil
}

func newCache(t *testing.T) (*MSALCache, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	return &MSALCache{Dir: filepath.Join(t.TempDir(), "ado"), ErrOut: &stderr}, &stderr
}

func assertMode0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestMSALCacheEncryptedRoundTrip(t *testing.T) {
	keyring.MockInit()
	c, stderr := newCache(t)
	ctx := context.Background()
	secret := []byte(`{"RefreshToken":{"rt":{"secret":"very-secret-refresh-token"}}}`)

	require.NoError(t, c.Export(ctx, &blob{data: secret}, cache.ExportHints{}))

	enc := filepath.Join(c.Dir, "msal_cache.bin")
	assertMode0600(t, enc)
	b, err := os.ReadFile(enc)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "very-secret", "cache file is encrypted")
	assert.NoFileExists(t, filepath.Join(c.Dir, "msal_cache.json"))
	assert.Empty(t, stderr.String())

	key, err := keyring.Get("ado", "msal-cache-key")
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(key)
	require.NoError(t, err)
	assert.Len(t, raw, 32, "only the 32-byte key lives in the keyring")

	var got blob
	require.NoError(t, c.Replace(ctx, &got, cache.ReplaceHints{}))
	assert.Equal(t, secret, got.data)

	// A second export reuses the key and leaves no temp files behind (atomic temp + rename).
	require.NoError(t, c.Export(ctx, &blob{data: []byte("v2")}, cache.ExportHints{}))
	key2, _ := keyring.Get("ado", "msal-cache-key")
	assert.Equal(t, key, key2)
	entries, _ := os.ReadDir(c.Dir)
	assert.Len(t, entries, 1)
	require.NoError(t, c.Replace(ctx, &got, cache.ReplaceHints{}))
	assert.Equal(t, []byte("v2"), got.data)
}

func TestMSALCacheReplaceEmpty(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, c *MSALCache)
	}{
		{name: "nothing stored", setup: func(*testing.T, *MSALCache) {}},
		{name: "undecryptable cache", setup: func(t *testing.T, c *MSALCache) {
			require.NoError(t, c.Export(context.Background(), &blob{data: []byte("x")}, cache.ExportHints{}))
			require.NoError(t, keyring.Set("ado", "msal-cache-key", base64.StdEncoding.EncodeToString(make([]byte, 32))))
		}},
		{name: "truncated cache file", setup: func(t *testing.T, c *MSALCache) {
			require.NoError(t, os.MkdirAll(c.Dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(c.Dir, "msal_cache.bin"), []byte("abc"), 0o600))
			require.NoError(t, keyring.Set("ado", "msal-cache-key", base64.StdEncoding.EncodeToString(make([]byte, 32))))
		}},
		{name: "key missing from the keyring", setup: func(t *testing.T, c *MSALCache) {
			require.NoError(t, c.Export(context.Background(), &blob{data: []byte("x")}, cache.ExportHints{}))
			require.NoError(t, keyring.Delete("ado", "msal-cache-key"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			c, _ := newCache(t)
			tt.setup(t, c)
			var got blob
			require.NoError(t, c.Replace(context.Background(), &got, cache.ReplaceHints{}))
			assert.Zero(t, got.unmarshal, "empty cache: nothing unmarshaled")
		})
	}
}

func TestMSALCacheInvalidKeyIsReplaced(t *testing.T) {
	keyring.MockInit()
	require.NoError(t, keyring.Set("ado", "msal-cache-key", "not base64!"))
	c, _ := newCache(t)
	require.NoError(t, c.Export(context.Background(), &blob{data: []byte("x")}, cache.ExportHints{}))
	var got blob
	require.NoError(t, c.Replace(context.Background(), &got, cache.ReplaceHints{}))
	assert.Equal(t, []byte("x"), got.data)
}

func TestMSALCachePlaintextFallback(t *testing.T) {
	keyring.MockInitWithError(errors.New("dbus: couldn't determine address of session bus"))
	c, stderr := newCache(t)
	ctx := context.Background()

	require.NoError(t, c.Export(ctx, &blob{data: []byte("tokens")}, cache.ExportHints{}), "Export must not fail the login")
	plain := filepath.Join(c.Dir, "msal_cache.json")
	assertMode0600(t, plain)
	b, _ := os.ReadFile(plain)
	assert.Equal(t, "tokens", string(b))
	assert.Contains(t, stderr.String(), "plain text")

	stderr.Reset()
	require.NoError(t, c.Export(ctx, &blob{data: []byte("tokens2")}, cache.ExportHints{}))
	assert.Empty(t, stderr.String(), "the warning is shown once")

	var got blob
	require.NoError(t, c.Replace(ctx, &got, cache.ReplaceHints{}))
	assert.Equal(t, []byte("tokens2"), got.data)

	// Once the keyring works again, the cache is encrypted and the plaintext file removed.
	keyring.MockInit()
	require.NoError(t, c.Export(ctx, &blob{data: []byte("tokens3")}, cache.ExportHints{}))
	assert.NoFileExists(t, plain)
	require.NoError(t, c.Replace(ctx, &got, cache.ReplaceHints{}))
	assert.Equal(t, []byte("tokens3"), got.data)
}

func TestMSALCacheClear(t *testing.T) {
	keyring.MockInit()
	c, _ := newCache(t)
	ctx := context.Background()
	require.NoError(t, c.Clear(ctx), "nothing stored")

	require.NoError(t, c.Export(ctx, &blob{data: []byte("tokens")}, cache.ExportHints{}))
	require.NoError(t, os.WriteFile(filepath.Join(c.Dir, "msal_cache.json"), []byte("x"), 0o600))
	assert.True(t, c.Plaintext())

	require.NoError(t, c.Clear(ctx))
	assert.False(t, c.Plaintext())
	assert.NoFileExists(t, filepath.Join(c.Dir, "msal_cache.bin"))
	_, err := keyring.Get("ado", "msal-cache-key")
	assert.ErrorIs(t, err, keyring.ErrNotFound)

	boom := errors.New("keyring locked")
	keyring.MockInitWithError(boom)
	assert.ErrorIs(t, c.Clear(ctx), boom)
}

func TestMSALCacheDefaultTimeout(t *testing.T) {
	keyring.MockInit()
	c, stderr := newCache(t)
	stubKeyring(t, time.Hour)
	old := cacheTimeout
	cacheTimeout = 20 * time.Millisecond
	t.Cleanup(func() { cacheTimeout = old })

	start := time.Now()
	// context.Background has no deadline: the accessor applies its own.
	require.NoError(t, c.Export(context.Background(), &blob{data: []byte("x")}, cache.ExportHints{}))
	assert.Less(t, time.Since(start), 10*time.Second)
	assert.FileExists(t, filepath.Join(c.Dir, "msal_cache.json"), "a hanging keyring falls back to plaintext")
	assert.Contains(t, stderr.String(), "plain text")

	// Replace with a hanging keyring reads an encrypted cache as empty.
	require.NoError(t, os.Rename(filepath.Join(c.Dir, "msal_cache.json"), filepath.Join(c.Dir, "msal_cache.bin")))
	var got blob
	require.NoError(t, c.Replace(context.Background(), &got, cache.ReplaceHints{}))
	assert.Zero(t, got.unmarshal)
}

func TestMSALCacheDefaultTimeoutValue(t *testing.T) {
	assert.Greater(t, cacheTimeout, keyringTimeout, "the keyring's own timeout fires first")
}

func TestMSALCacheExportErrors(t *testing.T) {
	keyring.MockInit()
	c, _ := newCache(t)
	boom := errors.New("boom")
	assert.ErrorIs(t, c.Export(context.Background(), &blob{err: boom}, cache.ExportHints{}), boom)

	// The cache directory can't be created: a file is in the way.
	f := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(f, nil, 0o600))
	c.Dir = filepath.Join(f, "ado")
	assert.Error(t, c.Export(context.Background(), &blob{data: []byte("x")}, cache.ExportHints{}))
	keyring.MockInitWithError(boom)
	assert.Error(t, c.Export(context.Background(), &blob{data: []byte("x")}, cache.ExportHints{}))
}
