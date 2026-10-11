package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

// templatesLocation is the undocumented git templates resource (gitignore templates), absent
// from the Go SDK; released at api-version 7.1.
var templatesLocation = uuid.MustParse("f88d498e-52c3-422a-a5f2-994f4265a25b")

// RepoClient calls the Git repository APIs of one project. An empty Project lists the
// repositories of the whole organization.
type RepoClient struct {
	Git git.Client
	// SDK sends the requests git.Client has no method for.
	SDK     *azuredevops.Client
	Project string
}

// NewRepoClient returns a RepoClient for project in the organization at orgURL, sending
// requests through hc.
func NewRepoClient(hc *http.Client, orgURL, project string) *RepoClient {
	sdk := NewSDKClient(hc, orgURL)
	return &RepoClient{Git: &git.ClientImpl{Client: *sdk}, SDK: sdk, Project: project}
}

// Repository is the public `--json` shape of a repository; absent API fields are zero values.
type Repository struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	// DefaultBranch is the branch name without "refs/heads/"; "" for an empty repository.
	DefaultBranch string `json:"defaultBranch"`
	// Size is the compressed size in bytes.
	Size       uint64 `json:"size"`
	IsDisabled bool   `json:"isDisabled"`
	IsFork     bool   `json:"isFork"`
	// ParentRepository is the fork parent; only Repository fills it.
	ParentRepository *ParentRepository `json:"parentRepository"`
	RemoteURL        string            `json:"remoteUrl"`
	SSHURL           string            `json:"sshUrl"`
	WebURL           string            `json:"webUrl"`
}

// ParentRepository is the repository a fork was made from.
type ParentRepository struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Project   string `json:"project"`
	RemoteURL string `json:"remoteUrl"`
	SSHURL    string `json:"sshUrl"`
}

// TreeEntry is one file or folder of a repository's root.
type TreeEntry struct {
	Name     string
	ObjectID string
	IsFolder bool
}

// DeletedRepository is a repository in the project's recycle bin.
type DeletedRepository struct {
	ID   string
	Name string
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func idString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func projectName(p *core.TeamProjectReference) string {
	if p == nil {
		return ""
	}
	return deref(p.Name)
}

func toRepository(r git.GitRepository) Repository {
	out := Repository{
		ID: idString(r.Id), Name: deref(r.Name), Project: projectName(r.Project),
		DefaultBranch: strings.TrimPrefix(deref(r.DefaultBranch), "refs/heads/"),
		Size:          deref(r.Size), IsDisabled: deref(r.IsDisabled), IsFork: deref(r.IsFork),
		RemoteURL: deref(r.RemoteUrl), SSHURL: deref(r.SshUrl), WebURL: deref(r.WebUrl),
	}
	if p := r.ParentRepository; p != nil {
		out.ParentRepository = &ParentRepository{
			ID: idString(p.Id), Name: deref(p.Name), Project: projectName(p.Project),
			RemoteURL: deref(p.RemoteUrl), SSHURL: deref(p.SshUrl),
		}
	}
	return out
}

func (c *RepoClient) project() *string {
	if c.Project == "" {
		return nil
	}
	return &c.Project
}

// ListRepositories returns every repository of the project, or of the organization when
// Project is empty. The API doesn't page; filter, sort and limit client-side.
func (c *RepoClient) ListRepositories(ctx context.Context) ([]Repository, error) {
	out, err := c.Git.GetRepositories(ctx, git.GetRepositoriesArgs{Project: c.project()})
	if err != nil {
		return nil, mapErr(err)
	}
	repos := make([]Repository, 0, len(*out))
	for _, r := range *out {
		repos = append(repos, toRepository(r))
	}
	return repos, nil
}

// Repository fetches the repository nameOrID of the project, with its fork parent.
func (c *RepoClient) Repository(ctx context.Context, nameOrID string) (Repository, error) {
	parent := true
	out, err := c.Git.GetRepositoryWithParent(ctx, git.GetRepositoryWithParentArgs{
		RepositoryId: &nameOrID, IncludeParent: &parent, Project: c.project(),
	})
	if err != nil {
		return Repository{}, mapErr(err)
	}
	return toRepository(*out), nil
}

// CreateRepository creates the empty repository name in the project.
func (c *RepoClient) CreateRepository(ctx context.Context, name string) (Repository, error) {
	out, err := c.Git.CreateRepository(ctx, git.CreateRepositoryArgs{
		GitRepositoryToCreate: &git.GitRepositoryCreateOptions{Name: &name}, Project: c.project(),
	})
	if err != nil {
		return Repository{}, mapErr(err)
	}
	return toRepository(*out), nil
}

// RootItems lists the files and folders at the root of repo on branch (with or without
// "refs/heads/"), or on the default branch when branch is "".
func (c *RepoClient) RootItems(ctx context.Context, repo, branch string) ([]TreeEntry, error) {
	scope := "/"
	args := git.GetItemsArgs{
		RepositoryId: &repo, Project: c.project(), ScopePath: &scope,
		RecursionLevel: &git.VersionControlRecursionTypeValues.OneLevel,
	}
	if branch != "" {
		branch = strings.TrimPrefix(branch, "refs/heads/")
		args.VersionDescriptor = &git.GitVersionDescriptor{Version: &branch, VersionType: &git.GitVersionTypeValues.Branch}
	}
	items, err := c.Git.GetItems(ctx, args)
	if err != nil {
		return nil, mapErr(err)
	}
	var out []TreeEntry
	for _, it := range *items {
		name := strings.TrimPrefix(deref(it.Path), "/")
		if name == "" { // the root folder itself
			continue
		}
		out = append(out, TreeEntry{Name: name, ObjectID: deref(it.ObjectId), IsFolder: deref(it.IsFolder)})
	}
	return out, nil
}

// BlobContent downloads the content of blob objectID (e.g. a TreeEntry's) in repo.
func (c *RepoClient) BlobContent(ctx context.Context, repo, objectID string) ([]byte, error) {
	return (&PRClient{Git: c.Git, Project: c.Project, Repo: repo}).BlobContent(ctx, objectID)
}

// GitignoreTemplates returns the names of the project's gitignore templates, e.g.
// "VisualStudio.gitignore".
func (c *RepoClient) GitignoreTemplates(ctx context.Context) ([]string, error) {
	resp, err := c.SDK.Send(ctx, http.MethodGet, templatesLocation, "7.1",
		map[string]string{"project": c.Project}, url.Values{"type": {"gitignore"}}, nil, "", "application/json", nil)
	if err != nil {
		return nil, mapErr(err)
	}
	defer resp.Body.Close()
	var templates []git.GitTemplate
	if err := c.SDK.UnmarshalCollectionBody(resp, &templates); err != nil {
		return nil, err
	}
	var names []string
	for _, t := range templates {
		if t.Name != nil {
			names = append(names, *t.Name)
		}
	}
	return names, nil
}

// InitialPush creates the first commit of the empty repository repoID on refs/heads/main,
// adding /README.md with readme and /.gitignore from gitignoreTemplate (a GitignoreTemplates
// name); an empty value skips that file.
func (c *RepoClient) InitialPush(ctx context.Context, repoID, comment, readme, gitignoreTemplate string) error {
	add := git.VersionControlChangeTypeValues.Add
	var changes []any // CommitRef.Changes is *[]interface{}
	if readme != "" {
		changes = append(changes, git.GitChange{
			ChangeType: &add, Item: map[string]string{"path": "/README.md"},
			NewContent: &git.ItemContent{Content: &readme, ContentType: &git.ItemContentTypeValues.RawText},
		})
	}
	if gitignoreTemplate != "" {
		typ := "gitignore"
		changes = append(changes, git.GitChange{
			ChangeType: &add, Item: map[string]string{"path": "/.gitignore"},
			NewContentTemplate: &git.GitTemplate{Name: &gitignoreTemplate, Type: &typ},
		})
	}
	ref, zero := "refs/heads/main", zeroObjectID
	_, err := c.Git.CreatePush(ctx, git.CreatePushArgs{
		RepositoryId: &repoID, Project: c.project(),
		Push: &git.GitPush{
			RefUpdates: &[]git.GitRefUpdate{{Name: &ref, OldObjectId: &zero}},
			Commits:    &[]git.GitCommitRef{{Comment: &comment, Changes: &changes}},
		},
	})
	return mapErr(err)
}

func parseRepoID(id string) (*uuid.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid repository ID %q", id)
	}
	return &u, nil
}

