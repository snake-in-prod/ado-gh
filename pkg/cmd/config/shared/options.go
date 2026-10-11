// Package shared lists the config keys that `ado config` can read and write.
package shared

import (
	"fmt"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// Option is a user-settable config key.
type Option struct {
	Key           string
	Description   string
	Default       string
	AllowedValues []string
}

// Options are the keys known to `ado config`, sorted by key.
var Options = []Option{
	{Key: "browser", Description: "the web browser to use for opening URLs"},
	{Key: "editor", Description: "the text editor program to use for authoring text"},
	{Key: "entra_client_id", Description: "the Microsoft Entra ID app client ID used by Entra login"},
	{Key: "org", Description: "the default Azure DevOps organization for repository names without one"},
	{Key: "prompt", Description: "toggle interactive prompting in the terminal", Default: "enabled", AllowedValues: []string{"enabled", "disabled"}},
}

// Find returns the option for key, or an error listing the valid keys.
func Find(key string) (Option, error) {
	keys := make([]string, len(Options))
	for i, o := range Options {
		if o.Key == key {
			return o, nil
		}
		keys[i] = o.Key
	}
	return Option{}, fmt.Errorf("unknown config key %q; valid keys: %s", key, strings.Join(keys, ", "))
}

// Validate rejects values outside AllowedValues, when the option has any.
func (o Option) Validate(value string) error {
	if len(o.AllowedValues) == 0 || slices.Contains(o.AllowedValues, value) {
		return nil
	}
	return fmt.Errorf("invalid value %q for %s; valid values: %s", value, o.Key, strings.Join(o.AllowedValues, ", "))
}

// Value returns the configured value for host, or the option's default.
func (o Option) Value(cfg cmdutil.Config, host string) string {
	if v := cfg.Get(host, o.Key); v != "" {
		return v
	}
	return o.Default
}
