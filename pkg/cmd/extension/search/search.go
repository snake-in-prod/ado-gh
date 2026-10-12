// Package search implements `ado extension search`.
package search

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
)

// NewCmdSearch returns `ado extension search [<query>...]`.
func NewCmdSearch(f *cmdutil.Factory) *cobra.Command {
	var owner, sort string
	var limit int
	var web bool
	var exporter *cmdutil.Exporter
	cmd := &cobra.Command{
		Use:   "search [<query>...]",
		Short: "Search GitHub for ado extensions",
		Long: `Search GitHub for repositories with the topic "` + extpkg.SearchTopic + `" matching the query.

Installed extensions are marked. Unauthenticated, GitHub allows 10 searches a minute; set
GH_TOKEN (or GITHUB_TOKEN) to a GitHub token to raise the limit.`,
		Example: `  ado extension search
  ado extension search lint --sort stars
  ado extension search --owner myco --json fullName,url
  ado extension search lint --web`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > extpkg.SearchMaxResults {
				return cmdutil.FlagErrorf("--limit must be between 1 and %d", extpkg.SearchMaxResults)
			}
			if sort != "" && sort != "stars" && sort != "updated" {
				return cmdutil.FlagErrorf("invalid --sort %q: use stars or updated", sort)
			}
			q, err := extpkg.SearchQuery(args, owner)
			if err != nil {
				return cmdutil.FlagErrorWrap(err)
			}
			if web {
				v := url.Values{"q": {q}, "type": {"repositories"}}
				if sort != "" {
					v.Set("s", sort)
					v.Set("o", "desc")
				}
				return cmdutil.OpenInBrowser(f, "https://github.com/search?"+v.Encode())
			}
			hc, err := extpkg.GitHubClient(f)
			if err != nil {
				return err
			}
			results, err := extpkg.SearchGitHub(cmd.Context(), hc, q, sort, limit)
			if err != nil {
				return err
			}
			if err := extpkg.MarkInstalled(extpkg.Dir(), results); err != nil {
				return err
			}
			ios := f.IOStreams
			if exporter != nil {
				return exporter.Write(ios, results)
			}
			if len(results) == 0 {
				if ios.IsStdoutTTY() {
					fmt.Fprintln(ios.ErrOut, "no extensions found")
				}
				return nil
			}
			tty := ios.IsStdoutTTY()
			cs := ios.ColorScheme()
			now := time.Now()
			tp := tableprinter.New(ios, "", "repo", "description", "stars", "updated")
			for _, r := range results {
				switch {
				case tty && r.Installed:
					tp.AddField("✓", tableprinter.WithColor(cs.Green))
				case tty:
					tp.AddField("")
				default:
					tp.AddField(strconv.FormatBool(r.Installed))
				}
				tp.AddField(tableprinter.OneLine(extpkg.Sanitize(r.FullName)), tableprinter.WithColor(cs.Bold))
				tp.AddField(tableprinter.OneLine(extpkg.Sanitize(r.Description)))
				tp.AddField(strconv.Itoa(r.Stars))
				tp.AddTimeField(now, r.UpdatedAt, cs.Gray)
				tp.EndRow()
			}
			return tp.Render()
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "Only repositories owned by `user` or organization")
	cmd.Flags().StringVar(&sort, "sort", "", "Sort by `field`: stars or updated (default: best match)")
	cmd.Flags().IntVarP(&limit, "limit", "L", 30, "Maximum number of results (at most 1000)")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the search in the browser")
	cmdutil.AddJSONFlags(cmd, &exporter, []string{"fullName", "description", "stars", "updatedAt", "installed", "url"})
	return cmd
}
