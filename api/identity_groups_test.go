package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
)

func TestMemberGroups(t *testing.T) {
	const team, nested, other = "aaaaaaaa-0000-0000-0000-000000000001", "aaaaaaaa-0000-0000-0000-000000000002", "aaaaaaaa-0000-0000-0000-000000000003"
	r, reg, org := newResolver(t, nil)
	stubMe(reg, org, "contoso")
	stubIMS(reg, org)
	reg.StubMemberGroups(org, idMe, []string{"Microsoft.TeamFoundation.Identity;S-1-team", "Microsoft.TeamFoundation.Identity;S-1-NESTED"},
		map[string]string{
			strings.ToUpper(team): "Microsoft.TeamFoundation.Identity;S-1-team",
			nested:                "Microsoft.TeamFoundation.Identity;s-1-nested",
			other:                 "Microsoft.TeamFoundation.Identity;S-1-other",
		})
	got, err := r.MemberGroups(context.Background(), []string{team, nested, other})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[team] || !got[nested] {
		t.Errorf("got %v, want %s and %s", got, team, nested)
	}
	q := reg.Requests[len(reg.Requests)-1].URL.Query()
	if q.Get("identityIds") != team+","+nested+","+other {
		t.Errorf("identityIds = %q", q.Get("identityIds"))
	}
}

func TestMemberGroupsShortCircuits(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	got, err := r.MemberGroups(context.Background(), nil)
	if err != nil || len(got) != 0 || len(reg.Requests) != 0 {
		t.Fatalf("no groups: got %v, %v, %d requests", got, err, len(reg.Requests))
	}
	// No memberships: the groups aren't looked up.
	stubMe(reg, org, "contoso")
	stubIMS(reg, org)
	reg.StubMemberGroups(org, idMe, nil, nil)
	got, err = r.MemberGroups(context.Background(), []string{idA})
	if err != nil || len(got) != 0 {
		t.Fatalf("no memberships: got %v, %v", got, err)
	}
}

func TestMemberGroupsErrors(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/connectionData"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
	if _, err := r.MemberGroups(context.Background(), []string{idA}); err == nil {
		t.Fatal("want connectionData error")
	}
}

func TestMemberGroupsScopeHint(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubMe(reg, org, "contoso")
	stubIMS(reg, org)
	reg.Register(func(req *http.Request) bool { return req.URL.Host == "vssps.dev.azure.com" }, httpmock.StatusStringResponse(http.StatusForbidden, ""))
	_, err := r.MemberGroups(context.Background(), []string{idA})
	var authErr *cmdutil.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want an AuthError", err)
	}
}

func TestMemberGroupsGroupLookupError(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubMe(reg, org, "contoso")
	stubIMS(reg, org)
	reg.StubMemberGroups(org, idMe, []string{"d"}, nil)
	reg.Register(func(req *http.Request) bool { return req.URL.Host == "vssps.dev.azure.com" }, httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
	if _, err := r.MemberGroups(context.Background(), []string{idA}); err == nil || !strings.Contains(err.Error(), "reviewer groups") {
		t.Fatalf("got %v, want a reviewer groups error", err)
	}
}

func TestMemberGroupsDiscoveryError(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubMe(reg, org, "contoso")
	reg.StubLocations(org, httpmock.ResourceAreasLocation)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/ResourceAreas"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
	if _, err := r.MemberGroups(context.Background(), []string{idA}); err == nil {
		t.Fatal("want discovery error")
	}
}
