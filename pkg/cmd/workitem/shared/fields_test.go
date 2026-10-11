package shared

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/witfixtures"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFieldTestResolver returns a FieldResolver on project "proj" of org (one org per test: the
// SDK caches discovery per base URL), serving fields.json once.
func newFieldTestResolver(t *testing.T, org string) (*FieldResolver, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.WorkItemLocations...)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/fields"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "fields.json"))))
	t.Cleanup(func() { reg.Verify(t) })
	client := api.NewWorkItemClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj")
	return &FieldResolver{Client: client}, reg
}

// fieldTestAllowedValues serves field ref of type typ from the Customer Request type-fields
// fixture, recording the $expand it was asked for.
func fieldTestAllowedValues(t *testing.T, reg *httpmock.Registry, org, typ, ref string, expand *string) {
	t.Helper()
	var list struct {
		Value []map[string]any `json:"value"`
	}
	require.NoError(t, json.Unmarshal(witfixtures.Read(t, "inherited_type_customerrequest_fields.json"), &list))
	for _, f := range list.Value {
		if f["referenceName"] == ref {
			reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/"+url.PathEscape(typ)+"/fields/"+ref),
				func(r *http.Request) (*http.Response, error) {
					*expand = r.URL.Query().Get("$expand")
					return httpmock.JSONResponse(f)(r)
				})
			return
		}
	}
	t.Fatalf("no field %s in fixture", ref)
}

func TestFieldResolverResolve(t *testing.T) {
	localDay := time.Date(2026, 10, 11, 0, 0, 0, 0, time.Local).Format(time.RFC3339)
	tests := []struct {
		arg     string
		wantRef string
		want    any
	}{
		{"Story Points=5", "Microsoft.VSTS.Scheduling.StoryPoints", 5.0},
		{"story points=2.5", "Microsoft.VSTS.Scheduling.StoryPoints", 2.5},
		{"Microsoft.VSTS.Scheduling.StoryPoints=3", "Microsoft.VSTS.Scheduling.StoryPoints", 3.0},
		{"microsoft.vsts.scheduling.storypoints= 8 ", "Microsoft.VSTS.Scheduling.StoryPoints", 8.0},
		{"Priority=2", "Microsoft.VSTS.Common.Priority", 2},
		{"Board Column Done=true", "System.BoardColumnDone", true},
		{"Start Date=2026-10-11T08:30:00Z", "Microsoft.VSTS.Scheduling.StartDate", "2026-10-11T08:30:00Z"},
		{"Start Date=2026-10-11", "Microsoft.VSTS.Scheduling.StartDate", localDay},
		{"Title=a = b", "System.Title", "a = b"},
		{" Title =x", "System.Title", "x"},
		{"Story Points=", "Microsoft.VSTS.Scheduling.StoryPoints", nil},
		{"Assigned To=", "System.AssignedTo", nil},
	}
	for i, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			r, _ := newFieldTestResolver(t, "wifieldresolve"+string(rune('a'+i)))
			r.Identity = func(context.Context, string) (string, error) { return "", errors.New("identity not expected") }
			got, err := r.Resolve(context.Background(), "User Story", tt.arg)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRef, got.Field.ReferenceName)
			assert.Equal(t, tt.want, got.Value)
		})
	}
}

func TestFieldResolverIdentity(t *testing.T) {
	r, _ := newFieldTestResolver(t, "wifieldidentity")
	var asked []string
	r.Identity = func(_ context.Context, v string) (string, error) {
		asked = append(asked, v)
		if v == "nobody" {
			return "", errors.New(`no user matches "nobody"`)
		}
		return "ann@example.com", nil
	}
	ctx := context.Background()

	// Assigned To is type "string" with isIdentity: the identity resolver runs, and no allowed
	// values are fetched (none are stubbed).
	got, err := r.Resolve(ctx, "Bug", "Assigned To=@me")
	require.NoError(t, err)
	assert.Equal(t, FieldValue{Field: api.Field{Name: "Assigned To", ReferenceName: "System.AssignedTo", Type: "string", IsIdentity: true},
		Value: "ann@example.com"}, got)

	_, err = r.Resolve(ctx, "Bug", "System.AssignedTo=nobody")
	assert.EqualError(t, err, `invalid value "nobody" for field "Assigned To" (identity): no user matches "nobody"`)
	assert.Equal(t, []string{"@me", "nobody"}, asked)

	r.Identity = nil
	got, err = r.Resolve(ctx, "Bug", "Assigned To=Ann Smith")
	require.NoError(t, err)
	assert.Equal(t, "Ann Smith", got.Value, "without a resolver the value is sent as is")
}

