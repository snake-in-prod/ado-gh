package cmdutil

import (
	"fmt"
	"io"
	"net/url"
	"os"

	clibrowser "github.com/cli/browser"
	ghbrowser "github.com/cli/go-gh/v2/pkg/browser"
)

// Browser opens URLs; Browse also fits MSAL's WithOpenURL.
type Browser interface {
	Browse(url string) error
}

type browserFunc func(string) error

func (f browserFunc) Browse(u string) error { return f(u) }

// ResolveBrowserLauncher returns the browser command from ADO_BROWSER, then the
// ado config "browser" key, then BROWSER. GH_BROWSER and gh's config are ignored.
func ResolveBrowserLauncher(cfg Config) string {
	if v := os.Getenv("ADO_BROWSER"); v != "" {
		return v
	}
	if v := cfg.Get("", "browser"); v != "" {
		return v
	}
	return os.Getenv("BROWSER")
}

// NewBrowser returns a Browser running launcher via go-gh, or the system
// opener when launcher is empty (go-gh would fall back to GH_BROWSER and gh's config).
func NewBrowser(launcher string, stdout, stderr io.Writer) Browser {
	if launcher == "" {
		return systemBrowser{}
	}
	return ghbrowser.New(launcher, stdout, stderr)
}

type systemBrowser struct{}

func (systemBrowser) Browse(u string) error {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return fmt.Errorf("opening unsupported URL: %s", u)
	}
	return clibrowser.OpenURL(p.String())
}
