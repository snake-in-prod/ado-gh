package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// OrganizationsTenant is the MSAL authority used when an org's tenant is unknown.
const OrganizationsTenant = "organizations"

const zeroTenant = "00000000-0000-0000-0000-000000000000"

// DiscoverTenant returns the Entra tenant behind the org at orgURL from an unauthenticated
// HEAD: the WWW-Authenticate authorization_uri tenant, else the first non-zero
// X-VSS-ResourceTenant, else OrganizationsTenant. Both headers are undocumented.
// An all-zeros tenant means a personal-Microsoft-account org, which Entra login can't serve.
func DiscoverTenant(ctx context.Context, hc *http.Client, orgURL, org string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, orgURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover the Entra tenant of %s: %w", orgURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("organization %q not found at %s", org, orgURL)
	}

	var candidates []string
	for _, v := range resp.Header.Values("WWW-Authenticate") {
		if _, rest, ok := strings.Cut(v, "authorization_uri="); ok {
			raw, _, _ := strings.Cut(rest, ",")
			if u, err := url.Parse(strings.Trim(strings.TrimSpace(raw), `"`)); err == nil {
				candidates = append(candidates, strings.Trim(u.Path, "/"))
			}
		}
	}
	for _, v := range resp.Header.Values("X-VSS-ResourceTenant") {
		for t := range strings.SplitSeq(v, ",") {
			candidates = append(candidates, strings.TrimSpace(t))
		}
	}
	msa := false
	for _, t := range candidates {
		switch t {
		case "", "common", OrganizationsTenant:
		case zeroTenant:
			msa = true
		default:
			return t, nil
		}
	}
	if msa {
		return "", fmt.Errorf("organization %q is backed by personal Microsoft accounts, which Microsoft Entra ID sign-in doesn't support; "+
			"log in with a PAT instead: ado auth login --org %s --with-token", org, org)
	}
	return OrganizationsTenant, nil
}
