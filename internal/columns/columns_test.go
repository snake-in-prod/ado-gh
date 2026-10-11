package columns

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func renderPlain(t *testing.T, width int, cols []Column) string {
	t.Helper()
	ios, _, _, _ := iostreams.Test()
	var buf bytes.Buffer
	Render(&buf, ios.ColorScheme(), width, cols)
	return buf.String()
}

func TestRender(t *testing.T) {
	tests := []struct {
		name  string
		width int
		cols  []Column
		want  string
	}{
		{
			name:  "side by side",
			width: 42,
			cols: []Column{
				{Name: "New", Cards: []Card{{ID: 1, Title: "Login", Assignee: "Ann"}, {ID: 2, Title: "Logout"}}},
				{Name: "Active", Cards: []Card{{ID: 3, Title: "Fix"}}},
			},
			want: "" +
				"New 2                 Active 1\n" +
				"#1 Login (Ann)        #3 Fix\n" +
				"#2 Logout\n",
		},
		{
			name:  "card truncation",
			width: 42,
			cols: []Column{
				{Name: "New", Cards: []Card{{ID: 1, Title: "A very long title that will not fit", Assignee: "Ann"}}},
				{Name: "Done"},
			},
			want: "" +
				"New 1                 Done 0\n" +
				"#1 A very long ti...\n",
		},
		{
			name:  "wip limit header",
			width: 42,
			cols: []Column{
				{Name: "Active", WIPLimit: 5, Cards: []Card{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}, {ID: 3, Title: "c"}}},
				{Name: "Closed"},
			},
			want: "" +
				"Active 3/5            Closed 0\n" +
				"#1 a\n" +
				"#2 b\n" +
				"#3 c\n",
		},
		{
			name:  "split doing done",
			width: 62,
			cols: []Column{
				{Name: "Active", WIPLimit: 5, Split: true,
					Cards: []Card{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}},
					Done:  []Card{{ID: 3, Title: "c"}}},
				{Name: "Closed", Cards: []Card{{ID: 4, Title: "d"}}},
			},
			want: "" +
				"Active 3/5                                Closed 1\n" +
				"Doing                Done\n" +
				"#1 a                 #3 c                 #4 d\n" +
				"#2 b\n",
		},
		{
			name:  "stacked when narrow",
			width: 30,
			cols: []Column{
				{Name: "New", Cards: []Card{{ID: 1, Title: "A long title for a card", Assignee: "Ann"}}},
				{Name: "Active", WIPLimit: 2, Split: true, Cards: []Card{{ID: 2, Title: "b"}}, Done: []Card{{ID: 3, Title: "c"}}},
				{Name: "Closed"},
			},
			want: "" +
				"New 1\n" +
				"  #1 A long title for a car...\n" +
				"\n" +
				"Active 2/2\n" +
				"  Doing\n" +
				"    #2 b\n" +
				"  Done\n" +
				"    #3 c\n" +
				"\n" +
				"Closed 0\n",
		},
		{
			name:  "no columns",
			width: 80,
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderPlain(t, tt.width, tt.cols)
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
			for _, line := range strings.Split(got, "\n") {
				if w := text.DisplayWidth(line); w > tt.width {
					t.Errorf("line %q is %d wide, over %d", line, w, tt.width)
				}
			}
		})
	}
}

func TestRenderColor(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	ios.SetColorEnabled(true)
	var buf bytes.Buffer
	Render(&buf, ios.ColorScheme(), 42, []Column{
		{Name: "Active", WIPLimit: 1, Cards: []Card{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}}},
		{Name: "New"},
	})
	got := buf.String()
	for _, want := range []string{"\x1b[31mActive 2/1\x1b[0m", "\x1b[1mNew 0\x1b[0m", "\x1b[36m#1\x1b[0m a"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRenderWideRunes(t *testing.T) {
	got := renderPlain(t, 42, []Column{
		{Name: "New", Cards: []Card{{ID: 1, Title: "日本語のタイトルはとても長いです"}}},
		{Name: "Done", Cards: []Card{{ID: 2, Title: "x"}}},
	})
	for _, line := range strings.Split(got, "\n") {
		if w := text.DisplayWidth(line); w > 42 {
			t.Errorf("line %q is %d wide", line, w)
		}
	}
	if !strings.Contains(got, "#2 x") {
		t.Errorf("second column missing: %q", got)
	}
}
