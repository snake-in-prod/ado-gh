package shared

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
)

func TestOrgURL(t *testing.T) {
	tests := []struct{ host, org, want string }{
		{"dev.azure.com", "contoso", "https://dev.azure.com/contoso/"},
		{"ssh.dev.azure.com", "contoso", "https://dev.azure.com/contoso/"},
		{"vs-ssh.visualstudio.com", "contoso", "https://dev.azure.com/contoso/"},
		{"contoso.visualstudio.com", "contoso", "https://contoso.visualstudio.com/"},
		{"dev.azure.com", "a b", "https://dev.azure.com/a%20b/"},
	}
	for _, tt := range tests {
		if got := OrgURL(tt.host, tt.org); got != tt.want {
			t.Errorf("OrgURL(%q, %q) = %q; want %q", tt.host, tt.org, got, tt.want)
		}
	}
}

func user(id string) map[string]any {
	return map[string]any{"authenticatedUser": map[string]any{"id": id, "providerDisplayName": "Mona"}}
}

func TestValidate(t *testing.T) {
	const path = "contoso/_apis/connectionData"
	tests := []struct {
		name     string
		resp     httpmock.Responder
		want     User
		wantErr  string
		wantAuth bool
	}{
		{name: "ok", resp: httpmock.JSONResponse(user("1234")), want: User{ID: "1234", DisplayName: "Mona"}},
		{name: "anonymous", resp: httpmock.JSONResponse(user(anonymousID)), wantErr: "rejected", wantAuth: true},
		{name: "no user", resp: httpmock.JSONResponse(map[string]any{}), wantErr: "rejected", wantAuth: true},
		{name: "401", resp: httpmock.StatusStringResponse(401, ""), wantErr: "rejected", wantAuth: true},
		{name: "203 sign-in page", resp: httpmock.StatusStringResponse(203, "<html>sign in</html>"), wantErr: "rejected", wantAuth: true},
		{name: "404", resp: httpmock.StatusStringResponse(404, ""), wantErr: `organization "contoso" not found`},
		{name: "500", resp: httpmock.StatusStringResponse(500, ""), wantErr: "HTTP 500"},
		{name: "not json", resp: httpmock.StringResponse("<html>"), wantErr: "unexpected response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &httpmock.Registry{}
			reg.Register(httpmock.REST("GET", path), tt.resp)
			hc := api.NewHTTPClient(api.ClientOptions{Transport: reg})
			got, err := Validate(context.Background(), hc, "dev.azure.com", "contoso")
			reg.Verify(t)
			if tt.wantErr == "" {
				if err != nil || got != tt.want {
					t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v; want %q", err, tt.wantErr)
			}
			var authErr *cmdutil.AuthError
			if got := errors.As(err, &authErr); got != tt.wantAuth {
				t.Errorf("AuthError = %v; want %v", got, tt.wantAuth)
			}
			if len(reg.Requests) != 1 || reg.Requests[0].URL.Host != "dev.azure.com" {
				t.Errorf("want one request to dev.azure.com, got %v", reg.Requests)
			}
		})
	}
}

func TestValidateTransportError(t *testing.T) {
	reg := &httpmock.Registry{} // no stubs: every request fails
	_, err := Validate(context.Background(), &http.Client{Transport: reg}, "dev.azure.com", "contoso")
	var authErr *cmdutil.AuthError
	if err == nil || errors.As(err, &authErr) {
		t.Fatalf("err = %v; want a non-auth error", err)
	}
	//nolint:staticcheck // a nil ctx is the error case under test
	if _, err := Validate(nil, http.DefaultClient, "dev.azure.com", "x"); err == nil {
		t.Error("nil ctx: want error")
	}
}
