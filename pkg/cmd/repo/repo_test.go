package repo

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

func TestNewCmdRepoRegistersSubcommands(t *testing.T) {
	cmd := NewCmdRepo(&cmdutil.Factory{})
	for _, name := range []string{"clone", "create", "delete", "list", "restore", "set-default", "view"} {
		if c, _, err := cmd.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("%s not registered: %v", name, err)
		}
	}
}
