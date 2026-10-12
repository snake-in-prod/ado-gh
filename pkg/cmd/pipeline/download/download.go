// Package download implements `ado pipeline download`.
package download

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/fsutil"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	run      string
	names    []string
	patterns []string
	dir      string
	clobber  bool
}

// NewCmdDownload returns the `pipeline download` command.
func NewCmdDownload(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "download [<run>]",
		Short: "Download the artifacts of a run",
		Long: `Download and extract the pipeline and build (container) artifacts of a run.

<run> is a run ID or URL. Without it, you are asked to pick one of the recent runs of
the current repository's pipeline on the current branch (or of the project).

With a single --name, the artifact is extracted directly into --dir, and files it would
overwrite are an error unless --clobber is given. Otherwise (several names, --pattern,
or all artifacts) each artifact goes to <dir>/<artifact>; an existing <dir>/<artifact>
is an error unless --clobber is given, which removes it first.

A top-level folder named after the artifact is stripped. File share artifacts are
skipped with their UNC path, and other artifact types with a warning.`,
		Example: `  ado pipeline download 812
  ado pipeline download 812 -n drop -D out
  ado run download 812 -p "web*" --clobber`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.run = args[0]
			}
			for _, p := range o.patterns {
				if _, err := path.Match(p, ""); err != nil {
					return cmdutil.FlagErrorf("invalid pattern %q: %v", p, err)
				}
			}
			return runDownload(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringArrayVarP(&o.names, "name", "n", nil, "Download the artifact with this name (repeatable)")
	fl.StringArrayVarP(&o.patterns, "pattern", "p", nil, "Download the artifacts whose names match this glob (repeatable)")
	fl.StringVarP(&o.dir, "dir", "D", ".", "The directory to download into")
	fl.BoolVar(&o.clobber, "clobber", false, "Overwrite existing files, replacing existing artifact directories")
	return cmd
}

// target is an artifact to download and the directory it is extracted into.
type target struct {
	name, url, dir string
}

func runDownload(ctx context.Context, f *cmdutil.Factory, o *options) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	var id int
	if o.run != "" {
		var p adocontext.Repo
		if id, p, err = shared.ParseRunRef(o.run); err != nil {
			return err
		}
		if p.Project != "" {
			project = p
		}
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	if o.run == "" {
		if id, err = shared.SelectRepoRun(ctx, f, c, project); err != nil {
			return err
		}
	}

	arts, err := c.Artifacts(ctx, id)
	if err != nil {
		return err
	}
	if len(arts) == 0 {
		return fmt.Errorf("run %d has no artifacts", id)
	}
	var selected []build.BuildArtifact
	for _, a := range arts {
		ok, err := o.selects(prshared.Deref(a.Name))
		if err != nil {
			return err
		}
		if ok {
			selected = append(selected, a)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("no artifact of run %d matches the given names or patterns", id)
	}

	// Check every artifact before downloading any, so a conflict fails fast.
	ios := f.IOStreams
	single := len(o.names) == 1 && len(o.patterns) == 0
	var targets []target
	// dirs maps each target directory, lower-cased, to its artifact: on a case-insensitive file
	// system "Drop" and "drop" share one, and the second would remove the first's files.
	dirs := map[string]string{}
	for _, a := range selected {
		t, ok := o.plan(ios.ErrOut, a, single)
		if !ok {
			continue
		}
		if err := shared.ValidArtifactName(t.name); err != nil {
			return err
		}
		if prev, dup := dirs[strings.ToLower(t.dir)]; dup && !single {
			return fmt.Errorf("artifacts %s and %s would extract to the same directory on case-insensitive file systems; pick one with --name", prev, t.name)
		}
		dirs[strings.ToLower(t.dir)] = t.name
		if !single && !o.clobber {
			if _, err := os.Lstat(t.dir); err == nil {
				return fmt.Errorf("%s already exists; use --clobber to replace it", t.dir)
			}
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		return errors.New("no artifacts downloaded")
	}

	for _, t := range targets {
		if err := download(ctx, ios.ErrOut, c, t, single, o.clobber); err != nil {
			return err
		}
		fmt.Fprintf(ios.ErrOut, "%s Downloaded %s to %s\n", ios.ColorScheme().Green("✓"), t.name, t.dir)
	}
	return nil
}

// selects reports whether the flags select the artifact name: all artifacts without -n or -p.
// A malformed pattern is an error, never a silent mismatch.
func (o *options) selects(name string) (bool, error) {
	if len(o.names) == 0 && len(o.patterns) == 0 || slices.Contains(o.names, name) {
		return true, nil
	}
	for _, p := range o.patterns {
		ok, err := path.Match(p, name)
		if err != nil {
			return false, cmdutil.FlagErrorf("invalid pattern %q: %v", p, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// plan returns where artifact a goes, or false after a warning when its type can't be
// downloaded. The download URL may be signed, so it is never printed.
func (o *options) plan(w io.Writer, a build.BuildArtifact, single bool) (target, bool) {
	t := target{name: prshared.Deref(a.Name), dir: o.dir}
	var typ, data string
	if r := a.Resource; r != nil {
		typ, data, t.url = prshared.Deref(r.Type), prshared.Deref(r.Data), prshared.Deref(r.DownloadUrl)
	}
	switch {
	// A null type with id 0 is a file share (Build API bug 378900, as the agent handles it).
	case strings.EqualFold(typ, "FilePath") || typ == "" && prshared.Deref(a.Id) == 0:
		fmt.Fprintf(w, "skipping %s: file share artifact; copy it from %s\n", t.name, data)
		return t, false
	case !strings.EqualFold(typ, "Container") && !strings.EqualFold(typ, "PipelineArtifact"):
		fmt.Fprintf(w, "skipping %s: unsupported artifact type %s\n", t.name, typ)
		return t, false
	}
	if !single {
		t.dir = filepath.Join(o.dir, t.name)
	}
	return t, true
}

// download fetches t's zip to a temp file (artifacts can be large; zip needs random access)
// and extracts it into t.dir. Unless single, t.dir is first removed (clobber was checked).
func download(ctx context.Context, w io.Writer, c *api.PipelineClient, t target, single, clobber bool) error {
	if t.url == "" {
		return fmt.Errorf("artifact %s has no download URL", t.name)
	}
	tmp, err := fetch(ctx, c, t)
	if tmp != "" {
		defer os.Remove(tmp)
	}
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(tmp)
	if err != nil {
		return fmt.Errorf("artifact %s: %w", t.name, err)
	}
	defer zr.Close()
	if !single {
		if err := os.RemoveAll(t.dir); err != nil {
			return err
		}
	}
	skipped, err := fsutil.ExtractZip(&zr.Reader, t.dir, t.name, clobber)
	if err != nil {
		return fmt.Errorf("artifact %s: %w", t.name, err)
	}
	for _, s := range skipped {
		fmt.Fprintf(w, "warning: skipped %q in %s: not a regular file\n", s, t.name)
	}
	return nil
}

// fetch downloads t into a new temp file and returns its path, also on error once created.
func fetch(ctx context.Context, c *api.PipelineClient, t target) (string, error) {
	body, err := c.Download(ctx, t.url)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", t.name, err)
	}
	defer body.Close()
	f, err := os.CreateTemp("", "ado-artifact-*.zip")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, body)
	if err = errors.Join(err, f.Close()); err != nil {
		return f.Name(), fmt.Errorf("downloading %s: %w", t.name, err)
	}
	return f.Name(), nil
}
