// Package shared holds code used by several `ado workitem` commands.
package shared

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxWIQLLength is the Azure Boards limit on WIQL query length in characters.
const MaxWIQLLength = 32000

// refNamePattern is what a field reference name may contain. WIQL has no
// escape for `]`, so anything else is refused rather than quoted.
var refNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

// IdentityFilter matches an identity field. Me emits the @Me macro, None
// matches unassigned, and Value is an already-resolved identity.
type IdentityFilter struct {
	Me    bool
	None  bool
	Value string
}

// FieldFilter matches a field by equality. RefName must be a resolved
// reference name; Value is always emitted as a quoted string.
type FieldFilter struct {
	RefName string
	Value   string
}

// WIQLFilter holds already-resolved list filters. Empty fields add no clause.
// IterationPath is a concrete path; callers resolve `@current` beforehand.
type WIQLFilter struct {
	Types         []string
	States        []string
	Assignee      IdentityFilter
	Author        IdentityFilter
	AreaPath      string
	IterationPath string
	Tags          []string
	IDs           []int
	Fields        []FieldFilter
}

// BuildWIQL turns f into a flat WIQL query scoped to the current project,
// newest changes first. Every user value is emitted as a quoted literal.
func BuildWIQL(f WIQLFilter) (string, error) {
	clauses := []string{"[System.TeamProject] = @project"}
	clauses = appendIn(clauses, "System.WorkItemType", f.Types)
	clauses = appendIn(clauses, "System.State", f.States)
	clauses = appendIdentity(clauses, "System.AssignedTo", f.Assignee)
	clauses = appendIdentity(clauses, "System.CreatedBy", f.Author)
	if f.AreaPath != "" {
		clauses = append(clauses, "[System.AreaPath] UNDER "+quoteWIQL(f.AreaPath))
	}
	if f.IterationPath != "" {
		clauses = append(clauses, "[System.IterationPath] UNDER "+quoteWIQL(f.IterationPath))
	}
	for _, tag := range f.Tags {
		clauses = append(clauses, "[System.Tags] CONTAINS "+quoteWIQL(tag))
	}
	if len(f.IDs) > 0 {
		ids := make([]string, len(f.IDs))
		for i, id := range f.IDs {
			ids[i] = strconv.Itoa(id)
		}
		clauses = append(clauses, "[System.Id] IN ("+strings.Join(ids, ", ")+")")
	}
	for _, fld := range f.Fields {
		if !refNamePattern.MatchString(fld.RefName) {
			return "", fmt.Errorf("invalid field reference name %q", fld.RefName)
		}
		clauses = append(clauses, "["+fld.RefName+"] = "+quoteWIQL(fld.Value))
	}

	q := "SELECT [System.Id] FROM WorkItems WHERE " + strings.Join(clauses, " AND ") +
		" ORDER BY [System.ChangedDate] DESC, [System.Id] DESC"
	if len(q) > MaxWIQLLength {
		return "", fmt.Errorf("query is %d characters, over the %d-character WIQL limit; narrow the filters", len(q), MaxWIQLLength)
	}
	return q, nil
}

// quoteWIQL returns s as a single-quoted WIQL string literal. Doubling the
// quote is the only escape WIQL has; backslashes are literal.
func quoteWIQL(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func appendIn(clauses []string, ref string, values []string) []string {
	switch len(values) {
	case 0:
		return clauses
	case 1:
		return append(clauses, "["+ref+"] = "+quoteWIQL(values[0]))
	}
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = quoteWIQL(v)
	}
	return append(clauses, "["+ref+"] IN ("+strings.Join(quoted, ", ")+")")
}

func appendIdentity(clauses []string, ref string, id IdentityFilter) []string {
	switch {
	case id.Me:
		return append(clauses, "["+ref+"] = @Me")
	case id.None:
		return append(clauses, "["+ref+"] = ''")
	case id.Value != "":
		return append(clauses, "["+ref+"] = "+quoteWIQL(id.Value))
	}
	return clauses
}
