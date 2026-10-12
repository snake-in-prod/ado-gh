// Package version implements `ado version`, also rendered by `ado --version`.
package version

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdVersion returns the `version` command.
func NewCmdVersion(f *cmdutil.Factory) *cobra.Command {
	var exporter *cmdutil.Exporter
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "Show ado version",
		GroupID: "additional",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if exporter != nil {
				i := current(f)
				return exporter.Write(f.IOStreams, map[string]any{
					"version": i.Version, "commit": i.Commit, "date": i.Date, "dirty": i.Dirty,
					"goVersion": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
				})
			}
			_, err := fmt.Fprint(f.IOStreams.Out, Text(f))
			return err
		},
	}
	cmdutil.AddJSONFlags(cmd, &exporter, []string{"version", "commit", "date", "dirty", "goVersion", "os", "arch"})
	return cmd
}

// Text is the `ado version` output, also used as the root --version template.
func Text(f *cmdutil.Factory) string {
	return format(current(f))
}

func current(f *cmdutil.Factory) build.Info {
	return build.Info{Version: f.AppVersion, Commit: build.Commit, Date: build.Date, Dirty: build.Dirty}
}

func format(i build.Info) string {
	var b strings.Builder
	b.WriteString("ado version " + i.Version)
	if day, _, _ := strings.Cut(i.Date, "T"); day != "" {
		b.WriteString(" (" + day + ")")
	}
	b.WriteString("\n")
	if i.Commit != "" {
		c := i.Commit[:min(7, len(i.Commit))]
		if i.Dirty {
			c += "-dirty"
		}
		fmt.Fprintf(&b, "commit %s · %s · %s/%s\n", c, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	}
	if i.Version != "DEV" && !build.IsPseudoVersion(i.Version) {
		fmt.Fprintf(&b, "%s/releases/tag/v%s\n", build.RepoURL, i.Version)
	}
	return b.String()
}
