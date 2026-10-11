package api

import (
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
)

// TimeOf returns the time an SDK timestamp holds, or nil when unset.
func TimeOf(t *azuredevops.Time) *time.Time {
	if t == nil {
		return nil
	}
	return &t.Time
}
