package api

import (
	"context"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveUser(t *testing.T) {
	ann := imsUser{ID: idA, Name: "Ann Lee", Account: "ann.lee@contoso.com", Domain: "contoso"}
	tests := []struct {
		name  string
		value string
		stub  func(search func(string, string, ...imsUser))
		want  User
	}{
		{"@me", "@ME", nil, User{ID: idMe, DisplayName: "Me", Account: "me@contoso.com"}},
		{"email", "ann.lee@contoso.com", func(search func(string, string, ...imsUser)) {
			search("General", "ann.lee@contoso.com", ann)
		}, User{ID: idA, DisplayName: "Ann Lee", Account: "ann.lee@contoso.com"}},
		{"display name", " Ann Lee ", func(search func(string, string, ...imsUser)) {
			search("General", "Ann Lee", ann)
		}, User{ID: idA, DisplayName: "Ann Lee", Account: "ann.lee@contoso.com"}},
		{"alias via fallback filter", "annl", func(search func(string, string, ...imsUser)) {
			search("General", "annl")
			search("DirectoryAlias", "annl", imsUser{ID: idB, Name: "Ann L"})
		}, User{ID: idB, DisplayName: "Ann L"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, reg, org := newResolver(t, nil)
			if tt.stub == nil {
				stubMe(reg, org, "contoso")
			} else {
				stubIMS(reg, org)
				tt.stub(func(f, v string, u ...imsUser) { stubSearch(reg, org, f, v, u...) })
			}
			got, err := r.ResolveUser(context.Background(), tt.value)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUserFieldValue(t *testing.T) {
	assert.Equal(t, "ann@contoso.com", User{DisplayName: "Ann", Account: "ann@contoso.com"}.FieldValue())
	assert.Equal(t, "Ann", User{DisplayName: "Ann"}.FieldValue())
}

func TestResolveUserAmbiguous(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubAmbiguous(reg, org)
	_, err := r.ResolveUser(context.Background(), "Ann")
	require.Error(t, err)
	for _, s := range []string{`multiple identities match "Ann"`, "Ann Lee <ann.lee@contoso.com>", "Ann Smith <ann.smith@contoso.com>"} {
		assert.Contains(t, err.Error(), s)
	}
}

func TestResolveUserAmbiguousTTYPicks(t *testing.T) {
	ios, in, _, _ := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	in.WriteString("2\n")
	r, reg, org := newResolver(t, &cmdutil.Prompter{IO: ios, Accessible: true})
	stubAmbiguous(reg, org)
	got, err := r.ResolveUser(context.Background(), "Ann")
	require.NoError(t, err)
	assert.Equal(t, "ann.smith@contoso.com", got.FieldValue())
}

func TestResolveUserRejectsGroups(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	stubSearch(reg, org, "General", `[Fabric]\Web Team`, imsUser{ID: idA, Name: `[Fabric]\Web Team`, Container: true})
	_, err := r.ResolveUser(context.Background(), `[Fabric]\Web Team`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a team or group")
}

func TestResolveUserErrors(t *testing.T) {
	r, reg, org := newResolver(t, nil)
	stubIMS(reg, org)
	for _, f := range searchFilters {
		stubSearch(reg, org, f, "ghost")
	}
	_, err := r.ResolveUser(context.Background(), "ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not resolve ghost")

	_, err = r.ResolveUser(context.Background(), "  ")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "empty"), err.Error())
}
