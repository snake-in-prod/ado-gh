package workitem

import (
	"bytes"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

func newWorkItemTestRoot() (*cobra.Command, *cobra.Command) {
	root := &cobra.Command{Use: "ado"}
	root.AddGroup(&cobra.Group{ID: "core", Title: "Core commands"})
	wi := NewCmdWorkItem(&cmdutil.Factory{})
	root.AddCommand(wi)
	return root, wi
}

func TestNewCmdWorkItemAliasesResolveToGroup(t *testing.T) {
	root, wi := newWorkItemTestRoot()
	for _, name := range []string{"workitem", "wi", "issue"} {
		c, _, err := root.Find([]string{name, "list"})
		if err != nil || c.Parent() != wi {
			t.Errorf("%s did not resolve to the workitem group: %v", name, err)
		}
	}
}

func TestNewCmdWorkItemHelp(t *testing.T) {
	for _, name := range []string{"workitem", "wi", "issue"} {
		root, _ := newWorkItemTestRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{name, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatalf("%s --help: %v", name, err)
		}
		help := out.String()
		for _, want := range []string{"Manage work items", "workitem, wi, issue", "list", "List work items"} {
			if !strings.Contains(help, want) {
				t.Errorf("%s --help missing %q:\n%s", name, want, help)
			}
		}
	}
}
