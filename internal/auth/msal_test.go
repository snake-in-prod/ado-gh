package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const otherTID = "22222222-2222-2222-2222-222222222222"

// fakeIDP is an MSAL HTTP client serving a minimal Entra ID at https://login.test.
type fakeIDP struct {
	mu    sync.Mutex
	paths []string
}

func (f *fakeIDP) CloseIdleConnections() {}

func b64JSON(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (f *fakeIDP) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.paths = append(f.paths, req.URL.Path)
	f.mu.Unlock()
	tenant, _, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/")
	authority := "https://login.test/" + tenant
	var body any
	switch {
	case strings.HasSuffix(req.URL.Path, "/.well-known/openid-configuration"):
		body = map[string]any{
			"token_endpoint":                authority + "/oauth2/v2.0/token",
			"authorization_endpoint":        authority + "/oauth2/v2.0/authorize",
			"device_authorization_endpoint": authority + "/oauth2/v2.0/devicecode",
			"issuer":                        authority + "/v2.0",
		}
	case strings.HasSuffix(req.URL.Path, "/devicecode"):
		body = map[string]any{
			"user_code": "ABCD", "device_code": "dc", "verification_uri": "https://microsoft.com/devicelogin",
			"expires_in": 900, "interval": 1, "message": "To sign in, enter the code ABCD",
		}
	case strings.HasSuffix(req.URL.Path, "/token"):
		realm := tenant
		if realm == OrganizationsTenant {
			realm = tid
		}
		now := time.Now().Unix()
		body = map[string]any{
			"access_token": "at-" + realm, "refresh_token": "rt", "token_type": "Bearer",
			"expires_in": 3600, "scope": EntraScope,
			"client_info": b64JSON(map[string]string{"uid": "uid", "utid": tid}),
			"id_token": "h." + b64JSON(map[string]any{
				"aud": "client", "iss": "https://login.test/" + realm + "/v2.0", "tid": realm, "oid": "uid",
				"preferred_username": "mona@contoso.com", "iat": now, "exp": now + 3600,
			}) + ".s",
		}
	default:
		return nil, fmt.Errorf("unexpected request %s", req.URL)
	}
	b, _ := json.Marshal(body)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}, nil
}

func (f *fakeIDP) reset() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.paths
	f.paths = nil
	return p
}

func newTestMSAL(t *testing.T, idp *fakeIDP, c *MSALCache) MSAL {
	t.Helper()
	m, err := newMSAL("client", c,
		public.WithAuthority("https://login.test/organizations"),
		public.WithHTTPClient(idp),
		public.WithInstanceDiscovery(false))
	require.NoError(t, err)
	return m
}

func TestMSALDeviceCodeAndSilentUseTenant(t *testing.T) {
	keyring.MockInit()
	idp := &fakeIDP{}
	c, _ := newCache(t)
	ctx := context.Background()

	var shown string
	res, err := newTestMSAL(t, idp, c).DeviceCode(ctx, tid, func(m string) { shown = m })
	require.NoError(t, err)
	assert.Equal(t, "To sign in, enter the code ABCD", shown)
	assert.Equal(t, "at-"+tid, res.AccessToken)
	assert.Equal(t, "uid."+tid, res.Account.HomeAccountID)
	for _, p := range idp.reset() {
		assert.True(t, strings.HasPrefix(p, "/"+tid+"/"), "device code request %s uses the org tenant", p)
	}

	// A new process: the account comes back from the encrypted cache file.
	m := newTestMSAL(t, idp, c)
	accounts, err := m.Accounts(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 1)

	res, err = m.Silent(ctx, accounts[0], tid)
	require.NoError(t, err)
	assert.Equal(t, "at-"+tid, res.AccessToken)
	for _, p := range idp.reset() {
		assert.NotContains(t, p, "/token", "cached token: no token request")
	}

	res, err = m.Silent(ctx, accounts[0], otherTID)
	require.NoError(t, err)
	assert.Equal(t, "at-"+otherTID, res.AccessToken, "refresh token redeemed in the other tenant")
	assert.Contains(t, idp.reset(), "/"+otherTID+"/oauth2/v2.0/token")

	require.NoError(t, m.RemoveAccount(ctx, accounts[0]))
	accounts, err = newTestMSAL(t, idp, c).Accounts(ctx)
	require.NoError(t, err)
	assert.Empty(t, accounts, "RemoveAccount drops the account from the persisted cache")
}

func TestMSALInteractive(t *testing.T) {
	for _, tenant := range []string{tid, OrganizationsTenant} {
		t.Run(tenant, func(t *testing.T) {
			keyring.MockInit()
			idp := &fakeIDP{}
			c, _ := newCache(t)
			var opened string
			openURL := func(u string) error {
				opened = u
				p, err := url.Parse(u)
				if err != nil {
					return err
				}
				q := p.Query()
				go func() {
					resp, err := http.PostForm(q.Get("redirect_uri"), url.Values{"code": {"authcode"}, "state": {q.Get("state")}})
					if err == nil {
						resp.Body.Close()
					}
				}()
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			res, err := newTestMSAL(t, idp, c).Interactive(ctx, tenant, openURL)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(opened, "https://login.test/"+tenant+"/oauth2/v2.0/authorize?"), opened)
			assert.Contains(t, idp.reset(), "/"+tenant+"/oauth2/v2.0/token")
			assert.NotEmpty(t, res.AccessToken)
		})
	}
}

func TestMSALInteractiveOpenURLError(t *testing.T) {
	keyring.MockInit()
	c, _ := newCache(t)
	boom := errors.New("no browser")
	_, err := newTestMSAL(t, &fakeIDP{}, c).Interactive(context.Background(), tid, func(string) error { return boom })
	assert.ErrorIs(t, err, boom)
}

