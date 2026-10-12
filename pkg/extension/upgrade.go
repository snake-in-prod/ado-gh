package extension

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// UpgradeOptions configures Upgrade.
type UpgradeOptions struct {
	// Root is the extensions root, Name the extension name (without ado-).
	Root, Name string
	// Force upgrades a pinned extension, and resets a script extension whose history no longer
	// fast-forwards to the remote's.
	Force bool
	// DryRun only finds the version an upgrade would install; it needs no Trust.Yes.
	DryRun bool
	Trust

	// Test hooks, as in ScriptOptions.
	goos, goarch string
	runTool      func(*exec.Cmd) error
}

// UpgradeResult says what Upgrade did. From is the installed version, tag or short commit and
// To the one installed (with DryRun, available); Skipped, when set, says why nothing changed.
type UpgradeResult struct {
	From, To, Skipped string
}

var (
	errUpToDate = errors.New("already up to date")
	errDryRun   = errors.New("dry run")
	// errReinstall: a script extension's new version installs differently (a binary).
	errReinstall = errors.New("reinstall")
)

// Upgrade upgrades the installed extension o.Name to its latest version: a binary to the
// latest GitHub release or Azure Artifacts package version, a script to the remote default
// branch (see upgradeScript). Local extensions are skipped, and pinned ones unless o.Force.
// The recorded source must still pass extension_sources; checksums and the confirmation
// work as for install. Token consent given before is kept; an extension that requests the
// token without it is asked again.
func Upgrade(ctx context.Context, f *cmdutil.Factory, o UpgradeOptions) (UpgradeResult, error) {
	ext, err := Lookup(o.Root, o.Name)
	if err != nil {
		return UpgradeResult{}, err
	}
	if ext.Kind == KindLocal {
		return UpgradeResult{Skipped: "local extension, nothing to upgrade"}, nil
	}
	rec, err := ReadRecord(o.Root, o.Name)
	if err != nil {
		return UpgradeResult{}, err
	}
	res := UpgradeResult{From: cmp.Or(rec.Version, rec.Tag, rec.Commit)}
	if rec.Pinned && !o.Force {
		res.Skipped = "pinned to " + shortRef(res.From) + "; use --force to upgrade"
		return res, nil
	}
	src, err := ParseSource(rec.Source)
	if rec.Source == "" || err != nil || src.IsLocal() {
		return res, fmt.Errorf("extension %q has no install record to upgrade from; reinstall it with --force", o.Name)
	}
	trust := o.Trust
	trust.Yes = trust.Yes || o.DryRun // a dry run never reaches the confirmation
	trust.AllowToken = trust.AllowToken || rec.TokenConsent
	if err := trust.Check(f.IOStreams); err != nil {
		return res, err
	}
	if err := CheckSource(ctx, f, src); err != nil {
		return res, err
	}
	resolved := func(ref string) error {
		res.To = ref
		if ref == res.From && !o.Force {
			return errUpToDate
		}
		if o.DryRun {
			return errDryRun
		}
		return nil
	}
	reinstall := func() error {
		_, err := InstallScript(ctx, f, ScriptOptions{Root: o.Root, Name: o.Name, Source: src, URL: rec.Source,
			Force: true, Trust: trust, goos: o.goos, goarch: o.goarch, runTool: o.runTool, resolved: resolved})
		return err
	}
	switch {
	case ext.Kind == KindScript:
		if err = upgradeScript(ctx, f, o, rec, src, trust, &res, resolved); errors.Is(err, errReinstall) {
			err = reinstall()
		}
	case rec.Host == hostGitHub:
		_, err = InstallGitHubBinary(ctx, f, BinaryOptions{Root: o.Root, Name: o.Name, Source: src, Force: true,
			Trust: trust, GOOS: o.goos, GOARCH: o.goarch, resolved: resolved})
	default: // an Azure Artifacts binary: its manifest is read from a fresh clone
		err = reinstall()
	}
	res.From, res.To = shortRef(res.From), shortRef(res.To)
	switch {
	case errors.Is(err, errUpToDate):
		res.Skipped = "already up to date"
		return res, nil
	case errors.Is(err, errDryRun):
		return res, nil
	}
	return res, err
}

// upgradeScript upgrades the script clone of o.Name in place, with the install's git env
// (token for a private Azure DevOps repo) on every call: `git fetch origin HEAD`, which
// follows a renamed default branch, then `git merge --ff-only FETCH_HEAD`, or with o.Force
// `git reset --hard FETCH_HEAD`. The new version is confirmed (with its manifest's token
// request) before the merge. A dry run compares `git ls-remote origin HEAD` with HEAD. A new
// version whose manifest names an Azure Artifacts package is errReinstall.
func upgradeScript(ctx context.Context, f *cmdutil.Factory, o UpgradeOptions, rec *Record, src Source, trust Trust,
	res *UpgradeResult, resolved func(string) error) error {
	env, err := scriptEnv(ctx, f, ScriptOptions{Source: src, URL: rec.Source})
	if err != nil {
		return err
	}
	g := *f.GitClient
	g.Dir, g.Env = filepath.Join(o.Root, Prefix+o.Name), env
	out, err := g.Exec(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	res.From = strings.TrimSpace(string(out))
	if o.DryRun {
		out, err := g.Exec(ctx, "ls-remote", "origin", "HEAD")
		if err != nil {
			return fmt.Errorf("checking %s: %w", rec.Source, err)
		}
		remote, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
		if remote == "" {
			return fmt.Errorf("checking %s: the remote has no HEAD", rec.Source)
		}
		return resolved(remote)
	}
	if _, err := g.Exec(ctx, "fetch", "--quiet", "--no-recurse-submodules", "origin", "HEAD"); err != nil {
		return fmt.Errorf("fetching %s: %w", rec.Source, err)
	}
	out, err = g.Exec(ctx, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	next := strings.TrimSpace(string(out))
	if err := resolved(next); err != nil {
		return err
	}
	// git fails for a missing file; that, like any other failure here, reads as no manifest.
	data, _ := g.Exec(ctx, "show", "FETCH_HEAD:"+ManifestFile)
	m, err := ParseManifest(data, o.Name)
	if err != nil {
		return err
	}
	if m.Binaries.AzureArtifacts != nil {
		return errReinstall
	}
	consent, err := Confirm(f, Summary{Name: o.Name, Description: m.Description, Source: rec.Source,
		Ref: next + " (default branch)", Kind: KindScript, Checksum: "not applicable (git)", Token: m.Token}, trust)
	if err != nil {
		return err
	}
	if o.Force {
		_, err = g.Exec(ctx, "reset", "--quiet", "--hard", "FETCH_HEAD")
	} else if _, err = g.Exec(ctx, "merge", "--quiet", "--ff-only", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("cannot fast-forward %s to the remote default branch (%w); use --force to reset it to the remote, discarding local changes",
			Prefix+o.Name, err)
	}
	if err != nil {
		return err
	}
	rec.Commit, rec.Tag, rec.Pinned, rec.TokenConsent = next, "", false, consent
	return WriteRecord(o.Root, o.Name, rec)
}

// shortRef shortens a full commit SHA to 8 characters, as `ado extension list` shows it.
func shortRef(ref string) string {
	if len(ref) == 40 && strings.Trim(ref, "0123456789abcdef") == "" {
		return ref[:8]
	}
	return ref
}
