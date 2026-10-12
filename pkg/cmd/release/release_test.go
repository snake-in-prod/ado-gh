package release

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

func TestNewCmdReleaseRegistersSubcommands(t *testing.T) {
	cmd := NewCmdRelease(&cmdutil.Factory{})
	for _, name := range []string{"list", "logs", "view", "watch"} {
		if c, _, err := cmd.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("%s not registered: %v", name, err)
		}
	}
}
