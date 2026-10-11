// Package api holds ado's authenticated HTTP client shared by raw and Azure DevOps SDK requests.
package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
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
// Authorization, User-Agent and X-TFS-FedAuthRedirect, logs when asked, turns
// 401 and 203 (sign-in page) responses into *cmdutil.AuthError and honours Azure DevOps
// throttling (see backoff).
func NewHTTPClient(opts ClientOptions) *http.Client {
	base := opts.Transport
	// Wrapping another ado transport would run it too: it strips Authorization (it has
	// no token) and logs every request twice. Use the transport underneath instead.
	if inner, ok := base.(*transport); ok {
		base = inner.base
	}
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

// areaClient returns an SDK client for the host of resource area areaID (e.g. vssps for IMS,
// almsearch for Search), found through the org's ResourceAreas. ok is false when the org lists
// no such area (on-prem lists none); the client then targets orgURL.
func areaClient(ctx context.Context, hc *http.Client, orgURL string, areaID uuid.UUID) (c *azuredevops.Client, ok bool, err error) {
	areas, err := NewSDKClient(hc, orgURL).GetResourceAreas(ctx)
	if err != nil {
		return nil, false, err
	}
	base := orgURL
	for _, a := range deref(areas) {
		if a.Id != nil && *a.Id == areaID && a.LocationUrl != nil {
			base, ok = *a.LocationUrl, true
		}
	}
	return NewSDKClient(hc, base), ok, nil
}

// Throttling limits: a 429 is retried at most maxRetries times; a server-requested delay is
// capped at maxBackoff, and defaultBackoff is used when the server names none.
const (
	maxRetries     = 3
	maxBackoff     = time.Minute
	defaultBackoff = time.Second
)

type transport struct {
	base      http.RoundTripper
	userAgent string
	token     func(*http.Request) (Token, error)
	log       io.Writer

	// notBefore holds back every request of the client after a throttling response.
	mu        sync.Mutex
	notBefore time.Time
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

	// A body can be resent only if it can be re-read.
	retryable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		if err := t.wait(req.Context()); err != nil {
			return nil, err
		}
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		start := time.Now()
		t.logRequest(req)
		var err error
		if resp, err = t.base.RoundTrip(req); err != nil {
			t.logf("< error: %v\n\n", err)
			return nil, err
		}
		t.logf("< HTTP %d (%s)\n\n", resp.StatusCode, time.Since(start).Round(time.Millisecond))
		d := backoff(resp)
		// A 429 with Retry-After: 0 asks for an immediate retry.
		if d == 0 && resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		t.mu.Lock()
		if until := time.Now().Add(d); until.After(t.notBefore) {
			t.notBefore = until
		}
		t.mu.Unlock()
		t.logf("< throttled; waiting %s\n\n", d)
		if resp.StatusCode != http.StatusTooManyRequests || attempt == maxRetries || !retryable {
			break
		}
		// Drain so the connection can be reused; a failed drain only costs that reuse.
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.logf("< drain error: %v\n\n", err)
		}
		resp.Body.Close()
	}

	// Without a valid credential ADO answers 401, or 203 with an HTML sign-in page that
	// the SDK would treat as success.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNonAuthoritativeInfo {
		resp.Body.Close()
		return nil, &cmdutil.AuthError{Err: fmt.Errorf("authentication failed for %s (HTTP %d)", req.URL.Host, resp.StatusCode)}
	}
	return resp, nil
}

// wait blocks until the client may send again after throttling, or ctx ends.
func (t *transport) wait(ctx context.Context) error {
	t.mu.Lock()
	d := time.Until(t.notBefore)
	t.mu.Unlock()
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// backoff returns how long the client should pause after resp: Retry-After (seconds, capped at
// maxBackoff) on any response, as Azure DevOps sends it on 200s too while delaying requests;
// defaultBackoff for a 429 without it or for X-RateLimit-Remaining: 0; else 0.
func backoff(resp *http.Response) time.Duration {
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return min(time.Duration(n)*time.Second, maxBackoff)
		}
		return defaultBackoff
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return defaultBackoff
	}
	return 0
}

func (t *transport) logRequest(req *http.Request) {
	if t.log == nil {
		return
	}
	// No query: it can hold a signed URL's sig or other secrets.
	u := *req.URL
	u.RawQuery, u.ForceQuery = "", false
	t.logf("> %s %s\n", req.Method, u.Redacted())
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
