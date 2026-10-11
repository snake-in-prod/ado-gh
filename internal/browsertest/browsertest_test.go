package browsertest

import (
	"errors"
	"testing"
)

func TestBrowserRecordsURLs(t *testing.T) {
	b := &Browser{Err: errors.New("boom")}
	if err := b.Browse("https://a"); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(b.URLs) != 1 || b.URLs[0] != "https://a" {
		t.Fatalf("URLs = %v", b.URLs)
	}
}
