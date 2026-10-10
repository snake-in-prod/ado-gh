package shared

import (
	"bytes"
	"context"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestIsADOHost(t *testing.T) {
	for host, want := range map[string]bool{
		"dev.azure.com":                  true,
		"vssps.dev.azure.com":            true,
		"contoso.visualstudio.com":       true,
		"Contoso.VisualStudio.com":       true,
		"vs-ssh.visualstudio.com":        false,
		"ssh.dev.azure.com":              false,
		"evil.com":                       false,
		"dev.azure.com.evil.com":         false,
		"evildev.azure.com":              false,
		"visualstudio.com":               false,
		"blob.core.windows.net":          false,
		"contoso.vssps.visualstudio.com": true,
	} {
		assert.Equal(t, want, IsADOHost(host), host)
	}
}

func TestHTTPClient(t *testing.T) {
	reg := &httpmock.Registry{}
	f, _ := newFactory(t, reg)
	f.AppVersion = "1.2.3"
	var log bytes.Buffer
	f.IOStreams.ErrOut = &log
	f.Debug = true
	require.NoError(t, keyring.Set("ado:dev.azure.com/contoso", "", "pat"))
	reg.Register(httpmock.MatchAny, httpmock.StringResponse("{}"))
	reg.Register(httpmock.MatchAny, httpmock.StringResponse("{}"))

	hc, err := HTTPClient(context.Background(), f, "dev.azure.com", "contoso")
	require.NoError(t, err)
	for _, u := range []string{"https://dev.azure.com/contoso/_apis/projects", "https://blob.core.windows.net/x"} {
		resp, err := hc.Get(u)
		require.NoError(t, err)
		resp.Body.Close()
	}
	require.Len(t, reg.Requests, 2)
	assert.Equal(t, "Basic OnBhdA==", reg.Requests[0].Header.Get("Authorization"))
	assert.Equal(t, "ado-cli/1.2.3", reg.Requests[0].Header.Get("User-Agent"))
	assert.Empty(t, reg.Requests[1].Header.Get("Authorization"), "never sent outside Azure DevOps")
	assert.Contains(t, log.String(), "Authorization: [REDACTED]")
	assert.NotContains(t, log.String(), "OnBhdA")
}

func TestHTTPClientNotLoggedIn(t *testing.T) {
	f, _ := newFactory(t, &httpmock.Registry{})
	_, err := HTTPClient(context.Background(), f, "dev.azure.com", "contoso")
	var authErr *cmdutil.AuthError
	assert.ErrorAs(t, err, &authErr)
}
