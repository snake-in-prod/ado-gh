package pr

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

func TestNewCmdPRRegistersSubcommands(t *testing.T) {
	cmd := NewCmdPR(&cmdutil.Factory{})
	for name, want := range map[string]string{
		"checkout": "checkout",
		"close":    "close",
		"comment":  "comment",
		"ready":    "ready",
		"reopen":   "reopen",
		"create":   "create",
		"diff":     "diff",
		"edit":     "edit",
		"list":     "list",
		"merge":    "merge",
		"complete": "merge",
		"review":   "review",
		"view":     "view",
	} {
		if c, _, err := cmd.Find([]string{name}); err != nil || c.Name() != want {
			t.Errorf("%s not registered as %s: %v", name, want, err)
		}
	}
}
