package witfixtures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/workitemtracking"
)

type list[T any] struct {
	Count int `json:"count"`
	Value []T `json:"value"`
}

func decode[T any](t *testing.T, b []byte) []T {
	t.Helper()
	var l list[T]
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	if l.Count != len(l.Value) || l.Count == 0 {
		t.Fatalf("count = %d, len(value) = %d", l.Count, len(l.Value))
	}
	return l.Value
}

func typeByName(types []workitemtracking.WorkItemType, name string) *workitemtracking.WorkItemType {
	for i := range types {
		if *types[i].Name == name {
			return &types[i]
		}
	}
	return nil
}

func category(cats []workitemtracking.WorkItemTypeCategory, ref string) *workitemtracking.WorkItemTypeCategory {
	for i := range cats {
		if *cats[i].ReferenceName == ref {
			return &cats[i]
		}
	}
	return nil
}

// Types the org-level process API leaves out but every project has (all in the Hidden category).
var projectOnlyTypes = []string{"Shared Steps", "Shared Parameter", "Code Review Request",
	"Code Review Response", "Feedback Request", "Feedback Response"}

func TestProcessFixtures(t *testing.T) {
	requirement := map[string][]string{
		"agile": {"User Story"}, "scrum": {"Product Backlog Item"}, "cmmi": {"Requirement"},
		"basic": {"Issue"}, "inherited": {"User Story", "Customer Request"},
	}
	for _, p := range Processes {
		t.Run(p, func(t *testing.T) {
			types := decode[workitemtracking.WorkItemType](t, Process(t, p, "types"))
			for _, typ := range types {
				counts := map[string]int{}
				for _, s := range *typ.States {
					counts[*s.Category]++
				}
				if counts["Completed"] != 1 {
					t.Errorf("%s: %d Completed states, want 1", *typ.Name, counts["Completed"])
				}
				if counts["Proposed"]+counts["InProgress"] == 0 {
					t.Errorf("%s: no Proposed or InProgress state", *typ.Name)
				}
			}

			cats := decode[workitemtracking.WorkItemTypeCategory](t, Process(t, p, "categories"))
			for _, c := range cats {
				for _, m := range *c.WorkItemTypes {
					if typeByName(types, *m.Name) == nil && !slices.Contains(projectOnlyTypes, *m.Name) {
						t.Errorf("%s lists unknown type %q", *c.ReferenceName, *m.Name)
					}
				}
			}
			req := category(cats, "Microsoft.RequirementCategory")
			var members []string
			for _, m := range *req.WorkItemTypes {
				members = append(members, *m.Name)
			}
			if !slices.Equal(members, requirement[p]) || *req.DefaultWorkItemType.Name != requirement[p][0] {
				t.Errorf("requirement category = %v (default %s), want %v", members, *req.DefaultWorkItemType.Name, requirement[p])
			}

			backlogs := decode[work.BacklogLevelConfiguration](t, Process(t, p, "backlogs"))
			i := slices.IndexFunc(backlogs, func(b work.BacklogLevelConfiguration) bool { return *b.Type == work.BacklogTypeValues.Requirement })
			if i < 0 || *backlogs[i].Id != "Microsoft.RequirementCategory" || len(*backlogs[i].WorkItemTypes) != len(requirement[p]) {
				t.Errorf("requirement backlog missing or wrong: %d", i)
			}

			var board work.Board
			if err := json.Unmarshal(Process(t, p, "board"), &board); err != nil {
				t.Fatal(err)
			}
			if *board.Name != *backlogs[i].Name || board.Fields.ColumnField == nil {
				t.Errorf("board %q for backlog %q", *board.Name, *backlogs[i].Name)
			}
			for _, col := range *board.Columns {
				for typ, state := range *col.StateMappings {
					wit := typeByName(types, typ)
					if wit == nil || !slices.ContainsFunc(*wit.States, func(s workitemtracking.WorkItemStateColor) bool { return *s.Name == state }) {
						t.Errorf("column %q maps %s to unknown state %q", *col.Name, typ, state)
					}
				}
			}
		})
	}
}

