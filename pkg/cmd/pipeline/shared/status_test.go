package shared

import (
	"io"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestStatusMark(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	ios.SetColorEnabled(true)
	cs := ios.ColorScheme()
	tests := []struct {
		status, result string
		mark           string
		color          func(string) string
	}{
		{"completed", "succeeded", "✓", cs.Green},
		{"completed", "failed", "X", cs.Red},
		{"completed", "partiallySucceeded", "!", cs.Yellow},
		{"completed", "succeededWithIssues", "!", cs.Yellow},
		{"completed", "canceled", "-", cs.Gray},
		{"completed", "skipped", "-", cs.Gray},
		{"completed", "abandoned", "X", cs.Red},
		{"inProgress", "none", "●", cs.Yellow},
		{"inProgress", "", "●", cs.Yellow},
		{"cancelling", "", "●", cs.Yellow},
		{"notStarted", "", "⏸", cs.Yellow},
		{"postponed", "", "⏸", cs.Yellow},
		{"pending", "", "⏸", cs.Yellow},
		// Release environment statuses carry the result in the status.
		{"rejected", "", "X", cs.Red},
		{"succeeded", "", "✓", cs.Green},
		{"queued", "", "⏸", cs.Yellow},
		{"completed", "", "-", cs.Gray},
	}
	for _, tt := range tests {
		mark, color := StatusMark(cs, tt.status, tt.result)
		if mark != tt.mark {
			t.Errorf("StatusMark(%q, %q) mark = %q, want %q", tt.status, tt.result, mark, tt.mark)
		}
		if got, want := color("x"), tt.color("x"); got != want {
			t.Errorf("StatusMark(%q, %q) color = %q, want %q", tt.status, tt.result, got, want)
		}
	}
}

func TestResultExitError(t *testing.T) {
	tests := []struct {
		result string
		want   cmdutil.ExitCode
	}{
		{"succeeded", cmdutil.ExitOK},
		{"failed", cmdutil.ExitError},
		{"partiallySucceeded", cmdutil.ExitError},
		{"succeededWithIssues", cmdutil.ExitError},
		{"rejected", cmdutil.ExitError},
		{"canceled", cmdutil.ExitCancel},
		{"none", cmdutil.ExitError},
	}
	for _, tt := range tests {
		if got := cmdutil.HandleError(io.Discard, ResultExitError(tt.result), ""); got != tt.want {
			t.Errorf("ResultExitError(%q) exits %d, want %d", tt.result, got, tt.want)
		}
	}
}
