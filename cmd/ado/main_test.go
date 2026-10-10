package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestRun(t *testing.T) {
	old := version
	version = "9.9.9" // as injected by -ldflags "-X main.version=9.9.9"
	t.Cleanup(func() { version = old })

	tests := []struct {
		args       []string
		wantCode   cmdutil.ExitCode
		wantOut    string
		wantErrOut string
	}{
		{[]string{"--version"}, cmdutil.ExitOK, "ado version 9.9.9\n", ""},
		{[]string{"nope"}, cmdutil.ExitError, "", "unknown command \"nope\" for \"ado\""},
		{[]string{"--nope"}, cmdutil.ExitError, "", "Usage:"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			if got := run(tt.args, ios); got != tt.wantCode {
				t.Errorf("code = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want %q", out, tt.wantOut)
			}
			if !strings.Contains(errOut.String(), tt.wantErrOut) {
				t.Errorf("stderr = %q, want %q", errOut, tt.wantErrOut)
			}
		})
	}
}

func TestNewFactoryHTTPClient(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	ios, _, _, errOut := iostreams.Test()
	f := newFactory(ios)
	f.Debug = true // as set by --debug before the client is first built
	c, err := f.HttpClient()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotUA != "ado-cli/"+version {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if !strings.Contains(errOut.String(), "> GET "+srv.URL) {
		t.Errorf("debug log = %q", errOut)
	}
}
