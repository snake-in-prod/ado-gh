package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefinitionType(t *testing.T) {
	for want, process := range map[string]any{
		"classic": map[string]any{"type": float64(1)},
		"yaml":    map[string]any{"type": float64(2), "yamlFilename": "azure-pipelines.yml"},
		"":        map[string]any{"type": float64(3)},
	} {
		assert.Equal(t, want, DefinitionType(process))
	}
	assert.Empty(t, DefinitionType(nil))
	assert.Empty(t, DefinitionType("yaml"))
}

func TestParsePairs(t *testing.T) {
	got, err := ParsePairs("variable", "key=value", false, []string{"a=1", "b=", "a=2=3"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a": "2=3", "b": ""}, got, "a later key wins; values may hold '='")

	for _, tt := range []struct {
		pairs     []string
		needValue bool
		err       string
	}{
		{[]string{"novalue"}, false, `invalid --x "novalue": expected alias=version`},
		{[]string{" =v"}, false, `invalid --x " =v": expected alias=version`},
		{[]string{"a="}, true, `invalid --x "a=": expected alias=version`},
	} {
		_, err := ParsePairs("x", "alias=version", tt.needValue, tt.pairs)
		assert.EqualError(t, err, tt.err)
	}
}
