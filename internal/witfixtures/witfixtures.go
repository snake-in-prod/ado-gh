// Package witfixtures loads the work item process fixtures kept in
// pkg/cmd/workitem/shared/testdata (see the README there). Import it from tests only.
//
// Serve a fixture through httpmock with httpmock.JSONResponse(json.RawMessage(b)).
package witfixtures

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Processes are the fixture processes: the four system processes and one inherited from Agile.
var Processes = []string{"agile", "scrum", "basic", "cmmi", "inherited"}

// Dir is the absolute path of the fixture directory.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "pkg", "cmd", "workitem", "shared", "testdata")
}

// Read returns the fixture file name (e.g. "fields.json"), failing the test if it is missing.
func Read(t testing.TB, name string) []byte {
	t.Helper()
	return must(t)(read(name))
}

// Process returns the <kind> fixture of process ("types", "categories", "backlogs", "board"),
// preferring the live capture live_<process>_<kind>.json over the hand-written <process>_<kind>.json.
func Process(t testing.TB, process, kind string) []byte {
	t.Helper()
	return must(t)(processFile(process, kind))
}

// TypeStates returns the GET {project}/_apis/wit/workitemtypes/{type}/states response for
// typeName, taken from the process's types fixture.
func TypeStates(t testing.TB, process, typeName string) []byte {
	t.Helper()
	return must(t)(typeStates(process, typeName))
}

func must(t testing.TB) func([]byte, error) []byte {
	return func(b []byte, err error) []byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
}

func read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(Dir(), name))
}

func processFile(process, kind string) ([]byte, error) {
	b, err := read(fmt.Sprintf("live_%s_%s.json", process, kind))
	if errors.Is(err, os.ErrNotExist) {
		return read(fmt.Sprintf("%s_%s.json", process, kind))
	}
	return b, err
}

func typeStates(process, typeName string) ([]byte, error) {
	b, err := processFile(process, "types")
	if err != nil {
		return nil, err
	}
	var types struct {
		Value []struct {
			Name   string            `json:"name"`
			States []json.RawMessage `json:"states"`
		} `json:"value"`
	}
	if err := json.Unmarshal(b, &types); err != nil {
		return nil, err
	}
	for _, t := range types.Value {
		if strings.EqualFold(t.Name, typeName) {
			return json.Marshal(map[string]any{"count": len(t.States), "value": t.States})
		}
	}
	return nil, fmt.Errorf("%s fixture has no type %q", process, typeName)
}
