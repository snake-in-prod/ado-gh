package shared

import (
	"errors"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/stretchr/testify/assert"
)

func TestApprovalError(t *testing.T) {
	names := []string{"Alice", "Bob"}
	tests := []struct {
		name string
		err  error
		want string
		auth bool
	}{
		{"4xx names approvers", &api.HTTPError{StatusCode: http.StatusBadRequest, Message: "no"},
			"x: no (HTTP 400); approvers: Alice, Bob", false},
		{"403 adds scopes", &api.HTTPError{StatusCode: http.StatusForbidden, Message: "denied"},
			"x: denied (HTTP 403); approvers: Alice, Bob; the credential needs the S scope", true},
		{"auth error", &cmdutil.AuthError{Err: errors.New("login")},
			"x: login; approvers: Alice, Bob; the credential needs the S scope", true},
		{"5xx untouched", &api.HTTPError{StatusCode: http.StatusInternalServerError, Message: "boom"}, "x: boom (HTTP 500)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ApprovalError(tt.err, "x", "S", names)
			assert.EqualError(t, err, tt.want)
			var ae *cmdutil.AuthError
			assert.Equal(t, tt.auth, errors.As(err, &ae))
		})
	}
}
