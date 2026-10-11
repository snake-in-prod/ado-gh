package shared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// ruleErrorField finds the field named in a TF401320 rule error, e.g. "TF401320: Rule Error
// for field Resolved Reason. Error code: Required, ...".
var ruleErrorField = regexp.MustCompile(`TF401320: Rule Error for field (.+?)\. Error code`)

// StateChange is a `close` or `reopen` of work items.
type StateChange struct {
	// Refs are work item references as ParseRef reads them.
	Refs []string
	// Category is the target state category: CategoryCompleted, CategoryResolved or
	// CategoryRemoved closes, CategoryProposed reopens.
	Category string
	// State, when not "", names the target state and overrides Category.
	State string
	// Reason sets System.Reason; checked against the type's allowed values, then by the server.
	Reason string
	// Comment is added in Markdown with the state change.
	Comment string
	// Fields are extra `Name=Value` field arguments.
	Fields []string
}

// stateProject is what a StateChange needs per project, built once per project.
type stateProject struct {
	repo    adocontext.Repo
	client  *api.WorkItemClient
	proc    *Process
	fields  *FieldResolver
	reasons map[string][]string
}

func (s *StateChange) reopen() bool { return s.Category == CategoryProposed }

// Run moves every work item of s.Refs to its target state. An item already there gets a
// warning. With several items, the failed ones are reported and Run fails after trying all.
func (s *StateChange) Run(ctx context.Context, f *cmdutil.Factory) error {
	type item struct {
		ref  string
		id   int
		repo adocontext.Repo
	}
	var items []item
	for _, ref := range s.Refs {
		id, repo, err := ParseRef(ref)
		if err != nil {
			return err
		}
		items = append(items, item{ref, id, repo})
	}
	verb := "close"
	if s.reopen() {
		verb = "reopen"
	}
	ios := f.IOStreams
	projects := map[adocontext.Repo]*stateProject{}
	var done, failed []string
	for _, it := range items {
		err := s.changeOne(ctx, f, projects, it.id, it.repo)
		if err == nil {
			done = append(done, strconv.Itoa(it.id))
			continue
		}
		if len(items) == 1 {
			return err
		}
		failed = append(failed, strconv.Itoa(it.id))
		fmt.Fprintf(ios.ErrOut, "%s Failed to %s work item %d: %v\n", ios.ColorScheme().Red("X"), verb, it.id, err)
	}
	if len(failed) == 0 {
		return nil
	}
	if len(done) == 0 {
		return fmt.Errorf("failed to %s work items %s", verb, strings.Join(failed, ", "))
	}
	return fmt.Errorf("failed to %s work items %s; done: %s", verb, strings.Join(failed, ", "), strings.Join(done, ", "))
}

func (s *StateChange) project(ctx context.Context, f *cmdutil.Factory, cache map[adocontext.Repo]*stateProject, repo adocontext.Repo) (*stateProject, error) {
	if repo.Project == "" {
		var err error
		if repo, err = f.BaseProject(); err != nil {
			return nil, err
		}
	}
	key := adocontext.Repo{Host: repo.Host, Org: strings.ToLower(repo.Org), Project: strings.ToLower(repo.Project)}
	if p, ok := cache[key]; ok {
		return p, nil
	}
	hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
	if err != nil {
		return nil, err
	}
	orgURL := authshared.OrgURL(repo.Host, repo.Org)
	c := api.NewWorkItemClient(hc, orgURL, repo.Project)
	resolver := &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}
	p := &stateProject{repo: repo, client: c, proc: NewProcess(c), reasons: map[string][]string{},
		fields: &FieldResolver{Client: c, Identity: func(ctx context.Context, v string) (string, error) {
			u, err := resolver.ResolveUser(ctx, v)
			return u.FieldValue(), err
		}},
	}
	cache[key] = p
	return p, nil
}

