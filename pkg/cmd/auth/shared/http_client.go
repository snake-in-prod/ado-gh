package shared

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// IsADOHost reports whether host is an Azure DevOps Services API host: dev.azure.com,
// *.dev.azure.com or *.visualstudio.com (except the vs-ssh git host).
func IsADOHost(host string) bool {
	host = strings.ToLower(host)
	switch {
	case host == "dev.azure.com", strings.HasSuffix(host, ".dev.azure.com") && host != "ssh.dev.azure.com":
		return true
	case strings.HasSuffix(host, ".visualstudio.com"):
		return host != "vs-ssh.visualstudio.com"
	}
	return false
}

// HTTPClient returns a client authenticated to host/org with the credential Lookup finds.
// It sends the credential only to Azure DevOps hosts (see IsADOHost), so redirects elsewhere
// go out without it, and logs requests to stderr when f.Debug is set. Requests go through
// f.HttpClient's transport.
func HTTPClient(ctx context.Context, f *cmdutil.Factory, host, org string) (*http.Client, error) {
	cred, err := Lookup(ctx, f, host, org)
	if err != nil {
		return nil, err
	}
	plain, err := f.HttpClient()
	if err != nil {
		return nil, err
	}
	var log io.Writer
	if f.Debug {
		log = f.IOStreams.ErrOut
	}
	return api.NewHTTPClient(api.ClientOptions{
		AppVersion: f.AppVersion,
		Token: func(req *http.Request) (api.Token, error) {
			if !IsADOHost(req.URL.Hostname()) {
				return api.Token{}, nil
			}
			return cred.Token, nil
		},
		Log:       log,
		Transport: plain.Transport,
	}), nil
}
