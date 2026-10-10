package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// EntraScope is the Azure DevOps resource scope requested for Entra ID tokens.
const EntraScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"

// HomeAccountIDKey is the per-host config key holding the MSAL account of an Entra login.
const HomeAccountIDKey = "home_account_id"

// TenantKey is the per-host config key caching org's Entra tenant.
func TenantKey(org string) string { return "tenant:" + org }

// MSAL is the part of an MSAL public client ado uses; tests fake it. Tenant
// OrganizationsTenant keeps the client's organizations authority.
type MSAL interface {
	Accounts(ctx context.Context) ([]public.Account, error)
	Silent(ctx context.Context, account public.Account, tenant string) (public.AuthResult, error)
	Interactive(ctx context.Context, tenant string, openURL func(string) error) (public.AuthResult, error)
	// DeviceCode passes the sign-in instructions to show, then waits for the user.
	DeviceCode(ctx context.Context, tenant string, show func(message string)) (public.AuthResult, error)
	RemoveAccount(ctx context.Context, account public.Account) error
}

// NewMSAL returns ado's one MSAL public client: organizations authority, tokens cached in c.
func NewMSAL(clientID string, c cache.ExportReplace) (MSAL, error) {
	return newMSAL(clientID, c)
}

func newMSAL(clientID string, c cache.ExportReplace, opts ...public.Option) (MSAL, error) {
	opts = append([]public.Option{
		public.WithAuthority("https://login.microsoftonline.com/" + OrganizationsTenant),
		public.WithCache(c),
	}, opts...)
	client, err := public.New(clientID, opts...)
	return msalClient{client}, err
}

type msalClient struct{ c public.Client }

var scopes = []string{EntraScope}

// tenantID maps OrganizationsTenant to "", which WithTenantID ignores (it rejects "organizations").
func tenantID(tenant string) string {
	if tenant == OrganizationsTenant {
		return ""
	}
	return tenant
}

func (m msalClient) Accounts(ctx context.Context) ([]public.Account, error) {
	return m.c.Accounts(ctx)
}

func (m msalClient) RemoveAccount(ctx context.Context, account public.Account) error {
	return m.c.RemoveAccount(ctx, account)
}

func (m msalClient) Silent(ctx context.Context, account public.Account, tenant string) (public.AuthResult, error) {
	return m.c.AcquireTokenSilent(ctx, scopes, public.WithSilentAccount(account), public.WithTenantID(tenantID(tenant)))
}

func (m msalClient) Interactive(ctx context.Context, tenant string, openURL func(string) error) (public.AuthResult, error) {
	return m.c.AcquireTokenInteractive(ctx, scopes, public.WithOpenURL(openURL), public.WithTenantID(tenantID(tenant)))
}

func (m msalClient) DeviceCode(ctx context.Context, tenant string, show func(string)) (public.AuthResult, error) {
	dc, err := m.c.AcquireTokenByDeviceCode(ctx, scopes, public.WithTenantID(tenantID(tenant)))
	if err != nil {
		return public.AuthResult{}, err
	}
	show(dc.Result.Message)
	return dc.AuthenticationResult(ctx)
}

// EntraTokens silently gets Entra ID access tokens for one org, for the api transport.
type EntraTokens struct {
	MSAL   MSAL
	Config Config
	Host   string
	Org    string
	// OrgURL and HTTPClient discover the org's tenant when the config has none cached.
	OrgURL     string
	HTTPClient *http.Client
}

// Token returns an access token from the MSAL cache, refreshing it when needed. Failures
// that a new login fixes are *cmdutil.AuthError.
func (e *EntraTokens) Token(ctx context.Context) (string, error) {
	id := e.Config.Get(e.Host, HomeAccountIDKey)
	if id == "" {
		return "", &cmdutil.AuthError{Err: fmt.Errorf("not logged in to %s with Microsoft Entra ID", e.Host)}
	}
	tenant := e.Config.Get(e.Host, TenantKey(e.Org))
	if tenant == "" {
		var err error
		if tenant, err = DiscoverTenant(ctx, e.HTTPClient, e.OrgURL, e.Org); err != nil {
			return "", err
		}
		e.Config.Set(e.Host, TenantKey(e.Org), tenant)
		if err := e.Config.Write(); err != nil {
			return "", err
		}
	}
	accounts, err := e.MSAL.Accounts(ctx)
	if err != nil {
		return "", &cmdutil.AuthError{Err: fmt.Errorf("read the Microsoft Entra ID token cache: %w", err)}
	}
	for _, a := range accounts {
		if a.HomeAccountID == id {
			res, err := e.MSAL.Silent(ctx, a, tenant)
			if err != nil {
				return "", &cmdutil.AuthError{Err: fmt.Errorf("the Microsoft Entra ID sign-in for %s expired or was revoked: %w", e.Host, err)}
			}
			return res.AccessToken, nil
		}
	}
	return "", &cmdutil.AuthError{Err: fmt.Errorf("the Microsoft Entra ID sign-in for %s is no longer in the token cache", e.Host)}
}
