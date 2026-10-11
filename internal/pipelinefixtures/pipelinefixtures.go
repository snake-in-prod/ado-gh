// Package pipelinefixtures loads the pipeline fixtures kept in pkg/cmd/pipeline/shared/testdata
// (see the README there). Import it from tests only.
//
// Serve a fixture through httpmock with httpmock.JSONResponse(json.RawMessage(b)), or
// httpmock.StatusJSONResponse(status, json.RawMessage(b)) for the error_*.json bodies.
package pipelinefixtures

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Dir is the absolute path of the fixture directory.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "pkg", "cmd", "pipeline", "shared", "testdata")
}

// Read returns the fixture file name (e.g. "timeline_yaml_stages.json"), failing the test if it
// is missing.
func Read(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Dir(), name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
