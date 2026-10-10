package cmdutil

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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
	for i, err := range []error{err1, err2, err3, err4} {
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
}
