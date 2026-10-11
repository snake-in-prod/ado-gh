package cmdutil

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestExporterWriteSections(t *testing.T) {
	ios, _, out, _ := iostreams.Test()
	e := &Exporter{Fields: []string{"id", "url"}}
	data := map[string]any{
		"currentBranch": map[string]any{"id": 1, "title": "a"},
		"createdBy":     []map[string]any{{"id": 2, "title": "b"}},
		"needsReview":   nil,
	}
	if err := e.WriteSections(ios, data); err != nil {
		t.Fatal(err)
	}
	want := `{"createdBy":[{"id":2,"url":null}],"currentBranch":{"id":1,"url":null},"needsReview":null}` + "\n"
	if out.String() != want {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
	if err := e.WriteSections(ios, make(chan int)); err == nil {
		t.Error("want marshal error")
	}
}
