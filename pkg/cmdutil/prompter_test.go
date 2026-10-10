package cmdutil

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestPrompterRefusesWithoutTTY(t *testing.T) {
	ios, in, out, _ := iostreams.Test()
	in.WriteString("answer\n")
	p := &Prompter{IO: ios, Accessible: true}

	_, err1 := p.Input("Org", "")
	_, err2 := p.Password("Token")
	_, err3 := p.Confirm("Sure?", false)
	_, err4 := p.Select("Pick", []string{"a", "b"})
	_, err5 := p.MultiSelect("Pick", nil, []string{"a", "b"})
	_, err6 := p.Text("Body", "")
	for i, err := range []error{err1, err2, err3, err4, err5, err6} {
		if !errors.Is(err, ErrNoPrompt) {
			t.Errorf("prompt %d: err = %v, want ErrNoPrompt", i, err)
		}
	}
	if in.Len() != len("answer\n") || out.Len() != 0 {
		t.Error("refused prompt must not touch stdin or stdout")
	}
}

// ttyPrompter returns an accessible prompter on a forced-TTY test stream that
// reads input and writes to the returned stdout buffer.
func ttyPrompter(input string) (*Prompter, *bytes.Buffer) {
	ios, in, out, _ := iostreams.Test()
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	in.WriteString(input)
	return &Prompter{IO: ios, Accessible: true}, out
}

func TestPrompterAccessible(t *testing.T) {
	t.Run("input", func(t *testing.T) {
		p, out := ttyPrompter("my-org\n")
		got, err := p.Input("Organization", "")
		if err != nil || got != "my-org" {
			t.Errorf("got %q, %v", got, err)
		}
		if !strings.Contains(out.String(), "Organization") {
			t.Errorf("prompt not written to stdout: %q", out)
		}
	})
	t.Run("input default", func(t *testing.T) {
		p, _ := ttyPrompter("\n")
		got, err := p.Input("Organization", "fallback")
		if err != nil || got != "fallback" {
			t.Errorf("got %q, %v", got, err)
		}
	})
	t.Run("select", func(t *testing.T) {
		p, out := ttyPrompter("2\n")
		got, err := p.Select("Login with", []string{"Entra ID", "PAT"})
		if err != nil || got != 1 {
			t.Errorf("got %d, %v", got, err)
		}
		if !strings.Contains(out.String(), "2. PAT") {
			t.Errorf("options not listed: %q", out)
		}
	})
	t.Run("confirm", func(t *testing.T) {
		p, _ := ttyPrompter("y\n")
		got, err := p.Confirm("Sure?", false)
		if err != nil || !got {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("confirm default", func(t *testing.T) {
		p, _ := ttyPrompter("\n")
		got, err := p.Confirm("Sure?", true)
		if err != nil || !got {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("multi select", func(t *testing.T) {
		p, out := ttyPrompter("1\n2\n3\n0\n")
		// Each prompt line is read by a new scanner, so feed one byte at a time.
		p.IO.In = iotest.OneByteReader(p.IO.In)
		got, err := p.MultiSelect("Edit", []string{"b"}, []string{"a", "b", "c"})
		if err != nil || len(got) != 2 || got[0] != 0 || got[1] != 2 {
			t.Errorf("got %v, %v; want [0 2]", got, err)
		}
		if !strings.Contains(out.String(), "Edit") {
			t.Errorf("prompt not written to stdout: %q", out)
		}
	})
	t.Run("multi select defaults", func(t *testing.T) {
		p, _ := ttyPrompter("0\n")
		got, err := p.MultiSelect("Edit", []string{"c", "a"}, []string{"a", "b", "c"})
		if err != nil || len(got) != 2 || got[0] != 0 || got[1] != 2 {
			t.Errorf("got %v, %v; want [0 2]", got, err)
		}
	})
	t.Run("text default", func(t *testing.T) {
		p, _ := ttyPrompter("\n")
		got, err := p.Text("Body", "old")
		if err != nil || got != "old" {
			t.Errorf("got %q, %v", got, err)
		}
	})
}
