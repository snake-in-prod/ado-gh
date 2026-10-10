// Package adocontext resolves the Azure DevOps repository a command targets.
package adocontext

import (
	"errors"
	"net/url"
	"strings"
	"unicode"

	"github.com/cli/go-gh/v2/pkg/ssh"
)

// Repo identifies an Azure DevOps Git repository.
type Repo struct {
	// Host is the remote's host, lowercased but not normalized to
	// dev.azure.com (e.g. fabrikam.visualstudio.com, ssh.dev.azure.com), so
	// per-host config is keyed by the host the user cloned from.
	Host    string
	Org     string
	Project string
	Name    string
}

const (
	hostDevAzure = "dev.azure.com"
	hostSSH      = "ssh.dev.azure.com"
	hostVSSSH    = "vs-ssh.visualstudio.com"
	vsSuffix     = ".visualstudio.com"
)

// errNotADO never includes the remote: it may carry a PAT in its userinfo.
var errNotADO = errors.New("not an Azure DevOps remote")

// translateSSH resolves ~/.ssh/config host aliases via `ssh -G`.
var translateSSH = ssh.NewTranslator().Translate

// ParseRemoteURL parses an Azure DevOps git remote (HTTPS on dev.azure.com or
// {org}.visualstudio.com, SSH v3 on ssh.dev.azure.com or
// vs-ssh.visualstudio.com, legacy vs-ssh `_ssh` paths, or an SSH host alias
// with a v3 path). Userinfo is dropped, except as the org of legacy `_ssh`
// remotes.
func ParseRemoteURL(raw string) (Repo, error) {
	if strings.ContainsFunc(raw, func(r rune) bool {
		return r == '\\' || r == '?' || r == '#' || unicode.IsControl(r)
	}) {
		return Repo{}, errNotADO
	}
	// scp-style `user@host:path` → `ssh://user@host/path` (go-gh pattern).
	if !strings.Contains(raw, "://") && strings.Contains(raw, ":") {
		raw = "ssh://" + strings.Replace(raw, ":", "/", 1)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Repo{}, errNotADO
	}
	segs, ok := segments(u.EscapedPath())
	if !ok {
		return Repo{}, errNotADO
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	switch {
	case u.Scheme == "https" && (port == "" || port == "443"):
		return parseHTTPS(host, segs)
	case u.Scheme == "ssh" && (port == "" || port == "22"):
		return parseSSH(u, host, segs)
	}
	return Repo{}, errNotADO
}

// segments splits an escaped path and unescapes each segment once, so `%2F`
// can't fake a separator. One trailing slash is tolerated.
func segments(escaped string) ([]string, bool) {
	segs := strings.Split(strings.TrimSuffix(strings.TrimPrefix(escaped, "/"), "/"), "/")
	for i, s := range segs {
		d, err := url.PathUnescape(s)
		if err != nil || !validSegment(d) {
			return nil, false
		}
		segs[i] = d
	}
	return segs, true
}

func validSegment(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return r == '/' || r == '\\' || unicode.IsControl(r)
	})
}

// parseHTTPS handles {org}/[{project}/]_git/[_full|_optimized/]{repo} on
// dev.azure.com and [DefaultCollection/][{project}/[{team}/]]_git/... on
// {org}.visualstudio.com. A missing project means project = repo.
func parseHTTPS(host string, segs []string) (Repo, error) {
	var org string
	maxBefore := 1
	if host == hostDevAzure {
		org, segs = segs[0], segs[1:]
	} else if label, ok := strings.CutSuffix(host, vsSuffix); ok && label != "" &&
		label != "vs-ssh" && !strings.Contains(label, ".") {
		org, maxBefore = label, 2
		if strings.EqualFold(segs[0], "DefaultCollection") {
			segs = segs[1:]
		}
	} else {
		return Repo{}, errNotADO
	}
	i := indexFold(segs, "_git")
	if i < 0 || i > maxBefore {
		return Repo{}, errNotADO
	}
	after := dropLimitedRefs(segs[i+1:])
	if len(after) != 1 {
		return Repo{}, errNotADO
	}
	project := after[0]
	if i > 0 {
		project = segs[0] // a visualstudio.com team segment is dropped
	}
	return Repo{Host: host, Org: org, Project: project, Name: after[0]}, nil
}

// parseSSH handles v3/{org}/{project}/[{team}/][_full|_optimized/]{repo} and
// legacy [{org}/]{project}/_ssh/{repo} on vs-ssh (org from the username in the
// short form). Unknown hosts with a v3 path are resolved as SSH aliases.
func parseSSH(u *url.URL, host string, segs []string) (Repo, error) {
	if strings.EqualFold(segs[0], "v3") {
		if host != hostDevAzure && host != hostSSH && !strings.HasSuffix(host, vsSuffix) {
			host = strings.ToLower(translateSSH(u).Hostname())
		}
		if host != hostSSH && host != hostVSSSH {
			return Repo{}, errNotADO
		}
		segs = dropLimitedRefs(segs[1:])
		if len(segs) != 3 && len(segs) != 4 { // 4: team segment dropped
			return Repo{}, errNotADO
		}
		return Repo{Host: host, Org: segs[0], Project: segs[1], Name: segs[len(segs)-1]}, nil
	}
	if host != hostVSSSH {
		return Repo{}, errNotADO
	}
	if len(segs) == 3 {
		segs = append([]string{u.User.Username()}, segs...)
	}
	if len(segs) != 4 || !strings.EqualFold(segs[2], "_ssh") ||
		!validSegment(segs[0]) || strings.EqualFold(segs[0], "DefaultCollection") {
		return Repo{}, errNotADO
	}
	return Repo{Host: host, Org: segs[0], Project: segs[1], Name: segs[3]}, nil
}

// dropLimitedRefs removes a `_full` / `_optimized` segment before the repo.
func dropLimitedRefs(segs []string) []string {
	if n := len(segs); n >= 2 && (strings.EqualFold(segs[n-2], "_full") || strings.EqualFold(segs[n-2], "_optimized")) {
		return append(segs[:n-2:n-2], segs[n-1])
	}
	return segs
}

func indexFold(segs []string, s string) int {
	for i, v := range segs {
		if strings.EqualFold(v, s) {
			return i
		}
	}
	return -1
}
