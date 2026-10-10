package cmdutil

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

type testPR struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
	URL    string `json:"url"`
}

var testFields = []string{"title", "id", "status", "url"}

var testData = []testPR{
	{ID: 1, Title: "a <b>", Status: "active", URL: "u1"},
	{ID: 2, Title: "c", URL: "u2"},
}

// runJSONCmd runs a command that writes testData through AddJSONFlags.
func runJSONCmd(t *testing.T, ios *iostreams.IOStreams, args ...string) (exported *Exporter, err error) {
	t.Helper()
	cmd := &cobra.Command{
		Use: "list",
		RunE: func(*cobra.Command, []string) error {
			if exported == nil {
				_, err := ios.Out.Write([]byte("table\n"))
				return err
			}
			return exported.Write(ios, testData)
		},
	}
	cmd.Flags().Bool("web", false, "")
	AddJSONFlags(cmd, &exported, testFields)
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err = cmd.Execute()
	return exported, err
}

func TestJSONFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		tty     bool
		want    string
		wantErr string
	}{
		{name: "no flags", want: "table\n"},
		{name: "fields filtered, missing field is null, no HTML escaping",
			args: []string{"--json", "id,title,status"},
			want: `[{"id":1,"status":"active","title":"a <b>"},{"id":2,"status":null,"title":"c"}]` + "\n"},
		{name: "jq", args: []string{"--json", "id", "--jq", ".[].id"}, want: "1\n2\n"},
		{name: "jq indents on TTY", args: []string{"--json", "id", "-q", ".[0]"}, tty: true,
			want: "{\n  \"id\": 1\n}\n"},
		{name: "template", args: []string{"--json", "id,title", "-t", `{{range .}}{{.id}}:{{.title}}{{"\n"}}{{end}}`},
			want: "1:a <b>\n2:c\n"},
		{name: "template tablerow", args: []string{"--json", "id,url", "-t", `{{range .}}{{tablerow .id .url}}{{end}}`},
			want: "1  u1\n2  u2\n"},
		{name: "bad jq", args: []string{"--json", "id", "--jq", ".[["}, wantErr: "failed to parse jq expression"},
		{name: "bad template", args: []string{"--json", "id", "-t", "{{"}, wantErr: "unclosed action"},
		{name: "unknown field", args: []string{"--json", "id,nope"},
			wantErr: "Unknown JSON field: \"nope\"\nAvailable fields:\n  id\n  status\n  title\n  url"},
		{name: "bare json", args: []string{"--json"},
			wantErr: "Specify one or more comma-separated fields for `--json`:\n  id\n  status\n  title\n  url"},
		{name: "empty json", args: []string{"--json="},
			wantErr: "Specify one or more comma-separated fields for `--json`:\n  id\n"},
		{name: "jq without json", args: []string{"--jq", "."}, wantErr: "cannot use `--jq` without specifying `--json`"},
		{name: "template without json", args: []string{"-t", "x"}, wantErr: "cannot use `--template` without specifying `--json`"},
		{name: "jq with template", args: []string{"--json", "id", "--jq", ".", "-t", "x"},
			wantErr: "if any flags in the group [jq template] are set none of the others can be"},
		{name: "web with json", args: []string{"--json", "id", "--web"}, wantErr: "cannot use `--web` with `--json`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, out, _ := iostreams.Test()
			ios.SetStdoutTTY(tt.tty)
			_, err := runJSONCmd(t, ios, tt.args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.want {
				t.Errorf("out = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestJSONFlagErrorExitsOneWithoutUsage(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	_, err := runJSONCmd(t, ios, "--json")
	var jerr *JSONFlagError
	if !errors.As(err, &jerr) {
		t.Fatalf("err = %T %v, want *JSONFlagError", err, err)
	}
	var stderr bytes.Buffer
	if code := HandleError(&stderr, err, "USAGE"); code != ExitError {
		t.Errorf("code = %d, want %d", code, ExitError)
	}
	if strings.Contains(stderr.String(), "USAGE") {
		t.Errorf("usage printed for JSON flag error: %q", stderr.String())
	}
}

func TestJSONFlagsKeepsOtherFlagErrorsAndPreRun(t *testing.T) {
	parent := &cobra.Command{Use: "ado"}
	parent.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return FlagErrorWrap(err) })
	ran := false
	var exp *Exporter
	cmd := &cobra.Command{
		Use:     "list",
		PreRunE: func(*cobra.Command, []string) error { ran = true; return nil },
		RunE:    func(*cobra.Command, []string) error { return nil },
	}
	AddJSONFlags(cmd, &exp, testFields)
	parent.AddCommand(cmd)
	parent.SilenceErrors, parent.SilenceUsage = true, true
	parent.SetOut(&bytes.Buffer{})
	parent.SetErr(&bytes.Buffer{})

	parent.SetArgs([]string{"list", "--nope"})
	var ferr *FlagError
	if err := parent.Execute(); !errors.As(err, &ferr) {
		t.Fatalf("err = %v, want *FlagError from parent", err)
	}

	parent.SetArgs([]string{"list", "--json", "id"})
	if err := parent.Execute(); err != nil || !ran || exp == nil {
		t.Fatalf("err = %v, preRun ran = %v, exporter = %v", err, ran, exp)
	}
}

func TestExporterColorOnTTY(t *testing.T) {
	ios, _, out, _ := iostreams.Test()
	ios.SetStdoutTTY(true)
	ios.SetColorEnabled(true)
	e := &Exporter{Fields: []string{"id"}}
	if err := e.Write(ios, testData[0]); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, "\n  ") {
		t.Errorf("want coloured indented JSON, got %q", got)
	}
	if strings.Contains(got, "title") {
		t.Errorf("unrequested field exported: %q", got)
	}
}

func TestExporterNonObjectAndErrors(t *testing.T) {
	ios, _, out, _ := iostreams.Test()
	e := &Exporter{Fields: []string{"id"}}
	if err := e.Write(ios, map[string]any{"id": int64(9007199254740993), "x": 1}); err != nil {
		t.Fatal(err)
	}
	if want := `{"id":9007199254740993}` + "\n"; out.String() != want {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
	out.Reset()
	if err := e.Write(ios, []any{1, "s", nil}); err != nil {
		t.Fatal(err)
	}
	if want := `[1,"s",null]` + "\n"; out.String() != want {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
	if err := e.Write(ios, make(chan int)); err == nil {
		t.Error("want marshal error")
	}
}
