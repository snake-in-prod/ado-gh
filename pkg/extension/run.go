package extension

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	aliasShared "github.com/snake-in-prod/ado-gh/pkg/cmd/alias/shared"
	authShared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// Run runs the installed extension ext with args on f's streams. It gets ADO_TOKEN only when its
// install record has token consent. A non-zero exit is a *cmdutil.ExternalExitError.
func Run(f *cmdutil.Factory, ext Extension, args []string) error {
	rec, err := ReadRecord(filepath.Dir(ext.Path), ext.Name)
	consent := err == nil && rec.TokenConsent
	exe := ext.Executable()
	// Checked here, not by exec: on Windows a file with no known extension runs through sh,
	// which would report a missing file as the extension's own exit code 127.
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("extension %q has no executable %s; reinstall it with ado extension install --force", ext.Name, filepath.Base(exe))
	}
	return launcher{}.launch(f, ext.Name, exe, consent, args)
}

// RunPath runs the executable ado-<name> found on PATH at path. It never gets a token.
func RunPath(f *cmdutil.Factory, name, path string, args []string) error {
	return launcher{}.launch(f, name, path, false, args)
}

// Executable returns the file that runs ext: ado-<name> in its Dir; on Windows the first of
// ado-<name>.exe, .ps1 and .cmd, else ado-<name> (run through sh).
func (e Extension) Executable() string { return executable(runtime.GOOS, e.Dir, e.Name) }

func executable(goos, dir, name string) string {
	base := filepath.Join(dir, Prefix+name)
	if goos == "windows" {
		for _, ext := range []string{".exe", ".ps1", ".cmd"} {
			if fi, err := os.Stat(base + ext); err == nil && fi.Mode().IsRegular() {
				return base + ext
			}
		}
	}
	return base
}

// launcher starts extensions. Zero fields mean the real OS; tests set them.
type launcher struct {
	goos     string
	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
	run      func(*exec.Cmd) error
	token    func(f *cmdutil.Factory, host, org string) (api.Token, error)
}

func lookupToken(f *cmdutil.Factory, host, org string) (api.Token, error) {
	c, err := authShared.Lookup(context.Background(), f, host, org)
	return c.Token, err
}

func (l launcher) launch(f *cmdutil.Factory, name, exe string, consent bool, args []string) error {
	if l.goos == "" {
		l.goos = runtime.GOOS
	}
	if l.lookPath == nil {
		l.lookPath = exec.LookPath
	}
	if l.stat == nil {
		l.stat = os.Stat
	}
	if l.run == nil {
		l.run = (*exec.Cmd).Run
	}
	if l.token == nil {
		l.token = lookupToken
	}
	cmd, err := l.command(exe, args)
	if err != nil {
		return err
	}
	cmd.Env = l.env(f, name, consent)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = f.IOStreams.In, f.IOStreams.Out, f.IOStreams.ErrOut
	// ^C reaches the extension too (same console / process group): ado waits for it to exit and
	// passes its code on. Notify, not Ignore: an ignored SIGINT stays ignored across exec on Unix.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	return cmdutil.ExternalExit(l.run(cmd))
}

// cmdMeta are the characters cmd.exe interprets in a batch file's arguments; Go can't quote
// them for it (golang/go#69939).
const cmdMeta = "&|<>^%\"\r\n"

// command returns the command running exe with args. Unix execs exe. Windows runs .exe
// directly, .ps1 through pwsh (else powershell) with -ExecutionPolicy Bypass, .cmd/.bat directly
// but refuses arguments with cmd.exe metacharacters, and anything else through sh like gh.
func (l launcher) command(exe string, args []string) (*exec.Cmd, error) {
	if l.goos != "windows" {
		return exec.Command(exe, args...), nil
	}
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".exe", ".com":
		return exec.Command(exe, args...), nil
	case ".ps1":
		for _, ps := range []string{"pwsh", "powershell"} {
			if p, err := l.lookPath(ps); err == nil {
				// ponytail: -File can't run the $ProgressPreference fix the shell aliases use;
				// stdio is inherited (a console), where 5.1 prints no CLIXML progress.
				return exec.Command(p, append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", exe}, args...)...), nil
			}
		}
		return nil, fmt.Errorf("PowerShell is required to run %s; install PowerShell 7 (pwsh)", exe)
	case ".cmd", ".bat":
		if i := slices.IndexFunc(args, func(a string) bool { return strings.ContainsAny(a, cmdMeta) }); i >= 0 {
			return nil, fmt.Errorf("refusing to pass %q to %s: cmd.exe can't safely receive arguments containing %s; the extension should ship as .exe or .ps1",
				args[i], filepath.Base(exe), `& | < > ^ % "`)
		}
		return exec.Command(exe, args...), nil
	}
	sh, ok := aliasShared.FindSh(l.lookPath, l.stat)
	if !ok {
		return nil, fmt.Errorf("the sh.exe interpreter is required to run %s; install Git for Windows", filepath.Base(exe))
	}
	return exec.Command(sh, append([]string{"-c", `command "$@"`, "--", exe}, args...)...), nil
}

