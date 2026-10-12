package update

import (
	"runtime/debug"
	"testing"
)

func TestDetectInstallMethod(t *testing.T) {
	winEnv := map[string]string{
		"LocalAppData": `C:\Users\Me\AppData\Local`,
		"ProgramData":  `C:\ProgramData`,
	}
	unixEnv := map[string]string{"HOME": "/home/me"}
	goInstall := &debug.BuildInfo{Path: "github.com/snake-in-prod/ado-gh/cmd/ado"}
	goInstall.Main.Sum = "h1:abc="
	release := &debug.BuildInfo{Path: "github.com/snake-in-prod/ado-gh/cmd/ado"}
	const (
		goCmd   = "go install github.com/snake-in-prod/ado-gh/cmd/ado@latest"
		cask    = "brew upgrade --cask ado"
		scoop   = "scoop update ado"
		winget  = "winget upgrade --id SnakeInProd.Ado"
		shCmd   = "curl -fsSL https://snake-in-prod.github.io/ado-gh/install.sh | sh"
		psCmd   = "irm https://snake-in-prod.github.io/ado-gh/install.ps1 | iex"
		pkgsDir = `\Microsoft\WinGet\Packages\`
	)

	tests := []struct {
		name    string
		exe     string
		goos    string
		env     map[string]string
		bi      *debug.BuildInfo
		stamped bool
		want    string // "" means: show the release URL
	}{
		{name: "go install custom GOBIN", exe: "/opt/tools/ado", goos: "linux", env: unixEnv, bi: goInstall, want: goCmd},
		{name: "go install windows", exe: `C:\Users\Me\go\bin\ado.exe`, goos: "windows", env: winEnv, bi: goInstall, want: goCmd},
		{name: "go install wins over install script dir", exe: "/home/me/.local/bin/ado", goos: "linux", env: unixEnv, bi: goInstall, want: goCmd},
		{name: "sum ignored when ldflags stamped", exe: "/opt/homebrew/Caskroom/ado/1.2.3/ado", goos: "darwin", bi: goInstall, stamped: true, want: cask},
		{name: "sum ignored when stamped unknown path", exe: "/opt/tools/ado", goos: "linux", env: unixEnv, bi: goInstall, stamped: true},
		{name: "nil build info", exe: "/opt/tools/ado", goos: "linux", env: unixEnv},
		{name: "brew cask apple silicon", exe: "/opt/homebrew/Caskroom/ado/1.2.3/ado", goos: "darwin", bi: release, want: cask},
		{name: "brew cask intel", exe: "/usr/local/Caskroom/ado/1.2.3/ado", goos: "darwin", bi: release, want: cask},
		{name: "brew cask linuxbrew", exe: "/home/linuxbrew/.linuxbrew/Caskroom/ado/1.2.3/ado", goos: "linux", env: unixEnv, want: cask},
		{name: "brew formula cellar", exe: "/opt/homebrew/Cellar/ado/1.2.3/bin/ado", goos: "darwin", want: "brew upgrade ado"},
		{name: "other cask not matched", exe: "/opt/homebrew/Caskroom/adobe/1/ado", goos: "darwin"},
		{name: "scoop user", exe: `C:\Users\Me\scoop\apps\ado\current\ado.exe`, goos: "windows", env: winEnv, want: scoop},
		{name: "scoop user versioned dir", exe: `C:\Users\Me\scoop\apps\ado\1.2.3\ado.exe`, goos: "windows", env: winEnv, want: scoop},
		{name: "scoop custom root", exe: `D:\tools\apps\ado\current\ado.exe`, goos: "windows", env: winEnv, want: scoop},
		{name: "scoop global default", exe: `C:\ProgramData\scoop\apps\ado\current\ado.exe`, goos: "windows", env: winEnv, want: scoop + " --global"},
		{name: "scoop global custom", exe: `E:\GlobalScoop\apps\ado\current\ado.exe`, goos: "windows",
			env: map[string]string{"SCOOP_GLOBAL": `E:\GlobalScoop\`}, want: scoop + " --global"},
		{name: "winget user", exe: winEnv["LocalAppData"] + pkgsDir + `SnakeInProd.Ado_Microsoft.Winget.Source_8wekyb3d8bbwe\ado.exe`,
			goos: "windows", env: winEnv, want: winget},
		{name: "winget machine", exe: `C:\Program Files\WinGet\Packages\SnakeInProd.Ado_Microsoft.Winget.Source_8wekyb3d8bbwe\ado.exe`,
			goos: "windows", env: winEnv, want: winget},
		{name: "winget links fallback", exe: winEnv["LocalAppData"] + `\Microsoft\WinGet\Links\ado.exe`, goos: "windows", env: winEnv, want: winget},
		{name: "winget other package", exe: winEnv["LocalAppData"] + pkgsDir + `Other.Tool_x\ado.exe`, goos: "windows", env: winEnv},
		{name: "install script unix", exe: "/home/me/.local/bin/ado", goos: "linux", env: unixEnv, bi: release, want: shCmd},
		{name: "install script trailing slash home", exe: "/Users/me/.local/bin/ado", goos: "darwin", env: map[string]string{"HOME": "/Users/me/"}, want: shCmd},
		{name: "install script windows", exe: winEnv["LocalAppData"] + `\Programs\ado\ado.exe`, goos: "windows", env: winEnv, want: psCmd},
		{name: "install script windows case", exe: `c:\users\me\appdata\local\programs\ado\ado.exe`, goos: "windows", env: winEnv, want: psCmd},
		{name: "local bin subdir", exe: "/home/me/.local/bin/sub/ado", goos: "linux", env: unixEnv},
		{name: "unset home", exe: "/.local/bin/ado", goos: "linux", env: map[string]string{}},
		{name: "unset local app data", exe: `\Programs\ado\ado.exe`, goos: "windows", env: map[string]string{}},
		{name: "deb or rpm", exe: "/usr/bin/ado", goos: "linux", env: unixEnv, bi: release},
		{name: "unknown windows", exe: `D:\bin\ado.exe`, goos: "windows", env: winEnv, bi: release},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			if got := detectInstallMethod(tt.exe, tt.goos, getenv, tt.bi, tt.stamped); got != tt.want {
				t.Errorf("detectInstallMethod(%q) = %q, want %q", tt.exe, got, tt.want)
			}
		})
	}
}
