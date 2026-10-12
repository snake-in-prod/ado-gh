package update

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"

	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// Timeout bounds the whole check, measured from Start: Print never waits past it.
const Timeout = 2 * time.Second

// interval is how long a successful check is trusted before GitHub is asked again.
const interval = 24 * time.Hour

var repo = extension.Source{Host: "github.com", Owner: "snake-in-prod", Repo: "ado-gh"}

// Release is the latest stable release.
type Release struct {
	Version string `yaml:"version"` // without the leading v
	URL     string `yaml:"url"`
}

type state struct {
	CheckedForUpdateAt time.Time `yaml:"checked_for_update_at"`
	LatestRelease      Release   `yaml:"latest_release"`
}

// Result is the outcome of a background check: a newer release, or none, or an error.
type Result struct {
	Release *Release
	Err     error
}

// disableEnv are the variables that turn the notifier off: the opt-out, an ado started by an
// extension, and CI systems (Azure Pipelines sets only TF_BUILD).
var disableEnv = []string{"ADO_NO_UPDATE_NOTIFIER", "ADO_EXTENSION_NAME",
	"CI", "TF_BUILD", "BUILD_NUMBER", "RUN_ID", "GITHUB_ACTIONS"}

// Enabled reports whether the notifier runs for version on ios.
func Enabled(ios *iostreams.IOStreams, version string) bool {
	if version == "DEV" {
		return false
	}
	for _, k := range disableEnv {
		if os.Getenv(k) != "" {
			return false
		}
	}
	return ios.IsStdoutTTY() && ios.IsStderrTTY()
}

// Start checks for a newer release than current in the background, until ctx ends, and
// returns the channel its Result arrives on; nil when the notifier is disabled. base is the
// transport under the GitHub client (nil: http.DefaultTransport).
func Start(ctx context.Context, ios *iostreams.IOStreams, current string, base http.RoundTripper) <-chan Result {
	if !Enabled(ios, current) {
		return nil
	}
	ch := make(chan Result, 1)
	go func() {
		// Not the command's Factory: its client and Debug are only final after flag parsing.
		f := &cmdutil.Factory{AppVersion: current, IOStreams: ios,
			HttpClient: func() (*http.Client, error) { return &http.Client{Transport: base}, nil }}
		hc, err := extension.GitHubClient(f)
		if err != nil {
			ch <- Result{Err: err}
			return
		}
		rel, err := Check(ctx, hc, filepath.Join(config.StateDir(), "state.yml"), current, time.Now())
		ch <- Result{Release: rel, Err: err}
	}()
	return ch
}

// Check returns the latest stable release when it is newer than current, else nil. GitHub is
// asked at most once per interval: the state file records each successful check, so failures
// and rate limits retry on the next command.
func Check(ctx context.Context, hc *http.Client, stateFile, current string, now time.Time) (*Release, error) {
	cur := "v" + strings.TrimPrefix(current, "v")
	if !semver.IsValid(cur) {
		return nil, nil
	}
	var st state
	if b, err := os.ReadFile(stateFile); err == nil {
		_ = yaml.Unmarshal(b, &st) // a broken state file means: check now
	}
	if now.Sub(st.CheckedForUpdateAt) < interval {
		return nil, nil
	}
	rel, err := extension.FetchRelease(ctx, hc, repo, "")
	if err != nil {
		return nil, err
	}
	latest := "v" + strings.TrimPrefix(rel.Tag, "v")
	if !semver.IsValid(latest) {
		return nil, fmt.Errorf("latest release has an invalid version %q", extension.Sanitize(rel.Tag))
	}
	st = state{CheckedForUpdateAt: now, LatestRelease: Release{Version: latest[1:], URL: build.RepoURL + "/releases/tag/" + latest}}
	b, err := yaml.Marshal(&st)
	if err == nil {
		err = config.WriteFileAtomic(stateFile, b)
	}
	if err != nil {
		return nil, err
	}
	// releases/latest is never a prerelease; skip one anyway.
	if rel.Prerelease || semver.Prerelease(latest) != "" || semver.Compare(latest, cur) <= 0 {
		return nil, nil
	}
	return &st.LatestRelease, nil
}

// Print waits for the result on ch until ctx ends and prints the notice to stderr; with
// debug it also reports a failed check. A nil ch (notifier disabled) returns at once.
func Print(ctx context.Context, ios *iostreams.IOStreams, ch <-chan Result, current string, debug bool) {
	if ch == nil {
		return
	}
	var r Result
	select {
	case r = <-ch:
	case <-ctx.Done():
		select {
		case r = <-ch: // finished just in time
		default:
			r.Err = ctx.Err()
		}
	}
	switch {
	case r.Err != nil && debug:
		fmt.Fprintf(ios.ErrOut, "warning: checking for update failed: %v\n", r.Err)
	case r.Release != nil:
		fmt.Fprint(ios.ErrOut, Message(ios.ColorScheme(), current, r.Release, upgradeCommand()))
	}
}

// Message is the notice for rel: the versions, the upgrade command for the install method
// (none when unknown) and the release URL.
func Message(cs *iostreams.ColorScheme, current string, rel *Release, upgrade string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s %s → %s\n", cs.Yellow("A new release of ado is available:"),
		cs.Cyan(strings.TrimPrefix(current, "v")), cs.Cyan(rel.Version))
	if upgrade != "" {
		fmt.Fprintf(&b, "To upgrade, run: %s\n", upgrade)
	}
	fmt.Fprintln(&b, cs.Yellow(rel.URL))
	return b.String()
}

// upgradeCommand detects how the running ado was installed (see detectInstallMethod).
func upgradeCommand() string {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		exe = ""
	}
	bi, _ := debug.ReadBuildInfo()
	return detectInstallMethod(exe, runtime.GOOS, os.Getenv, bi, build.Stamped)
}
