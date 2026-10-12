package shared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// Authentication methods reported in Credential.Method.
const (
	MethodPAT   = "pat"
	MethodEntra = "entra"
)

// SourcePlaintextCache is the Credential.Source of Entra ID tokens kept in the plaintext MSAL cache.
const SourcePlaintextCache = "plain text token cache"

// NoOrg is the error text when no organization is given or detected.
const NoOrg = "no organization: pass --org, run inside an Azure DevOps repository or set ADO_ORG"

// NewMSAL builds the MSAL client; tests replace it.
var NewMSAL = auth.NewMSAL

// OrgFromContext picks the org: orgFlag > git remote > ADO_ORG; org is "" when none is set.
// orgFlag and ADO_ORG are on dev.azure.com; a remote keeps its own host.
func OrgFromContext(orgFlag string, baseRepo func() (adocontext.Repo, error)) (host, org string) {
	if orgFlag != "" {
		return "dev.azure.com", orgFlag
	}
	if baseRepo != nil {
		if repo, err := baseRepo(); err == nil {
			return repo.Host, repo.Org
		}
	}
	if v := os.Getenv("ADO_ORG"); v != "" {
		return "dev.azure.com", v
	}
	return "", ""
}

// Credential is what ado authenticates to an organization with.
type Credential struct {
	Token  api.Token
	Method string
	// Source is an env variable name, auth.SourceKeyring, auth.SourceConfig or SourcePlaintextCache.
	Source string
}

// MSAL returns ado's MSAL client backed by cache c.
func MSAL(cfg auth.Config, c *auth.MSALCache) (auth.MSAL, error) {
	id, err := auth.EntraClientID(cfg)
	if err != nil {
		return nil, err
	}
	return NewMSAL(id, c)
}

// StoreConfig returns f's config as the credential store's config.
func StoreConfig(f *cmdutil.Factory) (auth.Config, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	store, ok := cfg.(auth.Config)
	if !ok {
		return nil, errors.New("config does not support storing credentials")
	}
	return store, nil
}

// Lookup returns the credential for host/org: an env or stored PAT, else the host's
// Microsoft Entra ID sign-in. No credential is a *cmdutil.AuthError. Tenant discovery
// goes unauthenticated through f.HttpClient.
func Lookup(ctx context.Context, f *cmdutil.Factory, host, org string) (Credential, error) {
	cfg, err := StoreConfig(f)
	if err != nil {
		return Credential{}, err
	}
	errOut := f.IOStreams.ErrOut
	if tok, src := (&auth.Store{Config: cfg, ErrOut: errOut}).Token(host, org); tok != "" {
		if src == "ADO_TOKEN" && strings.EqualFold(os.Getenv("ADO_TOKEN_TYPE"), "bearer") {
			// An Entra token handed to an extension (o3).
			return Credential{Token: api.Token{Value: tok, Bearer: true}, Method: MethodEntra, Source: src}, nil
		}
		return Credential{Token: api.Token{Value: tok}, Method: MethodPAT, Source: src}, nil
	}
	if cfg.Get(host, auth.HomeAccountIDKey) == "" {
		return Credential{}, &cmdutil.AuthError{Err: fmt.Errorf("not logged in to %s/%s", host, org)}
	}
	c := &auth.MSALCache{Dir: config.Dir(), ErrOut: errOut}
	m, err := MSAL(cfg, c)
	if err != nil {
		return Credential{}, err
	}
	plain, err := f.HttpClient()
	if err != nil {
		return Credential{}, err
	}
	tok, err := (&auth.EntraTokens{MSAL: m, Config: cfg, Host: host, Org: org, OrgURL: OrgURL(host, org), HTTPClient: plain}).Token(ctx)
	if err != nil {
		return Credential{}, err
	}
	src := auth.SourceKeyring
	if c.Plaintext() {
		src = SourcePlaintextCache
	}
	return Credential{Token: api.Token{Value: tok, Bearer: true}, Method: MethodEntra, Source: src}, nil
}
