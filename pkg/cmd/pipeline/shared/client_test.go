package shared

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// newClientTestFactory returns a Factory signed in with the PAT "pat" through ADO_TOKEN, sending
// requests to reg and logging them (--debug) to the returned buffer.
func newClientTestFactory(t *testing.T, reg *httpmock.Registry) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, _, _ := iostreams.Test()
	var log bytes.Buffer
	ios.ErrOut = &log
	return &cmdutil.Factory{
		AppVersion: "1.0.0",
		Debug:      true,
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
	}, &log
}

func TestNewClientDownloadRedirectToBlob(t *testing.T) {
	artprod := "https://artprodcus3.artifacts.visualstudio.com/A1/proj/_apis/artifact/abc/content?format=zip"
	blob := "https://x.blob.core.windows.net/c/webapp.zip?sv=2020-01-01&se=2026-10-12&sig=SECRETSIG"
	reg := &httpmock.Registry{}
	reg.Register(httpmock.REST(http.MethodGet, "A1/proj/_apis/artifact/abc/content"),
		httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusFound, ""), "Location", blob))
	reg.Register(httpmock.REST(http.MethodGet, "c/webapp.zip"), httpmock.StringResponse("PK"))
	f, log := newClientTestFactory(t, reg)

	c, err := NewClient(context.Background(), f, adocontext.Repo{Host: "dev.azure.com", Org: "contoso", Project: "proj"})
	require.NoError(t, err)
	assert.Equal(t, "proj", c.Project)
	assert.Zero(t, c.HTTP.Timeout, "no Client.Timeout: large artifact downloads are canceled through the context only")
	rc, err := c.Download(context.Background(), artprod)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, "PK", string(b))

	require.Len(t, reg.Requests, 2)
	assert.Equal(t, "Basic OnBhdA==", reg.Requests[0].Header.Get("Authorization"), "artprod is an Azure DevOps host")
	assert.Equal(t, "x.blob.core.windows.net", reg.Requests[1].URL.Host)
	assert.Empty(t, reg.Requests[1].Header.Get("Authorization"), "no token to blob storage")
	assert.Contains(t, log.String(), "> GET https://x.blob.core.windows.net/c/webapp.zip\n")
	assert.NotContains(t, log.String(), "sig")
	assert.NotContains(t, log.String(), "OnBhdA")
}

func TestNewClientNotLoggedIn(t *testing.T) {
	f, _ := newClientTestFactory(t, &httpmock.Registry{})
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("ADO_ENTRA_CLIENT_ID", "")
	_, err := NewClient(context.Background(), f, adocontext.Repo{Host: "dev.azure.com", Org: "contoso", Project: "proj"})
	var authErr *cmdutil.AuthError
	assert.ErrorAs(t, err, &authErr)
}
