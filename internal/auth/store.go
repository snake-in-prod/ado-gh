// Package auth stores credentials in the OS keyring, falling back to the
// 0600 config file when the keyring is unavailable.
package auth

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zalando/go-keyring"
)

// Token sources reported by Store.Token. Env tokens report the variable name.
const (
	SourceKeyring = "keyring"
	SourceConfig  = "config"
)

var envTokens = []string{"ADO_TOKEN", "AZURE_DEVOPS_EXT_PAT"}

// Config is the subset of internal/config.Config the store needs.
type Config interface {
	Get(host, key string) string
	Set(host, key, value string)
	Unset(host, key string)
	Write() error
}

// Store reads and writes tokens. Org "" addresses the per-host (Entra) token;
// a non-empty org addresses that org's PAT.
type Store struct {
	Config Config
	ErrOut io.Writer
}

// service is the keyring service name: ado:<host>/<org>, or ado:<host> for the host token.
func service(host, org string) string {
	if org == "" {
		return "ado:" + host
	}
	return "ado:" + host + "/" + org
}

// tokenKey is the per-host config key holding a plaintext token.
func tokenKey(org string) string {
	if org == "" {
		return "token"
	}
	return "token:" + org
}

// Token returns the token for host/org and its source: an env variable name,
// SourceKeyring or SourceConfig. Lookup: env, then the org key, then the host
// key; for each key the keyring before the config file. Returns "", "" if none.
func (s *Store) Token(host, org string) (token, source string) {
	for _, env := range envTokens {
		if v := os.Getenv(env); v != "" {
			return v, env
		}
	}
	orgs := []string{org}
	if org != "" {
		orgs = append(orgs, "")
	}
	for _, o := range orgs {
		// ErrNotFound means nothing stored; any other error means the keyring
		// is unusable. Either way the config file is the next source.
		if v, err := keyringGet(service(host, o)); err == nil && v != "" {
			return v, SourceKeyring
		}
		if v := s.Config.Get(host, tokenKey(o)); v != "" {
			return v, SourceConfig
		}
	}
	return "", ""
}

// Set stores token in the keyring, or in the config file in plaintext with a
// warning when insecure is set or the keyring fails.
func (s *Store) Set(host, org, token string, insecure bool) error {
	key := tokenKey(org)
	if !insecure && keyringSet(service(host, org), token) == nil {
		if s.Config.Get(host, key) == "" {
			return nil
		}
		s.Config.Unset(host, key)
		return s.Config.Write()
	}
	s.Config.Set(host, key, token)
	if err := s.Config.Write(); err != nil {
		return err
	}
	fmt.Fprintln(s.ErrOut, "! Authentication credentials saved in plain text")
	return nil
}

// Delete removes the token from the config file and the keyring. A keyring
// error other than not-found is returned after the config file is cleaned.
func (s *Store) Delete(host, org string) error {
	keyErr := keyringDelete(service(host, org))
	if s.Config.Get(host, tokenKey(org)) != "" {
		s.Config.Unset(host, tokenKey(org))
		if err := s.Config.Write(); err != nil {
			return err
		}
	}
	if errors.Is(keyErr, keyring.ErrNotFound) {
		return nil
	}
	return keyErr
}
