package shared

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// DefaultDefinition resolves the pipeline of the current repository repoName: the project's
// definitions built from that Azure Repos Git repository, disabled ones excluded (paused ones can
// still be queued manually). One is used; several prompt on a TTY and otherwise fail listing
// them; none fails suggesting a pipeline argument.
func DefaultDefinition(ctx context.Context, f *cmdutil.Factory, c *api.PipelineClient, repoName string) (build.BuildDefinitionReference, error) {
	var none build.BuildDefinitionReference
	if repoName == "" {
		return none, errors.New("no current repository; pass a pipeline name or ID as an argument")
	}
	repos := &api.RepoClient{Git: &git.ClientImpl{Client: *c.SDK}, SDK: c.SDK, Project: c.Project}
	repo, err := repos.Repository(ctx, repoName)
	if err != nil {
		return none, err
	}
	repoType := "TfsGit"
	defs, err := c.Definitions(ctx, build.GetDefinitionsArgs{RepositoryId: &repo.ID, RepositoryType: &repoType})
	if err != nil {
		return none, err
	}
	defs = slices.DeleteFunc(defs, func(d build.BuildDefinitionReference) bool {
		return d.QueueStatus != nil && *d.QueueStatus == build.DefinitionQueueStatusValues.Disabled
	})
	switch len(defs) {
	case 0:
		return none, fmt.Errorf("no pipeline uses repository %s; pass a pipeline name or ID as an argument", repo.Name)
	case 1:
		return defs[0], nil
	}

	// Names are unique only within a folder, so label each with its folder (root omitted).
	labels := make([]string, len(defs))
	for i, d := range defs {
		labels[i] = strings.TrimLeft(prshared.Deref(d.Path)+`\`+prshared.Deref(d.Name), `\`)
	}
	if !f.IOStreams.CanPrompt() {
		return none, fmt.Errorf("several pipelines use repository %s; pass one as an argument:\n  %s", repo.Name, strings.Join(labels, "\n  "))
	}
	i, err := f.Prompter.Select("Which pipeline?", labels)
	if err != nil {
		return none, err
	}
	return defs[i], nil
}
