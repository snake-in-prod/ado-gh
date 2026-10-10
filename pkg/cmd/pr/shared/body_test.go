package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(path, []byte("file"), 0o600))

	got, err := ReadBody(nil, path)
	require.NoError(t, err)
	assert.Equal(t, "file", got)

	got, err = ReadBody(strings.NewReader("stdin"), "-")
	require.NoError(t, err)
	assert.Equal(t, "stdin", got)

	_, err = ReadBody(nil, filepath.Join(t.TempDir(), "missing"))
	assert.ErrorContains(t, err, "reading the description")
}
