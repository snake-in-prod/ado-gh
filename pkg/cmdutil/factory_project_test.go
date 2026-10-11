package cmdutil

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFactoryBaseProject(t *testing.T) {
	fromGit := adocontext.Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}
	f := &Factory{RepoOverride: "org/proj", BaseRepo: func() (adocontext.Repo, error) { return fromGit, nil }}
	got, err := f.BaseProject()
	require.NoError(t, err)
	assert.Equal(t, adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "proj"}, got)

	f.RepoOverride = ""
	got, err = f.BaseProject()
	require.NoError(t, err)
	assert.Equal(t, fromGit, got, "falls back to BaseRepo")
}
