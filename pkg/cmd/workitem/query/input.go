package query

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// selectRE matches text that runs as inline WIQL rather than a saved query path.
var selectRE = regexp.MustCompile(`(?i)^\s*select\s`)

// queryInput is a resolved query: inline WIQL, or a saved query (Saved != nil).
type queryInput struct {
	WIQL string
	// Saved is the saved query, with its WIQL and query type.
	Saved *api.QueryItem
}

// resolveInput turns the argument or --wiql-file into a query. SELECT text and --wiql-file
// content (a path, or "-" for in) are WIQL. Anything else is a saved query ID (GUID) or path
// such as "Shared Queries/Bugs"; one GetQuery call takes either and returns the query's WIQL
// and type (link queries need the type before running, to leave out $top).
func resolveInput(ctx context.Context, c *api.WorkItemClient, in io.Reader, arg, wiqlFile string) (*queryInput, error) {
	if wiqlFile != "" {
		var b []byte
		var err error
		if wiqlFile == "-" {
			b, err = io.ReadAll(in)
		} else {
			b, err = os.ReadFile(wiqlFile)
		}
		if err != nil {
			return nil, fmt.Errorf("reading the WIQL: %w", err)
		}
		if strings.TrimSpace(string(b)) == "" {
			return nil, cmdutil.FlagErrorf("the --wiql-file query is empty")
		}
		return &queryInput{WIQL: string(b)}, nil
	}
	if selectRE.MatchString(arg) {
		return &queryInput{WIQL: arg}, nil
	}
	idOrPath := strings.Trim(strings.TrimSpace(arg), "/")
	if idOrPath == "" {
		return nil, cmdutil.FlagErrorf("a WIQL query, saved query path or query ID is required")
	}
	q, err := c.Query(ctx, idOrPath, 0, true)
	if err != nil {
		return nil, err
	}
	if q.IsFolder {
		return nil, fmt.Errorf("%q is a query folder, not a query; list its queries with `ado workitem query list --folder %q`", q.Path, q.Path)
	}
	return &queryInput{WIQL: q.Wiql, Saved: q}, nil
}

// teamFor returns the team whose context team macros (@CurrentIteration, @TeamAreas) use:
// flag, else the `team` config value for project. "" leaves the team out of the route, so the
// service uses the project's default team without the extra lookup shared.ResolveTeam makes.
func teamFor(cfg cmdutil.Config, project adocontext.Repo, flag string) string {
	if flag != "" {
		return flag
	}
	return cfg.Get(config.ProjectScope(project.Org, project.Project), "team")
}

// runQuery runs in in the context of team, returning at most top results. Macros are passed
// through to the service.
func runQuery(ctx context.Context, c *api.WorkItemClient, in *queryInput, team string, top int) (*api.QueryResult, error) {
	if in.Saved != nil {
		return c.QueryByID(ctx, in.Saved.ID, team, top)
	}
	return c.QueryWIQL(ctx, in.WIQL, team, top)
}
