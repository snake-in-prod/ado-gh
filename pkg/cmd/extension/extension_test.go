package extension

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// runExtCmd runs `ado ext <args>` against a fresh XDG_DATA_HOME prepared by seed.
func runExtCmd(t *testing.T, tty bool, seed func(root string), args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if seed != nil {
		seed(extpkg.Dir())
	}
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetStderrTTY(tty)
	f := &cmdutil.Factory{
		IOStreams:  ios,
		Executable: "ado",
		BaseRepo:   func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("no repo") },
	}
	root := &cobra.Command{Use: "ado", SilenceErrors: true, SilenceUsage: true}
	root.AddGroup(&cobra.Group{ID: "additional", Title: "Additional commands:"})
	root.AddCommand(NewCmdExtension(f))
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"ext"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func mkExtDir(t *testing.T, root, name string, git bool) string {
	t.Helper()
	dir := filepath.Join(root, "ado-"+name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	if git {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	}
	return dir
}

// seedListExts installs a pinned binary, a script with a token-consenting record and a
// record-less pointer-file local extension pointing at target.
func seedListExts(t *testing.T, target string) func(string) {
	return func(root string) {
		mkExtDir(t, root, "bin", false)
		require.NoError(t, extpkg.WriteRecord(root, "bin", &extpkg.Record{
			Source: "github.com/o/ado-bin", Kind: extpkg.KindBinary, Tag: "v1.0.0", Version: "1.0.0", Pinned: true}))
		mkExtDir(t, root, "scr", true)
		require.NoError(t, extpkg.WriteRecord(root, "scr", &extpkg.Record{
			Source: "dev.azure.com/o/p/ado-scr", Kind: extpkg.KindScript, Commit: "0123456789abcdef", TokenConsent: true}))
		require.NoError(t, extpkg.WritePointer(filepath.Join(root, "ado-loc"), target))
	}
}

func TestExtensionListTSV(t *testing.T) {
	target := t.TempDir()
	out, _, err := runExtCmd(t, false, seedListExts(t, target), "list")
	require.NoError(t, err)
	assert.Equal(t,
		"bin\tgithub.com/o/ado-bin\tbinary\t1.0.0\ttrue\tfalse\n"+
			"loc\t"+target+"\tlocal\t\tfalse\tfalse\n"+
			"scr\tdev.azure.com/o/p/ado-scr\tscript\t0123456789abcdef\tfalse\ttrue\n", out)
}

func TestExtensionListTable(t *testing.T) {
	out, _, err := runExtCmd(t, true, seedListExts(t, t.TempDir()), "ls")
	require.NoError(t, err)
	for _, want := range []string{"NAME", "SOURCE", "KIND", "VERSION", "PINNED", "TOKEN", "github.com/o/ado-bin", "01234567 "} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "0123456789abcdef", "commit shortened on a TTY")
	assert.Contains(t, out, "✓")
}

func TestExtensionListJSON(t *testing.T) {
	target := t.TempDir()
	out, _, err := runExtCmd(t, false, seedListExts(t, target), "list", "--json", "name,source,kind,version,pinned,tokenConsent,path")
	require.NoError(t, err)
	root := extpkg.Dir()
	want := `[
 {"kind":"binary","name":"bin","path":` + jsonStr(filepath.Join(root, "ado-bin")) + `,"pinned":true,"source":"github.com/o/ado-bin","tokenConsent":false,"version":"1.0.0"},
 {"kind":"local","name":"loc","path":` + jsonStr(target) + `,"pinned":false,"source":` + jsonStr(target) + `,"tokenConsent":false,"version":""},
 {"kind":"script","name":"scr","path":` + jsonStr(filepath.Join(root, "ado-scr")) + `,"pinned":false,"source":"dev.azure.com/o/p/ado-scr","tokenConsent":true,"version":"0123456789abcdef"}
]`
	assert.JSONEq(t, want, out)
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestExtensionListEmpty(t *testing.T) {
	out, errOut, err := runExtCmd(t, true, nil, "list")
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "no installed extensions")

	out, _, err = runExtCmd(t, false, nil, "list", "--json", "name")
	require.NoError(t, err)
	assert.JSONEq(t, "[]", out)
}

