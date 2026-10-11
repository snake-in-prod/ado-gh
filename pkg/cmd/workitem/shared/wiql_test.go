package shared

import (
	"regexp"
	"strings"
	"testing"
)

// wiqlStringLiteral matches a WIQL single-quoted string with ” escapes.
var wiqlStringLiteral = regexp.MustCompile(`'(?:[^']|'')*'`)

// wiqlOutsideLiterals returns the query with every string literal blanked.
func wiqlOutsideLiterals(q string) string {
	return wiqlStringLiteral.ReplaceAllString(q, "''")
}

const wiqlTestPrefix = "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project"
const wiqlTestOrder = " ORDER BY [System.ChangedDate] DESC, [System.Id] DESC"

func TestBuildWIQLClauses(t *testing.T) {
	tests := []struct {
		name   string
		filter WIQLFilter
		want   string
	}{
		{"no filters", WIQLFilter{}, ""},
		{"single type", WIQLFilter{Types: []string{"Bug"}}, " AND [System.WorkItemType] = 'Bug'"},
		{"many types", WIQLFilter{Types: []string{"Bug", "User Story"}}, " AND [System.WorkItemType] IN ('Bug', 'User Story')"},
		{"states", WIQLFilter{States: []string{"New", "Active"}}, " AND [System.State] IN ('New', 'Active')"},
		{"assignee me", WIQLFilter{Assignee: IdentityFilter{Me: true}}, " AND [System.AssignedTo] = @Me"},
		{"assignee none", WIQLFilter{Assignee: IdentityFilter{None: true}}, " AND [System.AssignedTo] = ''"},
		{"assignee user", WIQLFilter{Assignee: IdentityFilter{Value: "jane@contoso.com"}}, " AND [System.AssignedTo] = 'jane@contoso.com'"},
		{"author me", WIQLFilter{Author: IdentityFilter{Me: true}}, " AND [System.CreatedBy] = @Me"},
		{"author user", WIQLFilter{Author: IdentityFilter{Value: "Jane Doe"}}, " AND [System.CreatedBy] = 'Jane Doe'"},
		{"area", WIQLFilter{AreaPath: `Proj\Web`}, ` AND [System.AreaPath] UNDER 'Proj\Web'`},
		{"current iteration resolved", WIQLFilter{IterationPath: `Proj\Sprint 7`}, ` AND [System.IterationPath] UNDER 'Proj\Sprint 7'`},
		{"tags", WIQLFilter{Tags: []string{"ui", "p1"}}, " AND [System.Tags] CONTAINS 'ui' AND [System.Tags] CONTAINS 'p1'"},
		{"ids", WIQLFilter{IDs: []int{12, 7, 300}}, " AND [System.Id] IN (12, 7, 300)"},
		{"field", WIQLFilter{Fields: []FieldFilter{{RefName: "Microsoft.VSTS.Common.Priority", Value: "1"}}}, " AND [Microsoft.VSTS.Common.Priority] = '1'"},
		{"combined", WIQLFilter{Types: []string{"Bug"}, States: []string{"Active"}, Tags: []string{"x"}, IDs: []int{1}},
			" AND [System.WorkItemType] = 'Bug' AND [System.State] = 'Active' AND [System.Tags] CONTAINS 'x' AND [System.Id] IN (1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildWIQL(tt.filter)
			if err != nil {
				t.Fatalf("BuildWIQL: %v", err)
			}
			if want := wiqlTestPrefix + tt.want + wiqlTestOrder; got != want {
				t.Errorf("got\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestBuildWIQLEscaping(t *testing.T) {
	payloads := []struct {
		value, literal string
	}{
		{"O'Brien", "'O''Brien'"},
		{"' OR 1=1", "''' OR 1=1'"},
		{"x' OR [System.Id] > 0 OR 'a'='a", "'x'' OR [System.Id] > 0 OR ''a''=''a'"},
		{`C:\path\`, `'C:\path\'`},
		{"a]b", "'a]b'"},
		{"@Me", "'@Me'"},
	}
	for _, p := range payloads {
		t.Run(p.value, func(t *testing.T) {
			f := WIQLFilter{
				Types:         []string{p.value},
				States:        []string{p.value},
				Assignee:      IdentityFilter{Value: p.value},
				Author:        IdentityFilter{Value: p.value},
				AreaPath:      p.value,
				IterationPath: p.value,
				Tags:          []string{p.value},
				Fields:        []FieldFilter{{RefName: "Custom.Field", Value: p.value}},
			}
			got, err := BuildWIQL(f)
			if err != nil {
				t.Fatalf("BuildWIQL: %v", err)
			}
			if n := strings.Count(got, p.literal); n != 8 {
				t.Errorf("literal %s appears %d times, want 8 in\n%s", p.literal, n, got)
			}
			outside := wiqlOutsideLiterals(got)
			for _, bad := range []string{" OR ", "1=1", "@Me", "]b", `\`} {
				if strings.Contains(outside, bad) {
					t.Errorf("%q escaped a string literal:\n%s", bad, outside)
				}
			}
		})
	}
}

func TestBuildWIQLRejectsBadFieldNames(t *testing.T) {
	for _, name := range []string{"", "System.Title] = 'x' OR [System.Id", "Story Points", "a'b", "a]"} {
		if _, err := BuildWIQL(WIQLFilter{Fields: []FieldFilter{{RefName: name, Value: "1"}}}); err == nil {
			t.Errorf("field %q: expected error", name)
		}
	}
}

func TestBuildWIQLLengthLimit(t *testing.T) {
	ids := make([]int, 1000)
	for i := range ids {
		ids[i] = 100000000 + i
	}
	got, err := BuildWIQL(WIQLFilter{IDs: ids})
	if err != nil {
		t.Fatalf("1000 IDs: %v", err)
	}
	if len(got) > MaxWIQLLength {
		t.Errorf("length %d > %d", len(got), MaxWIQLLength)
	}

	_, err = BuildWIQL(WIQLFilter{Tags: []string{strings.Repeat("a", MaxWIQLLength)}})
	if err == nil || !strings.Contains(err.Error(), "32000") {
		t.Errorf("expected length error, got %v", err)
	}
}
