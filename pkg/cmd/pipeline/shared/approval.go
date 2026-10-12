package shared

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// ApprovalError prefixes err from an approvals call with what. A 4xx gets the approver names
// (the server decides who may act and its refusal is undocumented); a 401/403 becomes an
// AuthError naming the scopes the call needs.
func ApprovalError(err error, what, scopes string, names []string) error {
	var authErr *cmdutil.AuthError
	var he *api.HTTPError
	status := 0
	if errors.As(err, &he) {
		status = he.StatusCode
	}
	auth := errors.As(err, &authErr) || status == http.StatusUnauthorized || status == http.StatusForbidden
	err = fmt.Errorf("%s: %w", what, err)
	if len(names) > 0 && (auth || status >= 400 && status < 500) {
		err = fmt.Errorf("%w; approvers: %s", err, strings.Join(names, ", "))
	}
	if auth {
		return &cmdutil.AuthError{Err: fmt.Errorf("%w; the credential needs the %s scope", err, scopes)}
	}
	return err
}
