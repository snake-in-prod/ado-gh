package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

const (
	idA  = "11111111-1111-1111-1111-111111111111"
	idB  = "22222222-2222-2222-2222-222222222222"
	idMe = "33333333-3333-3333-3333-333333333333"
)

type imsUser struct {
	ID, Name, Account, Domain string
	Container                 bool
}

func (u imsUser) json() map[string]any {
	str := func(v string) map[string]any { return map[string]any{"$type": "System.String", "$value": v} }
	return map[string]any{
		"id":                  u.ID,
		"providerDisplayName": u.Name,
		"isContainer":         u.Container,
		"properties":          map[string]any{"Account": str(u.Account), "Domain": str(u.Domain)},
	}
}

// newResolver returns a resolver for its own org (the SDK caches locations per base URL for the
// whole process) whose requests go through ado's transport to reg.
func newResolver(t *testing.T, p *cmdutil.Prompter) (*IdentityResolver, *httpmock.Registry, string) {
	t.Helper()
	org := "ims-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	reg := &httpmock.Registry{}
	t.Cleanup(func() { reg.Verify(t) })
	hc := NewHTTPClient(ClientOptions{Transport: reg})
	return &IdentityResolver{HTTP: hc, OrgURL: "https://dev.azure.com/" + org + "/", Prompter: p}, reg, org
}

// stubIMS stubs the discovery calls before the first identities request: OPTIONS and
// ResourceAreas on the org, then OPTIONS on the vssps host.
func stubIMS(reg *httpmock.Registry, org string) {
	reg.StubLocations(org, httpmock.ResourceAreasLocation)
	reg.StubIMS(org)
}

func stubSearch(reg *httpmock.Registry, org, filter, value string, users ...imsUser) {
	stubSearchResponse(reg, org, filter, value, func() httpmock.Responder {
		out := make([]map[string]any, len(users))
		for i, u := range users {
			out[i] = u.json()
		}
		return httpmock.JSONResponse(map[string]any{"count": len(out), "value": out})
	}())
}

func stubSearchResponse(reg *httpmock.Registry, org, filter, value string, resp httpmock.Responder) {
	reg.Register(func(req *http.Request) bool {
		q := req.URL.Query()
		return req.Method == http.MethodGet && req.URL.Host == "vssps.dev.azure.com" &&
			strings.EqualFold(req.URL.Path, "/"+org+"/_apis/identities") && q.Get("searchFilter") == filter &&
			q.Get("filterValue") == value && strings.EqualFold(q.Get("queryMembership"), "None")
	}, resp)
}

func stubMe(reg *httpmock.Registry, org, domain string) {
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/connectionData"), httpmock.JSONResponse(map[string]any{
		"authenticatedUser": imsUser{ID: idMe, Name: "Me", Account: "me@contoso.com", Domain: domain}.json(),
	}))
}

func ids(t *testing.T, s ...string) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, len(s))
	for i, v := range s {
		out[i] = uuid.MustParse(v)
	}
	return out
}

func assertIDs(t *testing.T, got []uuid.UUID, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	w := ids(t, want...)
	if len(got) != len(w) {
		t.Fatalf("got %v, want %v", got, w)
	}
	for i := range w {
		if got[i] != w[i] {
			t.Fatalf("got %v, want %v", got, w)
		}
	}
}

func TestResolveGUIDPassthroughAndDedupe(t *testing.T) {
	r, reg, _ := newResolver(t, nil)
	got, err := r.Resolve(context.Background(), []string{idA, strings.ToUpper(idA), " " + idB, ""})
	assertIDs(t, got, err, idA, idB)
	if len(reg.Requests) != 0 {
		t.Errorf("GUIDs must not hit the network, got %d requests", len(reg.Requests))
	}
}

func TestResolveMe(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubMe(reg, org, "contoso")
	got, err := r.Resolve(context.Background(), []string{"@me", "@ME", idMe})
	assertIDs(t, got, err, idMe)
}

func TestResolveGeneral(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	stubSearch(reg, org, "General", "ann@contoso.com", imsUser{ID: idA, Name: "Ann", Account: "ann@contoso.com"})
	stubSearch(reg, org, "General", "Bob Builder", imsUser{ID: idB, Name: "Bob Builder"})
	got, err := r.Resolve(context.Background(), []string{"ann@contoso.com", "Bob Builder", idA})
	assertIDs(t, got, err, idA, idB)
}

func TestResolveFallbackFilters(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	stubSearch(reg, org, "General", "ann")
	stubSearch(reg, org, "DirectoryAlias", "ann", imsUser{ID: idA, Name: "Ann"})
	stubSearch(reg, org, "General", `contoso\bob`)
	stubSearch(reg, org, "DirectoryAlias", `contoso\bob`)
	stubSearch(reg, org, "AccountName", `contoso\bob`, imsUser{ID: idB, Name: "Bob"})
	got, err := r.Resolve(context.Background(), []string{"ann", `contoso\bob`})
	assertIDs(t, got, err, idA, idB)
}

