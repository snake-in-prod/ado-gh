package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/witfixtures"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkItemClient_Fields(t *testing.T) {
	org := "wifieldsorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/fields"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "fields.json"))))

	fields, err := c.Fields(context.Background())
	require.NoError(t, err)
	byRef := map[string]Field{}
	for _, f := range fields {
		byRef[f.ReferenceName] = f
	}
	assert.Equal(t, Field{Name: "Story Points", ReferenceName: "Microsoft.VSTS.Scheduling.StoryPoints", Type: "double"},
		byRef["Microsoft.VSTS.Scheduling.StoryPoints"])
	assert.Equal(t, Field{Name: "Assigned To", ReferenceName: "System.AssignedTo", Type: "string", IsIdentity: true},
		byRef["System.AssignedTo"])
	assert.Equal(t, Field{Name: "Customer Impact", ReferenceName: "Custom.CustomerImpact", Type: "string", IsPicklist: true},
		byRef["Custom.CustomerImpact"])
	assert.True(t, byRef["System.Id"].ReadOnly)
}

func TestWorkItemClient_FieldsSkipsDeletedAndMapsErrors(t *testing.T) {
	org := "wifieldsdelorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/fields"), httpmock.StringResponse(`{"count": 2, "value": [
		{"name": "Old", "referenceName": "Custom.Old", "type": "string", "isDeleted": true},
		{"name": "Title", "referenceName": "System.Title", "type": "string"}]}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/fields"), httpmock.StatusJSONResponse(http.StatusUnauthorized,
		map[string]any{"message": "denied"}))

	fields, err := c.Fields(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []Field{{Name: "Title", ReferenceName: "System.Title", Type: "string"}}, fields)

	_, err = c.Fields(context.Background())
	assert.Error(t, err)
}

func TestWorkItemClient_FieldAllowedValues(t *testing.T) {
	org := "wiallowedorg"
	c, reg := newWITClient(t, org)
	var expand string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/Customer%20Request/fields/Custom.CustomerImpact"),
		func(r *http.Request) (*http.Response, error) {
			expand = r.URL.Query().Get("$expand")
			return httpmock.StringResponse(`{"referenceName": "Custom.CustomerImpact", "allowedValues": ["High", "Medium", "Low"]}`)(r)
		})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/Bug/fields/Microsoft.VSTS.Common.Priority"),
		httpmock.StringResponse(`{"referenceName": "Microsoft.VSTS.Common.Priority", "allowedValues": [1, 2]}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/Bug/fields/Custom.Missing"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF51535: Cannot find field Custom.Missing."}))
	ctx := context.Background()

	vals, err := c.FieldAllowedValues(ctx, "Customer Request", "Custom.CustomerImpact")
	require.NoError(t, err)
	assert.Equal(t, []string{"High", "Medium", "Low"}, vals)
	assert.Equal(t, "allowedValues", expand, "never $expand=all: it lists every identity")

	vals, err = c.FieldAllowedValues(ctx, "Bug", "Microsoft.VSTS.Common.Priority")
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, vals)

	_, err = c.FieldAllowedValues(ctx, "Bug", "Custom.Missing")
	assert.True(t, IsNotFound(err))
}
