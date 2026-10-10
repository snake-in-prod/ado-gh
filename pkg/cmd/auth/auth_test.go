package auth

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

func TestNewCmdAuth(t *testing.T) {
	cmd := NewCmdAuth(&cmdutil.Factory{})
	for _, name := range []string{"login", "logout", "status", "token"} {
		if sub, _, err := cmd.Find([]string{name}); err != nil || sub.Name() != name {
			t.Fatalf("%s subcommand: %v, %v", name, sub, err)
		}
	}
}