func TestMSALDeviceCodeError(t *testing.T) {
	keyring.MockInit()
	c, _ := newCache(t)
	m, err := newMSAL("client", c,
		public.WithAuthority("https://login.test/organizations"),
		public.WithHTTPClient(failingClient{}),
		public.WithInstanceDiscovery(false))
	require.NoError(t, err)
	_, err = m.DeviceCode(context.Background(), tid, func(string) { t.Error("nothing to show") })
	assert.Error(t, err)
}

type failingClient struct{}

func (failingClient) Do(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }
func (failingClient) CloseIdleConnections()                    {}

func TestNewMSAL(t *testing.T) {
	_, err := NewMSAL("client", &MSALCache{})
	assert.NoError(t, err)
}

// fakeMSAL records silent calls.
type fakeMSAL struct {
	accounts    []public.Account
	accountsErr error
	silentErr   error
	tenants     []string
}

func (f *fakeMSAL) Accounts(context.Context) ([]public.Account, error) {
	return f.accounts, f.accountsErr
}

func (f *fakeMSAL) Silent(_ context.Context, a public.Account, tenant string) (public.AuthResult, error) {
	f.tenants = append(f.tenants, tenant)
	return public.AuthResult{AccessToken: "at:" + a.HomeAccountID + ":" + tenant}, f.silentErr
}

func (f *fakeMSAL) Interactive(context.Context, string, func(string) error) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}

func (f *fakeMSAL) RemoveAccount(context.Context, public.Account) error { return nil }

func (f *fakeMSAL) DeviceCode(context.Context, string, func(string)) (public.AuthResult, error) {
	return public.AuthResult{}, errors.New("not used")
}

func TestEntraTokens(t *testing.T) {
	s, _, _ := newStore(t)
	cfg := s.Config
	cfg.Set(host, HomeAccountIDKey, "uid."+tid)
	reg := &httpmock.Registry{}
	reg.Register(httpmock.REST("HEAD", "contoso/"), headResponse(401, map[string][]string{"X-VSS-ResourceTenant": {tid}}))
	fake := &fakeMSAL{accounts: []public.Account{{HomeAccountID: "other"}, {HomeAccountID: "uid." + tid}}}
	e := &EntraTokens{MSAL: fake, Config: cfg, Host: host, Org: "contoso",
		OrgURL: "https://dev.azure.com/contoso/", HTTPClient: &http.Client{Transport: reg}}

	// The token provider plugs into the shared api transport as a Bearer token.
	apiReg := &httpmock.Registry{}
	apiReg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), httpmock.StringResponse("{}"))
	apiReg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), httpmock.StringResponse("{}"))
	hc := api.NewHTTPClient(api.ClientOptions{AppVersion: "1", Transport: apiReg, Token: func(r *http.Request) (api.Token, error) {
		v, err := e.Token(r.Context())
		return api.Token{Value: v, Bearer: true}, err
	}})
	for range 2 {
		resp, err := hc.Get("https://dev.azure.com/contoso/_apis/connectionData")
		require.NoError(t, err)
		resp.Body.Close()
	}
	assert.Equal(t, "Bearer at:uid."+tid+":"+tid, apiReg.Requests[0].Header.Get("Authorization"))
	assert.Len(t, reg.Requests, 1, "tenant discovered once, then cached per org")
	assert.Equal(t, tid, reload(t).Get(host, TenantKey("contoso")), "tenant cache persisted")
	assert.Equal(t, []string{tid, tid}, fake.tenants, "silent calls use the org tenant")
}

func TestEntraTokensErrors(t *testing.T) {
	boom := errors.New("AADSTS700082: refresh token expired")
	tests := []struct {
		name     string
		account  string
		fake     *fakeMSAL
		wantAuth bool
		want     string
	}{
		{name: "not logged in", fake: &fakeMSAL{}, wantAuth: true, want: "not logged in"},
		{name: "silent error", account: "a", fake: &fakeMSAL{accounts: []public.Account{{HomeAccountID: "a"}}, silentErr: boom}, wantAuth: true, want: "AADSTS700082"},
		{name: "account gone", account: "a", fake: &fakeMSAL{}, wantAuth: true, want: "no longer in the token cache"},
		{name: "cache error", account: "a", fake: &fakeMSAL{accountsErr: boom}, wantAuth: true, want: "token cache"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newStore(t)
			if tt.account != "" {
				s.Config.Set(host, HomeAccountIDKey, tt.account)
			}
			s.Config.Set(host, TenantKey("contoso"), tid)
			_, err := (&EntraTokens{MSAL: tt.fake, Config: s.Config, Host: host, Org: "contoso"}).Token(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			var authErr *cmdutil.AuthError
			assert.Equal(t, tt.wantAuth, errors.As(err, &authErr), "AuthError exits 4 with \"run: ado auth login\"")
		})
	}
}

func TestEntraTokensDiscoveryError(t *testing.T) {
	s, _, _ := newStore(t)
	s.Config.Set(host, HomeAccountIDKey, "a")
	reg := &httpmock.Registry{}
	reg.Register(httpmock.REST("HEAD", "contoso/"), headResponse(401, map[string][]string{"X-VSS-ResourceTenant": {zeroTID}}))
	_, err := (&EntraTokens{MSAL: &fakeMSAL{}, Config: s.Config, Host: host, Org: "contoso",
		OrgURL: "https://dev.azure.com/contoso/", HTTPClient: &http.Client{Transport: reg}}).Token(context.Background())
	assert.ErrorContains(t, err, "personal Microsoft accounts")
}
