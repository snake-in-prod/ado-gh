package shared

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// selectRunLimit is how many recent runs the run prompt offers.
const selectRunLimit = 10

// Errors never include the ref: a URL's userinfo may hold a PAT.
var (
	errInvalidPipeline = errors.New("invalid pipeline: expected an ID, a name, a folder path (\\Folder\\Name or Folder/Name) or a URL such as https://dev.azure.com/ORG/PROJECT/_build?definitionId=ID")
	errInvalidRun      = errors.New("invalid run: expected an ID or a URL such as https://dev.azure.com/ORG/PROJECT/_build/results?buildId=ID")
)

// PipelineRef is a parsed pipeline argument: an ID, or a name in an optional folder.
type PipelineRef struct {
	ID   int
	Name string
	// Folder is the normalized folder (`\` for the root) when the ref names one, else "" for any.
	Folder string
	// Project is the project a URL names; empty otherwise.
	Project adocontext.Repo
}

// ParsePipelineRef parses a pipeline argument: a definition ID, a name, a folder path
// (`\Folder\Name` or `Folder/Name`), or a pipeline URL (_build?definitionId=N,
// _build/index?definitionId=N, or REST _apis/build/definitions/N).
func ParsePipelineRef(ref string) (PipelineRef, error) {
	if strings.Contains(ref, "://") {
		def, _, project, ok := parseBuildURL(ref)
		if !ok || def == 0 {
			return PipelineRef{}, errInvalidPipeline
		}
		return PipelineRef{ID: def, Project: project}, nil
	}
	if id, ok := parseID(ref); ok {
		return PipelineRef{ID: id}, nil
	}
	var r PipelineRef
	if strings.ContainsAny(ref, `\/`) {
		path := NormalizeFolder(ref)
		i := strings.LastIndex(path, `\`)
		r.Folder, r.Name = NormalizeFolder(path[:i]), path[i+1:]
	} else {
		r.Name = ref
	}
	if strings.TrimSpace(r.Name) == "" {
		return PipelineRef{}, errInvalidPipeline
	}
	return r, nil
}

// NormalizeFolder returns folder in the form the Build API uses: `\` separators, a leading `\`
// and no trailing one, so "A/B" and `\A\B\` become `\A\B`, and "" the root `\`.
func NormalizeFolder(folder string) string {
	return `\` + strings.Trim(strings.ReplaceAll(folder, "/", `\`), `\`)
}

// ResolvePipeline returns the definition ID ref names in c's project (for a URL ref, build c
// for ref.Project). A name is sent as the server's name filter, then matched exactly, ignoring
// case, together with the folder when the ref gives one. Names are unique only per folder, so
// a name found in several folders errors listing them.
func ResolvePipeline(ctx context.Context, c *api.PipelineClient, ref PipelineRef) (int, error) {
	if ref.ID > 0 {
		return ref.ID, nil
	}
	defs, err := c.Definitions(ctx, build.GetDefinitionsArgs{Name: &ref.Name})
	if err != nil {
		return 0, err
	}
	var found []build.BuildDefinitionReference
	for _, d := range defs {
		if strings.EqualFold(prshared.Deref(d.Name), ref.Name) &&
			(ref.Folder == "" || strings.EqualFold(NormalizeFolder(prshared.Deref(d.Path)), ref.Folder)) {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("no pipeline with that name in project %s; list them with: ado pipeline list -d", c.Project)
	case 1:
		return prshared.Deref(found[0].Id), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "several pipelines in project %s have that name; pass an ID or a folder path:", c.Project)
	for _, d := range found {
		fmt.Fprintf(&b, "\n  %s (ID %d)", strings.TrimSuffix(NormalizeFolder(prshared.Deref(d.Path)), `\`)+`\`+prshared.Deref(d.Name), prshared.Deref(d.Id))
	}
	return 0, errors.New(b.String())
}

// ParseRunRef parses a run argument: a run (build) ID or a run URL (_build/results?buildId=N,
// _build/index?buildid=N, or REST _apis/build/builds/N or _apis/pipelines/P/runs/N). For a
// URL it also returns the project it names; otherwise the project is empty.
func ParseRunRef(ref string) (int, adocontext.Repo, error) {
	if strings.Contains(ref, "://") {
		_, run, project, ok := parseBuildURL(ref)
		if !ok || run == 0 {
			return 0, adocontext.Repo{}, errInvalidRun
		}
		return run, project, nil
	}
	if id, ok := parseID(ref); ok {
		return id, adocontext.Repo{}, nil
	}
	return 0, adocontext.Repo{}, errInvalidRun
}

// SelectRun picks the run to act on when no <run> argument is given. On a terminal it asks
// which of the newest runs matching args (e.g. a definition and branch, or in-progress runs)
// to use; otherwise it errors naming the missing argument, before any request.
func SelectRun(ctx context.Context, f *cmdutil.Factory, c *api.PipelineClient, args build.GetBuildsArgs) (int, error) {
	if !f.IOStreams.CanPrompt() {
		return 0, cmdutil.FlagErrorf("<run> argument required when not running interactively")
	}
	top := selectRunLimit
	args.Top = &top
	runs, _, err := c.Builds(ctx, args)
	if err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, errors.New("no recent runs found; pass a run ID")
	}
	cs := f.IOStreams.ColorScheme()
	opts := make([]string, len(runs))
	for i, r := range runs {
		mark, _ := StatusMark(cs, string(prshared.Deref(r.Status)), string(prshared.Deref(r.Result)))
		var def string
		if r.Definition != nil {
			def = prshared.Deref(r.Definition.Name)
		}
		opts[i] = fmt.Sprintf("%s %s %s [%s] ID %d", mark, def, prshared.Deref(r.BuildNumber),
			strings.TrimPrefix(prshared.Deref(r.SourceBranch), "refs/heads/"), prshared.Deref(r.Id))
	}
	i, err := f.Prompter.Select("Select a run", opts)
	if err != nil {
		return 0, err
	}
	return prshared.Deref(runs[i].Id), nil
}

// parseBuildURL returns the definition and run IDs a pipeline or run URL names (0 when absent)
// and its project. Query keys match ignoring case and other parameters are ignored.
func parseBuildURL(ref string) (def, run int, project adocontext.Repo, ok bool) {
	u, err := url.Parse(ref)
	if err != nil {
		return 0, 0, adocontext.Repo{}, false
	}
	project, rest, ok := adocontext.SplitOrgURL(u)
	if !ok || project.Project == "" {
		return 0, 0, adocontext.Repo{}, false
	}
	n := len(rest)
	is := func(i int, s string) bool { return strings.EqualFold(rest[i], s) }
	switch {
	case is(0, "_build") && n <= 2: // _build, _build/index, _build/results, ...
		q := map[string]string{}
		for k, v := range u.Query() {
			q[strings.ToLower(k)] = v[0]
		}
		def, _ = parseID(q["definitionid"])
		run, _ = parseID(q["buildid"])
	case n == 4 && is(0, "_apis") && is(1, "build") && is(2, "definitions"):
		def, _ = parseID(rest[3])
	case n == 4 && is(0, "_apis") && is(1, "build") && is(2, "builds"):
		run, _ = parseID(rest[3])
	case n == 5 && is(0, "_apis") && is(1, "pipelines") && is(3, "runs"):
		run, _ = parseID(rest[4])
	}
	return def, run, project, true
}

// parseID parses a positive decimal ID without sign; it returns 0 and false for anything else.
func parseID(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || s[0] == '+' {
		return 0, false
	}
	return n, true
}