func TestExtensionRemoveDir(t *testing.T) {
	for _, git := range []bool{false, true} {
		var root string
		_, errOut, err := runExtCmd(t, true, func(r string) {
			root = r
			mkExtDir(t, r, "hello", git)
			require.NoError(t, extpkg.WriteRecord(r, "hello", &extpkg.Record{Kind: extpkg.KindBinary}))
			mkExtDir(t, r, "other", false)
		}, "remove", "hello")
		require.NoError(t, err)
		assert.Contains(t, errOut, "Removed extension hello")
		assert.NoDirExists(t, filepath.Join(root, "ado-hello"))
		assert.NoFileExists(t, extpkg.RecordPath(root, "hello"))
		assert.DirExists(t, filepath.Join(root, "ado-other"))
	}
}

func TestExtensionRemoveLocalKeepsTarget(t *testing.T) {
	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "ado-loc"), []byte("x"), 0o755))
	links := map[string]func(root string){
		"pointer": func(root string) {
			require.NoError(t, extpkg.WritePointer(filepath.Join(root, "ado-loc"), target))
		},
		"broken pointer": func(root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "ado-loc"), []byte("relative\n"), 0o644))
		},
	}
	if runtime.GOOS != "windows" {
		links["symlink"] = func(root string) {
			require.NoError(t, os.Symlink(target, filepath.Join(root, "ado-loc")))
		}
	}
	for name, link := range links {
		t.Run(name, func(t *testing.T) {
			var root string
			_, _, err := runExtCmd(t, false, func(r string) {
				root = r
				require.NoError(t, os.MkdirAll(r, 0o755))
				link(r)
				require.NoError(t, extpkg.WriteRecord(r, "loc", &extpkg.Record{Kind: extpkg.KindLocal}))
			}, "remove", "loc")
			require.NoError(t, err)
			_, err = os.Lstat(filepath.Join(root, "ado-loc"))
			assert.ErrorIs(t, err, os.ErrNotExist)
			assert.NoFileExists(t, extpkg.RecordPath(root, "loc"))
			assert.FileExists(t, filepath.Join(target, "ado-loc"), "target untouched")
		})
	}
}

func TestExtensionRemoveErrors(t *testing.T) {
	tests := map[string]struct {
		args    []string
		wantErr string
	}{
		"unknown":   {args: []string{"remove", "nope"}, wantErr: `extension "nope" is not installed`},
		"traversal": {args: []string{"remove", "../x"}, wantErr: "invalid extension name"},
		"no name":   {args: []string{"remove"}, wantErr: "accepts 1 arg"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := runExtCmd(t, false, nil, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExtensionExec(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	out, _, err := runExtCmd(t, false, func(root string) {
		dir := mkExtDir(t, root, "hello", false)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-hello"), []byte("#!/bin/sh\necho \"hello $*\"\n"), 0o755))
	}, "exec", "hello", "a", "--flag", "-R", "x")
	require.NoError(t, err)
	assert.Equal(t, "hello a --flag -R x\n", out)
}

func TestExtensionExecErrors(t *testing.T) {
	tests := map[string]struct {
		args    []string
		wantErr string
	}{
		"unknown":   {args: []string{"exec", "nope"}, wantErr: `extension "nope" is not installed`},
		"traversal": {args: []string{"exec", ".."}, wantErr: "invalid extension name"},
		"no name":   {args: []string{"exec"}, wantErr: "requires at least 1 arg"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := runExtCmd(t, false, nil, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExtensionListSanitizesVersion(t *testing.T) {
	out, _, err := runExtCmd(t, false, func(root string) {
		mkExtDir(t, root, "bin", false)
		require.NoError(t, extpkg.WriteRecord(root, "bin", &extpkg.Record{
			Source: "github.com/o/ado-bin", Kind: extpkg.KindBinary, Tag: "v1\u202e0\u009b2"}))
	}, "list")
	require.NoError(t, err)
	assert.Contains(t, out, "\tv102\t", "bidi and C1 controls stripped from the GitHub tag")
}
