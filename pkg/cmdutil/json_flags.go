package cmdutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/cli/go-gh/v2/pkg/jq"
	"github.com/cli/go-gh/v2/pkg/template"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

// JSONFlagError is a `--json` usage mistake that lists the available fields.
// It exits 1 without printing the command usage.
type JSONFlagError struct{ error }

// Exporter writes command data as JSON filtered to Fields, optionally through
// a jq expression or a Go template. Commands get a nil *Exporter when --json
// was not passed.
type Exporter struct {
	Fields   []string
	JQ       string
	Template string
}

// AddJSONFlags adds --json, -q/--jq and -t/--template to cmd. After flag
// parsing *target is set to an Exporter when --json was passed, else nil.
// fields are the allowed (camelCase, public API) field names.
func AddJSONFlags(cmd *cobra.Command, target **Exporter, fields []string) {
	f := cmd.Flags()
	f.StringSlice("json", nil, "Output JSON with the specified `fields`")
	f.StringP("jq", "q", "", "Filter JSON output using a jq `expression`")
	f.StringP("template", "t", "", "Format JSON output using a Go template")
	cmd.MarkFlagsMutuallyExclusive("jq", "template")

	sorted := slices.Sorted(slices.Values(fields))
	list := strings.Join(sorted, "\n  ")
	noFields := &JSONFlagError{fmt.Errorf("Specify one or more comma-separated fields for `--json`:\n  %s", list)} //nolint:staticcheck // gh wording, printed as-is

	oldPreRun := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		if oldPreRun != nil {
			if err := oldPreRun(c, args); err != nil {
				return err
			}
		}
		e, err := exporterFromFlags(c)
		if err != nil || e == nil {
			*target = nil
			return err
		}
		if len(e.Fields) == 0 {
			return noFields
		}
		for _, name := range e.Fields {
			if !slices.Contains(fields, name) {
				return &JSONFlagError{fmt.Errorf("Unknown JSON field: %q\nAvailable fields:\n  %s", name, list)} //nolint:staticcheck // gh wording, printed as-is
			}
		}
		*target = e
		return nil
	}

	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if c == cmd && err.Error() == "flag needs an argument: --json" {
			return noFields
		}
		if cmd.HasParent() {
			return cmd.Parent().FlagErrorFunc()(c, err)
		}
		return err
	})
}

func exporterFromFlags(cmd *cobra.Command) (*Exporter, error) {
	f := cmd.Flags()
	jqExpr, _ := f.GetString("jq")
	tmpl, _ := f.GetString("template")
	if !f.Changed("json") {
		switch {
		case f.Changed("jq"):
			return nil, FlagErrorf("cannot use `--jq` without specifying `--json`")
		case f.Changed("template"):
			return nil, FlagErrorf("cannot use `--template` without specifying `--json`")
		}
		return nil, nil
	}
	if web := f.Lookup("web"); web != nil && web.Changed {
		return nil, FlagErrorf("cannot use `--web` with `--json`")
	}
	fields, _ := f.GetStringSlice("json")
	return &Exporter{Fields: fields, JQ: jqExpr, Template: tmpl}, nil
}

// Write encodes data (a struct, map or slice of them with camelCase JSON
// keys) keeping only e.Fields, then prints it raw, through --jq or
// --template, or coloured and indented when color is enabled.
func (e *Exporter) Write(ios *iostreams.IOStreams, data any) error {
	return e.write(ios, data, false)
}

// WriteSections is Write for data that encodes to an object of sections (an object, an array
// of objects or null each), such as `pr status`: e.Fields filter inside every section.
func (e *Exporter) WriteSections(ios *iostreams.IOStreams, data any) error {
	return e.write(ios, data, true)
}

func (e *Exporter) write(ios *iostreams.IOStreams, data any, sections bool) error {
	var buf bytes.Buffer
	if err := e.encode(&buf, data, sections); err != nil {
		return err
	}
	w := ios.Out
	switch {
	case e.JQ != "":
		indent := ""
		if ios.IsStdoutTTY() {
			indent = "  "
		}
		return jq.EvaluateFormatted(&buf, w, e.JQ, indent, ios.ColorEnabled())
	case e.Template != "":
		t := template.New(w, ios.TerminalWidth(), ios.ColorEnabled())
		if err := t.Parse(e.Template); err != nil {
			return err
		}
		if err := t.Execute(&buf); err != nil {
			return err
		}
		return t.Flush()
	case ios.ColorEnabled():
		return jq.EvaluateFormatted(&buf, w, ".", "  ", true)
	}
	_, err := io.Copy(w, &buf)
	return err
}

// encode round-trips data through JSON and filters object keys to e.Fields,
// at the top level or in each element of a top-level array (one level deeper
// with sections). Requested fields that are absent become null so the output
// shape is stable.
func (e *Exporter) encode(w io.Writer, data any, sections bool) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	if m, ok := v.(map[string]any); ok && sections {
		for k := range m {
			m[k] = e.filterAll(m[k])
		}
	} else {
		v = e.filterAll(v)
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// filterAll filters v, or each element of v when it is an array.
func (e *Exporter) filterAll(v any) any {
	if a, ok := v.([]any); ok {
		for i := range a {
			a[i] = e.filter(a[i])
		}
		return a
	}
	return e.filter(v)
}

func (e *Exporter) filter(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(e.Fields))
	for _, f := range e.Fields {
		out[f] = m[f]
	}
	return out
}
