package shared

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/pflag"
)

const (
	hyperlinkRel = "Hyperlink"
	artifactRel  = "ArtifactLink"
	relatedRel   = "System.LinkTypes.Related"
	// ParentRel is the parent link type; a work item can have only one parent.
	ParentRel = "System.LinkTypes.Hierarchy-Reverse"
	// ChildRel is the child link type.
	ChildRel = "System.LinkTypes.Hierarchy-Forward"
)

// shaRE matches a full or abbreviated commit SHA.
var shaRE = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// Link is a relation to add to or remove from a work item.
type Link struct {
	Rel string
	URL string
	// ID is the linked work item's ID; 0 for a hyperlink or an artifact.
	ID int
	// Attributes go with an added relation, e.g. an artifact link's name; nil for none.
	Attributes map[string]any
}

// LinkParser turns link targets into Links: work item links (types looked up in the
// organization's relation types on first use), hyperlinks, and pull request, commit and branch
// artifact links in the current (or -R) repository of F, resolved on first use.
type LinkParser struct {
	F      *cmdutil.Factory
	Client *api.WorkItemClient
	// OrgURL is the organization URL, ending in "/", work item link URLs are built on.
	OrgURL string

	types             []api.RelationType
	git               *api.PRClient
	projectID, repoID string
}

// ParseArg parses a `<type>:<target>` link argument (edit --add-link/--remove-link) with Parse.
func (p *LinkParser) ParseArg(ctx context.Context, arg string) (Link, error) {
	name, target, ok := strings.Cut(arg, ":")
	name, target = strings.TrimSpace(name), strings.TrimSpace(target)
	if !ok || name == "" || target == "" {
		return Link{}, fmt.Errorf("invalid link %q: expected <type>:<target>, e.g. related:123", arg)
	}
	return p.Parse(ctx, name, target)
}

// Parse returns the link of type typ (any case) to target: pr (a pull request number, !number,
// URL or branch), commit (a full or abbreviated SHA), branch (a name), url/hyperlink (a URL),
// or a work item link type by friendly or reference name with a work item reference.
func (p *LinkParser) Parse(ctx context.Context, typ, target string) (Link, error) {
	switch strings.ToLower(typ) {
	case "url", strings.ToLower(hyperlinkRel):
		if !strings.Contains(target, "://") {
			return Link{}, fmt.Errorf("invalid link %s:%s: a hyperlink needs a URL", typ, target)
		}
		return Link{Rel: hyperlinkRel, URL: target}, nil
	case "pr":
		return p.pullRequest(ctx, target)
	case "commit":
		return p.commit(ctx, target)
	case "branch":
		if err := p.loadRepo(ctx); err != nil {
			return Link{}, err
		}
		return artifact(api.BranchArtifactURL(p.projectID, p.repoID, target), "Branch"), nil
	}
	if p.types == nil {
		types, err := p.Client.RelationTypes(ctx)
		if err != nil {
			return Link{}, err
		}
		p.types = types
	}
	i := slices.IndexFunc(p.types, func(t api.RelationType) bool {
		return t.Usage == "workItemLink" && (strings.EqualFold(t.Name, typ) || strings.EqualFold(t.ReferenceName, typ))
	})
	if i < 0 {
		var names []string
		for _, t := range p.types {
			if t.Usage == "workItemLink" {
				names = append(names, t.Name)
			}
		}
		return Link{}, fmt.Errorf("unknown link type %q; valid types: %s, pr, commit, branch, url", typ, strings.Join(names, ", "))
	}
	id, _, err := ParseRef(target)
	if err != nil {
		return Link{}, err
	}
	return Link{Rel: p.types[i].ReferenceName, URL: p.OrgURL + "_apis/wit/workItems/" + strconv.Itoa(id), ID: id}, nil
}

func artifact(url, name string) Link {
	return Link{Rel: artifactRel, URL: url, Attributes: map[string]any{"name": name}}
}

func (p *LinkParser) pullRequest(ctx context.Context, ref string) (Link, error) {
	if err := p.needRepo(); err != nil {
		return Link{}, err
	}
	pr, _, _, err := prshared.Find(ctx, p.F, ref)
	if err != nil {
		return Link{}, err
	}
	id, r := prshared.Deref(pr.PullRequestId), pr.Repository
	if r == nil || r.Id == nil || r.Project == nil || r.Project.Id == nil {
		return Link{}, fmt.Errorf("cannot link pull request !%d: its repository and project IDs are unknown", id)
	}
	return artifact(api.PullRequestArtifactURL(r.Project.Id.String(), r.Id.String(), id), "Pull Request"), nil
}

