// Package shared holds code shared by the pipeline and release commands.
package shared

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// StatusMark returns the display mark and its color for a run, timeline record or release
// environment. result wins when set; otherwise status decides, so release environment
// statuses, which carry the outcome themselves, work with an empty result. Failures use "X",
// matching `pr checks` and the work item commands.
func StatusMark(cs *iostreams.ColorScheme, status, result string) (string, func(string) string) {
	key := result
	if key == "" || key == "none" {
		key = status
	}
	switch key {
	case "succeeded":
		return "✓", cs.Green
	case "failed", "rejected", "abandoned":
		return "X", cs.Red
	case "partiallySucceeded", "succeededWithIssues":
		return "!", cs.Yellow
	case "inProgress", "cancelling":
		return "●", cs.Yellow
	case "notStarted", "postponed", "pending", "queued", "scheduled":
		return "⏸", cs.Yellow
	}
	// canceled, skipped, and completed without a result.
	return "-", cs.Gray
}

// ResultExitError maps a completed result to the error a watching command returns: nil
// (exit 0) for succeeded, CancelError (exit 2) for canceled, SilentError (exit 1) otherwise,
// including partiallySucceeded.
func ResultExitError(result string) error {
	switch result {
	case "succeeded":
		return nil
	case "canceled":
		return cmdutil.CancelError
	}
	return cmdutil.SilentError
}
