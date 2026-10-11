package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const refsPath = "/proj/_apis/git/repositories/repo/refs"

func TestRef(t *testing.T) {
	tests := []struct {
		name    string
		refs    []map[string]any
		status  int
		wantOID string
		wantNil bool
		wantErr string
	}{
		{
			name: "exact match among prefix matches",
			refs: []map[string]any{
				{"name": "refs/heads/feat-x", "objectId": "bbb"},
				{"name": "refs/heads/feat", "objectId": "aaa"},
			},
			wantOID: "aaa",
		},
		{name: "only prefix matches", refs: []map[string]any{{"name": "refs/heads/feature", "objectId": "ccc"}}, wantNil: true},
		{name: "absent", wantNil: true},
		{name: "http error", status: http.StatusForbidden, wantErr: "denied (HTTP 403)"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := "reforg" + strconv.Itoa(i)
			c, reg := newTestPRClient(t, org)
			var filter string
			reg.Register(httpmock.REST(http.MethodGet, org+refsPath), func(r *http.Request) (*http.Response, error) {
				filter = r.URL.Query().Get("filter")
				if tt.status != 0 {
					return httpmock.StatusJSONResponse(tt.status, map[string]any{"message": "denied"})(r)
				}
				return page(tt.refs)(r)
			})
			ref, err := c.Ref(context.Background(), "refs/heads/feat")
			assert.Equal(t, "heads/feat", filter)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, ref)
				return
			}
			assert.Equal(t, tt.wantOID, *ref.ObjectId)
		})
	}
}

func TestDeleteRef(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		success  bool
		httpErr  bool
		wantErr  string
		wantGone bool
	}{
		{name: "succeeded", status: "succeeded", success: true},
		{name: "corrupt ref counts as deleted", status: "succeededCorruptRef", success: true},
		{name: "already deleted", status: "succeededNonExistentRef", success: true, wantGone: true},
		{name: "write permission", status: "writePermissionRequired",
			wantErr: "cannot delete branch feat: you need the Force push permission on it"},
		{name: "force push", status: "forcePushRequired",
			wantErr: "cannot delete branch feat: you need the Force push permission on it"},
		{name: "locked", status: "locked", wantErr: "cannot delete branch feat: it is locked"},
		{name: "policy", status: "rejectedByPolicy", wantErr: "cannot delete branch feat: rejected by a branch policy"},
		{name: "plugin", status: "rejectedByPlugin", wantErr: "cannot delete branch feat: rejected by plugin: no"},
		{name: "stale", status: "staleOldObjectId", wantErr: "cannot delete branch feat: it was updated since it was read; retry"},
		{name: "invalid name", status: "invalidRefName", wantErr: "cannot delete branch feat: invalid branch name"},
		{name: "other status", status: "unprocessed", wantErr: "cannot delete branch feat: unprocessed: no"},
		{name: "http error", httpErr: true, wantErr: "cannot delete branch feat: denied (HTTP 403)"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := "delreforg" + strconv.Itoa(i)
			c, reg := newTestPRClient(t, org)
			var body []map[string]any
			reg.Register(httpmock.REST(http.MethodPost, org+refsPath), func(r *http.Request) (*http.Response, error) {
				b, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(b, &body))
				if tt.httpErr {
					return httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"})(r)
				}
				return page([]map[string]any{{"name": "refs/heads/feat", "success": tt.success, "updateStatus": tt.status, "customMessage": "no"}})(r)
			})
			err := c.DeleteRef(context.Background(), "feat", "abc123")
			assert.Equal(t, []map[string]any{{
				"name": "refs/heads/feat", "oldObjectId": "abc123", "newObjectId": "0000000000000000000000000000000000000000",
			}}, body)
			switch {
			case tt.wantGone:
				assert.ErrorIs(t, err, ErrRefNotFound)
			case tt.wantErr != "":
				require.EqualError(t, err, tt.wantErr)
			default:
				require.NoError(t, err)
			}
			if tt.httpErr {
				var he *HTTPError
				assert.True(t, errors.As(err, &he), "the HTTP error stays inspectable")
			}
		})
	}
}
