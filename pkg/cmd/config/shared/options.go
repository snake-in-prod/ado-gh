// Package shared lists the config keys that `ado config` can read and write.
package shared

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// Option is a user-settable config key.
type Option struct {
	Key           string
	Description   string
	Default       string
	AllowedValues []string
	// PerProject options can be set for one project with --project ORG/PROJECT.
	PerProject bool
}

// Options are the keys known to `ado config`, sorted by key.
var Options = []Option{
	{Key: "browser", Description: "the web browser to use for opening URLs"},
	{Key: "editor", Description: "the text editor program to use for authoring text"},
	{Key: "entra_client_id", Description: "the Microsoft Entra ID app client ID used by Entra login"},
	{Key: "git_protocol", Description: "the protocol to use for git clone and push operations", Default: "https", AllowedValues: []string{"https", "ssh"}},
	{Key: "org", Description: "the default Azure DevOps organization for repository names without one"},
	{Key: "prompt", Description: "toggle interactive prompting in the terminal", Default: "enabled", AllowedValues: []string{"enabled", "disabled"}},
	{Key: "team", Description: "the team for work item, sprint and board commands; set per project with --project", PerProject: true},
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

// Scope returns the config scope of the --host and --project flags: the host, the project's
// scope key (config.ProjectScope) for a PerProject option, or "" for the global value.
func (o Option) Scope(host, project string) (string, error) {
	if project == "" {
		return host, nil
	}
	if host != "" {
		return "", errors.New("specify only one of --host or --project")
	}
	if !o.PerProject {
		var keys []string
		for _, opt := range Options {
			if opt.PerProject {
				keys = append(keys, opt.Key)
			}
		}
		return "", fmt.Errorf("--project is only supported for: %s", strings.Join(keys, ", "))
	}
	p, err := adocontext.ParseProject("--project", project)
	if err != nil {
		return "", err
	}
	return config.ProjectScope(p.Org, p.Project), nil
}

// Value returns the configured value for host, or the option's default.
func (o Option) Value(cfg cmdutil.Config, host string) string {
	if v := cfg.Get(host, o.Key); v != "" {
		return v
	}
	return o.Default
}
