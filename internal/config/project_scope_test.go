package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectScope(t *testing.T) {
	assert.Equal(t, "dev.azure.com/contoso/web app", ProjectScope("Contoso", "Web App"))
}

func TestProjectScopedTeam(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	c, err := Load()
	require.NoError(t, err)
	scope := ProjectScope("org", "proj")
	c.Set("", "team", "Global Team")
	c.Set(scope, "team", "Web Team")
	require.NoError(t, c.Write())

	b, err := os.ReadFile(filepath.Join(Dir(), fileName))
	require.NoError(t, err)
	assert.Contains(t, string(b), "dev.azure.com/org/proj:\n        team: Web Team")

	c, err = Load()
	require.NoError(t, err)
	assert.Equal(t, "Web Team", c.Get(scope, "team"))
	assert.Equal(t, "Global Team", c.Get(ProjectScope("org", "other"), "team"), "falls back to the global team")
}
