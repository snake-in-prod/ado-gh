package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
)

func staticToken(tok Token) func(*http.Request) (Token, error) {
	return func(*http.Request) (Token, error) { return tok, nil }
}

// send issues a GET through a client built from opts on top of reg and returns the request reg saw.
func send(t *testing.T, opts ClientOptions, reg *httpmock.Registry) (*http.Request, error) {
	t.Helper()
	opts.Transport = reg
	req, err := http.NewRequest(http.MethodGet, "https://dev.azure.com/org/_apis/projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "something-else")
	resp, err := NewHTTPClient(opts).Do(req)
	if err == nil {
		resp.Body.Close()
	}
	if req.Header.Get("User-Agent") != "something-else" || req.Header.Get("Authorization") != "" {
		t.Error("caller's request was mutated")
	}
	if len(reg.Requests) == 0 {
		return nil, err
	}
	return reg.Requests[0], err
}

func TestTransportHeaders(t *testing.T) {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(":my-pat"))
	tests := []struct {
		name     string
		token    func(*http.Request) (Token, error)
		wantAuth string
	}{
		{"PAT uses Basic", staticToken(Token{Value: "my-pat"}), basic},
		{"Entra uses Bearer", staticToken(Token{Value: "jwt", Bearer: true}), "Bearer jwt"},
		{"empty token sends none", staticToken(Token{}), ""},
		{"no provider sends none", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &httpmock.Registry{}
			reg.Register(httpmock.MatchAny, httpmock.StringResponse("{}"))
			got, err := send(t, ClientOptions{AppVersion: "1.2.3", Token: tt.token}, reg)
			if err != nil {
				t.Fatal(err)
			}
			if a := got.Header.Get("Authorization"); a != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", a, tt.wantAuth)
			}
			if ua := got.Header.Get("User-Agent"); ua != "ado-cli/1.2.3" {
				t.Errorf("User-Agent = %q", ua)
			}
			if h := got.Header.Get("X-TFS-FedAuthRedirect"); h != "Suppress" {
				t.Errorf("X-TFS-FedAuthRedirect = %q", h)
			}
			reg.Verify(t)
		})
	}
}

func TestTransportTokenError(t *testing.T) {
	reg := &httpmock.Registry{}
	boom := errors.New("keychain locked")
	_, err := send(t, ClientOptions{Token: func(*http.Request) (Token, error) { return Token{}, boom }}, reg)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
	if len(reg.Requests) != 0 {
		t.Error("request sent despite token error")
	}
}

func TestTransportAuthErrors(t *testing.T) {
	tests := []struct {
		status   int
		wantAuth bool
	}{
		{http.StatusOK, false},
		{http.StatusNonAuthoritativeInfo, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, false},
		{http.StatusNotFound, false},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			reg := &httpmock.Registry{}
			reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(tt.status, "<html>sign in</html>"))
			_, err := send(t, ClientOptions{}, reg)
			var authErr *cmdutil.AuthError
			if got := errors.As(err, &authErr); got != tt.wantAuth {
				t.Fatalf("AuthError = %v (err %v), want %v", got, err, tt.wantAuth)
			}
			if tt.wantAuth {
				if code := cmdutil.HandleError(&bytes.Buffer{}, err, ""); code != cmdutil.ExitAuth {
					t.Errorf("exit code = %d, want %d", code, cmdutil.ExitAuth)
				}
				if !strings.Contains(err.Error(), "dev.azure.com") {
					t.Errorf("error %q does not name the host", err)
				}
			} else if err != nil {
				t.Errorf("unexpected err %v", err)
			}
		})
	}
}

func TestTransportDebugLog(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(httpmock.MatchAny, httpmock.StringResponse("{}"))
	var log bytes.Buffer
	if _, err := send(t, ClientOptions{AppVersion: "1.2.3", Token: staticToken(Token{Value: "secret-pat"}), Log: &log}, reg); err != nil {
		t.Fatal(err)
	}
	out := log.String()
	for _, want := range []string{
		"> GET https://dev.azure.com/org/_apis/projects\n",
		"> Authorization: [REDACTED]\n",
		"> User-Agent: ado-cli/1.2.3\n",
		"> X-Tfs-Fedauthredirect: Suppress\n",
		"< HTTP 200 (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-pat") || strings.Contains(out, base64.StdEncoding.EncodeToString([]byte(":secret-pat"))) {
		t.Errorf("log leaks the token:\n%s", out)
	}
}

func TestTransportDebugLogTransportError(t *testing.T) {
	reg := &httpmock.Registry{} // no stubs: the round trip fails
	var log bytes.Buffer
	if _, err := send(t, ClientOptions{Log: &log}, reg); err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(log.String(), "< error: no registered HTTP stubs matched") {
		t.Errorf("log = %q", log.String())
	}
}