// DeleteRepository soft-deletes repository id, moving it to the project's recycle bin.
func (c *RepoClient) DeleteRepository(ctx context.Context, id string) error {
	u, err := parseRepoID(id)
	if err != nil {
		return err
	}
	return mapErr(c.Git.DeleteRepository(ctx, git.DeleteRepositoryArgs{RepositoryId: u, Project: c.project()}))
}

// PurgeRepository permanently deletes the soft-deleted repository id from the recycle bin.
func (c *RepoClient) PurgeRepository(ctx context.Context, id string) error {
	u, err := parseRepoID(id)
	if err != nil {
		return err
	}
	return mapErr(c.Git.DeleteRepositoryFromRecycleBin(ctx, git.DeleteRepositoryFromRecycleBinArgs{RepositoryId: u, Project: &c.Project}))
}

// RecycleBinRepositories lists the soft-deleted repositories of the project.
func (c *RepoClient) RecycleBinRepositories(ctx context.Context) ([]DeletedRepository, error) {
	out, err := c.Git.GetRecycleBinRepositories(ctx, git.GetRecycleBinRepositoriesArgs{Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	repos := make([]DeletedRepository, 0, len(*out))
	for _, r := range *out {
		repos = append(repos, DeletedRepository{ID: idString(r.Id), Name: deref(r.Name)})
	}
	return repos, nil
}

// RestoreRepository restores the soft-deleted repository id from the recycle bin.
func (c *RepoClient) RestoreRepository(ctx context.Context, id string) (Repository, error) {
	u, err := parseRepoID(id)
	if err != nil {
		return Repository{}, err
	}
	deleted := false
	out, err := c.Git.RestoreRepositoryFromRecycleBin(ctx, git.RestoreRepositoryFromRecycleBinArgs{
		RepositoryDetails: &git.GitRecycleBinRepositoryDetails{Deleted: &deleted}, Project: &c.Project, RepositoryId: u,
	})
	if err != nil {
		return Repository{}, mapErr(err)
	}
	return toRepository(*out), nil
}
