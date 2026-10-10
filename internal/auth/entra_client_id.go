// Package auth handles ado's credentials: PATs and Microsoft Entra ID tokens.
package auth

import (
	"errors"
	"os"
)

// DefaultEntraClientID is ado's multi-tenant public-client Entra app. A public
// client has no secret, so the ID is safe to ship. A var, not a const, so tests can set it.
var DefaultEntraClientID = "41d205f0-a5e5-4a1f-813a-46eec13d0249"

// ConfigGetter reads a config value; cmdutil.Config satisfies it.
type ConfigGetter interface {
	Get(host, key string) string
}

// EntraClientID returns the Entra app client ID: $ADO_ENTRA_CLIENT_ID, else the
// global config key entra_client_id, else the built-in default. cfg may be nil.
func EntraClientID(cfg ConfigGetter) (string, error) {
	if id := os.Getenv("ADO_ENTRA_CLIENT_ID"); id != "" {
		return id, nil
	}
	if cfg != nil {
		if id := cfg.Get("", "entra_client_id"); id != "" {
			return id, nil
		}
	}
	if DefaultEntraClientID != "" {
		return DefaultEntraClientID, nil
	}
	return "", errors.New("Entra ID login needs a client ID: set ADO_ENTRA_CLIENT_ID (or `ado config set entra_client_id <id>`), or log in with a PAT") //nolint:revive,staticcheck // user-facing sentence, starts with a proper noun
}
