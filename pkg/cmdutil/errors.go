// Package cmdutil holds shared command plumbing: errors, exit codes and the Factory.
package cmdutil

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ExitCode is the process exit status of ado.
type ExitCode int

// Exit codes (spec FR1).
const (
	ExitOK      ExitCode = 0
	ExitError   ExitCode = 1
	ExitCancel  ExitCode = 2
	ExitAuth    ExitCode = 4
	ExitPending ExitCode = 8
)

// SilentError exits 1 without printing; the command already reported the failure.
var SilentError = errors.New("SilentError") //nolint:revive,staticcheck // gh naming, kept on purpose

// CancelError exits 2 without printing; the user cancelled.
var CancelError = errors.New("CancelError") //nolint:revive,staticcheck // gh naming, kept on purpose

// PendingError exits 8 without printing; nothing failed but something is pending.
var PendingError = errors.New("PendingError") //nolint:revive,staticcheck // gh naming, kept on purpose

// AuthError means credentials are missing or rejected; exits 4.
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// FlagError is a usage mistake; it is printed followed by the command usage.
type FlagError struct{ Err error }

func (e *FlagError) Error() string { return e.Err.Error() }
func (e *FlagError) Unwrap() error { return e.Err }

// FlagErrorf returns a FlagError with a formatted message.
func FlagErrorf(format string, args ...any) error {
	return &FlagError{Err: fmt.Errorf(format, args...)}
}

// FlagErrorWrap marks err as a FlagError.
func FlagErrorWrap(err error) error { return &FlagError{Err: err} }

// ExternalExitError passes the exit code of an extension or shell alias through ado.
// HandleError returns it without printing; the child already reported its failure.
type ExternalExitError struct{ Code int }

func (e *ExternalExitError) Error() string {
	return fmt.Sprintf("external command exited with status %d", e.Code)
}

// ExternalExit turns a child process's *exec.ExitError into an ExternalExitError. Other
// errors, such as a missing executable, are returned unchanged.
func ExternalExit(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExternalExitError{Code: exitErr.ExitCode()}
	}
	return err
}

// HandleError reports err on stderr and returns the exit code main should use.
// usage is printed after flag errors (cobra's cmd.UsageString()).
func HandleError(stderr io.Writer, err error, usage string) ExitCode {
	if err == nil {
		return ExitOK
	}
	var ext *ExternalExitError
	if errors.As(err, &ext) {
		if ext.Code < 0 { // killed by a signal
			return ExitError
		}
		return ExitCode(ext.Code)
	}
	if errors.Is(err, CancelError) {
		return ExitCancel
	}
	if errors.Is(err, PendingError) {
		return ExitPending
	}
	if errors.Is(err, SilentError) {
		return ExitError
	}

	msg := err.Error()
	fmt.Fprint(stderr, msg)
	if !strings.HasSuffix(msg, "\n") {
		fmt.Fprintln(stderr)
	}

	var authErr *AuthError
	if errors.As(err, &authErr) {
		fmt.Fprintln(stderr, "run: ado auth login")
		return ExitAuth
	}
	var flagErr *FlagError
	if errors.As(err, &flagErr) || strings.HasPrefix(msg, "unknown command ") {
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, usage)
	}
	return ExitError
}
