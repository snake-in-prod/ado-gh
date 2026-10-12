// Package shared stores, validates and expands ado aliases.
package shared

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/shlex"
	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// Group IDs of the cobra commands registered for aliases and extensions. Commands in these
// groups are not built-ins, so expansions can't target them.
const (
	AliasGroupID     = "alias"
	ExtensionGroupID = "extension"
)

// Config is the alias store of ado's config; *config.Config satisfies it.
type Config interface {
	Aliases() map[string]string
	SetAlias(name, expansion string)
	DeleteAlias(name string)
	Write() error
}

// AliasConfig returns cfg's alias store.
func AliasConfig(cfg cmdutil.Config) (Config, error) {
	ac, ok := cfg.(Config)
	if !ok {
		return nil, errors.New("config does not support aliases")
	}
	return ac, nil
}

// IsShell reports whether expansion is a shell alias, stored with a leading "!".
func IsShell(expansion string) bool { return strings.HasPrefix(expansion, "!") }

var placeholder = regexp.MustCompile(`\$[1-9][0-9]*`)

// Expand turns a non-shell alias into ado arguments: the expansion is shell-split first,
// then each $N in a token is replaced by args[N-1], and every arg no placeholder used is
// appended. Args are never re-split or re-substituted.
func Expand(expansion string, args []string) ([]string, error) {
	words, err := shlex.Split(expansion)
	if err != nil {
		return nil, fmt.Errorf("invalid alias expansion %q: %w", expansion, err)
	}
	used := make([]bool, len(args))
	missing := false
	for i, w := range words {
		words[i] = placeholder.ReplaceAllStringFunc(w, func(m string) string {
			n, err := strconv.Atoi(m[1:])
			if err != nil || n > len(args) {
				missing = true
				return m
			}
			used[n-1] = true
			return args[n-1]
		})
	}
	if missing {
		return nil, fmt.Errorf("not enough arguments for alias: %s", expansion)
	}
	for i, a := range args {
		if !used[i] {
			words = append(words, a)
		}
	}
	return words, nil
}

// ValidateExpansion checks that a non-shell expansion starts with a built-in ado command.
// Aliases and extensions don't count, so an alias expands exactly once.
func ValidateExpansion(root *cobra.Command, expansion string) error {
	if IsShell(expansion) {
		return nil
	}
	words, err := shlex.Split(expansion)
	if err != nil {
		return fmt.Errorf("invalid alias expansion %q: %w", expansion, err)
	}
	if len(words) > 0 {
		c, _, err := root.Find(words[:1])
		if err == nil && c != root && c.GroupID != AliasGroupID && c.GroupID != ExtensionGroupID {
			return nil
		}
	}
	return fmt.Errorf("expansion %q does not start with an ado command", expansion)
}