func TestResolveSameDomain(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	stubSearch(reg, org, "General", "Ann",
		imsUser{ID: idA, Name: "Ann", Account: "ann@fabrikam.com", Domain: "fabrikam"},
		imsUser{ID: idB, Name: "Ann", Account: "ann@contoso.com", Domain: "contoso"})
	stubMe(reg, org, "contoso")
	got, err := r.Resolve(context.Background(), []string{"Ann"})
	assertIDs(t, got, err, idB)
}

func stubAmbiguous(reg *httpmock.Registry, org string) {
	stubIMS(reg, org)
	stubSearch(reg, org, "General", "Ann",
		imsUser{ID: idA, Name: "Ann Lee", Account: "ann.lee@contoso.com", Domain: "contoso"},
		imsUser{ID: idB, Name: "Ann Smith", Account: "ann.smith@contoso.com", Domain: "contoso"})
	stubMe(reg, org, "contoso")
}

func TestResolveAmbiguousNonTTY(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	for name, p := range map[string]*cmdutil.Prompter{"no prompter": nil, "non-tty": {IO: ios, Accessible: true}} {
		t.Run(name, func(t *testing.T) {
			r, reg, org := newResolver(t, p)
			stubAmbiguous(reg, org)
			_, err := r.Resolve(context.Background(), []string{"Ann"})
			if err == nil {
				t.Fatal("want error")
			}
			for _, s := range []string{`multiple identities match "Ann"`, "Ann Lee <ann.lee@contoso.com>", "Ann Smith <ann.smith@contoso.com>"} {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q lacks %q", err, s)
				}
			}
		})
	}
}

func TestResolveAmbiguousTTYPrompts(t *testing.T) {
	ios, in, out, _ := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	in.WriteString("2\n")
	r, reg, org := newResolver(t, &cmdutil.Prompter{IO: ios, Accessible: true})
	stubAmbiguous(reg, org)
	got, err := r.Resolve(context.Background(), []string{"Ann"})
	assertIDs(t, got, err, idB)
	if !strings.Contains(out.String(), "Ann Smith <ann.smith@contoso.com>") {
		t.Errorf("candidates not shown: %q", out)
	}
}

func TestResolveNotFound(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	for _, f := range []string{"General", "DirectoryAlias", "AccountName"} {
		stubSearch(reg, org, f, "ghost@contoso.com")
	}
	_, err := r.Resolve(context.Background(), []string{"ghost@contoso.com"})
	if err == nil || err.Error() != "could not resolve ghost@contoso.com: user must be added to the organization" {
		t.Errorf("err = %v", err)
	}
}

func TestResolveNotFoundTeamHint(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	for _, f := range []string{"General", "DirectoryAlias", "AccountName"} {
		stubSearch(reg, org, f, "Web Team")
	}
	_, err := r.Resolve(context.Background(), []string{"Web Team"})
	if err == nil || !strings.Contains(err.Error(), "user must be added to the organization") || !strings.Contains(err.Error(), `[Project]\Team`) {
		t.Errorf("err = %v", err)
	}
}

func TestResolveProjectTeam(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	stubSearch(reg, org, "General", `[Fabric]\Web Team`, imsUser{ID: idA, Name: `[Fabric]\Web Team`, Container: true})
	got, err := r.Resolve(context.Background(), []string{`[Fabric]\Web Team`})
	assertIDs(t, got, err, idA)
}

func TestResolveBareTeamRejected(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	stubSearch(reg, org, "General", "Web Team", imsUser{ID: idA, Name: `[Fabric]\Web Team`, Container: true})
	_, err := r.Resolve(context.Background(), []string{"Web Team"})
	if err == nil || !strings.Contains(err.Error(), `use [Project]\Team or a GUID`) {
		t.Errorf("err = %v", err)
	}
}

func TestResolveScopeHint(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			r, reg, org := newResolver(t, nil)
			stubIMS(reg, org)
			stubSearchResponse(reg, org, "General", "ann@contoso.com",
				httpmock.StatusJSONResponse(status, map[string]any{"message": "denied"}))
			_, err := r.Resolve(context.Background(), []string{"ann@contoso.com"})
			var authErr *cmdutil.AuthError
			if !errors.As(err, &authErr) || !strings.Contains(err.Error(), `"Identity (Read)"`) {
				t.Errorf("err = %v, want AuthError with the Identity (Read) hint", err)
			}
		})
	}
}

func TestResolveOtherErrorPassesThrough(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	stubSearchResponse(reg, org, "General", "ann@contoso.com",
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	_, err := r.Resolve(context.Background(), []string{"ann@contoso.com"})
	var authErr *cmdutil.AuthError
	if err == nil || errors.As(err, &authErr) || !strings.HasPrefix(err.Error(), "could not resolve ann@contoso.com: ") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveMeErrors(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/connectionData"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
	if _, err := r.Resolve(context.Background(), []string{"@me"}); err == nil {
		t.Error("want error")
	}
}
