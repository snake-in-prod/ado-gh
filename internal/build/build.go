// Package build holds the version metadata of the ado binary.
package build

import (
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

// RepoURL is the canonical repository, base of release and docs links.
const RepoURL = "https://github.com/snake-in-prod/ado-gh"

// Version, Commit and Date are injected at release time:
//
//	-ldflags "-X github.com/snake-in-prod/ado-gh/internal/build.Version=1.2.3
//	          -X github.com/snake-in-prod/ado-gh/internal/build.Commit=<sha>
//	          -X github.com/snake-in-prod/ado-gh/internal/build.Date=<RFC3339>"
//
// Without ldflags they fall back to Go's build info; Version is "DEV" for source builds.
var (
	Version string
	Commit  string
	Date    string
	// Dirty reports uncommitted changes in the source tree (vcs.modified).
	Dirty bool
	// Stamped reports whether Version was set via ldflags (release builds). It is
	// evaluated before init replaces Version with the build-info fallback.
	Stamped = Version != ""
)

// Info is the resolved build metadata.
type Info struct {
	Version string
	Commit  string
	Date    string
	Dirty   bool
}

func init() {
	i := resolve(Version, Commit, Date, debug.ReadBuildInfo)
	Version, Commit, Date, Dirty = i.Version, i.Commit, i.Date, i.Dirty
}

// pseudoVersion matches the timestamp and short commit at the end of a Go pseudo-version.
var pseudoVersion = regexp.MustCompile(`[-.](\d{14})-([0-9a-f]{12})$`)

// IsPseudoVersion reports whether v is a Go pseudo-version (an untagged commit, no release).
func IsPseudoVersion(v string) bool { return pseudoVersion.MatchString(v) }

func resolve(version, commit, date string, readBuildInfo func() (*debug.BuildInfo, bool)) Info {
	i := Info{Version: strings.TrimPrefix(version, "v"), Commit: commit, Date: date}
	bi, ok := readBuildInfo()
	if !ok {
		if i.Version == "" {
			i.Version = "DEV"
		}
		return i
	}
	s := map[string]string{}
	for _, kv := range bi.Settings {
		s[kv.Key] = kv.Value
	}
	_, local := s["vcs"]
	i.Dirty = s["vcs.modified"] == "true"
	if local {
		// Source checkout build: Go 1.24+ stamps a tag version, but it is still a DEV build.
		if i.Version == "" {
			i.Version = "DEV"
		}
		if i.Commit == "" {
			i.Commit = s["vcs.revision"]
		}
		if i.Date == "" {
			i.Date = s["vcs.time"]
		}
		return i
	}
	mv := bi.Main.Version
	if mv == "" || mv == "(devel)" {
		if i.Version == "" {
			i.Version = "DEV"
		}
		return i
	}
	// Module-cache build (go install …@version): no vcs.* settings.
	if i.Version == "" {
		i.Version = strings.TrimPrefix(mv, "v")
	}
	if m := pseudoVersion.FindStringSubmatch(mv); m != nil {
		if i.Commit == "" {
			i.Commit = m[2]
		}
		if t, err := time.Parse("20060102150405", m[1]); i.Date == "" && err == nil {
			i.Date = t.UTC().Format(time.RFC3339)
		}
	}
	return i
}
