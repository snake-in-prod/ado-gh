package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
)

// ReleaseClient calls the Classic Release API of one project. Release has its own host
// (vsrm.dev.azure.com/{org}, or {org}.vsrm.visualstudio.com), found through the org's
// ResourceAreas on first use; an org listing no Release area (Azure DevOps Server) is served
// from OrgURL itself.
type ReleaseClient struct {
	HTTP *http.Client
	// OrgURL is the organization URL.
	OrgURL  string
	Project string
	// Release is the typed client, set on first use; tests may set it beforehand.
	Release release.Client
}

// NewReleaseClient returns a ReleaseClient for project in the organization at orgURL, sending
// requests through hc. Nothing is sent until the first call.
func NewReleaseClient(hc *http.Client, orgURL, project string) *ReleaseClient {
	return &ReleaseClient{HTTP: hc, OrgURL: orgURL, Project: project}
}

// client returns the typed client, resolving the Release host on first use.
func (c *ReleaseClient) client(ctx context.Context) (release.Client, error) {
	if c.Release == nil {
		sdk, _, err := areaClient(ctx, c.HTTP, c.OrgURL, release.ResourceAreaId)
		if err != nil {
			return nil, mapErr(err)
		}
		c.Release = &release.ClientImpl{Client: *sdk}
	}
	return c.Release, nil
}

// pageAll collects the items fetch returns for each page, passing back the continuation token
// of the previous page ("" for the first), until a page has no token or limit items are in hand
// (limit <= 0: every page).
func pageAll[T any](limit int, fetch func(token string) ([]T, string, error)) ([]T, error) {
	var out []T
	token := ""
	for {
		page, next, err := fetch(token)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		if next == "" {
			return out, nil
		}
		token = next
	}
}

// intToken converts a continuation token, which the SDK reads from the response header as a
// string, to the int the releases and approvals endpoints take back; "" means the first page.
func intToken(token string) (*int, error) {
	if token == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(token)
	if err != nil {
		return nil, fmt.Errorf("unexpected continuation token %q from the release API", token)
	}
	return &n, nil
}

// topFor returns top, or the limit when top is unset and there is one.
func topFor(top *int, limit int) *int {
	if top == nil && limit > 0 {
		return &limit
	}
	return top
}

// Releases returns the releases matching args (newest first by default), at most limit (<= 0:
// all), following continuation tokens. Without $expand they carry no environments.
func (c *ReleaseClient) Releases(ctx context.Context, args release.GetReleasesArgs, limit int) ([]release.Release, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	args.Project, args.Top = &c.Project, topFor(args.Top, limit)
	return pageAll(limit, func(token string) ([]release.Release, string, error) {
		if args.ContinuationToken, err = intToken(token); err != nil {
			return nil, "", err
		}
		page, err := rc.GetReleases(ctx, args)
		if err != nil {
			return nil, "", mapErr(err)
		}
		return page.Value, page.ContinuationToken, nil
	})
}

// ReleaseByID fetches release id with its environments, deploy steps (down to the tasks of
// each deployment job) and approvals.
func (c *ReleaseClient) ReleaseByID(ctx context.Context, id int) (*release.Release, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	expand := release.SingleReleaseExpandsValues.Tasks
	r, err := rc.GetRelease(ctx, release.GetReleaseArgs{Project: &c.Project, ReleaseId: &id, Expand: &expand})
	return r, mapErr(err)
}

// Definitions returns the release definitions matching args, at most limit (<= 0: all),
// following continuation tokens.
func (c *ReleaseClient) Definitions(ctx context.Context, args release.GetReleaseDefinitionsArgs, limit int) ([]release.ReleaseDefinition, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	args.Project, args.Top = &c.Project, topFor(args.Top, limit)
	return pageAll(limit, func(token string) ([]release.ReleaseDefinition, string, error) {
		args.ContinuationToken = nil
		if token != "" {
			args.ContinuationToken = &token
		}
		page, err := rc.GetReleaseDefinitions(ctx, args)
		if err != nil {
			return nil, "", mapErr(err)
		}
		return page.Value, page.ContinuationToken, nil
	})
}

// Approvals returns the release approvals matching args (pending ones by default), at most
// limit (<= 0: all), following continuation tokens.
func (c *ReleaseClient) Approvals(ctx context.Context, args release.GetApprovalsArgs, limit int) ([]release.ReleaseApproval, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	args.Project, args.Top = &c.Project, topFor(args.Top, limit)
	return pageAll(limit, func(token string) ([]release.ReleaseApproval, string, error) {
		if args.ContinuationToken, err = intToken(token); err != nil {
			return nil, "", err
		}
		page, err := rc.GetApprovals(ctx, args)
		if err != nil {
			return nil, "", mapErr(err)
		}
		return page.Value, page.ContinuationToken, nil
	})
}

// UpdateApproval sets release approval id to status, recording comment (if any).
func (c *ReleaseClient) UpdateApproval(ctx context.Context, id int, status release.ApprovalStatus, comment string) (*release.ReleaseApproval, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	a := &release.ReleaseApproval{Status: &status}
	if comment != "" {
		a.Comments = &comment
	}
	got, err := rc.UpdateReleaseApproval(ctx, release.UpdateReleaseApprovalArgs{Project: &c.Project, ApprovalId: &id, Approval: a})
	return got, mapErr(err)
}

// TaskLog opens the plain-text log of task taskID (ReleaseTask.Id) in deploy phase phaseID
// (ReleaseDeployPhase.Id, not its phaseId) of environment envID of release releaseID. The
// caller closes it.
func (c *ReleaseClient) TaskLog(ctx context.Context, releaseID, envID, phaseID, taskID int) (io.ReadCloser, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	r, err := rc.GetTaskLog(ctx, release.GetTaskLogArgs{
		Project: &c.Project, ReleaseId: &releaseID, EnvironmentId: &envID, ReleaseDeployPhaseId: &phaseID, TaskId: &taskID,
	})
	return r, mapErr(err)
}

// Logs opens a zip of every log of release releaseID. The caller closes it.
func (c *ReleaseClient) Logs(ctx context.Context, releaseID int) (io.ReadCloser, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	r, err := rc.GetLogs(ctx, release.GetLogsArgs{Project: &c.Project, ReleaseId: &releaseID})
	return r, mapErr(err)
}
