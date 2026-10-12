package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidArtifactName(t *testing.T) {
	for _, name := range []string{"drop", "web app", "logs-1.2", "Drop ✓"} {
		assert.NoError(t, ValidArtifactName(name), name)
	}
	for _, name := range []string{"", ".", "..", "../x", "a/b", `a\b`, "NUL", "con.txt", "C:", "x:y", "trailing.", "tab\t"} {
		assert.Error(t, ValidArtifactName(name), "%q", name)
	}
}
