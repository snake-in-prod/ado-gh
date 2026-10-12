package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseClientUpdateEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, comment string
		status        release.EnvironmentStatus
		want          map[string]any
	}{
		{"deploy with comment", "ship it", release.EnvironmentStatusValues.InProgress, map[string]any{"status": "inProgress", "comment": "ship it"}},
		{"cancel", "", release.EnvironmentStatusValues.Canceled, map[string]any{"status": "canceled"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			org := "relenvupd" + string(tt.status)
			c, reg := newReleaseTestClient(t, org)
			var body map[string]any
			reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/release/releases/3/environments/6"), func(r *http.Request) (*http.Response, error) {
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				return httpmock.JSONResponse(map[string]any{"id": 6, "name": "Prod", "status": "queued"})(r)
			})
			e, err := c.UpdateEnvironment(context.Background(), 3, 6, tt.status, tt.comment)
			require.NoError(t, err)
			assert.Equal(t, tt.want, body)
			assert.Equal(t, release.EnvironmentStatusValues.Queued, *e.Status)
		})
	}

	t.Run("error", func(t *testing.T) {
		c, reg := newReleaseTestClient(t, "relenvupderr")
		reg.Register(httpmock.REST(http.MethodPatch, "relenvupderr/proj/_apis/release/releases/3/environments/6"),
			httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "deployment is not in progress"}))
		_, err := c.UpdateEnvironment(context.Background(), 3, 6, release.EnvironmentStatusValues.Canceled, "")
		require.ErrorContains(t, err, "deployment is not in progress")
	})
}