// commit links sha, resolving an abbreviated SHA to 40 characters (as the web UI stores them)
// with local git. The Git API only accepts full SHAs, so outside a clone of the repository
// (or with -R) a short SHA is an error.
func (p *LinkParser) commit(ctx context.Context, sha string) (Link, error) {
	if !shaRE.MatchString(sha) {
		return Link{}, fmt.Errorf("invalid commit %q: expected a SHA of 4 to 40 hex characters", sha)
	}
	if err := p.loadRepo(ctx); err != nil {
		return Link{}, err
	}
	full := sha
	if len(sha) < 40 {
		var err error
		if full, err = p.resolveCommit(ctx, sha); err != nil {
			return Link{}, err
		}
	}
	return artifact(api.CommitArtifactURL(p.projectID, p.repoID, strings.ToLower(full)), "Fixed in Commit"), nil
}

func (p *LinkParser) resolveCommit(ctx context.Context, sha string) (string, error) {
	if !p.F.BaseRepoFromGit() {
		return "", fmt.Errorf("cannot resolve short commit %s without a local clone; pass the full 40-character SHA", sha)
	}
	out, err := p.F.GitClient.Exec(ctx, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if full := strings.TrimSpace(string(out)); err == nil && len(full) == 40 {
		return full, nil
	}
	return "", fmt.Errorf("commit %s not found in the local clone; fetch it or pass the full 40-character SHA", sha)
}

// needRepo refuses pull request, commit and branch links when -R names only ORG/PROJECT, which
// project-scoped commands accept but which has no repository (BaseRepo would misread it as
// PROJECT/REPO).
func (p *LinkParser) needRepo() error {
	if strings.Count(p.F.RepoOverride, "/") == 1 {
		return cmdutil.FlagErrorf("pull request, commit and branch links need a repository, and -R %s names only ORG/PROJECT; "+
			"use -R ORG/PROJECT/REPO or run inside a clone", p.F.RepoOverride)
	}
	return nil
}

// loadRepo looks up the GUIDs of the base repository and its project once.
func (p *LinkParser) loadRepo(ctx context.Context) error {
	if p.git != nil {
		return nil
	}
	if err := p.needRepo(); err != nil {
		return err
	}
	repo, err := p.F.BaseRepo()
	if err != nil {
		return err
	}
	c, err := prshared.NewClient(ctx, p.F, repo)
	if err != nil {
		return err
	}
	r, err := c.Repository(ctx)
	if err != nil {
		return err
	}
	if r.Id == nil || r.Project == nil || r.Project.Id == nil {
		return fmt.Errorf("cannot link: the IDs of repository %s/%s are unknown", repo.Project, repo.Name)
	}
	p.git, p.projectID, p.repoID = c, r.Project.Id.String(), r.Id.String()
	return nil
}

// LinkFlags are the link target flags shared by `workitem link` and `unlink`.
type LinkFlags struct {
	Parent                                                 string
	Children, Related, Links, PRs, Commits, Branches, URLs []string
}

// Register adds the flags to fl.
func (l *LinkFlags) Register(fl *pflag.FlagSet) {
	fl.StringVar(&l.Parent, "parent", "", "Parent work item")
	fl.StringArrayVar(&l.Children, "child", nil, "Child work item (repeatable)")
	fl.StringArrayVar(&l.Related, "related", nil, "Related work item (repeatable)")
	fl.StringArrayVar(&l.Links, "link", nil, "Work item link of any type: <type>=<id>, e.g. Predecessor=12 (repeatable)")
	fl.StringArrayVar(&l.PRs, "pr", nil, "Pull request of the repository: number, URL or branch (repeatable)")
	fl.StringArrayVar(&l.Commits, "commit", nil, "Commit SHA of the repository (repeatable)")
	fl.StringArrayVar(&l.Branches, "branch", nil, "Branch of the repository (repeatable)")
	fl.StringArrayVar(&l.URLs, "url", nil, "Hyperlink URL (repeatable)")
}

// Parse returns the links the flags name, parent first; none is a flag error.
func (l *LinkFlags) Parse(ctx context.Context, p *LinkParser) ([]Link, error) {
	var targets [][2]string
	add := func(typ string, values ...string) {
		for _, v := range values {
			targets = append(targets, [2]string{typ, v})
		}
	}
	if l.Parent != "" {
		add(ParentRel, l.Parent)
	}
	add(ChildRel, l.Children...)
	add(relatedRel, l.Related...)
	for _, s := range l.Links {
		typ, id, ok := strings.Cut(s, "=")
		if typ, id = strings.TrimSpace(typ), strings.TrimSpace(id); !ok || typ == "" || id == "" {
			return nil, cmdutil.FlagErrorf("invalid --link %q: expected <type>=<id>, e.g. Predecessor=12", s)
		}
		add(typ, id)
	}
	add("pr", l.PRs...)
	add("commit", l.Commits...)
	add("branch", l.Branches...)
	add("url", l.URLs...)
	if len(targets) == 0 {
		return nil, cmdutil.FlagErrorf("specify a link: --parent, --child, --related, --link, --pr, --commit, --branch or --url")
	}
	out := make([]Link, 0, len(targets))
	for _, t := range targets {
		link, err := p.Parse(ctx, t[0], t[1])
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, nil
}

// Matches reports whether r is this link: the same type and, for a work item link, a URL
// ending in /workItems/{ID} (hosts and casing differ), else the same URL.
func (l Link) Matches(r api.WorkItemRelation) bool {
	if !strings.EqualFold(r.Rel, l.Rel) {
		return false
	}
	if l.ID > 0 {
		return strings.HasSuffix(strings.ToLower(r.URL), "/workitems/"+strconv.Itoa(l.ID))
	}
	return strings.EqualFold(r.URL, l.URL)
}

// String is the link as `<type>:<target>`.
func (l Link) String() string {
	if l.ID > 0 {
		return l.Rel + ":" + strconv.Itoa(l.ID)
	}
	return l.Rel + ":" + l.URL
}

// AddOp is the JSON Patch op adding the link.
func (l Link) AddOp() webapi.JsonPatchOperation {
	value := map[string]any{"rel": l.Rel, "url": l.URL}
	if l.Attributes != nil {
		value["attributes"] = l.Attributes
	}
	op, path := webapi.OperationValues.Add, "/relations/-"
	return webapi.JsonPatchOperation{Op: &op, Path: &path, Value: value}
}

// RemoveOps returns the JSON Patch ops removing the relation of rels each link matches,
// highest index first so the indexes stay valid. A link with no match is an error naming
// work item id.
func RemoveOps(id int, rels []api.WorkItemRelation, links []Link) ([]webapi.JsonPatchOperation, error) {
	var remove []int
	for _, l := range links {
		i := -1
		for j, r := range rels {
			if l.Matches(r) && !slices.Contains(remove, j) {
				i = j
				break
			}
		}
		if i < 0 {
			return nil, fmt.Errorf("work item %d has no %s link", id, l)
		}
		remove = append(remove, i)
	}
	slices.Sort(remove)
	slices.Reverse(remove)
	ops := make([]webapi.JsonPatchOperation, 0, len(remove))
	for _, i := range remove {
		op, path := webapi.OperationValues.Remove, "/relations/"+strconv.Itoa(i)
		ops = append(ops, webapi.JsonPatchOperation{Op: &op, Path: &path})
	}
	return ops, nil
}

// LinkTarget is the work item `link` or `unlink` changes, with a LinkParser for its project.
type LinkTarget struct {
	ID      int
	Project adocontext.Repo
	Parser  *LinkParser
}

// NewLinkTarget resolves ref (an ID, #ID or URL) to a LinkTarget; an ID is in the current project.
func NewLinkTarget(ctx context.Context, f *cmdutil.Factory, ref string) (*LinkTarget, error) {
	id, project, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	if project.Project == "" {
		if project, err = f.BaseProject(); err != nil {
			return nil, err
		}
	}
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return nil, err
	}
	orgURL := authshared.OrgURL(project.Host, project.Org)
	return &LinkTarget{ID: id, Project: project, Parser: &LinkParser{
		F: f, Client: api.NewWorkItemClient(hc, orgURL, project.Project), OrgURL: orgURL,
	}}, nil
}
