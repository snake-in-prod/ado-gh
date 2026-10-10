package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/zalando/go-keyring"
)

// The MSAL cache is too big for one keyring item (2560 B on Windows), so it is stored
// AES-GCM-encrypted in a file and only its key goes to the keyring.
const (
	cacheFile       = "msal_cache.bin"
	plainCacheFile  = "msal_cache.json"
	cacheKeyService = "ado"
	cacheKeyUser    = "msal-cache-key"
)

// cacheTimeout bounds Replace and Export when MSAL passes a ctx without a deadline.
var cacheTimeout = 90 * time.Second

// MSALCache persists MSAL's token cache in Dir (cache.ExportReplace). Without a usable
// keyring it falls back to a plaintext 0600 file and warns once on ErrOut.
type MSALCache struct {
	Dir    string
	ErrOut io.Writer
}

var _ cache.ExportReplace = (*MSALCache)(nil)

func withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, cacheTimeout)
}

// Replace loads the stored cache into u. Anything unreadable (missing file or key,
// failed decryption, unusable keyring) is an empty cache: the user logs in again.
func (c *MSALCache) Replace(ctx context.Context, u cache.Unmarshaler, _ cache.ReplaceHints) error {
	if b, err := os.ReadFile(filepath.Join(c.Dir, plainCacheFile)); err == nil {
		return u.Unmarshal(b)
	}
	b, err := os.ReadFile(filepath.Join(c.Dir, cacheFile))
	if err != nil {
		return nil
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	key, err := c.key(ctx, false)
	if err != nil {
		return nil
	}
	data, err := open(key, b)
	if err != nil {
		return nil
	}
	return u.Unmarshal(data)
}

// Export saves m encrypted, or in plaintext when the keyring is unusable, so a keyring
// failure never discards a successful login.
func (c *MSALCache) Export(ctx context.Context, m cache.Marshaler, _ cache.ExportHints) error {
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	enc, plain := filepath.Join(c.Dir, cacheFile), filepath.Join(c.Dir, plainCacheFile)
	if key, err := c.key(ctx, true); err == nil {
		sealed, err := seal(key, data)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(enc, sealed); err != nil {
			return err
		}
		return removeIfExists(plain)
	}
	_, statErr := os.Stat(plain)
	if err := writeFileAtomic(plain, data); err != nil {
		return err
	}
	if statErr != nil {
		fmt.Fprintln(c.ErrOut, "! No system credential store available: Microsoft Entra ID tokens saved in plain text")
	}
	return removeIfExists(enc)
}

// Plaintext reports whether the cache is stored in plain text (no usable keyring).
func (c *MSALCache) Plaintext() bool {
	_, err := os.Stat(filepath.Join(c.Dir, plainCacheFile))
	return err == nil
}

// Clear deletes the cache files and the cache key from the keyring.
func (c *MSALCache) Clear(ctx context.Context) error {
	for _, f := range []string{cacheFile, plainCacheFile} {
		if err := removeIfExists(filepath.Join(c.Dir, f)); err != nil {
			return err
		}
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	_, err := withTimeoutCtx(ctx, "delete", func() (struct{}, error) {
		return struct{}{}, keyring.Delete(cacheKeyService, cacheKeyUser)
	})
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// key reads the cache key from the keyring; with create, a missing or invalid key is
// replaced by a new random one.
func (c *MSALCache) key(ctx context.Context, create bool) ([]byte, error) {
	v, err := withTimeoutCtx(ctx, "get", func() (string, error) { return keyring.Get(cacheKeyService, cacheKeyUser) })
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return nil, err
	}
	if k, decErr := base64.StdEncoding.DecodeString(v); err == nil && decErr == nil && len(k) == 32 {
		return k, nil
	}
	if !create {
		return nil, errors.New("no cache key")
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	_, err = withTimeoutCtx(ctx, "set", func() (struct{}, error) {
		return struct{}{}, keyring.Set(cacheKeyService, cacheKeyUser, base64.StdEncoding.EncodeToString(k))
	})
	return k, err
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal returns nonce || AES-GCM ciphertext.
func seal(key, data []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, data, nil), nil
}

func open(key, b []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(b) < aead.NonceSize() {
		return nil, errors.New("cache file truncated")
	}
	return aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], nil)
}

// writeFileAtomic writes data to path with mode 0600 via a temp file and rename.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*") // CreateTemp uses 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
