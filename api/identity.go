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
	i, err := r.lookup(ctx, v)
	if err != nil {
		return uuid.Nil, err
	}
	return *i.Id, nil
}

// User is a resolved user identity.
type User struct {
	ID          string
	DisplayName string
	// Account is the sign-in name (email or UPN); empty when IMS has none.
	Account string
}

// FieldValue is the value identity fields such as System.AssignedTo take: the account, else
// the display name.
func (u User) FieldValue() string {
	if u.Account != "" {
		return u.Account
	}
	return u.DisplayName
}

// ResolveUser resolves @me, an email/UPN, a display name or an alias to one user, like Resolve
// (ambiguous matches prompt on a TTY, else error listing the candidates). Teams and groups are
// refused.
func (r *IdentityResolver) ResolveUser(ctx context.Context, v string) (User, error) {
	if v = strings.TrimSpace(v); v == "" {
		return User{}, errors.New("empty user")
	}
	i, err := r.lookup(ctx, v)
	if err != nil {
		return User{}, err
	}
	if i.IsContainer != nil && *i.IsContainer {
		return User{}, fmt.Errorf("%q is a team or group; work items are assigned to users", v)
	}
	return User{ID: i.Id.String(), DisplayName: deref(i.ProviderDisplayName), Account: property(*i, "Account")}, nil
}

// lookup finds the one identity v (@me, email, name, alias or [Project]\Team) names.
func (r *IdentityResolver) lookup(ctx context.Context, v string) (*identity.Identity, error) {
	if strings.EqualFold(v, "@me") {
		return r.current(ctx)
	}

	matches, err := r.search(ctx, v)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		msg := "could not resolve " + v + ": user must be added to the organization"
		if !strings.Contains(v, "@") {
			msg += `; for a team use [Project]\Team or its GUID`
		}
		return nil, errors.New(msg)
	}
	if !strings.HasPrefix(v, "[") && slices.ContainsFunc(matches, func(i identity.Identity) bool {
		return i.IsContainer != nil && *i.IsContainer
	}) {
		return nil, fmt.Errorf(`%q matches a team or group: use [Project]\Team or a GUID`, v)
	}
	if len(matches) > 1 {
		if matches, err = r.sameDomain(ctx, matches); err != nil {
			return nil, err
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return r.pick(v, matches)
}

// MemberGroups returns the subset of groupIDs (group or team identity ids, e.g. container
// reviewers of pull requests) that the signed-in user belongs to, directly or through nested
// groups. Keys are lower-case ids.
func (r *IdentityResolver) MemberGroups(ctx context.Context, groupIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	me, err := r.current(ctx)
	if err != nil {
		return nil, err
	}
	if r.ims == nil {
		if r.ims, err = r.imsClient(ctx); err != nil {
			return nil, err
		}
	}
	up, none, myID := identity.QueryMembershipValues.ExpandedUp, identity.QueryMembershipValues.None, me.Id.String()
	mine, err := r.ims.ReadIdentities(ctx, identity.ReadIdentitiesArgs{IdentityIds: &myID, QueryMembership: &up})
	if err != nil {
		return nil, scopeHint("your groups", err)
	}
	memberOf := map[string]bool{}
	for _, i := range *mine {
		if i.MemberOf != nil {
			for _, d := range *i.MemberOf {
				memberOf[strings.ToLower(d)] = true
			}
		}
	}
	if len(memberOf) == 0 {
		return out, nil
	}
	// ponytail: one request for every id; batch it if URLs grow too long for many groups.
	ids := strings.Join(groupIDs, ",")
	groups, err := r.ims.ReadIdentities(ctx, identity.ReadIdentitiesArgs{IdentityIds: &ids, QueryMembership: &none})
	if err != nil {
		return nil, scopeHint("reviewer groups", err)
	}
	for _, g := range *groups {
		if g.Id != nil && g.Descriptor != nil && memberOf[strings.ToLower(*g.Descriptor)] {
			out[strings.ToLower(g.Id.String())] = true
		}
	}
	return out, nil
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

// imsClient returns the identity client on the IMS host (vssps), or on the org URL when the
// org lists no IMS area (on-prem). The SDK's identity.NewClient can't use ado's HTTP client.
func (r *IdentityResolver) imsClient(ctx context.Context) (identity.Client, error) {
	c, _, err := areaClient(ctx, r.HTTP, r.OrgURL, identity.ResourceAreaId)
	if err != nil {
		return nil, err
	}
	return &identity.ClientImpl{Client: *c}, nil
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

func (r *IdentityResolver) pick(v string, matches []identity.Identity) (*identity.Identity, error) {
	labels := make([]string, len(matches))
	for i, m := range matches {
		labels[i] = label(m)
	}
	if r.Prompter != nil {
		i, err := r.Prompter.Select(fmt.Sprintf("Multiple identities match %q, pick one", v), labels)
		if err == nil {
			return &matches[i], nil
		}
		if !errors.Is(err, cmdutil.ErrNoPrompt) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("multiple identities match %q; use a more specific value or a GUID:\n  %s", v, strings.Join(labels, "\n  "))
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
