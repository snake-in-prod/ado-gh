package login

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

var errNoRepo = errors.New("not an Azure DevOps repository")

type env struct {
	ios       *iostreams.IOStreams
	in        *bytes.Buffer
	out       *bytes.Buffer
	errOut    *bytes.Buffer
	reg       *httpmock.Registry
	configDir string
	repo      adocontext.Repo
	repoErr   error
	debug     bool
	// accessible switches to line-based prompts reading stdin one byte at a time, so
	// several prompts can share stdin.
	accessible bool
	browser    *browsertest.Browser
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", dir)
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_TOKEN", "")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	e := &env{ios: ios, in: in, out: out, errOut: errOut, reg: &httpmock.Registry{}, configDir: dir, repoErr: errNoRepo, browser: &browsertest.Browser{}}
	baseTransport = e.reg
	t.Cleanup(func() { baseTransport = nil })
	return e
}

func (e *env) tty() {
	e.ios.SetStdinTTY(true)
	e.ios.SetStdoutTTY(true)
}

func (e *env) factory() *cmdutil.Factory {
	if e.accessible {
		e.ios.In = iotest.OneByteReader(e.in)
	}
	return &cmdutil.Factory{
		AppVersion: "1.0",
		Debug:      e.debug,
		IOStreams:  e.ios,
		Config: func() (cmdutil.Config, error) {
			return config.Load()
		},
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: e.accessible},
		BaseRepo: func() (adocontext.Repo, error) { return e.repo, e.repoErr },
		Browser:  e.browser,
	}
}

func (e *env) run(args ...string) error {
	cmd := NewCmdLogin(e.factory())
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func connectionData(id string) httpmock.Responder {
	return httpmock.JSONResponse(map[string]any{
		"authenticatedUser": map[string]any{"id": id, "providerDisplayName": "Mona Lisa"},
	})
}

func basic(token string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+token))
}

func TestLoginWithToken(t *testing.T) {
	e := setup(t)
	// A short token proves there is no PAT length check.
	e.in.WriteString("  abc \n")
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	if err := e.run("--org", "contoso", "--with-token"); err != nil {
		t.Fatal(err)
	}
	e.reg.Verify(t)
	if len(e.reg.Requests) != 1 {
		t.Fatalf("requests = %d; want only connectionData", len(e.reg.Requests))
	}
	req := e.reg.Requests[0]
	if req.URL.Host != "dev.azure.com" || req.Header.Get("Authorization") != basic("abc") {
		t.Errorf("request %s with Authorization %q", req.URL, req.Header.Get("Authorization"))
	}
	if got, err := keyring.Get("ado:dev.azure.com/contoso", ""); err != nil || got != "abc" {
		t.Errorf("keyring = %q, %v", got, err)
	}
	if got := e.errOut.String(); !strings.Contains(got, "Logged in to dev.azure.com/contoso as Mona Lisa") {
		t.Errorf("stderr = %q", got)
	}
	if strings.Contains(e.out.String()+e.errOut.String(), "abc") {
		t.Error("token echoed")
	}
}

func TestLoginRejected(t *testing.T) {
	tests := []struct {
		name string
		resp httpmock.Responder
	}{
		{"anonymous user", connectionData("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")},
		{"203 sign-in page", httpmock.StatusStringResponse(203, "<html>Sign in</html>")},
		{"invalid token", httpmock.StatusStringResponse(401, "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			e.in.WriteString("bad-token\n")
			e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), tt.resp)

			err := e.run("--org", "contoso", "--with-token")
			if err == nil || !strings.Contains(err.Error(), "rejected") || !strings.Contains(err.Error(), "2026-12-01") {
				t.Fatalf("err = %v; want rejection mentioning the global-PAT cutoff", err)
			}
			var authErr *cmdutil.AuthError
			if errors.As(err, &authErr) {
				t.Error("login must not ask to run `ado auth login`")
			}
			if _, err := keyring.Get("ado:dev.azure.com/contoso", ""); !errors.Is(err, keyring.ErrNotFound) {
				t.Errorf("token stored after rejection: %v", err)
			}
		})
	}
}

