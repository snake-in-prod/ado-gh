package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

func newPipelineTestRoot() (*cobra.Command, *cobra.Command) {
	root := &cobra.Command{Use: "ado"}
	root.AddGroup(&cobra.Group{ID: "core", Title: "Core commands"})
	p := NewCmdPipeline(&cmdutil.Factory{})
	root.AddCommand(p)
	return root, p
}

func TestNewCmdPipelineAliasResolvesToGroup(t *testing.T) {
	root, p := newPipelineTestRoot()
	for _, name := range []string{"pipeline", "run"} {
		if c, _, err := root.Find([]string{name}); err != nil || c != p {
			t.Errorf("%s did not resolve to the pipeline group: %v", name, err)
		}
	}
}

func TestNewCmdPipelineHelp(t *testing.T) {
	for _, name := range []string{"pipeline", "run"} {
		root, _ := newPipelineTestRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{name, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatalf("%s --help: %v", name, err)
		}
		// Cobra omits the usage/alias block until subcommands are registered.
		if !strings.Contains(out.String(), "Manage pipelines and runs") {
			t.Errorf("%s --help missing short description:\n%s", name, out.String())
		}
	}
}
