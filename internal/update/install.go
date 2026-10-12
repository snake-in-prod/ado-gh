// Package update checks GitHub for newer ado releases.
package update

import (
	"path"
	"runtime/debug"
	"strings"
)

const (
	wingetID  = "SnakeInProd.Ado"
	scriptURL = "https://snake-in-prod.github.io/ado-gh/install"
)

// detectInstallMethod returns the command that upgrades ado for the way it was installed,
// or "" when unknown (the caller shows the release URL). exe is the symlink-resolved
// executable path; bi is the binary's build info (may be nil); stamped reports whether
// the build metadata came from ldflags (release builds). Paths are matched with `\`
// turned into `/` and lowercased, so every OS's rules run on any host.
func detectInstallMethod(exe, goos string, getenv func(string) string, bi *debug.BuildInfo, stamped bool) string {
	// go install …@version builds from the module cache and records its checksum; local
	// and release builds don't. Release builds also carry ldflags, which go install never sets.
	if bi != nil && bi.Main.Sum != "" && !stamped {
		return "go install " + bi.Path + "@latest"
	}
	p := norm(exe)
	switch {
	case strings.Contains(p, "/caskroom/ado/"):
		return "brew upgrade --cask ado"
	case strings.Contains(p, "/cellar/ado/"):
		return "brew upgrade ado"
	case strings.Contains(p, "/apps/ado/current/"), strings.Contains(p, "/scoop/apps/ado/"):
		if under(p, getenv("SCOOP_GLOBAL")) || under(p, joinEnv(getenv("ProgramData"), "scoop")) {
			return "scoop update ado --global"
		}
		return "scoop update ado"
	case strings.Contains(p, "/winget/packages/"+strings.ToLower(wingetID)+"_"), strings.Contains(p, "/winget/links/"):
		return "winget upgrade --id " + wingetID
	}
	dir := path.Dir(p)
	if goos == "windows" {
		if d := joinEnv(getenv("LocalAppData"), "programs/ado"); d != "" && dir == d {
			return "irm " + scriptURL + ".ps1 | iex"
		}
	} else if d := joinEnv(getenv("HOME"), ".local/bin"); d != "" && dir == d {
		return "curl -fsSL " + scriptURL + ".sh | sh"
	}
	return ""
}

// norm makes a path comparable across OSes: forward slashes, lowercase, no trailing slash.
func norm(p string) string {
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(p, `\`, "/")), "/")
}

// joinEnv joins a normalized environment directory with sub, or returns "" when dir is unset.
func joinEnv(dir, sub string) string {
	if dir == "" {
		return ""
	}
	return norm(dir) + "/" + sub
}

// under reports whether p lies inside dir (already set or empty).
func under(p, dir string) bool {
	return dir != "" && strings.HasPrefix(p, norm(dir)+"/")
}