func TestLoginOrgSelection(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		adoOrg  string
		repo    *adocontext.Repo
		path    string
		host    string
		service string
	}{
		{
			name: "flag wins over remote and env", args: []string{"--org", "flagorg"}, adoOrg: "envorg",
			repo: &adocontext.Repo{Host: "dev.azure.com", Org: "remoteorg"},
			path: "flagorg/_apis/connectionData", host: "dev.azure.com", service: "ado:dev.azure.com/flagorg",
		},
		{
			name: "git remote wins over env", adoOrg: "envorg",
			repo: &adocontext.Repo{Host: "fabrikam.visualstudio.com", Org: "fabrikam", Project: "p", Name: "r"},
			path: "_apis/connectionData", host: "fabrikam.visualstudio.com", service: "ado:fabrikam.visualstudio.com/fabrikam",
		},
		{
			name: "ADO_ORG", adoOrg: "envorg",
			path: "envorg/_apis/connectionData", host: "dev.azure.com", service: "ado:dev.azure.com/envorg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			t.Setenv("ADO_ORG", tt.adoOrg)
			if tt.repo != nil {
				e.repo, e.repoErr = *tt.repo, nil
			}
			e.in.WriteString("tok\n")
			e.reg.Register(httpmock.REST("GET", tt.path), connectionData("1234"))

			if err := e.run(append(tt.args, "--with-token")...); err != nil {
				t.Fatal(err)
			}
			e.reg.Verify(t)
			if got := e.reg.Requests[0].URL.Host; got != tt.host {
				t.Errorf("host = %q; want %q", got, tt.host)
			}
			if got, err := keyring.Get(tt.service, ""); err != nil || got != "tok" {
				t.Errorf("keyring %s = %q, %v", tt.service, got, err)
			}
		})
	}
}

// runPAT runs the PAT flow past the login menu.
func (e *env) runPAT(org string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return patLogin(cmd, &options{f: e.factory(), org: org}, cfg)
}

// TestLoginInteractive drives the masked huh prompt. One test drives one
// prompt: a huh form reads all of stdin, leaving nothing for a second one.
// The masked prompt needs a real TTY in accessible mode, so this starts past the menu.
func TestLoginInteractive(t *testing.T) {
	e := setup(t)
	e.tty()
	e.in.WriteString("secret-pat\r")
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	if err := e.runPAT("contoso"); err != nil {
		t.Fatal(err)
	}
	e.reg.Verify(t)
	if got := e.reg.Requests[0].Header.Get("Authorization"); got != basic("secret-pat") {
		t.Errorf("Authorization = %q", got)
	}
	if strings.Contains(e.out.String(), "secret-pat") {
		t.Errorf("PAT echoed by the prompt: %q", e.out)
	}
	if got, err := keyring.Get("ado:dev.azure.com/contoso", ""); err != nil || got != "secret-pat" {
		t.Errorf("keyring = %q, %v", got, err)
	}
}

func TestResolveOrgPrompt(t *testing.T) {
	tests := []struct{ input, want, wantErr string }{
		{input: " promptorg \n", want: "promptorg"},
		{input: "\n", wantErr: "no organization"},
	}
	for _, tt := range tests {
		e := setup(t)
		e.tty()
		e.in.WriteString(tt.input)
		opts := &options{f: &cmdutil.Factory{
			IOStreams: e.ios,
			Prompter:  &cmdutil.Prompter{IO: e.ios, Accessible: true},
			BaseRepo:  func() (adocontext.Repo, error) { return adocontext.Repo{}, errNoRepo },
		}}
		host, org, err := resolveOrg(opts)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("input %q: err = %v; want %q", tt.input, err, tt.wantErr)
			}
			continue
		}
		if err != nil || host != "dev.azure.com" || org != tt.want {
			t.Errorf("input %q: got %q, %q, %v", tt.input, host, org, err)
		}
		if !strings.Contains(e.out.String(), "organization") {
			t.Errorf("org prompt not shown: %q", e.out)
		}
	}
}

