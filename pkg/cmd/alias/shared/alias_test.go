package shared

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

func TestIsShell(t *testing.T) {
	assert.True(t, IsShell("!ado pr list | head"))
	assert.False(t, IsShell("pr list"))
	assert.False(t, IsShell(" !pr list"))
}

func TestExpand(t *testing.T) {
	tests := []struct {
		name      string
		expansion string
		args      []string
		want      []string
		wantErr   string
	}{
		{"no placeholders appends args", "pr list", []string{"-a", "@me"}, []string{"pr", "list", "-a", "@me"}, ""},
		{"no args", "pr list", nil, []string{"pr", "list"}, ""},
		{"arg with spaces stays one token", "pr create -t $1", []string{"fix the bug"}, []string{"pr", "create", "-t", "fix the bug"}, ""},
		{"arg with quotes stays literal", "pr create -t $1", []string{`it's "done"`}, []string{"pr", "create", "-t", `it's "done"`}, ""},
		{"$2 only appends the unused first arg", "pr list -a $2", []string{"x", "y"}, []string{"pr", "list", "-a", "y", "x"}, ""},
		{"$10 is not $1", "x $10 $1", []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10"},
			[]string{"x", "a10", "a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}, ""},
		{"literal $ in a jq filter keeps args", `pr list --json id -q '.[] | $x'`, []string{"-L", "5"},
			[]string{"pr", "list", "--json", "id", "-q", ".[] | $x", "-L", "5"}, ""},
		{"placeholder inside a token", "api projects/$1/teams", []string{"web"}, []string{"api", "projects/web/teams"}, ""},
		{"placeholder used twice", "x $1 $1", []string{"a"}, []string{"x", "a", "a"}, ""},
		{"$0 is literal", "x $0", []string{"a"}, []string{"x", "$0", "a"}, ""},
		{"no re-substitution of arg text", "x $1 $2", []string{"$2", "b"}, []string{"x", "$2", "b"}, ""},
		{"quoted placeholder", `pr create -t "$1 now"`, []string{"a b"}, []string{"pr", "create", "-t", "a b now"}, ""},
		{"missing arg", "pr list -a $1 -s $2", []string{"me"}, nil, "not enough arguments for alias: pr list -a $1 -s $2"},
		{"huge placeholder is missing", "x $99999999999999999999", nil, nil, "not enough arguments"},
		{"unbalanced quote", `pr list -q "oops`, nil, nil, "invalid alias expansion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.expansion, tt.args)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func newAliasTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "ado"}
	root.AddGroup(&cobra.Group{ID: AliasGroupID, Title: "Alias commands:"}, &cobra.Group{ID: ExtensionGroupID, Title: "Extension commands:"})
	pr := &cobra.Command{Use: "pr"}
	pr.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
	wi := &cobra.Command{Use: "workitem", Aliases: []string{"wi"}}
	root.AddCommand(pr, wi,
		&cobra.Command{Use: "prs", GroupID: AliasGroupID, Run: func(*cobra.Command, []string) {}},
		&cobra.Command{Use: "lint", GroupID: ExtensionGroupID, Run: func(*cobra.Command, []string) {}},
	)
	return root
}

func TestValidateExpansion(t *testing.T) {
	root := newAliasTestRoot()
	tests := []struct {
		expansion string
		wantErr   bool
	}{
		{"pr list -a @me", false},
		{"pr", false},
		{"wi list", false},
		{"!anything | goes", false},
		{"prs", true},
		{"lint --fix", true},
		{"nope list", true},
		{"--help", true},
		{"", true},
		{`pr "oops`, true},
	}
	for _, tt := range tests {
		t.Run(tt.expansion, func(t *testing.T) {
			err := ValidateExpansion(root, tt.expansion)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

type aliasTestPlainConfig struct{}

func (aliasTestPlainConfig) Get(string, string) string  { return "" }
func (aliasTestPlainConfig) Set(string, string, string) {}
func (aliasTestPlainConfig) Write() error               { return nil }

func TestAliasConfig(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)

	ac, err := AliasConfig(cfg)
	require.NoError(t, err)
	ac.SetAlias("prs", "pr list")
	assert.Equal(t, map[string]string{"prs": "pr list"}, cfg.Aliases())

	var plain cmdutil.Config = aliasTestPlainConfig{}
	_, err = AliasConfig(plain)
	assert.Error(t, err)
}
