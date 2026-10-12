package alias

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/alias/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

type aliasCmdResult struct {
	out, errOut string
	err         error
	cfg         *config.Config
}

// runAliasCmd runs `ado alias <args>` against a small root holding pr, workitem (alias wi) and
// an extension `lint`, with the config seeded from aliases and stdin set to in.
func runAliasCmd(t *testing.T, tty bool, aliases map[string]string, in string, args ...string) aliasCmdResult {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	for k, v := range aliases {
		cfg.SetAlias(k, v)
	}
	ios, stdin, out, errOut := iostreams.Test()
	stdin.WriteString(in)
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.SetStderrTTY(tty)
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return cfg, nil }}

	root := &cobra.Command{Use: "ado", SilenceErrors: true, SilenceUsage: true}
	root.AddGroup(&cobra.Group{ID: "additional", Title: "Additional commands:"},
		&cobra.Group{ID: shared.AliasGroupID, Title: "Alias commands:"},
		&cobra.Group{ID: shared.ExtensionGroupID, Title: "Extension commands:"})
	pr := &cobra.Command{Use: "pr"}
	pr.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(pr, &cobra.Command{Use: "workitem", Aliases: []string{"wi"}},
		&cobra.Command{Use: "lint", GroupID: shared.ExtensionGroupID, Run: func(*cobra.Command, []string) {}},
		NewCmdAlias(f))
	root.SetIn(stdin)
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"alias"}, args...))
	err = root.Execute()
	return aliasCmdResult{out: out.String(), errOut: errOut.String(), err: err, cfg: cfg}
}

// savedAliases reloads the config from disk, proving the change was written.
func savedAliases(t *testing.T) map[string]string {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg.Aliases()
}

func TestAliasSet(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]string
		in       string
		args     []string
		want     map[string]string
		wantErr  string
		wantErrO string
	}{
		{name: "simple", args: []string{"prs", "pr list -a @me"}, want: map[string]string{"prs": "pr list -a @me"}, wantErrO: "Added alias prs"},
		{name: "built-in alias expansion", args: []string{"w", "wi list"}, want: map[string]string{"w": "wi list"}},
		{name: "shell flag adds !", args: []string{"--shell", "x", "ado pr list | head"}, want: map[string]string{"x": "!ado pr list | head"}},
		{name: "shell flag keeps existing !", args: []string{"-s", "x", "!echo"}, want: map[string]string{"x": "!echo"}},
		{name: "shell expansion via !", args: []string{"x", "!anything goes"}, want: map[string]string{"x": "!anything goes"}},
		{name: "stdin expansion", in: "pr list -s active\n", args: []string{"act", "-"}, want: map[string]string{"act": "pr list -s active"}},
		{name: "stdin shell expansion keeps inner newlines", in: "echo a\necho b\n", args: []string{"-s", "two", "-"}, want: map[string]string{"two": "!echo a\necho b"}},
		{name: "built-in refused", args: []string{"pr", "pr list"}, wantErr: `"pr" is already an ado command`},
		{name: "built-in cobra alias refused", args: []string{"wi", "pr list"}, wantErr: `"wi" is already an ado command`},
		{name: "alias command refused", args: []string{"alias", "pr list"}, wantErr: "already an ado command"},
		{name: "help reserved", args: []string{"help", "pr list"}, wantErr: "already an ado command"},
		{name: "nested name refused", args: []string{"pr mine", "pr list -a @me"}, wantErr: "single word"},
		{name: "empty name refused", args: []string{"", "pr list"}, wantErr: "single word"},
		{name: "dash name refused", args: []string{"--", "-x", "pr list"}, wantErr: "must not start with"},
		{name: "invalid expansion", args: []string{"x", "nope list"}, wantErr: "does not start with an ado command"},
		{name: "expansion to extension refused", args: []string{"x", "lint"}, wantErr: "does not start with an ado command"},
		{name: "existing without clobber", existing: map[string]string{"prs": "pr list"}, args: []string{"prs", "pr list -a @me"},
			wantErr: "already exists; use --clobber"},
		{name: "existing with clobber", existing: map[string]string{"prs": "pr list"}, args: []string{"--clobber", "prs", "pr list -a @me"},
			want: map[string]string{"prs": "pr list -a @me"}, wantErrO: "Changed alias prs"},
		{name: "extension shadowing warns", args: []string{"lint", "pr list"}, want: map[string]string{"lint": "pr list"},
			wantErrO: `alias "lint" shadows the extension of the same name`},
		{name: "wrong arg count", args: []string{"x"}, wantErr: "accepts 2 arg(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runAliasCmd(t, true, tt.existing, tt.in, append([]string{"set"}, tt.args...)...)
			if tt.wantErr != "" {
				assert.ErrorContains(t, r.err, tt.wantErr)
				return
			}
			require.NoError(t, r.err)
			assert.Equal(t, tt.want, savedAliases(t))
			assert.Contains(t, r.errOut, tt.wantErrO)
		})
	}
}

