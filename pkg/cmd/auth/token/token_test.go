package token

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func run(t *testing.T, repo adocontext.Repo, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, out, errOut := iostreams.Test()
	reg := &httpmock.Registry{}
	f := &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			if repo.Org == "" {
				return repo, errors.New("no repo")
			}
			return repo, nil
		},
	}
	cmd := NewCmdToken(f)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true // as under the root command
	cmd.SetOut(&bytes.Buffer{})
	err = cmd.Execute()
	assert.Empty(t, reg.Requests, "printing a PAT sends no request")
	return out.String(), errOut.String(), err
}

func clearEnv(t *testing.T) {
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
}

func TestToken(t *testing.T) {
	clearEnv(t)
	require.NoError(t, keyring.Set("ado:contoso.visualstudio.com/contoso", "", "raw-pat"))

	out, errOut, err := run(t, adocontext.Repo{Host: "contoso.visualstudio.com", Org: "contoso"})
	require.NoError(t, err)
	assert.Equal(t, "raw-pat\n", out, "only the raw token")
	assert.Empty(t, errOut)

	t.Setenv("ADO_TOKEN", "env-pat")
	out, _, err = run(t, adocontext.Repo{}, "--org", "contoso")
	require.NoError(t, err)
	assert.Equal(t, "env-pat\n", out)
}

func TestTokenErrors(t *testing.T) {
	clearEnv(t)
	_, _, err := run(t, adocontext.Repo{})
	assert.EqualError(t, err, shared.NoOrg)

	out, _, err := run(t, adocontext.Repo{}, "--org", "contoso")
	var authErr *cmdutil.AuthError
	require.ErrorAs(t, err, &authErr, "not logged in exits 4")
	assert.Empty(t, out)
}
