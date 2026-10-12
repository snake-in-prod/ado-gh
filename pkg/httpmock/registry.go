// Registry, Stub and the REST/String/JSON responders are adapted from
// github.com/cli/cli pkg/httpmock (registry.go, stub.go):
//
// MIT License
//
// Copyright (c) 2019 GitHub Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Package httpmock is an http.RoundTripper that serves registered stubs in tests.
package httpmock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Matcher reports whether a stub handles req.
type Matcher func(req *http.Request) bool

// Responder builds the response for a matched request.
type Responder func(req *http.Request) (*http.Response, error)

// Stub pairs a Matcher with a Responder. Each stub matches at most once.
type Stub struct {
	matched bool
	// optional stubs may go unmatched without failing Verify.
	optional  bool
	Matcher   Matcher
	Responder Responder
}

// Registry records requests and answers them from registered stubs.
type Registry struct {
	mu    sync.Mutex
	stubs []*Stub
	// Requests are all requests sent, matched or not, so an empty list proves none was sent.
	Requests []*http.Request
}

// ReplaceTripper sets client's transport to a new Registry and returns it.
func ReplaceTripper(client *http.Client) *Registry {
	reg := &Registry{}
	client.Transport = reg
	return reg
}

// Register adds a stub. Stubs are tried in registration order.
func (r *Registry) Register(m Matcher, resp Responder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs = append(r.stubs, &Stub{Matcher: m, Responder: resp})
}

// RegisterOptional adds a stub that Verify doesn't require to be matched, for calls a client
// may serve from a process-wide cache instead (the SDK's location discovery, when a test runs
// again in the same process with -count).
func (r *Registry) RegisterOptional(m Matcher, resp Responder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs = append(r.stubs, &Stub{optional: true, Matcher: m, Responder: resp})
}

// Testing is the subset of testing.TB that Verify needs.
type Testing interface {
	Errorf(string, ...any)
	Helper()
}

// Verify fails t if any registered stub, optional ones aside, was never matched.
func (r *Registry) Verify(t Testing) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.stubs {
		if !s.matched && !s.optional {
			n++
		}
	}
	if n > 0 {
		t.Helper()
		t.Errorf("%d HTTP stubs unmatched", n)
	}
}

// RoundTrip satisfies http.RoundTripper.
func (r *Registry) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.Requests = append(r.Requests, req)
	var stub *Stub
	for _, s := range r.stubs {
		if !s.matched && s.Matcher(req) {
			stub = s
			break
		}
	}
	if stub == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("no registered HTTP stubs matched %v %v", req.Method, req.URL)
	}
	stub.matched = true
	r.mu.Unlock()

	return stub.Responder(req)
}

// MatchAny matches every request.
func MatchAny(*http.Request) bool { return true }

// REST matches method (case-insensitive) and the escaped URL path "/"+p; host and query are ignored.
func REST(method, p string) Matcher {
	return func(req *http.Request) bool {
		return strings.EqualFold(req.Method, method) && req.URL.EscapedPath() == "/"+p
	}
}

// StringResponse responds 200 with body.
func StringResponse(body string) Responder {
	return StatusStringResponse(http.StatusOK, body)
}

// StatusStringResponse responds with status and body.
func StatusStringResponse(status int, body string) Responder {
	return func(req *http.Request) (*http.Response, error) {
		return httpResponse(status, req, bytes.NewBufferString(body), http.Header{}), nil
	}
}

// JSONResponse responds 200 with body marshaled as JSON.
func JSONResponse(body any) Responder {
	return StatusJSONResponse(http.StatusOK, body)
}

// StatusJSONResponse responds with status and body marshaled as JSON.
func StatusJSONResponse(status int, body any) Responder {
	return func(req *http.Request) (*http.Response, error) {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		header := http.Header{}
		header.Set("Content-Type", "application/json")
		return httpResponse(status, req, bytes.NewBuffer(b), header), nil
	}
}

// WithHeader adds a response header to responder's responses.
func WithHeader(responder Responder, header, value string) Responder {
	return func(req *http.Request) (*http.Response, error) {
		resp, err := responder(req)
		if resp != nil {
			resp.Header.Set(header, value)
		}
		return resp, err
	}
}

func httpResponse(status int, req *http.Request, body *bytes.Buffer, header http.Header) *http.Response {
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode: status,
		Request:    req,
		Body:       io.NopCloser(body),
		// The SDK skips reading error bodies when ContentLength is 0.
		ContentLength: int64(body.Len()),
		Header:        header,
	}
}
