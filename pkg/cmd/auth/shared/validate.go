// Package shared holds helpers used by the `ado auth` commands.
package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// anonymousID is the user id connectionData reports for an unauthenticated caller.
const anonymousID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

// User is the identity a credential authenticates as.
type User struct {
	ID          string
	DisplayName string
}

// OrgURL returns the organization's base URL; see adocontext.OrgURL.
func OrgURL(host, org string) string {
	return adocontext.OrgURL(host, org)
}

// Validate checks the credential hc sends against the org-scoped connectionData API and
// returns the authenticated user. A rejected credential yields a *cmdutil.AuthError.
// PATs can't call the profile API, so this is the only check for them.
func Validate(ctx context.Context, hc *http.Client, host, org string) (User, error) {
	orgURL := OrgURL(host, org)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, orgURL+"_apis/connectionData", nil)
	if err != nil {
		return User{}, err
	}
	rejected := &cmdutil.AuthError{Err: fmt.Errorf("the credentials were rejected by %s", orgURL)}
	resp, err := hc.Do(req)
	var authErr *cmdutil.AuthError
	if errors.As(err, &authErr) {
		return User{}, rejected
	}
	if err != nil {
		return User{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return User{}, fmt.Errorf("organization %q not found at %s", org, orgURL)
	case resp.StatusCode != http.StatusOK:
		return User{}, fmt.Errorf("validating credentials at %s: HTTP %d", orgURL, resp.StatusCode)
	}
	var data struct {
		AuthenticatedUser struct {
			ID                  string `json:"id"`
			ProviderDisplayName string `json:"providerDisplayName"`
		} `json:"authenticatedUser"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return User{}, fmt.Errorf("validating credentials at %s: unexpected response: %w", orgURL, err)
	}
	// Orgs with public projects can answer 200 for a bad token, as the anonymous user.
	if u := data.AuthenticatedUser; u.ID != "" && u.ID != anonymousID {
		return User{ID: u.ID, DisplayName: u.ProviderDisplayName}, nil
	}
	return User{}, rejected
}
