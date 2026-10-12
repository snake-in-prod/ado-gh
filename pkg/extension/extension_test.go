package extension

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/xdg")
	assert.Equal(t, filepath.Join("/xdg", "ado", "extensions"), Dir())
}

func TestValidateName(t *testing.T) {
	for _, name := range []string{"hello", "a", "my-ext", "my_ext", "v1.2", "0x"} {
		assert.NoError(t, ValidateName(name), name)
	}
	for _, name := range []string{"", ".", "..", "../x", "a/b", `a\b`, "-x", ".hidden", "_x", "Hello", "a b", "x.", "ado-\x00", "a:b"} {
		assert.Error(t, ValidateName(name), "%q", name)
	}
}

func TestNameFromRepo(t *testing.T) {
	got, err := NameFromRepo("ado-hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", got)

	got, err = NameFromRepo("ADO-Hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", got)

	for _, repo := range []string{"hello", "ado-", "ado-..", "ado-../x", "gh-hello"} {
		_, err := NameFromRepo(repo)
		assert.Error(t, err, repo)
	}
}

func TestCheckCollision(t *testing.T) {
	root := &cobra.Command{Use: "ado"}
	root.AddGroup(&cobra.Group{ID: "alias", Title: "a"}, &cobra.Group{ID: "extension", Title: "e"})
	root.AddCommand(
		&cobra.Command{Use: "workitem", Aliases: []string{"wi"}, Run: func(*cobra.Command, []string) {}},
		&cobra.Command{Use: "mine", GroupID: "alias", Run: func(*cobra.Command, []string) {}},
		&cobra.Command{Use: "hello", GroupID: "extension", Run: func(*cobra.Command, []string) {}},
	)
	for _, name := range []string{"workitem", "wi", "help"} {
		assert.ErrorContains(t, CheckCollision(root, name), "core command", name)
	}
	for _, name := range []string{"mine", "hello", "fresh"} {
		assert.NoError(t, CheckCollision(root, name), name)
	}
}

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(`
name: hello
description: Says hello
token: true
minAdoVersion: 1.2.0
binaries:
  azure-artifacts:
    organization: myorg
    project: tools
    feed: ext
    package: ado-hello
`), "hello")
	require.NoError(t, err)
	assert.Equal(t, &Manifest{
		Name:          "hello",
		Description:   "Says hello",
		Token:         true,
		MinAdoVersion: "1.2.0",
		Binaries: Binaries{AzureArtifacts: &AzureArtifacts{
			Organization: "myorg", Project: "tools", Feed: "ext", Package: "ado-hello",
		}},
	}, m)

	m, err = ParseManifest([]byte("binaries:\n  github-releases: true\n"), "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", m.Name, "name defaults to the repo-derived name")
	assert.True(t, m.Binaries.GitHubReleases)

	m, err = ParseManifest([]byte("name: ado-hello\n"), "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", m.Name, "ado- prefix accepted")
}

func TestParseManifestInvalid(t *testing.T) {
	tests := map[string]string{
		"bad yaml":          "name: [",
		"name mismatch":     "name: other\n",
		"name traversal":    "name: ../hello\n",
		"both binary kinds": "binaries:\n  github-releases: true\n  azure-artifacts: {organization: o, feed: f, package: p}\n",
		"missing feed":      "binaries:\n  azure-artifacts: {organization: o, package: p}\n",
		"dash package":      "binaries:\n  azure-artifacts: {organization: o, feed: f, package: --evil}\n",
		"dash project":      "binaries:\n  azure-artifacts: {organization: o, project: -x, feed: f, package: p}\n",
		"space in feed":     "binaries:\n  azure-artifacts: {organization: o, feed: a b, package: p}\n",
		"control in org":    "binaries:\n  azure-artifacts: {organization: \"o\\x1b\", feed: f, package: p}\n",
		"token not a bool":  "token: maybe\n",
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseManifest([]byte(data), "hello")
			assert.Error(t, err)
		})
	}
	_, err := ParseManifest([]byte("name: hello\n"), "..")
	assert.Error(t, err, "repo-derived name validated too")
}

func TestRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []Kind{KindScript, KindBinary, KindLocal} {
		t.Run(string(kind), func(t *testing.T) {
			name := "x" + string(kind)
			want := &Record{
				Source: "dev.azure.com/o/p/ado-" + name, Host: "dev.azure.com", Kind: kind,
				Tag: "v1.0.0", Commit: "abc", Version: "1.0.0", Pinned: true, TokenConsent: true, Checksum: "sha256:ff",
			}
			require.NoError(t, WriteRecord(root, name, want))
			assert.FileExists(t, filepath.Join(root, ".records", name+".yml"))
			assert.NoDirExists(t, filepath.Join(root, "ado-"+name), "record never written inside the entry")
			got, err := ReadRecord(root, name)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, filepath.Join(root, ".records", name+".yml"), RecordPath(root, name))
		})
	}
}

func TestReadRecordMissingMeansNoConsent(t *testing.T) {
	r, err := ReadRecord(t.TempDir(), "hello")
	require.NoError(t, err)
	assert.False(t, r.TokenConsent)
}

func TestRecordRejectsBadNames(t *testing.T) {
	root := t.TempDir()
	assert.Error(t, WriteRecord(root, "../evil", &Record{}))
	_, err := ReadRecord(root, "../evil")
	assert.Error(t, err)
}

func TestReadRecordCorrupt(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".records"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".records", "hello.yml"), []byte("tokenConsent: ["), 0o600))
	_, err := ReadRecord(root, "hello")
	assert.Error(t, err)
}

func TestListAndLookupKinds(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()

	script := filepath.Join(root, "ado-script")
	require.NoError(t, os.MkdirAll(filepath.Join(script, ".git"), 0o755))
	bin := filepath.Join(root, "ado-bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, WritePointer(filepath.Join(root, "ado-pointer"), target))
	require.NoError(t, WriteRecord(root, "script", &Record{Kind: KindScript}))
	// Noise that must be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "other"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ado-..x"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ado-Bad"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ado-relative"), []byte("some/dir\n"), 0o600))

	want := []Extension{
		{Name: "bin", Path: bin, Dir: bin, Kind: KindBinary},
		{Name: "pointer", Path: filepath.Join(root, "ado-pointer"), Dir: target, Kind: KindLocal},
		{Name: "script", Path: script, Dir: script, Kind: KindScript},
	}
	symlinked := false
	if err := os.Symlink(target, filepath.Join(root, "ado-symlink")); err == nil {
		symlinked = true
		want = append(want, Extension{Name: "symlink", Path: filepath.Join(root, "ado-symlink"), Dir: filepath.Join(root, "ado-symlink"), Kind: KindLocal})
	}

	got, err := List(root)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	for _, w := range want {
		e, err := Lookup(root, w.Name)
		require.NoError(t, err)
		assert.Equal(t, w, e)
	}
	if !symlinked {
		t.Log("symlinks unavailable; symlink kind not checked")
	}
}

func TestLookupErrors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ado-relative"), []byte("some/dir"), 0o600))
	for _, name := range []string{"missing", "../x", "..", "relative"} {
		_, err := Lookup(root, name)
		assert.Error(t, err, name)
	}
	_, err := Lookup(root, "missing")
	assert.ErrorContains(t, err, `extension "missing" is not installed`)
}

func TestListMissingRoot(t *testing.T) {
	got, err := List(filepath.Join(t.TempDir(), "nope"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestPointerRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ado-x")
	target := t.TempDir()
	require.NoError(t, WritePointer(p, target))
	got, err := readPointer(p)
	require.NoError(t, err)
	assert.Equal(t, target, got)

	// CRLF and surrounding whitespace, as a Windows editor might leave them.
	require.NoError(t, os.WriteFile(p, []byte("  "+target+"\r\n"), 0o600))
	got, err = readPointer(p)
	require.NoError(t, err)
	assert.Equal(t, target, got)

	assert.Error(t, WritePointer(p, "relative/dir"))
	require.NoError(t, os.WriteFile(p, []byte(""), 0o600))
	_, err = readPointer(p)
	assert.Error(t, err)
}
