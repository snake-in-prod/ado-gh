package browse

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// runBrowseCmd runs `ado browse args` with -R semantics (no git), no credentials (--settings
// falls back offline) and a recording browser.
func runBrowseCmd(t *testing.T, tty bool, args ...string) (*browsertest.Browser, string, string, error) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	f := browseURLFactory(t, &gittest.Runner{}, true)
	f.Config = func() (cmdutil.Config, error) { return config.Load() }
	f.HttpClient = func() (*http.Client, error) { return &http.Client{Transport: &httpmock.Registry{}}, nil }
	f.IOStreams.SetStdoutTTY(tty)
	out := f.IOStreams.Out.(*bytes.Buffer)
	errOut := f.IOStreams.ErrOut.(*bytes.Buffer)
	b := &browsertest.Browser{}
	f.Browser = b
	cmd := NewCmdBrowse(f)
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	return b, out.String(), errOut.String(), err
}

func TestBrowseNoBrowserPrintsURL(t *testing.T) {
	for _, tty := range []bool{true, false} {
		b, out, errOut, err := runBrowseCmd(t, tty, "-n", "!12")
		require.NoError(t, err)
		assert.Equal(t, browseURLRepo+"/pullrequest/12\n", out)
		assert.Empty(t, errOut)
		assert.Empty(t, b.URLs)
	}
}

func TestBrowseOpensBrowserTTY(t *testing.T) {
	b, out, errOut, err := runBrowseCmd(t, true, "--wiki")
	require.NoError(t, err)
	assert.Equal(t, []string{browseURLProj + "/_wiki"}, b.URLs)
	assert.Empty(t, out)
	assert.Equal(t, "Opening "+browseURLProj+"/_wiki in your browser.\n", errOut)
}

func TestBrowseOpensBrowserPiped(t *testing.T) {
	b, out, errOut, err := runBrowseCmd(t, false, "-b", "dev", "a.go:42")
	require.NoError(t, err)
	assert.Equal(t, []string{browseURLRepo + "?path=%2Fa.go&version=GBdev" + browseURLLine + "&_a=contents"}, b.URLs)
	assert.Empty(t, out)
	assert.Empty(t, errOut)
}

func TestBrowseFlags(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, browseURLRepo},
		{[]string{"#456"}, browseURLProj + "/_workitems/edit/456"},
		{[]string{"-c=abc1234"}, browseURLRepo + "?version=GCabc1234"},
		{[]string{"-w"}, browseURLProj + "/_wiki"},
		{[]string{"--pipelines"}, browseURLProj + "/_build"},
		{[]string{"--boards"}, browseURLProj + "/_boards"},
		{[]string{"--prs"}, browseURLRepo + "/pullrequests"},
		{[]string{"--branches"}, browseURLRepo + "/branches"},
		{[]string{"-s"}, browseURLProj + "/_settings/repositories"},
	}
	for _, tt := range tests {
		_, out, _, err := runBrowseCmd(t, false, append([]string{"-n"}, tt.args...)...)
		require.NoError(t, err, tt.args)
		assert.Equal(t, tt.want+"\n", out, tt.args)
	}
}

func TestBrowseCommitWithoutValueIsHEAD(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	run := &gittest.Runner{Replies: map[string]string{
		"rev-parse --verify --quiet HEAD^{commit}": browseURLSHA + "\n",
		"rev-parse --show-prefix":                  "\n",
	}}
	f := browseURLFactory(t, run, false)
	cmd := NewCmdBrowse(f)
	cmd.SetArgs([]string{"-n", "-c", "a.go"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, browseURLRepo+"?path=%2Fa.go&version=GC"+browseURLSHA+"&_a=contents\n", f.IOStreams.Out.(*bytes.Buffer).String())
}

func TestBrowseErrors(t *testing.T) {
	_, _, _, err := runBrowseCmd(t, false, "-n", "--wiki", "--boards")
	var flagErr *cmdutil.FlagError
	assert.True(t, errors.As(err, &flagErr))

	_, _, _, err = runBrowseCmd(t, false, "a", "b")
	assert.Error(t, err)
}

func TestBrowseLaunchError(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	f := browseURLFactory(t, &gittest.Runner{}, true)
	f.Browser = &browsertest.Browser{Err: errors.New("no browser")}
	cmd := NewCmdBrowse(f)
	cmd.SetArgs(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	assert.EqualError(t, cmd.Execute(), "no browser")
}
