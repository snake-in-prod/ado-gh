package shared

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
)

// Work item state categories.
const (
	CategoryProposed   = "Proposed"
	CategoryInProgress = "InProgress"
	CategoryResolved   = "Resolved"
	CategoryCompleted  = "Completed"
	CategoryRemoved    = "Removed"
)

const (
	hiddenCategory = "Microsoft.HiddenCategory"
	bugCategory    = "Microsoft.BugCategory"
)

// Fields --body writes to.
const (
	DescriptionField = "System.Description"
	// ReproStepsField is what the Agile, Scrum and CMMI Bug forms show instead of Description.
	ReproStepsField = "Microsoft.VSTS.TCM.ReproSteps"
)

// typeAliases maps the --type aliases to their category and to the type name tried when the
// project has no such category.
var typeAliases = map[string]struct{ category, typeName string }{
	"bug":     {"Microsoft.BugCategory", "Bug"},
	"story":   {"Microsoft.RequirementCategory", "User Story"},
	"task":    {"Microsoft.TaskCategory", "Task"},
	"feature": {"Microsoft.FeatureCategory", "Feature"},
	"epic":    {"Microsoft.EpicCategory", "Epic"},
}

// stateFilters are the --state keywords and the categories they stand for.
var stateFilters = map[string][]string{
	"open":   {CategoryProposed, CategoryInProgress, CategoryResolved},
	"closed": {CategoryCompleted, CategoryRemoved},
}

// Process resolves work item types and states from a project's process, without assuming
// which process it is. Types and categories are fetched on first use and kept, so create one
// Process per invocation.
type Process struct {
	client     *api.WorkItemClient
	loaded     bool
	types      []api.WorkItemType
	categories []api.WorkItemTypeCategory
	// usable are the names of the types users may pick: not disabled, not Hidden.
	usable []string
	// bodyFields caches BodyField per lowercased type name.
	bodyFields map[string]string
}

// NewProcess returns a Process reading the metadata of client's project.
func NewProcess(client *api.WorkItemClient) *Process {
	return &Process{client: client}
}

