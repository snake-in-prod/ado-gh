package setdefault

import (
	"bytes"
	"errors"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	setDefaultRemotes = "origin\thttps://dev.azure.com/org/proj/_git/web (fetch)\n" +
		"origin\thttps://dev.azure.com/org/proj/_git/web (push)\n" +
		"upstream\tgit@ssh.dev.azure.com:v3/org/proj/api (fetch)\n" +
		"upstream\tgit@ssh.dev.azure.com:v3/org/proj/api (push)\n" +
		"gh\thttps://github.com/a/b (fetch)\n" +
		"web\thttps://dev.azure.com/org/proj/_git/other (fetch)\n"
	setDefaultRegexp = `config --get-regexp ^remote\..*\.ado-resolved$`
	setDefaultSetUp  = "config remote.upstream.ado-resolved base"
)

type setDefaultEnv struct {
	git    *gittest.Runner
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	f      *cmdutil.Factory
}

// newSetDefaultEnv returns an env whose git remotes are remoteV and whose ado-resolved keys are
// resolved (the `config --get-regexp` output; "" = none set).
func newSetDefaultEnv(t *testing.T, tty bool, remoteV, resolved string) *setDefaultEnv {
	t.Helper()
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_PROJECT", "")
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	g := &gittest.Runner{Replies: map[string]string{"remote -v": remoteV}, Fails: map[string]error{}}
	if resolved == "" {
		g.Fails[setDefaultRegexp] = &git.Error{ExitCode: 1}
	} else {
		g.Replies[setDefaultRegexp] = resolved
	}
	base := adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "proj", Name: "web"}
	return &setDefaultEnv{git: g, in: in, out: out, errOut: errOut, f: &cmdutil.Factory{
		IOStreams: ios,
		Config:    func() (cmdutil.Config, error) { return nil, errors.New("no config") },
		GitClient: &git.Client{Run: g.Run},
		BaseRepo:  func() (adocontext.Repo, error) { return base, nil },
		Prompter:  &cmdutil.Prompter{IO: ios, Accessible: true},
	}}
}

func (e *setDefaultEnv) run(args ...string) error {
	cmd := NewCmdSetDefault(e.f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // as under the root command
	return cmd.Execute()
}

func TestSetDefaultWithArgument(t *testing.T) {
	tests := []struct {
		name, arg, resolved string
		want                []string
	}{
		{"remote name", "upstream", "", []string{setDefaultSetUp}},
		{"remote name before repo name", "web", "", []string{"config remote.web.ado-resolved base"}},
		{"bare repo name", "api", "", []string{setDefaultSetUp}},
		{"project/repo", "PROJ/API", "", []string{setDefaultSetUp}},
		{"org/project/repo", "org/proj/api", "", []string{setDefaultSetUp}},
		{"URL", "https://dev.azure.com/org/proj/_git/api", "", []string{setDefaultSetUp}},
		{"unsets every resolved remote first", "upstream",
			"remote.origin.ado-resolved base\nremote.web.ado-resolved base\n",
			[]string{"config --unset-all remote.origin.ado-resolved", "config --unset-all remote.web.ado-resolved", setDefaultSetUp}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSetDefaultEnv(t, false, setDefaultRemotes, tt.resolved)
			require.NoError(t, e.run(tt.arg))
			assert.Equal(t, tt.want, e.git.Mutations())
			assert.Empty(t, e.out.String())
		})
	}
}

func TestSetDefaultArgumentErrors(t *testing.T) {
	tests := []struct{ name, arg, wantErr string }{
		{"no matching remote", "nope", "org/proj/nope does not match any Azure DevOps remote of this repository"},
		{"non-ADO remote", "gh", `remote "gh" is not an Azure DevOps remote`},
		{"bad URL", "https://github.com/a/b", "invalid repository URL: expected https://dev.azure.com/ORG/PROJECT/_git/REPO"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSetDefaultEnv(t, true, setDefaultRemotes, "")
			assert.EqualError(t, e.run(tt.arg), tt.wantErr)
			assert.Empty(t, e.git.Mutations())
		})
	}
}

func TestSetDefaultTTYMessage(t *testing.T) {
	e := newSetDefaultEnv(t, true, setDefaultRemotes, "")
	require.NoError(t, e.run("upstream"))
	assert.Equal(t, "✓ Set org/proj/api as the default repository for the current directory\n", e.errOut.String())
}