func TestProcessFacts(t *testing.T) {
	states := func(p, typ string) []string {
		var out []string
		for _, s := range decode[workitemtracking.WorkItemStateColor](t, TypeStates(t, p, typ)) {
			out = append(out, *s.Name+"/"+*s.Category)
		}
		return out
	}
	tests := []struct {
		process, typ string
		want         []string
	}{
		{"agile", "Bug", []string{"New/Proposed", "Active/InProgress", "Resolved/Resolved", "Closed/Completed"}},
		{"agile", "Issue", []string{"Active/InProgress", "Closed/Completed"}},
		{"scrum", "Impediment", []string{"Open/InProgress", "Closed/Completed"}},
		{"scrum", "Product Backlog Item", []string{"New/Proposed", "Approved/Proposed", "Committed/InProgress", "Done/Completed", "Removed/Removed"}},
		{"basic", "Issue", []string{"To Do/Proposed", "Doing/InProgress", "Done/Completed"}},
		{"cmmi", "Requirement", []string{"Proposed/Proposed", "Active/InProgress", "Resolved/InProgress", "Closed/Completed"}},
		{"inherited", "Bug", []string{"New/Proposed", "Triaged/Proposed", "Active/InProgress", "Resolved/Resolved", "Closed/Completed"}},
		{"inherited", "Customer Request", []string{"New/Proposed", "In Review/InProgress", "Closed/Completed"}},
	}
	for _, tt := range tests {
		if got := states(tt.process, tt.typ); !slices.Equal(got, tt.want) {
			t.Errorf("%s %s states = %v, want %v", tt.process, tt.typ, got, tt.want)
		}
	}

	// Basic has no Bug type; its Bug category resolves to Issue (live).
	bug := category(decode[workitemtracking.WorkItemTypeCategory](t, Process(t, "basic", "categories")), "Microsoft.BugCategory")
	if *bug.DefaultWorkItemType.Name != "Issue" {
		t.Errorf("basic bug category default = %s", *bug.DefaultWorkItemType.Name)
	}
	issue := typeByName(decode[workitemtracking.WorkItemType](t, Process(t, "inherited", "types")), "Issue")
	if issue == nil || !*issue.IsDisabled {
		t.Error("inherited Issue should be disabled")
	}

	fields := decode[workitemtracking.WorkItemField2](t, Read(t, "fields.json"))
	byRef := map[string]workitemtracking.WorkItemField2{}
	for _, f := range fields {
		byRef[*f.ReferenceName] = f
	}
	if f := byRef["System.AssignedTo"]; !*f.IsIdentity || *f.Type != workitemtracking.FieldTypeValues.String {
		t.Errorf("AssignedTo = %+v", f)
	}
	if f := byRef["Custom.CustomerImpact"]; !*f.IsPicklist || f.PicklistId == nil {
		t.Errorf("CustomerImpact = %+v", f)
	}
	tf := decode[workitemtracking.WorkItemTypeFieldWithReferences](t, Read(t, "inherited_type_customerrequest_fields.json"))
	if i := slices.IndexFunc(tf, func(f workitemtracking.WorkItemTypeFieldWithReferences) bool {
		return *f.ReferenceName == "Custom.CustomerImpact"
	}); i < 0 || len(*tf[i].AllowedValues) != 3 {
		t.Errorf("Customer Request lacks the Custom.CustomerImpact picklist")
	}
	live := decode[workitemtracking.WorkItemTypeFieldWithReferences](t, Read(t, "live_basic_type_issue_fields.json"))
	if !slices.ContainsFunc(live, func(f workitemtracking.WorkItemTypeFieldWithReferences) bool {
		return *f.ReferenceName == "System.Reason" && len(*f.AllowedValues) > 0
	}) {
		t.Error("live Issue fields lack System.Reason allowed values")
	}

	rels := decode[workitemtracking.WorkItemRelationType](t, Read(t, "live_relationtypes.json"))
	if !slices.ContainsFunc(rels, func(r workitemtracking.WorkItemRelationType) bool {
		return *r.ReferenceName == "System.LinkTypes.Hierarchy-Reverse" && *r.Name == "Parent"
	}) {
		t.Error("relation types lack Parent")
	}
	if boards := decode[work.BoardReference](t, Read(t, "live_basic_boards.json")); len(boards) != 2 {
		t.Errorf("basic boards = %d", len(boards))
	}
}

// TestFixturesScrubbed guards against committing the live org, real GUIDs, scale-unit hosts or
// identities, and checks the README documents every file.
func TestFixturesScrubbed(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(Dir(), "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", Dir(), err)
	}
	readme, err := os.ReadFile(filepath.Join(Dir(), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	guid := regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	hex32 := regexp.MustCompile(`(?i)\b[0-9a-f]{32}\b|WEF_[0-9a-f]{32}_`)
	email := regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]+`)
	for _, f := range files {
		name := filepath.Base(f)
		b := Read(t, name)
		s := string(b)
		for _, bad := range []string{"snake-in-prod", "visualstudio.com", "descriptor", "uniqueName", "\r\n"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
		for _, g := range guid.FindAllString(s, -1) {
			if !strings.HasPrefix(g, "00000000-0000-0000-0000-") {
				t.Errorf("%s contains unscrubbed GUID %s", name, g)
			}
		}
		for _, h := range hex32.FindAllString(s, -1) {
			if !strings.Contains(h, "0000000000000000000000000") {
				t.Errorf("%s contains unscrubbed id %s", name, h)
			}
		}
		if e := email.FindString(s); e != "" {
			t.Errorf("%s contains an email-like value %s", name, e)
		}
		if !json.Valid(b) {
			t.Errorf("%s is not valid JSON", name)
		}
		if !strings.Contains(string(readme), name) {
			t.Errorf("README.md does not document %s", name)
		}
	}
}

func TestLoaderErrors(t *testing.T) {
	if _, err := read("nope.json"); err == nil {
		t.Error("read of a missing file succeeded")
	}
	if _, err := processFile("nope", "types"); err == nil {
		t.Error("unknown process succeeded")
	}
	if _, err := typeStates("agile", "Nope"); err == nil || !strings.Contains(err.Error(), `"Nope"`) {
		t.Errorf("unknown type err = %v", err)
	}
	if _, err := typeStates("nope", "Bug"); err == nil {
		t.Error("unknown process succeeded")
	}
}
