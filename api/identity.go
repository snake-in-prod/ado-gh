package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/identity"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// IdentityResolver turns user arguments (GUID, @me, email, name, alias or [Project]\Team) into
// Azure DevOps identity ids through the IMS Identities API. Used for PR reviewers and authors.
type IdentityResolver struct {
	HTTP *http.Client
	// OrgURL is the organization URL ending in "/", e.g. https://dev.azure.com/org/.
	OrgURL string
	// Prompter picks among ambiguous matches; nil or a non-TTY turns them into an error.
	Prompter *cmdutil.Prompter

	ims identity.Client
	me  *identity.Identity
}

// searchFilters are tried in order until one matches. General covers display, account and
// unique names; DirectoryAlias (undocumented, used by az) and AccountName catch bare aliases.
var searchFilters = []string{"General", "DirectoryAlias", "AccountName"}

// Resolve returns the identity ids for values in order, without duplicates. Empty values are skipped.
func (r *IdentityResolver) Resolve(ctx context.Context, values []string) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		id, err := r.resolve(ctx, v)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (r *IdentityResolver) resolve(ctx context.Context, v string) (uuid.UUID, error) {
	if id, err := uuid.Parse(v); err == nil {
		return id, nil
	}
	if strings.EqualFold(v, "@me") {
		me, err := r.current(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		return *me.Id, nil
	}

	matches, err := r.search(ctx, v)
	if err != nil {
		return uuid.Nil, err
	}
	if len(matches) == 0 {
		msg := "could not resolve " + v + ": user must be added to the organization"
		if !strings.Contains(v, "@") {
			msg += `; for a team use [Project]\Team or its GUID`
		}
		return uuid.Nil, errors.New(msg)
	}
	if !strings.HasPrefix(v, "[") && slices.ContainsFunc(matches, func(i identity.Identity) bool {
		return i.IsContainer != nil && *i.IsContainer
	}) {
		return uuid.Nil, fmt.Errorf(`%q matches a team or group: use [Project]\Team or a GUID`, v)
	}
	if len(matches) > 1 {
		if matches, err = r.sameDomain(ctx, matches); err != nil {
			return uuid.Nil, err
		}
	}
	if len(matches) == 1 {
		return *matches[0].Id, nil
	}
	return r.pick(v, matches)
}

// search runs the IMS lookup with each filter until one returns matches.
func (r *IdentityResolver) search(ctx context.Context, v string) ([]identity.Identity, error) {
	if r.ims == nil {
		c, err := r.imsClient(ctx)
		if err != nil {
			return nil, err
		}
		r.ims = c
	}
	none := identity.QueryMembershipValues.None
	for _, f := range searchFilters {
		res, err := r.ims.ReadIdentities(ctx, identity.ReadIdentitiesArgs{SearchFilter: &f, FilterValue: &v, QueryMembership: &none})
		if err != nil {
			return nil, scopeHint(v, err)
		}
		var found []identity.Identity
		for _, i := range *res {
			if i.Id != nil {
				found = append(found, i)
			}
		}
		if len(found) > 0 {
			return found, nil
		}
	}
	return nil, nil
}

// imsClient finds the IMS host (vssps) via ResourceAreas; an empty list (on-prem) or a missing
// area means the org URL itself. The SDK's identity.NewClient can't use ado's HTTP client.
func (r *IdentityResolver) imsClient(ctx context.Context) (identity.Client, error) {
	areas, err := NewSDKClient(r.HTTP, r.OrgURL).GetResourceAreas(ctx)
	if err != nil {
		return nil, err
	}
	base := r.OrgURL
	for _, a := range *areas {
		if a.Id != nil && *a.Id == identity.ResourceAreaId && a.LocationUrl != nil {
			base = *a.LocationUrl
		}
	}
	return &identity.ClientImpl{Client: *NewSDKClient(r.HTTP, base)}, nil
}

// scopeHint maps 401/403 from the Identities API to a hint about the missing PAT scope.
func scopeHint(v string, err error) error {
	var authErr *cmdutil.AuthError
	status := 0
	var we *azuredevops.WrappedError
	var wv azuredevops.WrappedError
	switch {
	case errors.As(err, &we) && we.StatusCode != nil:
		status = *we.StatusCode
	case errors.As(err, &wv) && wv.StatusCode != nil:
		status = *wv.StatusCode
	}
	if errors.As(err, &authErr) || status == http.StatusUnauthorized || status == http.StatusForbidden {
		return &cmdutil.AuthError{Err: fmt.Errorf(`could not resolve %s: %w; the credential needs the "Identity (Read)" scope`, v, err)}
	}
	return fmt.Errorf("could not resolve %s: %w", v, err)
}

// sameDomain keeps the matches in the caller's domain, as az does; all of them if none match.
func (r *IdentityResolver) sameDomain(ctx context.Context, matches []identity.Identity) ([]identity.Identity, error) {
	me, err := r.current(ctx)
	if err != nil {
		return nil, err
	}
	domain := property(*me, "Domain")
	var same []identity.Identity
	for _, m := range matches {
		if domain != "" && strings.EqualFold(property(m, "Domain"), domain) {
			same = append(same, m)
		}
	}
	if len(same) == 0 {
		return matches, nil
	}
	return same, nil
}

func (r *IdentityResolver) pick(v string, matches []identity.Identity) (uuid.UUID, error) {
	labels := make([]string, len(matches))
	for i, m := range matches {
		labels[i] = label(m)
	}
	if r.Prompter != nil {
		i, err := r.Prompter.Select(fmt.Sprintf("Multiple identities match %q, pick one", v), labels)
		if err == nil {
			return *matches[i].Id, nil
		}
		if !errors.Is(err, cmdutil.ErrNoPrompt) {
			return uuid.Nil, err
		}
	}
	return uuid.Nil, fmt.Errorf("multiple identities match %q; use a more specific value or a GUID:\n  %s", v, strings.Join(labels, "\n  "))
}

func label(i identity.Identity) string {
	name := ""
	if i.ProviderDisplayName != nil {
		name = *i.ProviderDisplayName
	}
	if acct := property(i, "Account"); acct != "" {
		name += " <" + acct + ">"
	}
	return name + " (" + i.Id.String() + ")"
}

// property reads an IMS property, serialized as {"$type": ..., "$value": ...}.
func property(i identity.Identity, key string) string {
	props, _ := i.Properties.(map[string]any)
	p, _ := props[key].(map[string]any)
	s, _ := p["$value"].(string)
	return s
}

// current returns the caller's identity from connectionData, fetched once.
func (r *IdentityResolver) current(ctx context.Context) (*identity.Identity, error) {
	if r.me != nil {
		return r.me, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.OrgURL+"_apis/connectionData", nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data struct {
		AuthenticatedUser identity.Identity `json:"authenticatedUser"`
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading the current user from %s: HTTP %d", r.OrgURL, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || data.AuthenticatedUser.Id == nil {
		return nil, fmt.Errorf("reading the current user from %s: unexpected response", r.OrgURL)
	}
	r.me = &data.AuthenticatedUser
	return r.me, nil
}
