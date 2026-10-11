package sprint

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

func TestNewCmdSprintRegistersSubcommands(t *testing.T) {
	cmd := NewCmdSprint(&cmdutil.Factory{})
	for _, name := range []string{"board", "current", "list", "view"} {
		if c, _, err := cmd.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("%s not registered: %v", name, err)
		}
	}
}
