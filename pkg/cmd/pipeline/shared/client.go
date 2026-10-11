package shared

import (
	"context"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// NewClient returns a pipeline client for project.Project, authenticated for its organization.
// Its HTTP client sends the token only to Azure DevOps hosts, so artifact downloads redirected
// to blob storage go out without it.
func NewClient(ctx context.Context, f *cmdutil.Factory, project adocontext.Repo) (*api.PipelineClient, error) {
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return nil, err
	}
	return api.NewPipelineClient(hc, adocontext.OrgURL(project.Host, project.Org), project.Project), nil
}
