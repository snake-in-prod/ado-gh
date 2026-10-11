package shared

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// maxFieldSuggestions caps the close matches listed for an unknown field.
const maxFieldSuggestions = 5

// FieldResolver turns `--field Name=Value` arguments into reference names and typed values.
// Field definitions and allowed values are fetched once per resolver, so use one per invocation.
type FieldResolver struct {
	Client *api.WorkItemClient
	// Identity turns a value for an identity field (@me, email, display name) into the value
	// to send. nil sends values unchanged.
	Identity func(ctx context.Context, value string) (string, error)

	fields  []api.Field
	allowed map[[2]string][]string
}

// FieldValue is a resolved `--field` argument.
type FieldValue struct {
	Field api.Field
	// Value is a string, int, float64 or bool; nil clears the field.
	Value any
}

// Op returns the JSON Patch operation that sets the field, or removes it when Value is nil.
func (v FieldValue) Op() webapi.JsonPatchOperation {
	path := "/fields/" + v.Field.ReferenceName
	if v.Value == nil {
		return webapi.JsonPatchOperation{Op: &webapi.OperationValues.Remove, Path: &path}
	}
	return webapi.JsonPatchOperation{Op: &webapi.OperationValues.Add, Path: &path, Value: v.Value}
}

// Resolve parses arg ("Name=Value"; an empty value clears the field) for a work item of type
// typ. Name is a friendly or reference name, matched case-insensitively. The value is checked
// against the field: identity fields go through Identity, picklists must hold one of the type's
// allowed values, and integer, double, boolean and dateTime values are parsed.
func (r *FieldResolver) Resolve(ctx context.Context, typ, arg string) (FieldValue, error) {
	name, value, ok := strings.Cut(arg, "=")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return FieldValue{}, fmt.Errorf("invalid field %q: expected Name=Value", arg)
	}
	f, err := r.Field(ctx, name)
	if err != nil {
		return FieldValue{}, err
	}
	if value == "" {
		return FieldValue{Field: f}, nil
	}
	v, err := r.coerce(ctx, typ, f, value)
	if err != nil {
		return FieldValue{}, err
	}
	return FieldValue{Field: f, Value: v}, nil
}

// Field returns the field whose reference name or friendly name is name (case-insensitive), or
// an error listing close matches.
func (r *FieldResolver) Field(ctx context.Context, name string) (api.Field, error) {
	if r.fields == nil {
		fs, err := r.Client.Fields(ctx)
		if err != nil {
			return api.Field{}, err
		}
		r.fields = fs
	}
	for _, match := range []func(api.Field) string{
		func(f api.Field) string { return f.ReferenceName },
		func(f api.Field) string { return f.Name },
	} {
		if i := slices.IndexFunc(r.fields, func(f api.Field) bool { return strings.EqualFold(match(f), name) }); i >= 0 {
			return r.fields[i], nil
		}
	}
	return api.Field{}, r.unknownField(name)
}

func (r *FieldResolver) unknownField(name string) error {
	want := strings.ToLower(name)
	type scored struct {
		f    api.Field
		dist int
	}
	var matches []scored
	for _, f := range r.fields {
		dist := min(cmdutil.Levenshtein(want, strings.ToLower(f.Name)), cmdutil.Levenshtein(want, strings.ToLower(f.ReferenceName)))
		if dist <= max(2, len(want)/3) || (len(want) >= 3 && strings.Contains(strings.ToLower(f.ReferenceName+" "+f.Name), want)) {
			matches = append(matches, scored{f, dist})
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("unknown field %q", name)
	}
	slices.SortStableFunc(matches, func(a, b scored) int { return cmp.Compare(a.dist, b.dist) })
	var names []string
	for _, s := range matches[:min(len(matches), maxFieldSuggestions)] {
		names = append(names, fmt.Sprintf("%s (%s)", s.f.Name, s.f.ReferenceName))
	}
	return fmt.Errorf("unknown field %q; did you mean: %s", name, strings.Join(names, ", "))
}

// coerce checks isIdentity and isPicklist before Type: identity and picklist fields are stored
// as plain string/integer/double fields.
func (r *FieldResolver) coerce(ctx context.Context, typ string, f api.Field, s string) (any, error) {
	invalid := func(kind, msg string) error {
		return fmt.Errorf("invalid value %q for field %q (%s): %s", s, f.Name, kind, msg)
	}
	switch {
	case f.IsIdentity:
		if r.Identity == nil {
			return s, nil
		}
		v, err := r.Identity(ctx, s)
		if err != nil {
			return nil, invalid("identity", err.Error())
		}
		return v, nil
	case f.IsPicklist:
		allowed, err := r.allowedValues(ctx, typ, f.ReferenceName)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(allowed, func(a string) bool { return strings.EqualFold(a, s) }); i >= 0 {
			s = allowed[i]
		} else if !f.IsPicklistSuggested {
			return nil, invalid("picklist", "allowed values are "+strings.Join(allowed, ", "))
		}
	}
	t := strings.TrimSpace(s)
	switch strings.ToLower(f.Type) {
	case "integer":
		n, err := strconv.Atoi(t)
		if err != nil {
			return nil, invalid(f.Type, "expected a whole number")
		}
		return n, nil
	case "double":
		n, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, invalid(f.Type, "expected a number")
		}
		return n, nil
	case "boolean":
		b, err := strconv.ParseBool(t)
		if err != nil {
			return nil, invalid(f.Type, "expected true or false")
		}
		return b, nil
	case "datetime":
		if tm, err := time.Parse(time.RFC3339, t); err == nil {
			return tm.Format(time.RFC3339), nil
		}
		// Zone-less forms are local time, as the web form reads them.
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
			if tm, err := time.ParseInLocation(layout, t, time.Local); err == nil {
				return tm.Format(time.RFC3339), nil
			}
		}
		return nil, invalid(f.Type, "expected a date (YYYY-MM-DD) or an RFC 3339 time")
	}
	return s, nil
}

// allowedValues returns field's allowed values on type typ, fetched once per type and field.
func (r *FieldResolver) allowedValues(ctx context.Context, typ, field string) ([]string, error) {
	key := [2]string{strings.ToLower(typ), field}
	if v, ok := r.allowed[key]; ok {
		return v, nil
	}
	v, err := r.Client.FieldAllowedValues(ctx, typ, field)
	if err != nil {
		return nil, err
	}
	if r.allowed == nil {
		r.allowed = map[[2]string][]string{}
	}
	r.allowed[key] = v
	return v, nil
}
