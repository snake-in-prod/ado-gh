package cmdutil

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// Config is ado's config file; internal/config.Config satisfies it.
type Config interface {
	// Get returns key for host, falling back to the global value; host "" reads globals.
	Get(host, key string) string
	// Set stores key for host in memory; host "" sets the global value.
	Set(host, key, value string)
	// Write saves the config file.
	Write() error
}

// Factory provides lazily built dependencies to commands. Tests replace fields.
type Factory struct {
	AppVersion string
	// Debug is set by ADO_DEBUG and the --debug flag.
	Debug     bool
	IOStreams *iostreams.IOStreams
	Config    func() (Config, error)
	// HttpClient returns the shared client used by raw and SDK requests.
	HttpClient func() (*http.Client, error) //nolint:revive // gh naming, as in the plan
	Browser    Browser
	Prompter   *Prompter
	GitClient  *git.Client
	// RepoOverride is the -R/--repo flag value.
	RepoOverride string
	// BaseRepo resolves the target repo: -R > ADO_REPO > git remotes.
	BaseRepo func() (adocontext.Repo, error)
}

// BaseRepoFromGit reports whether BaseRepo comes from the git remotes of the current directory,
// not from -R or ADO_REPO, so that local branches belong to the base repository.
func (f *Factory) BaseRepoFromGit() bool {
	return f.RepoOverride == "" && os.Getenv("ADO_REPO") == ""
}

// NewFactory returns the production Factory for the given version and streams.
func NewFactory(appVersion string, ios *iostreams.IOStreams) *Factory {
	f := &Factory{
		AppVersion: appVersion,
		Debug:      envTrue(os.Getenv("ADO_DEBUG")),
		IOStreams:  ios,
		Config: sync.OnceValues(func() (Config, error) {
			cfg, err := config.Load()
			if err != nil {
				return nil, err
			}
			return cfg, nil
		}),
		HttpClient: sync.OnceValues(func() (*http.Client, error) {
			// Auth, User-Agent and debug logging are layered on in the api package.
			return &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}, nil
		}),
		Prompter:  &Prompter{IO: ios},
		GitClient: &git.Client{},
	}
	f.BaseRepo = func() (adocontext.Repo, error) {
		return adocontext.ResolveBaseRepo(f.RepoOverride, func() ([]git.Remote, error) {
			return f.GitClient.Remotes(context.Background())
		})
	}
	f.Browser = browserFunc(func(url string) error {
		cfg, err := f.Config()
		if err != nil {
			return err
		}
		return NewBrowser(ResolveBrowserLauncher(cfg), f.IOStreams.Out, f.IOStreams.ErrOut).Browse(url)
	})
	return f
}

// envTrue reports whether an env flag is on: any value except "", "0" and false-like.
func envTrue(v string) bool {
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return v != ""
}
