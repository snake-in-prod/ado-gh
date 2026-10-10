package cmdutil

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"

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

// systemBrowser opens URLs with the OS default browser. Zero fields mean the real
// OS; tests set them.
type systemBrowser struct {
	goos     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	run      func(name string, args ...string) error
	openURL  func(string) error
}

func (b systemBrowser) Browse(u string) error {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return fmt.Errorf("opening unsupported URL: %s", u)
	}
	goos, getenv, lookPath, run, openURL := b.goos, b.getenv, b.lookPath, b.run, b.openURL
	if goos == "" {
		goos = runtime.GOOS
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if run == nil {
		run = func(name string, args ...string) error { return exec.Command(name, args...).Run() }
	}
	if openURL == nil {
		openURL = clibrowser.OpenURL
	}
	// In WSL, xdg-open may open the Windows browser yet exit non-zero, which made MSAL
	// abandon its localhost listener. Open the Windows browser directly, like az does.
	if goos == "linux" && (getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != "") {
		if _, err := lookPath("wslview"); err == nil {
			return run("wslview", p.String())
		}
		return run("rundll32.exe", "url.dll,FileProtocolHandler", p.String())
	}
	return openURL(p.String())
}