// env returns the extension's environment: ado's own (so TTY and colour settings are the
// user's, not ado's resolved state) plus the context variables. ADO_REPO is set only from -R
// (or kept when inherited), since a child ado reads it as an override; otherwise ADO_REPO_NAME
// names the repo. Unresolved context leaves the variables empty, keeping inherited ADO_ORG and
// ADO_PROJECT. Without token consent, inherited tokens are removed and a warning is printed.
func (l launcher) env(f *cmdutil.Factory, name string, consent bool) []string {
	repo, repoErr := f.BaseRepo()
	if repoErr != nil {
		repo = adocontext.Repo{Org: os.Getenv("ADO_ORG"), Project: os.Getenv("ADO_PROJECT")}
	}
	host := ""
	if repo.Org != "" {
		// The API host: dev.azure.com or <org>.visualstudio.com, never ssh.dev.azure.com.
		if u, err := url.Parse(adocontext.OrgURL(repo.Host, repo.Org)); err == nil {
			host = u.Host
		}
	}
	set := [][2]string{
		{"ADO_HOST", host},
		{"ADO_ORG", repo.Org},
		{"ADO_PROJECT", repo.Project},
		{"ADO_EXTENSION_NAME", name},
		{"ADO_EXECUTABLE", f.Executable},
	}
	drop := []string{"ADO_REPO_NAME"}
	switch {
	case f.RepoOverride != "" && repoErr == nil:
		set = append(set, [2]string{"ADO_REPO", repo.Org + "/" + repo.Project + "/" + repo.Name})
	case f.RepoOverride != "":
		set = append(set, [2]string{"ADO_REPO", f.RepoOverride}) // the extension's ado reports it
	case os.Getenv("ADO_REPO") != "": // inherited: kept as is
	default:
		set = append(set, [2]string{"ADO_REPO_NAME", repo.Name})
	}
	if f.Debug {
		set = append(set, [2]string{"ADO_DEBUG", "1"})
	}

	errOut := f.IOStreams.ErrOut
	switch {
	case !consent:
		drop = append(drop, "ADO_TOKEN_TYPE")
		var withheld []string
		for _, k := range auth.EnvTokens {
			drop = append(drop, k)
			if os.Getenv(k) != "" {
				withheld = append(withheld, k)
			}
		}
		if len(withheld) > 0 {
			fmt.Fprintf(errOut, "! %s not passed to extension %q: it has no token consent\n", strings.Join(withheld, " and "), name)
		}
	case repo.Org != "":
		tokHost := repo.Host
		if tokHost == "" {
			tokHost = "dev.azure.com" // org from ADO_ORG
		}
		tok, err := l.token(f, tokHost, repo.Org)
		if err == nil && tok.Value != "" {
			set = append(set, [2]string{"ADO_TOKEN", tok.Value})
			if tok.Bearer {
				set = append(set, [2]string{"ADO_TOKEN_TYPE", "bearer"})
			} else {
				drop = append(drop, "ADO_TOKEN_TYPE")
			}
		} else if err != nil {
			fmt.Fprintf(errOut, "! no token for extension %q: %v\n", name, err)
		}
	}

	for _, kv := range set {
		drop = append(drop, kv[0])
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(drop, func(d string) bool { return strings.EqualFold(k, d) })
	})
	for _, kv := range set {
		env = append(env, kv[0]+"="+kv[1])
	}
	return env
}
