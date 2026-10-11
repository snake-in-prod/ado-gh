package api

import (
	"testing"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
)

func TestTimeOf(t *testing.T) {
	if TimeOf(nil) != nil {
		t.Error("TimeOf(nil) != nil")
	}
	want := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	if got := TimeOf(&azuredevops.Time{Time: want}); got == nil || !got.Equal(want) {
		t.Errorf("TimeOf = %v, want %v", got, want)
	}
}
