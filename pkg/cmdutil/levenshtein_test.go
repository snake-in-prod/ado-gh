package cmdutil

import "testing"

func TestLevenshtein(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"abc", "", 3}, {"kitten", "sitting", 3}, {"story points", "story pts", 3}, {"żółw", "zółw", 1}} {
		if got := Levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("Levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
