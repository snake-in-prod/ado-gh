// Package shared holds code used by several `ado repo` commands.
package shared

import (
	"context"
	"strconv"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// NewClient returns a repository client for repo's project (the whole organization when
// repo.Project is empty), authenticated for its organization.
func NewClient(ctx context.Context, f *cmdutil.Factory, repo adocontext.Repo) (*api.RepoClient, error) {
	hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
	if err != nil {
		return nil, err
	}
	return api.NewRepoClient(hc, adocontext.OrgURL(repo.Host, repo.Org), repo.Project), nil
}

// ParseRepo resolves a `<repo>` argument with adocontext.ParseRepoRef against the current
// repository, ADO_ORG / ADO_PROJECT and the `org` config key; "" is the current repository.
func ParseRepo(f *cmdutil.Factory, arg string) (adocontext.Repo, error) {
	if arg == "" {
		return f.BaseRepo()
	}
	return adocontext.ParseRepoRef(arg, f.BaseRepo, func() string {
		cfg, err := f.Config()
		if err != nil {
			return "" // the missing-organization error names the fix
		}
		return cfg.Get("", "org")
	})
}

// HumanSize formats bytes with binary units, e.g. 1.5 KiB.
func HumanSize(b uint64) string {
	if b < 1024 {
		return strconv.FormatUint(b, 10) + " B"
	}
	v, unit := float64(b)/1024, 0
	for v >= 1024 && unit < 3 {
		v /= 1024
		unit++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + []string{"KiB", "MiB", "GiB", "TiB"}[unit]
}
