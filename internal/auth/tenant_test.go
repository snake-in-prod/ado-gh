package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tid     = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	zeroTID = "00000000-0000-0000-0000-000000000000"
)

func headResponse(status int, headers map[string][]string) httpmock.Responder {
	return func(req *http.Request) (*http.Response, error) {
		h := http.Header{}
		for k, vs := range headers {
			for _, v := range vs {
				h.Add(k, v)
			}
		}
		return &http.Response{StatusCode: status, Header: h, Body: http.NoBody, Request: req}, nil
	}
}

func TestDiscoverTenant(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		headers map[string][]string
		want    string
		wantErr string
	}{
		{
			name: "WWW-Authenticate authorization_uri", status: 401,
			headers: map[string][]string{
				"WWW-Authenticate":     {`Bearer authorization_uri=https://login.microsoftonline.com/` + tid + `, Basic realm="https://tfsprodcus.app.visualstudio.com/"`},
				"X-VSS-ResourceTenant": {"11111111-1111-1111-1111-111111111111"},
			},
			want: tid,
		},
		{
			name: "X-VSS-ResourceTenant first non-zero", status: 401,
			headers: map[string][]string{
				"WWW-Authenticate":     {`Basic realm="x"`},
				"X-VSS-ResourceTenant": {zeroTID + ", " + tid},
			},
			want: tid,
		},
		{
			name: "all-zeros tenant is an MSA-backed org", status: 401,
			headers: map[string][]string{"X-VSS-ResourceTenant": {zeroTID}},
			wantErr: "personal Microsoft accounts",
		},
		{name: "404", status: 404, wantErr: `organization "contoso" not found`},
		{name: "unknown tenant", status: 200, want: "organizations"},
		{
			name: "unparsable authorization_uri", status: 401,
			headers: map[string][]string{"WWW-Authenticate": {"Bearer authorization_uri=%zz"}},
			want:    "organizations",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &httpmock.Registry{}
			reg.Register(httpmock.REST("HEAD", "contoso/"), headResponse(tt.status, tt.headers))

			got, err := DiscoverTenant(context.Background(), &http.Client{Transport: reg}, "https://dev.azure.com/contoso/", "contoso")
			reg.Verify(t)
			require.Len(t, reg.Requests, 1)
			assert.Empty(t, reg.Requests[0].Header.Get("Authorization"), "discovery is unauthenticated")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				if strings.Contains(tt.wantErr, "Microsoft accounts") {
					assert.Contains(t, err.Error(), "--with-token", "MSA error points to a PAT")
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDiscoverTenantNetworkError(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(httpmock.MatchAny, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	_, err := DiscoverTenant(context.Background(), &http.Client{Transport: reg}, "https://dev.azure.com/contoso/", "contoso")
	assert.ErrorContains(t, err, "offline")
}