func TestSetDefaultAutoPicksSingleRemote(t *testing.T) {
	remotes := "origin\thttps://dev.azure.com/org/proj/_git/web (fetch)\n" +
		"mirror\thttps://org@dev.azure.com/Org/Proj/_git/Web (fetch)\n" +
		"gh\thttps://github.com/a/b (fetch)\n"
	e := newSetDefaultEnv(t, true, remotes, "")
	require.NoError(t, e.run())
	assert.Equal(t, []string{"config remote.origin.ado-resolved base"}, e.git.Mutations())
	assert.Contains(t, e.errOut.String(), "Found only one Azure DevOps repository: org/proj/web\n")
}

func TestSetDefaultSelectPrompt(t *testing.T) {
	e := newSetDefaultEnv(t, true, setDefaultRemotes, "remote.origin.ado-resolved base\n")
	e.in.WriteString("2\n")
	require.NoError(t, e.run())
	assert.Equal(t, []string{"config --unset-all remote.origin.ado-resolved", setDefaultSetUp}, e.git.Mutations())
	assert.Contains(t, e.out.String(), "Which repository should be the default?")
	assert.Contains(t, e.out.String(), "3. org/proj/other")
}

func TestSetDefaultNoADORemotes(t *testing.T) {
	e := newSetDefaultEnv(t, true, "gh\thttps://github.com/a/b (fetch)\n", "")
	assert.EqualError(t, e.run(), "none of the git remotes point to an Azure DevOps repository")
	assert.Empty(t, e.git.Mutations())
}

func TestSetDefaultFlagErrors(t *testing.T) {
	tests := []struct {
		name     string
		override string
		args     []string
		wantErr  string
	}{
		{"non-TTY without argument", "", nil, "repository required when not running interactively"},
		{"-R rejected", "org/proj/web", []string{"web"}, "-R/--repo is not supported; pass the repository as an argument"},
		{"argument with --view", "", []string{"web", "--view"}, "cannot combine <repo> with --view or --unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSetDefaultEnv(t, false, setDefaultRemotes, "")
			e.f.RepoOverride = tt.override
			err := e.run(tt.args...)
			var flagErr *cmdutil.FlagError
			require.ErrorAs(t, err, &flagErr)
			assert.EqualError(t, err, tt.wantErr)
			assert.Empty(t, e.git.Calls)
		})
	}
	e := newSetDefaultEnv(t, false, setDefaultRemotes, "")
	assert.Error(t, e.run("--view", "--unset"))
}

func TestSetDefaultView(t *testing.T) {
	e := newSetDefaultEnv(t, false, setDefaultRemotes, "remote.gh.ado-resolved base\nremote.upstream.ado-resolved base\n")
	require.NoError(t, e.run("--view"))
	assert.Equal(t, "org/proj/api\n", e.out.String())
	assert.Empty(t, e.git.Mutations())

	e = newSetDefaultEnv(t, false, setDefaultRemotes, "")
	require.NoError(t, e.run("-v"))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no default repository has been set; use `ado repo set-default` to select one\n", e.errOut.String())
}

func TestSetDefaultUnset(t *testing.T) {
	e := newSetDefaultEnv(t, false, setDefaultRemotes, "remote.origin.ado-resolved base\nremote.gh.ado-resolved base\n")
	require.NoError(t, e.run("--unset"))
	assert.Equal(t, []string{"config --unset-all remote.origin.ado-resolved", "config --unset-all remote.gh.ado-resolved"}, e.git.Mutations())
	assert.Equal(t, "✓ Unset org/proj/web as the default repository for the current directory\n", e.errOut.String())

	e = newSetDefaultEnv(t, false, setDefaultRemotes, "")
	require.NoError(t, e.run("-u"))
	assert.Empty(t, e.git.Mutations())
	assert.Equal(t, "no default repository has been set\n", e.errOut.String())
}

func TestSetDefaultOutsideGitRepo(t *testing.T) {
	e := newSetDefaultEnv(t, false, "", "")
	e.git.Fails["remote -v"] = &git.Error{ExitCode: 128, Stderr: "fatal: not a git repository (or any of the parent directories): .git"}
	assert.EqualError(t, e.run("web"), "must be run from inside a git repository")

	e = newSetDefaultEnv(t, false, setDefaultRemotes, "")
	e.git.Fails[setDefaultSetUp] = &git.Error{ExitCode: 4, Stderr: "error: could not lock config file"}
	assert.EqualError(t, e.run("upstream"), "error: could not lock config file")
}