func TestLoginErrors(t *testing.T) {
	tests := []struct {
		name     string
		tty      bool
		args     []string
		stdin    string
		pat      bool
		wantErr  string
		wantFlag bool
	}{
		{name: "non-TTY without org", args: []string{"--with-token"}, stdin: "tok\n", wantErr: "no organization"},
		{name: "non-TTY without --with-token", args: []string{"--org", "contoso"}, wantErr: "--with-token required", wantFlag: true},
		{name: "empty token", args: []string{"--org", "contoso", "--with-token"}, stdin: " \n", wantErr: "no token"},
		{name: "empty prompted token", tty: true, pat: true, stdin: "\r", wantErr: "no token"},
		{name: "cancelled PAT prompt", tty: true, pat: true, stdin: "\x03", wantErr: "CancelError"},
		{name: "cancelled login menu", tty: true, args: []string{"--org", "contoso"}, stdin: "\x03", wantErr: "CancelError"},
		{name: "--device with --with-token", args: []string{"--device", "--with-token"}, wantErr: "none of the others can be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			if tt.tty {
				e.tty()
			}
			e.in.WriteString(tt.stdin)
			var err error
			if tt.pat {
				err = e.runPAT("contoso")
			} else {
				err = e.run(tt.args...)
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v; want %q", err, tt.wantErr)
			}
			var flagErr *cmdutil.FlagError
			if errors.As(err, &flagErr) != tt.wantFlag {
				t.Errorf("FlagError = %v; want %v", !tt.wantFlag, tt.wantFlag)
			}
			if len(e.reg.Requests) != 0 {
				t.Error("validated without an org and a token")
			}
		})
	}
}

func TestLoginDebugOrgNotFound(t *testing.T) {
	e := setup(t)
	e.debug = true
	e.in.WriteString("tok\n")
	e.reg.Register(httpmock.REST("GET", "nope/_apis/connectionData"), httpmock.StatusStringResponse(404, ""))

	err := e.run("--org", "nope", "--with-token")
	if err == nil || !strings.Contains(err.Error(), `organization "nope" not found`) {
		t.Fatalf("err = %v", err)
	}
	log := e.errOut.String()
	if !strings.Contains(log, "> GET https://dev.azure.com/nope/_apis/connectionData") ||
		!strings.Contains(log, "Authorization: [REDACTED]") || strings.Contains(log, basic("tok")) {
		t.Errorf("debug log = %q", log)
	}
}

type memConfig struct{}

func (memConfig) Get(string, string) string  { return "" }
func (memConfig) Set(string, string, string) {}
func (memConfig) Write() error               { return nil }

func TestLoginConfigWithoutUnset(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return memConfig{}, nil }}
	cmd := NewCmdLogin(f)
	cmd.SetArgs([]string{"--org", "contoso", "--with-token"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "storing credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginConfigError(t *testing.T) {
	e := setup(t)
	if err := os.WriteFile(filepath.Join(e.configDir, "config.yml"), []byte(":\n\t- bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.run("--org", "contoso", "--with-token"); err == nil {
		t.Fatal("want config parse error")
	}
}

func TestLoginInsecureStorage(t *testing.T) {
	e := setup(t)
	e.in.WriteString("plain-pat\n")
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	if err := e.run("--org", "contoso", "--with-token", "--insecure-storage"); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Get("ado:dev.azure.com/contoso", ""); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring used despite --insecure-storage: %v", err)
	}
	path := filepath.Join(e.configDir, "config.yml")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "plain-pat") {
		t.Fatalf("config = %q, %v", b, err)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v; want 0600", fi.Mode().Perm())
	}
	if !strings.Contains(e.errOut.String(), "saved in plain text") {
		t.Errorf("stderr = %q; want plaintext warning", e.errOut)
	}
}

func TestLoginHelp(t *testing.T) {
	cmd := NewCmdLogin(&cmdutil.Factory{})
	for _, want := range []string{"2026-12-01", "--with-token", "--org", "--insecure-storage", "--device", "Microsoft Entra ID", "device code"} {
		if !strings.Contains(cmd.Long+cmd.Example+cmd.Flags().FlagUsages(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
