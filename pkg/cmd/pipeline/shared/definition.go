package shared

import (
	"strings"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// DefinitionType is "yaml" or "classic" by a build definition's process type (2 or 1), else "".
func DefinitionType(process any) string {
	p, ok := process.(map[string]any)
	if !ok {
		return ""
	}
	switch t, _ := p["type"].(float64); t {
	case 1:
		return "classic"
	case 2:
		return "yaml"
	}
	return ""
}

// ParsePairs parses repeated flag values in format (e.g. "key=value") with a non-empty key, and
// a non-empty value when needValue; a later key wins.
func ParsePairs(flag, format string, needValue bool, pairs []string) (map[string]string, error) {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || strings.TrimSpace(k) == "" || (needValue && v == "") {
			return nil, cmdutil.FlagErrorf("invalid --%s %q: expected %s", flag, p, format)
		}
		out[k] = v
	}
	return out, nil
}