func (s *StateChange) changeOne(ctx context.Context, f *cmdutil.Factory, cache map[adocontext.Repo]*stateProject, id int, repo adocontext.Repo) error {
	p, err := s.project(ctx, f, cache, repo)
	if err != nil {
		return err
	}
	wi, err := p.client.WorkItem(ctx, id, false)
	if err != nil {
		return err
	}
	typ, _ := wi.Fields["System.WorkItemType"].(string)
	current, _ := wi.Fields["System.State"].(string)
	title, _ := wi.Fields["System.Title"].(string)
	states, err := p.proc.States(ctx, typ)
	if err != nil {
		return err
	}
	target, err := s.target(ctx, p.proc, typ, states)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	cs := ios.ColorScheme()
	if s.already(states, current, target) {
		fmt.Fprintf(ios.ErrOut, "%s Work item %d (%s) is already %s\n", cs.Yellow("!"), id, title, current)
		return nil
	}

	revPath := "/rev"
	ops := []webapi.JsonPatchOperation{
		// The target state was picked from the state read; fail if it changed since.
		{Op: &webapi.OperationValues.Test, Path: &revPath, Value: wi.Rev},
		addOp(fieldsPrefix+"System.State", target),
	}
	if s.Reason != "" {
		reason, err := p.reason(ctx, typ, s.Reason)
		if err != nil {
			return err
		}
		ops = append(ops, addOp(fieldsPrefix+"System.Reason", reason))
	}
	for _, arg := range s.Fields {
		v, err := p.fields.Resolve(ctx, typ, arg)
		if err != nil {
			return err
		}
		ops = append(ops, v.Ops(wi, false)...)
	}
	send := func(ops []webapi.JsonPatchOperation) (*api.WorkItem, error) {
		return p.client.UpdateWorkItem(ctx, id, ops)
	}
	if s.Comment == "" {
		_, err = SendWithHTMLFallback(ops, send)
	} else {
		_, err = send(append(slices.Clone(ops), BodyOps("System.History", s.Comment, nil, false)...))
		if err != nil && historyRejected(err) {
			// The org takes no Markdown History: change the state (Markdown field values as HTML
			// if need be), then comment through the comments API, which keeps the Markdown.
			if _, err = SendWithHTMLFallback(ops, send); err == nil {
				if _, cerr := p.client.AddComment(ctx, id, s.Comment, true); cerr != nil {
					return fmt.Errorf("work item %d is now %s, but adding the comment failed: %w", id, target, cerr)
				}
			}
		}
	}
	if err != nil {
		return updateErr(id, err)
	}
	if ios.IsStdoutTTY() {
		verb := "Closed"
		if s.reopen() {
			verb = "Reopened"
		}
		fmt.Fprintf(ios.ErrOut, "%s %s work item %d (%s) as %s\n", cs.Green("✓"), verb, id, title, target)
	}
	return nil
}

// target returns the state a typ work item moves to.
func (s *StateChange) target(ctx context.Context, proc *Process, typ string, states []api.WorkItemState) (string, error) {
	switch {
	case s.State != "":
		if name, ok := firstState(states, func(st api.WorkItemState) bool { return strings.EqualFold(st.Name, s.State) }); ok {
			return name, nil
		}
		names := make([]string, len(states))
		for i, st := range states {
			names[i] = st.Name
		}
		return "", fmt.Errorf("work item type %q has no state %q; valid states: %s", typ, s.State, strings.Join(names, ", "))
	case s.reopen():
		return proc.ReopenState(ctx, typ)
	}
	return proc.CloseState(ctx, typ, s.Category)
}

// already reports whether current is the target or, without --state, a state of a target
// category: Completed, Resolved or Removed for close, Proposed or InProgress (open) for reopen.
func (s *StateChange) already(states []api.WorkItemState, current, target string) bool {
	if strings.EqualFold(current, target) {
		return true
	}
	if s.State != "" {
		return false
	}
	categories := []string{s.Category}
	if s.reopen() {
		categories = []string{CategoryProposed, CategoryInProgress}
	}
	i := slices.IndexFunc(states, func(st api.WorkItemState) bool { return strings.EqualFold(st.Name, current) })
	return i >= 0 && slices.Contains(categories, states[i].Category)
}

// reason returns the allowed System.Reason value of type typ matching reason (any case). The
// allowed values are the type's, across all transitions, so the server may still refuse it.
func (p *stateProject) reason(ctx context.Context, typ, reason string) (string, error) {
	allowed, ok := p.reasons[typ]
	if !ok {
		var err error
		if allowed, err = p.client.FieldAllowedValues(ctx, typ, "System.Reason"); err != nil {
			return "", err
		}
		p.reasons[typ] = allowed
	}
	if len(allowed) == 0 {
		return reason, nil
	}
	if i := slices.IndexFunc(allowed, func(a string) bool { return strings.EqualFold(a, reason) }); i >= 0 {
		return allowed[i], nil
	}
	return "", fmt.Errorf("invalid reason %q for %s; allowed reasons: %s", reason, typ, strings.Join(allowed, ", "))
}

// historyRejected reports whether err is a 400 refusing the Markdown History ops.
func historyRejected(err error) bool {
	var he *api.HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusBadRequest {
		return false
	}
	m := strings.ToLower(he.Message)
	return strings.Contains(m, "multilinefieldsformat") || strings.Contains(m, "system.history")
}

// updateErr explains a failed state change: a changed work item, or a rule error with a hint
// to set the field (the server message is kept as is).
func updateErr(id int, err error) error {
	if api.IsRevConflict(err) {
		return fmt.Errorf("work item %d changed since read; retry", id)
	}
	if m := ruleErrorField.FindStringSubmatch(err.Error()); m != nil {
		return fmt.Errorf("%w\nset the field with -f \"%s=<value>\"", err, m[1])
	}
	return err
}