func TestFieldResolverPicklist(t *testing.T) {
	org := "wifieldpicklist"
	r, reg := newFieldTestResolver(t, org)
	var expand string
	fieldTestAllowedValues(t, reg, org, "Customer Request", "Custom.CustomerImpact", &expand)
	ctx := context.Background()

	got, err := r.Resolve(ctx, "Customer Request", "Customer Impact=high")
	require.NoError(t, err)
	assert.Equal(t, "High", got.Value, "picklist values take the list's spelling")
	assert.Equal(t, "allowedValues", expand, "allowed values use $expand=allowedValues, never all")

	// Customer Impact is type "string": only the picklist check rejects this. Fields and allowed
	// values are cached, so no further requests are made.
	_, err = r.Resolve(ctx, "customer request", "Custom.CustomerImpact=Huge")
	assert.EqualError(t, err, `invalid value "Huge" for field "Customer Impact" (picklist): allowed values are High, Medium, Low`)
}

func TestFieldResolverSuggestedPicklist(t *testing.T) {
	org := "wifieldsuggested"
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.WorkItemLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/fields"), httpmock.StringResponse(`{"value": [
		{"name": "Size", "referenceName": "Custom.Size", "type": "integer", "isPicklist": true, "isPicklistSuggested": true}]}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/Bug/fields/Custom.Size"),
		httpmock.StringResponse(`{"referenceName": "Custom.Size", "allowedValues": [1, 2, 3]}`))
	r := &FieldResolver{Client: api.NewWorkItemClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj")}
	ctx := context.Background()

	got, err := r.Resolve(ctx, "Bug", "Size=7")
	require.NoError(t, err)
	assert.Equal(t, 7, got.Value, "a suggested picklist accepts other values, still typed")

	_, err = r.Resolve(ctx, "Bug", "Size=big")
	assert.EqualError(t, err, `invalid value "big" for field "Size" (integer): expected a whole number`)
}

func TestFieldResolverErrors(t *testing.T) {
	tests := []struct {
		arg, want string
	}{
		{"Story Points=abc", `invalid value "abc" for field "Story Points" (double): expected a number`},
		{"Priority=1.5", `invalid value "1.5" for field "Priority" (integer): expected a whole number`},
		{"Board Column Done=maybe", `invalid value "maybe" for field "Board Column Done" (boolean): expected true or false`},
		{"Start Date=tomorrow", `invalid value "tomorrow" for field "Start Date" (dateTime): expected a date (YYYY-MM-DD) or an RFC 3339 time`},
		{"Story Pts=3", `unknown field "Story Pts"; did you mean: Story Points (Microsoft.VSTS.Scheduling.StoryPoints)`},
		{"StoryPoints=3", `unknown field "StoryPoints"; did you mean: Story Points (Microsoft.VSTS.Scheduling.StoryPoints)`},
		{"Qwertyuiop=1", `unknown field "Qwertyuiop"`},
	}
	for i, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			r, _ := newFieldTestResolver(t, "wifielderr"+string(rune('a'+i)))
			_, err := r.Resolve(context.Background(), "Bug", tt.arg)
			assert.EqualError(t, err, tt.want)
		})
	}
}

func TestFieldResolverMalformedArg(t *testing.T) {
	// No client: malformed arguments fail before any request.
	r := &FieldResolver{}
	for _, arg := range []string{"Title", "=5", " =5"} {
		_, err := r.Resolve(context.Background(), "Bug", arg)
		assert.EqualError(t, err, `invalid field "`+arg+`": expected Name=Value`)
	}
}

func TestFieldResolverFieldsFetchError(t *testing.T) {
	org := "wifieldfetcherr"
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.WorkItemLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/fields"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/fields"), httpmock.StringResponse(`{"value": [
		{"name": "Impact", "referenceName": "Custom.Impact", "type": "string", "isPicklist": true}]}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/Bug/fields/Custom.Impact"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF51535: Cannot find field Custom.Impact."}))
	r := &FieldResolver{Client: api.NewWorkItemClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj")}
	ctx := context.Background()

	_, err := r.Resolve(ctx, "Bug", "Title=x")
	assert.ErrorContains(t, err, "denied")
	_, err = r.Resolve(ctx, "Bug", "Impact=High")
	assert.ErrorContains(t, err, "TF51535")
}

func TestFieldValueOp(t *testing.T) {
	f := api.Field{ReferenceName: "Microsoft.VSTS.Scheduling.StoryPoints"}
	add, remove := webapi.OperationValues.Add, webapi.OperationValues.Remove
	path := "/fields/Microsoft.VSTS.Scheduling.StoryPoints"

	assert.Equal(t, webapi.JsonPatchOperation{Op: &add, Path: &path, Value: 5.0}, FieldValue{Field: f, Value: 5.0}.Op())
	assert.Equal(t, webapi.JsonPatchOperation{Op: &remove, Path: &path}, FieldValue{Field: f}.Op(), "nil clears the field")
}
