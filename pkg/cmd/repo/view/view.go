// Package view implements `ado repo view`.
package view

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/charmbracelet/glamour"
	ghterm "github.com/cli/go-gh/v2/pkg/term"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

var jsonFields = []string{"id", "name", "project", "defaultBranch", "size", "isDisabled", "isFork",
	"parentRepository", "remoteUrl", "sshUrl", "webUrl", "readme"}

const noReadme = "This repository does not have a README"

type options struct {
	branch   string
	web      bool
	exporter *cmdutil.Exporter
}

type viewJSON struct {
	api.Repository
	Readme string `json:"readme"`
}

// NewCmdView returns the `repo view` command.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "view [<repo>]",
		Short: "View a repository",
		Long: `Show a repository's details and its README. <repo> is REPO, PROJECT/REPO,
ORG/PROJECT/REPO or a URL; the default is the current repository.

The README is rendered as markdown on a terminal and printed raw when piped.`,
		Example: `  ado repo view
  ado repo view myproject/api --branch develop
  ado repo view api --web`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := shared.ParseRepo(f, strings.Join(args, ""))
			if err != nil {
				return err
			}
			if o.web {
				u := adocontext.RepoURL(repo)
				if o.branch != "" {
					u += "?version=GB" + strings.ReplaceAll(url.QueryEscape(o.branch), "+", "%20")
				}
				return cmdutil.OpenInBrowser(f, u)
			}
			return run(cmd.Context(), f, o, repo)
		},
	}
	cmd.Flags().StringVarP(&o.branch, "branch", "b", "", "Show the README of `branch` (default: the default branch)")
	cmd.Flags().BoolVarP(&o.web, "web", "w", false, "Open the repository in the browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options, repo adocontext.Repo) error {
	client, err := shared.NewClient(ctx, f, repo)
	if err != nil {
		return err
	}
	r, err := client.Repository(ctx, repo.Name)
	if err != nil {
		return err
	}
	data := viewJSON{Repository: r}
	var readmeName string
	// Disabled repositories refuse content calls; empty ones have nothing to list.
	wantReadme := o.exporter == nil || slices.Contains(o.exporter.Fields, "readme")
	if wantReadme && !r.IsDisabled && (o.branch != "" || r.DefaultBranch != "") {
		if readmeName, data.Readme, err = readme(ctx, client, r.ID, o.branch); err != nil {
			return err
		}
	}

	ios := f.IOStreams
	if o.exporter != nil {
		return o.exporter.Write(ios, data)
	}

	body := noReadme
	switch {
	case r.IsDisabled:
		body = "This repository is disabled, so its files can't be shown."
	case readmeName != "":
		body = data.Readme
		if ios.IsStdoutTTY() && isMarkdown(readmeName) {
			if body, err = renderMarkdown(ios, body); err != nil {
				return err
			}
		}
	}
	fullName := r.Project + "/" + r.Name
	if !ios.IsStdoutTTY() {
		fmt.Fprintf(ios.Out, "name:\t%s\nproject:\t%s\ndefault branch:\t%s\nsize:\t%d\nhttps url:\t%s\nssh url:\t%s\nurl:\t%s\n",
			fullName, r.Project, r.DefaultBranch, r.Size, r.RemoteURL, r.SSHURL, r.WebURL)
		if p := r.ParentRepository; p != nil {
			fmt.Fprintf(ios.Out, "parent:\t%s/%s\n", p.Project, p.Name)
		}
		if r.IsDisabled {
			fmt.Fprintln(ios.Out, "disabled:\ttrue")
		}
		fmt.Fprintf(ios.Out, "--\n%s\n", strings.TrimRight(body, "\n"))
		return nil
	}

	cs := ios.ColorScheme()
	fmt.Fprintln(ios.Out, cs.Bold(fullName))
	branch := r.DefaultBranch
	if branch == "" {
		branch = "(empty)"
	}
	fmt.Fprintf(ios.Out, "%s %s  %s %s\n", cs.Gray("Default branch:"), cs.Cyan(branch), cs.Gray("Size:"), shared.HumanSize(r.Size))
	if p := r.ParentRepository; p != nil {
		fmt.Fprintf(ios.Out, "%s %s/%s\n", cs.Gray("Fork of"), p.Project, p.Name)
	}
	if r.IsDisabled {
		fmt.Fprintln(ios.Out, cs.Yellow("Disabled"))
	}
	fmt.Fprintf(ios.Out, "%s %s\n%s %s\n\n", cs.Gray("Clone (HTTPS):"), r.RemoteURL, cs.Gray("Clone (SSH):  "), r.SSHURL)
	fmt.Fprintf(ios.Out, "%s\n\n", strings.TrimRight(body, "\n"))
	fmt.Fprintln(ios.Out, cs.Gray("View this repository on Azure DevOps: "+r.WebURL))
	return nil
}

// readme returns the root README's file name and content (BOM and CR stripped) on branch, or
// on the default branch when branch is ""; name is "" when there is none.
func readme(ctx context.Context, client *api.RepoClient, repoID, branch string) (name, content string, err error) {
	entries, err := client.RootItems(ctx, repoID, branch)
	if api.IsNotFound(err) { // e.g. an unknown --branch
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	e, ok := pickReadme(entries)
	if !ok {
		return "", "", nil
	}
	b, err := client.BlobContent(ctx, repoID, e.ObjectID)
	if err != nil {
		return "", "", err
	}
	s := strings.TrimPrefix(string(b), "\uFEFF")
	return e.Name, strings.ReplaceAll(s, "\r\n", "\n"), nil
}

// pickReadme returns the root file named README* (case-insensitive), preferring a markdown
// extension, then the first in sorted order.
func pickReadme(entries []api.TreeEntry) (api.TreeEntry, bool) {
	var best api.TreeEntry
	found := false
	better := func(e api.TreeEntry) bool {
		if !found || isMarkdown(e.Name) != isMarkdown(best.Name) {
			return !found || isMarkdown(e.Name)
		}
		return e.Name < best.Name
	}
	for _, e := range entries {
		if !e.IsFolder && strings.HasPrefix(strings.ToLower(e.Name), "readme") && better(e) {
			best, found = e, true
		}
	}
	return best, found
}

func isMarkdown(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkdown":
		return true
	}
	return false
}

// isRealTerminal reports whether w is an actual terminal (not just ADO_FORCE_TTY): glamour's
// background detection queries the terminal and must not run otherwise.
var isRealTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && ghterm.IsTerminal(f)
}

// markdownStyle picks the glamour style: notty without colour, else GLAMOUR_STYLE, else the
// detected dark/light background on a real terminal, else dark.
func markdownStyle(ios *iostreams.IOStreams) string {
	if !ios.ColorEnabled() {
		return "notty"
	}
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" && s != "auto" {
		return s
	}
	if isRealTerminal(ios.Out) {
		return "auto"
	}
	return "dark"
}

func renderMarkdown(ios *iostreams.IOStreams, md string) (string, error) {
	tr, err := glamour.NewTermRenderer(
		glamour.WithStylePath(markdownStyle(ios)),
		glamour.WithWordWrap(min(ios.TerminalWidth(), 120)),
		glamour.WithEmoji(),
		glamour.WithPreservedNewLines(),
		glamour.WithTableWrap(false),
	)
	if err != nil {
		return "", err
	}
	return tr.Render(md)
}