func TestAliasSetQuietWhenPiped(t *testing.T) {
	r := runAliasCmd(t, false, nil, "", "set", "prs", "pr list")
	require.NoError(t, r.err)
	assert.Empty(t, r.out+r.errOut)
}

var aliasListSeed = map[string]string{"prs": "pr list -a @me", "co": "!ado pr checkout \"$1\"", "w": "wi list"}

func TestAliasList(t *testing.T) {
	t.Run("table on a TTY", func(t *testing.T) {
		r := runAliasCmd(t, true, aliasListSeed, "", "list")
		require.NoError(t, r.err)
		lines := strings.Split(strings.TrimSpace(r.out), "\n")
		require.Len(t, lines, 4)
		assert.Regexp(t, `^NAME\s+EXPANSION$`, lines[0])
		assert.Regexp(t, `^co\s+!ado pr checkout "\$1"$`, lines[1])
		assert.Regexp(t, `^prs\s+pr list -a @me$`, lines[2])
		assert.Regexp(t, `^w\s+wi list$`, lines[3])
	})
	t.Run("YAML when piped round-trips into import", func(t *testing.T) {
		r := runAliasCmd(t, false, aliasListSeed, "", "ls")
		require.NoError(t, r.err)
		assert.Equal(t, "co: '!ado pr checkout \"$1\"'\nprs: pr list -a @me\nw: wi list\n", r.out)

		imp := runAliasCmd(t, false, nil, r.out, "import")
		require.NoError(t, imp.err)
		assert.Equal(t, aliasListSeed, savedAliases(t))
	})
	t.Run("json", func(t *testing.T) {
		r := runAliasCmd(t, false, map[string]string{"co": "!ado pr checkout", "prs": "pr list"}, "", "list", "--json", "name,expansion,shell")
		require.NoError(t, r.err)
		assert.JSONEq(t, `[{"name":"co","expansion":"ado pr checkout","shell":true},{"name":"prs","expansion":"pr list","shell":false}]`, r.out)
	})
	t.Run("json empty", func(t *testing.T) {
		r := runAliasCmd(t, false, nil, "", "list", "--json", "name")
		require.NoError(t, r.err)
		assert.JSONEq(t, `[]`, r.out)
	})
	t.Run("empty on a TTY", func(t *testing.T) {
		r := runAliasCmd(t, true, nil, "", "list")
		require.NoError(t, r.err)
		assert.Empty(t, r.out)
		assert.Contains(t, r.errOut, "no aliases configured")
	})
	t.Run("empty when piped", func(t *testing.T) {
		r := runAliasCmd(t, false, nil, "", "list")
		require.NoError(t, r.err)
		assert.Empty(t, r.out+r.errOut)
	})
	t.Run("multi-line expansion stays one table row", func(t *testing.T) {
		r := runAliasCmd(t, true, map[string]string{"two": "!echo a\necho b"}, "", "list")
		require.NoError(t, r.err)
		assert.Len(t, strings.Split(strings.TrimSpace(r.out), "\n"), 2)
	})
}

