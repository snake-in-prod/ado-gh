// Package api holds ado's authenticated HTTP client shared by raw and Azure DevOps SDK requests.
package api

import (
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// Token is an Azure DevOps credential.
type Token struct {
	Value string
	// Bearer marks an Entra ID access token; otherwise Value is a PAT.
	Bearer bool
}

// ClientOptions configures NewHTTPClient.
type ClientOptions struct {
	// AppVersion goes into the User-Agent "ado-cli/<AppVersion>".
	AppVersion string
	// Token returns the credential for req; nil or an empty Value sends no Authorization.
	Token func(req *http.Request) (Token, error)
	// Log receives a request/response trace with Authorization redacted (--debug); nil disables it.
	Log io.Writer
	// Transport sends the requests; nil means a clone of http.DefaultTransport.
	Transport http.RoundTripper
}

// NewHTTPClient returns the client shared by raw and SDK requests. Its transport sets
// Authorization, User-Agent and X-TFS-FedAuthRedirect, logs when asked and turns
// 401 and 203 (sign-in page) responses into *cmdutil.AuthError.
func NewHTTPClient(opts ClientOptions) *http.Client {
	base := opts.Transport
	if base == nil {
		// Never the global transport: MSAL uses it and must not get ADO headers.
		base = http.DefaultTransport.(*http.Transport).Clone()
	}
	return &http.Client{Transport: &transport{
		base:      base,
		userAgent: "ado-cli/" + opts.AppVersion,
		token:     opts.Token,
		log:       opts.Log,
	}}
}

// NewSDKClient returns an SDK client for baseURL that sends every request through hc. Wrap it
// in a typed client: &git.ClientImpl{Client: *NewSDKClient(hc, orgURL)}. Auth comes only from
// hc's transport, so the Connection's AuthorizationString stays empty.
// ponytail: assumes the area lives under baseURL (true for git/core/wit/build/pipelines on
// dev.azure.com); add a (*azuredevops.Client).GetResourceAreas lookup if a live check disagrees.
func NewSDKClient(hc *http.Client, baseURL string) *azuredevops.Client {
	return azuredevops.NewClientWithOptions(&azuredevops.Connection{}, baseURL, azuredevops.WithHTTPClient(hc))
}

type transport struct {
	base      http.RoundTripper
	userAgent string
	token     func(*http.Request) (Token, error)
	log       io.Writer
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("X-TFS-FedAuthRedirect", "Suppress")
	req.Header.Del("Authorization")
	if t.token != nil {
		tok, err := t.token(req)
		if err != nil {
			return nil, err
		}
		switch {
		case tok.Value == "":
		case tok.Bearer:
			req.Header.Set("Authorization", "Bearer "+tok.Value)
		default:
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+tok.Value)))
		}
	}

	start := time.Now()
	t.logRequest(req)
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.logf("< error: %v\n\n", err)
		return nil, err
	}
	t.logf("< HTTP %d (%s)\n\n", resp.StatusCode, time.Since(start).Round(time.Millisecond))

	// Without a valid credential ADO answers 401, or 203 with an HTML sign-in page that
	// the SDK would treat as success.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNonAuthoritativeInfo {
		resp.Body.Close()
		return nil, &cmdutil.AuthError{Err: fmt.Errorf("authentication failed for %s (HTTP %d)", req.URL.Host, resp.StatusCode)}
	}
	return resp, nil
}

func (t *transport) logRequest(req *http.Request) {
	if t.log == nil {
		return
	}
	t.logf("> %s %s\n", req.Method, req.URL.Redacted())
	for _, k := range slices.Sorted(maps.Keys(req.Header)) {
		for _, v := range req.Header[k] {
			if k == "Authorization" {
				v = "[REDACTED]"
			}
			t.logf("> %s: %s\n", k, v)
		}
	}
}

func (t *transport) logf(format string, args ...any) {
	if t.log != nil {
		fmt.Fprintf(t.log, format, args...)
	}
}
