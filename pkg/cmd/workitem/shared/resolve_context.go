package shared

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// ResolveTeam returns the team for team-scoped calls: flag (--team), else the `team` config
// value for project (falling back to the global value), else the project's default team.
func ResolveTeam(ctx context.Context, c *api.WorkItemClient, cfg cmdutil.Config, project adocontext.Repo, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if t := cfg.Get(config.ProjectScope(project.Org, project.Project), "team"); t != "" {
		return t, nil
	}
	return c.DefaultTeam(ctx)
}

// ResolvePath checks path against the project's classification nodes of group ("areas" or
// "iterations") and returns it as a System.AreaPath / System.IterationPath value, e.g.
// Proj\Team A. path may start with the project name and use `\` or `/` as separator.
func ResolvePath(ctx context.Context, c *api.WorkItemClient, group, path string) (string, error) {
	segs := strings.FieldsFunc(path, func(r rune) bool { return r == '\\' || r == '/' })
	if len(segs) > 0 && strings.EqualFold(segs[0], c.Project) {
		segs = segs[1:]
	}
	// The tree is walked client-side: the SDK escapes `/` in a node path as %2F, and names
	// match case-insensitively. ponytail: reads every node down to the path's depth.
	n, err := c.ClassificationNode(ctx, group, "", len(segs))
	if err != nil {
		return "", err
	}
	for _, s := range segs {
		i := slices.IndexFunc(n.Children, func(ch api.ClassificationNode) bool { return strings.EqualFold(ch.Name, s) })
		if i < 0 {
			return "", fmt.Errorf(`%s path "%s" not found in project %s`, strings.TrimSuffix(group, "s"), path, c.Project)
		}
		n = &n.Children[i]
	}
	// Node paths are \Proj\Area\X or \Proj\Iteration\X; field values drop the group segment.
	parts := strings.Split(strings.TrimPrefix(n.Path, `\`), `\`)
	if len(parts) < 2 {
		return parts[0], nil
	}
	return strings.Join(append(parts[:1], parts[2:]...), `\`), nil
}

// ResolveIterationPath is ResolvePath for iterations, where `@current` is the current iteration
// of the team returned by team (only called for `@current`).
func ResolveIterationPath(ctx context.Context, c *api.WorkItemClient, path string, team func() (string, error)) (string, error) {
	if !strings.EqualFold(path, "@current") {
		return ResolvePath(ctx, c, "iterations", path)
	}
	t, err := team()
	if err != nil {
		return "", err
	}
	return c.CurrentIterationPath(ctx, t)
}

// ResolveIdentityFilter turns a list filter value into an IdentityFilter: `@me` as the WIQL
// @Me macro, `none` as unassigned, anything else resolved to one user through r.
func ResolveIdentityFilter(ctx context.Context, r *api.IdentityResolver, v string) (IdentityFilter, error) {
	switch {
	case strings.EqualFold(v, "@me"):
		return IdentityFilter{Me: true}, nil
	case strings.EqualFold(v, "none"):
		return IdentityFilter{None: true}, nil
	}
	u, err := r.ResolveUser(ctx, v)
	if err != nil {
		return IdentityFilter{}, err
	}
	return IdentityFilter{Value: u.FieldValue()}, nil
}