func TestAliasDelete(t *testing.T) {
	seed := map[string]string{"prs": "pr list", "w": "wi list"}
	tests := []struct {
		name     string
		args     []string
		want     map[string]string
		wantErr  string
		wantErrO string
	}{
		{name: "one", args: []string{"prs"}, want: map[string]string{"w": "wi list"}, wantErrO: "Deleted alias prs; was pr list"},
		{name: "all", args: []string{"--all"}, want: map[string]string{}, wantErrO: "Deleted alias w; was wi list"},
		{name: "unknown", args: []string{"nope"}, wantErr: `no such alias "nope"`},
		{name: "name and all", args: []string{"prs", "--all"}, wantErr: "either an alias name or --all"},
		{name: "neither", args: nil, wantErr: "either an alias name or --all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runAliasCmd(t, true, seed, "", append([]string{"delete"}, tt.args...)...)
			if tt.wantErr != "" {
				assert.ErrorContains(t, r.err, tt.wantErr)
				return
			}
			require.NoError(t, r.err)
			got := savedAliases(t)
			if len(tt.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
			assert.Contains(t, r.errOut, tt.wantErrO)
		})
	}
	t.Run("all with none", func(t *testing.T) {
		r := runAliasCmd(t, true, nil, "", "delete", "--all")
		assert.ErrorContains(t, r.err, "no aliases configured")
	})
}

func TestAliasImport(t *testing.T) {
	const doc = `prs: pr list -a @me
pr: pr list
"pr mine": pr list
bad: nope list
co: '!ado pr checkout "$1"'
taken: pr list -s active
lint: pr list
`
	existing := map[string]string{"taken": "pr list"}
	wantStored := map[string]string{"prs": "pr list -a @me", "co": `!ado pr checkout "$1"`, "lint": "pr list", "taken": "pr list"}
	wantReport := []string{
		`Skipped "bad": expansion "nope list" does not start with an ado command`,
		"Imported co",
		"Imported lint",
		`alias "lint" shadows the extension of the same name`,
		`Skipped "pr": "pr" is already an ado command`,
		`Skipped "pr mine": alias names must be a single word`,
		"Imported prs",
		`Skipped "taken": alias "taken" already exists; use --clobber to overwrite it`,
	}

	t.Run("stdin", func(t *testing.T) {
		r := runAliasCmd(t, false, existing, doc, "import", "-")
		require.NoError(t, r.err)
		assert.Equal(t, wantStored, savedAliases(t))
		for _, w := range wantReport {
			assert.Contains(t, r.errOut, w)
		}
		assert.Empty(t, r.out)
	})
	t.Run("file with clobber", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "aliases.yml")
		require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
		r := runAliasCmd(t, true, existing, "", "import", path, "--clobber")
		require.NoError(t, r.err)
		assert.Equal(t, "pr list -s active", savedAliases(t)["taken"])
		assert.Contains(t, r.errOut, "Changed taken")
	})
	t.Run("missing file", func(t *testing.T) {
		r := runAliasCmd(t, false, nil, "", "import", filepath.Join(t.TempDir(), "nope.yml"))
		assert.Error(t, r.err)
	})
	t.Run("invalid YAML fails the whole import", func(t *testing.T) {
		r := runAliasCmd(t, false, existing, "- a list\n", "import")
		assert.ErrorContains(t, r.err, "invalid alias file")
		assert.Equal(t, existing, r.cfg.Aliases())
	})
	t.Run("no file and TTY stdin", func(t *testing.T) {
		r := runAliasCmd(t, true, nil, "", "import")
		var fe *cmdutil.FlagError
		assert.ErrorAs(t, r.err, &fe)
	})
	t.Run("nothing imported leaves config untouched", func(t *testing.T) {
		r := runAliasCmd(t, false, nil, "pr: pr list\n", "import")
		require.NoError(t, r.err)
		_, err := os.Stat(filepath.Join(config.Dir(), "config.yml"))
		assert.True(t, os.IsNotExist(err))
	})
}

func TestAliasConfigError(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return nil, os.ErrPermission }}
	for _, args := range [][]string{{"set", "x", "pr"}, {"list"}, {"delete", "x"}, {"import", "-"}} {
		cmd := NewCmdAlias(f)
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		assert.ErrorIs(t, cmd.Execute(), os.ErrPermission, args)
	}
}
