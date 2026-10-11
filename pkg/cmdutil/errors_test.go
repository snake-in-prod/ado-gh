package cmdutil

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestHandleError(t *testing.T) {
	const usage = "Usage:\n  ado pr list [flags]"
	tests := []struct {
		name       string
		err        error
		wantCode   ExitCode
		wantStderr string
	}{
		{"nil", nil, ExitOK, ""},
		{"generic", errors.New("boom"), ExitError, "boom\n"},
		{"silent", SilentError, ExitError, ""},
		{"wrapped silent", fmt.Errorf("x: %w", SilentError), ExitError, ""},
		{"cancel", CancelError, ExitCancel, ""},
		{"pending", PendingError, ExitPending, ""},
		{"wrapped pending", fmt.Errorf("checks: %w", PendingError), ExitPending, ""},
		{"auth", &AuthError{Err: errors.New("not logged in to dev.azure.com")}, ExitAuth,
			"not logged in to dev.azure.com\nrun: ado auth login\n"},
		{"wrapped auth", fmt.Errorf("pr list: %w", &AuthError{Err: errors.New("401")}), ExitAuth,
			"pr list: 401\nrun: ado auth login\n"},
		{"flag", FlagErrorf("unknown flag: --%s", "nope"), ExitError,
			"unknown flag: --nope\n\n" + usage + "\n"},
		{"flag wrap", FlagErrorWrap(errors.New("bad value")), ExitError,
			"bad value\n\n" + usage + "\n"},
		{"cobra unknown command", errors.New(`unknown command "nope" for "ado"`), ExitError,
			"unknown command \"nope\" for \"ado\"\n\n" + usage + "\n"},
		{"message with trailing newline", errors.New("boom\n"), ExitError, "boom\n"}, //nolint:revive // testing the newline case
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := HandleError(&stderr, tt.err, usage)
			if got != tt.wantCode {
				t.Errorf("code = %d, want %d", got, tt.wantCode)
			}
			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	inner := errors.New("inner")
	if !errors.Is(&AuthError{Err: inner}, inner) {
		t.Error("AuthError does not unwrap")
	}
	if !errors.Is(FlagErrorWrap(inner), inner) {
		t.Error("FlagError does not unwrap")
	}
}
