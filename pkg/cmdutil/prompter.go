package cmdutil

import (
	"errors"
	"slices"

	"charm.land/huh/v2"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// ErrNoPrompt is returned when a prompt is needed but IOStreams cannot prompt.
var ErrNoPrompt = errors.New("cannot prompt: not an interactive terminal; pass the value with flags")

// Prompter asks questions on the terminal with huh. It refuses to prompt
// unless IO.CanPrompt().
type Prompter struct {
	IO *iostreams.IOStreams
	// Accessible switches to line-based prompts (screen readers, tests).
	Accessible bool
}

func (p *Prompter) run(field huh.Field) error {
	if !p.IO.CanPrompt() {
		return ErrNoPrompt
	}
	err := huh.NewForm(huh.NewGroup(field)).
		WithInput(p.IO.In).
		WithOutput(p.IO.Out).
		WithAccessible(p.Accessible).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return CancelError
	}
	return err
}

// Input asks for a line of text; an empty answer yields defaultValue.
func (p *Prompter) Input(prompt, defaultValue string) (string, error) {
	v := defaultValue
	err := p.run(huh.NewInput().Title(prompt).Value(&v))
	return v, err
}

// Password asks for a secret without echoing it.
func (p *Prompter) Password(prompt string) (string, error) {
	var v string
	err := p.run(huh.NewInput().Title(prompt).EchoMode(huh.EchoModePassword).Value(&v))
	return v, err
}

// Confirm asks a yes/no question.
func (p *Prompter) Confirm(prompt string, defaultValue bool) (bool, error) {
	v := defaultValue
	err := p.run(huh.NewConfirm().Title(prompt).Value(&v))
	return v, err
}

// Select asks to pick one of options and returns its index.
func (p *Prompter) Select(prompt string, options []string) (int, error) {
	opts := make([]huh.Option[int], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, i)
	}
	var v int
	err := p.run(huh.NewSelect[int]().Title(prompt).Options(opts...).Value(&v))
	return v, err
}

// Text asks for multi-line text prefilled with defaultValue; in accessible mode it reads one
// line and an empty answer yields defaultValue.
func (p *Prompter) Text(prompt, defaultValue string) (string, error) {
	v := defaultValue
	err := p.run(huh.NewText().Title(prompt).Value(&v))
	return v, err
}

// MultiSelect asks to pick any of options, with those in defaults selected at first, and
// returns the indexes of the picked options in ascending order.
func (p *Prompter) MultiSelect(prompt string, defaults, options []string) ([]int, error) {
	opts := make([]huh.Option[int], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, i).Selected(slices.Contains(defaults, o))
	}
	var v []int
	err := p.run(huh.NewMultiSelect[int]().Title(prompt).Options(opts...).Value(&v))
	slices.Sort(v)
	return v, err
}