func (p *Process) load(ctx context.Context) error {
	if p.loaded {
		return nil
	}
	types, err := p.client.WorkItemTypes(ctx)
	if err != nil {
		return err
	}
	cats, err := p.client.WorkItemTypeCategories(ctx)
	if err != nil {
		return err
	}
	var hidden []string
	for _, c := range cats {
		if strings.EqualFold(c.ReferenceName, hiddenCategory) {
			hidden = append(hidden, c.Types...)
		}
	}
	p.usable = nil
	for _, t := range types {
		if !t.IsDisabled && !slices.ContainsFunc(hidden, func(h string) bool { return strings.EqualFold(h, t.Name) }) {
			p.usable = append(p.usable, t.Name)
		}
	}
	slices.SortFunc(p.usable, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	p.types, p.categories, p.loaded = types, cats, true
	return nil
}

// Types returns the names of the types users may pick, sorted: disabled and Hidden-category
// types are left out.
func (p *Process) Types(ctx context.Context) ([]string, error) {
	if err := p.load(ctx); err != nil {
		return nil, err
	}
	return slices.Clone(p.usable), nil
}

// ResolveType resolves a --type value to a type name: the type's name (any case), else an
// alias (bug, story, task, feature, epic) through its category's default type, else the
// alias's usual type name.
func (p *Process) ResolveType(ctx context.Context, name string) (string, error) {
	if err := p.load(ctx); err != nil {
		return "", err
	}
	if t, ok := p.usableType(name); ok {
		return t, nil
	}
	if alias, ok := typeAliases[strings.ToLower(name)]; ok {
		for _, c := range p.categories {
			if !strings.EqualFold(c.ReferenceName, alias.category) {
				continue
			}
			for _, candidate := range append([]string{c.DefaultType}, c.Types...) {
				if t, ok := p.usableType(candidate); ok {
					return t, nil
				}
			}
		}
		if t, ok := p.usableType(alias.typeName); ok {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown work item type %q; valid types: %s", name, strings.Join(p.usable, ", "))
}

func (p *Process) usableType(name string) (string, bool) {
	i := slices.IndexFunc(p.usable, func(t string) bool { return strings.EqualFold(t, name) })
	if i < 0 {
		return "", false
	}
	return p.usable[i], true
}

// BodyField returns the field --body writes for a typ work item: Repro Steps for a type of the
// Bug category that has that field (Agile, Scrum and CMMI bugs, whose forms show no
// Description), else System.Description. Looked up once per type.
func (p *Process) BodyField(ctx context.Context, typ string) (string, error) {
	if err := p.load(ctx); err != nil {
		return "", err
	}
	key := strings.ToLower(typ)
	if f, ok := p.bodyFields[key]; ok {
		return f, nil
	}
	field := DescriptionField
	isBug := slices.ContainsFunc(p.categories, func(c api.WorkItemTypeCategory) bool {
		return strings.EqualFold(c.ReferenceName, bugCategory) &&
			slices.ContainsFunc(c.Types, func(t string) bool { return strings.EqualFold(t, typ) })
	})
	if isBug {
		refs, err := p.client.TypeFieldRefs(ctx, typ)
		if err != nil {
			return "", err
		}
		if slices.ContainsFunc(refs, func(r string) bool { return strings.EqualFold(r, ReproStepsField) }) {
			field = ReproStepsField
		}
	}
	if p.bodyFields == nil {
		p.bodyFields = map[string]string{}
	}
	p.bodyFields[key] = field
	return field, nil
}

// States returns the states of type typ (any type, also disabled or Hidden ones) in workflow
// order, from the types list, else from the type's states endpoint (fetched once).
func (p *Process) States(ctx context.Context, typ string) ([]api.WorkItemState, error) {
	if err := p.load(ctx); err != nil {
		return nil, err
	}
	i := slices.IndexFunc(p.types, func(t api.WorkItemType) bool { return strings.EqualFold(t.Name, typ) })
	if i >= 0 && p.types[i].States != nil {
		return p.types[i].States, nil
	}
	states, err := p.client.WorkItemTypeStates(ctx, typ)
	if err != nil {
		return nil, err
	}
	if i < 0 {
		p.types = append(p.types, api.WorkItemType{Name: typ, States: states})
	} else {
		p.types[i].States = states
	}
	return states, nil
}

// ExpandStates turns a --state value into state names for types (all usable types when
// empty): "all" is no filter (nil), "open" the Proposed, InProgress and Resolved states,
// "closed" the Completed and Removed states; anything else must name a state (any case).
func (p *Process) ExpandStates(ctx context.Context, types []string, state string) ([]string, error) {
	if strings.EqualFold(state, "all") {
		return nil, nil
	}
	if len(types) == 0 {
		var err error
		if types, err = p.Types(ctx); err != nil {
			return nil, err
		}
	}
	// ponytail: a state name open in one type and closed in another matches both ways;
	// per-type (type AND state) clauses if a process ever does that.
	categories, keyword := stateFilters[strings.ToLower(state)]
	var names []string
	for _, typ := range types {
		states, err := p.States(ctx, typ)
		if err != nil {
			return nil, err
		}
		for _, s := range states {
			if (keyword && slices.Contains(categories, s.Category) || !keyword && strings.EqualFold(s.Name, state)) &&
				!slices.Contains(names, s.Name) {
				names = append(names, s.Name)
			}
		}
	}
	if !keyword && len(names) == 0 {
		return nil, fmt.Errorf("unknown state %q; use open, closed, all or a state of %s", state, strings.Join(types, ", "))
	}
	return names, nil
}

// CloseState returns the state `close` moves a typ work item to: the first state of category
// CategoryCompleted, CategoryResolved (--resolve; else a state named "Resolved") or
// CategoryRemoved (--removed).
func (p *Process) CloseState(ctx context.Context, typ, category string) (string, error) {
	states, err := p.States(ctx, typ)
	if err != nil {
		return "", err
	}
	if s, ok := firstState(states, func(s api.WorkItemState) bool { return s.Category == category }); ok {
		return s, nil
	}
	if category == CategoryResolved {
		if s, ok := firstState(states, func(s api.WorkItemState) bool { return strings.EqualFold(s.Name, "Resolved") }); ok {
			return s, nil
		}
	}
	return "", noStateErr(typ, category, states)
}

// ReopenState returns the state `reopen` moves a typ work item to: its first Proposed state,
// else its first InProgress state (types such as Agile Issue have no Proposed state).
func (p *Process) ReopenState(ctx context.Context, typ string) (string, error) {
	states, err := p.States(ctx, typ)
	if err != nil {
		return "", err
	}
	for _, c := range []string{CategoryProposed, CategoryInProgress} {
		if s, ok := firstState(states, func(s api.WorkItemState) bool { return s.Category == c }); ok {
			return s, nil
		}
	}
	return "", noStateErr(typ, CategoryProposed, states)
}

func firstState(states []api.WorkItemState, match func(api.WorkItemState) bool) (string, bool) {
	i := slices.IndexFunc(states, match)
	if i < 0 {
		return "", false
	}
	return states[i].Name, true
}

func noStateErr(typ, category string, states []api.WorkItemState) error {
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = s.Name
	}
	return fmt.Errorf("work item type %q has no %s state; pick one with --state: %s", typ, category, strings.Join(names, ", "))
}