func TestTransportDebugLogOmitsQuery(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(httpmock.MatchAny, httpmock.StringResponse("{}"))
	reg.Register(httpmock.MatchAny, httpmock.StringResponse("{}"))
	var log bytes.Buffer
	hc := NewHTTPClient(ClientOptions{Log: &log, Transport: reg})
	for _, u := range []string{
		"https://dev.azure.com/org/_apis/build/builds?api-version=7.1&continuationToken=abc",
		"https://user:pw@x.blob.core.windows.net/c/drop.zip?sv=2020&sig=SECRETSIG",
	} {
		resp, err := hc.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	out := log.String()
	for _, want := range []string{
		"> GET https://dev.azure.com/org/_apis/build/builds\n",
		"> GET https://user:xxxxx@x.blob.core.windows.net/c/drop.zip\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"?", "sig", "continuationToken", ":pw@"} {
		if strings.Contains(out, leak) {
			t.Errorf("log contains %q:\n%s", leak, out)
		}
	}
}

func TestNewHTTPClientClonesDefaultTransport(t *testing.T) {
	c := NewHTTPClient(ClientOptions{})
	tr, ok := c.Transport.(*transport)
	if !ok {
		t.Fatalf("Transport = %T", c.Transport)
	}
	if _, ok := tr.base.(*http.Transport); !ok || tr.base == http.DefaultTransport {
		t.Errorf("base = %T, want a clone of http.DefaultTransport", tr.base)
	}
}

func TestSDKClientUsesSharedTransport(t *testing.T) {
	reg := &httpmock.Registry{}
	// A base URL of its own: the SDK caches OPTIONS _apis results per base URL process-wide.
	reg.StubLocations("sdkorg", httpmock.Location{
		ID:            "225f7195-f9c7-4d14-ab28-a83f7ff77e1f",
		Area:          "git",
		ResourceName:  "repositories",
		RouteTemplate: "{project}/_apis/git/repositories/{repositoryId}",
	})
	reg.Register(httpmock.REST(http.MethodGet, "sdkorg/proj/_apis/git/repositories/repo"),
		httpmock.JSONResponse(map[string]any{"id": "8b6f0a8e-0000-0000-0000-000000000001", "name": "repo"}))
	var log bytes.Buffer
	hc := NewHTTPClient(ClientOptions{
		AppVersion: "1.2.3",
		Token:      staticToken(Token{Value: "jwt", Bearer: true}),
		Log:        &log,
		Transport:  reg,
	})

	client := &git.ClientImpl{Client: *NewSDKClient(hc, "https://dev.azure.com/sdkorg")}
	proj, name := "proj", "repo"
	repo, err := client.GetRepository(context.Background(), git.GetRepositoryArgs{Project: &proj, RepositoryId: &name})
	if err != nil {
		t.Fatal(err)
	}
	if repo.Name == nil || *repo.Name != "repo" {
		t.Errorf("repo = %+v", repo)
	}
	reg.Verify(t)
	for _, req := range reg.Requests {
		if got := req.Header.Values("Authorization"); len(got) != 1 || got[0] != "Bearer jwt" {
			t.Errorf("%s %s Authorization = %q", req.Method, req.URL, got)
		}
		if got := req.Header.Values("User-Agent"); len(got) != 1 || got[0] != "ado-cli/1.2.3" {
			t.Errorf("%s %s User-Agent = %q", req.Method, req.URL, got)
		}
	}
	if !strings.Contains(log.String(), "> GET https://dev.azure.com/sdkorg/proj/_apis/git/repositories/repo") {
		t.Errorf("SDK request not logged:\n%s", log.String())
	}
}

func TestSDKClientAuthError(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(httpmock.REST(http.MethodOptions, "sdkorg401/_apis"), httpmock.StatusStringResponse(http.StatusUnauthorized, ""))
	hc := NewHTTPClient(ClientOptions{Transport: reg})
	client := &git.ClientImpl{Client: *NewSDKClient(hc, "https://dev.azure.com/sdkorg401")}
	id := "repo"
	_, err := client.GetRepository(context.Background(), git.GetRepositoryArgs{RepositoryId: &id})
	var authErr *cmdutil.AuthError
	if !errors.As(err, &authErr) {
		t.Errorf("err = %v (%T), want AuthError", err, err)
	}
}

// Regression: the authenticated client wraps the Factory's client, which is itself an
// ado transport without a token. The inner transport must not strip Authorization.
func TestNewHTTPClientOverAdoTransportKeepsAuth(t *testing.T) {
	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var log bytes.Buffer
	plain := NewHTTPClient(ClientOptions{AppVersion: "1", Log: &log})
	authed := NewHTTPClient(ClientOptions{
		AppVersion: "1",
		Log:        &log,
		Transport:  plain.Transport,
		Token:      func(*http.Request) (Token, error) { return Token{Value: "jwt", Bearer: true}, nil },
	})
	resp, err := authed.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(gotAuth) != 1 || gotAuth[0] != "Bearer jwt" {
		t.Errorf("server saw Authorization %q, want one request with Bearer jwt", gotAuth)
	}
	if n := strings.Count(log.String(), "> GET"); n != 1 {
		t.Errorf("request logged %d times, want 1:\n%s", n, log.String())
	}
}
