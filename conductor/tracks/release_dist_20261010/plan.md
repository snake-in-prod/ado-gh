# Plan: Release Engineering and Distribution

Track: `release_dist_20261010` · Spec: [spec.md](./spec.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers). Workflow/config tasks are verified by snapshot builds, actionlint and dry runs instead of unit tests.

---

## Phase 1: Build Metadata, `ado version`, Update Notifier

- [ ] Task: Record new dependencies and tools
    - [ ] Add `golang.org/x/mod/semver`, `spf13/cobra/doc`, and CI tools (GoReleaser, syft, cosign, git-cliff, MkDocs Material, shellcheck, PSScriptAnalyzer, actionlint) to `tech-stack.md` with dated note
- [ ] Task: Build info (`internal/build`)
    - [ ] Write tests: ldflags values win; `debug.ReadBuildInfo` fallback (module version, revision, time, dirty); `DEV` for local builds
    - [ ] Implement
- [ ] Task: `ado version` / `--version`
    - [ ] Write tests: three-line output with release URL, dev build omits URL, `--json` fields, `--version` and `version` identical
    - [ ] Implement (extends MVP `--version`)
- [ ] Task: Install-method detection
    - [ ] Write tests (table-driven paths per OS): Homebrew Cellar/Caskroom, Scoop, WinGet packages/links, GOPATH/bin, install-script bin dir, unknown → release URL
    - [ ] Implement
- [ ] Task: Update notifier
    - [ ] Write tests (fake clock, httpmock, temp state dir): once per 24 h, state file round-trip, SemVer newer-only, stable only, disabled by `ADO_NO_UPDATE_NOTIFIER`/CI vars/non-TTY/dev build/extension, 2 s timeout never blocks longer, errors silent (debug only), message per install method, no ADO token sent
    - [ ] Implement and wire into root command
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Build Metadata, ado version, Update Notifier' (Protocol in workflow.md)

## Phase 2: Reference Docs and Docs Site

- [ ] Task: `ado help reference`
    - [ ] Write tests: Markdown to stdout covers every visible command, hidden/aliases/extensions excluded, `--dir` writes one page per command, `--man` writes section-1 pages, two runs byte-identical (no autogen date)
    - [ ] Implement with `cobra/doc`
- [ ] Task: Docs site skeleton
    - [ ] `mkdocs.yml`, `docs/requirements.txt` (pinned), nav: install, getting started, auth, gh differences, extensions, releasing, reference
    - [ ] Gitignore `docs/reference/`; local build instructions
- [ ] Task: Docs workflows
    - [ ] PR CI: build `ado`, generate reference, `mkdocs build --strict`
    - [ ] `docs.yml`: on stable release published + manual dispatch → generate, build, copy install scripts to site root, deploy to Pages
    - [ ] actionlint passes
- [ ] Task: Conductor - User Manual Verification 'Phase 2: Reference Docs and Docs Site' (Protocol in workflow.md)

## Phase 3: GoReleaser and Changelog

- [ ] Task: `.goreleaser.yaml` builds and archives
    - [ ] 6 targets, `-trimpath`, `CGO_ENABLED=0`, ldflags, `mod_timestamp`; before-hooks generate manpages and completions; archives with LICENSE/README/man/completions
    - [ ] Verify: `goreleaser check`, `goreleaser release --snapshot --clean`, archive contents listed
- [ ] Task: Checksums, SBOM, signing config
    - [ ] `checksums.txt`, syft SPDX SBOM per archive, cosign keyless `sign-blob` bundle, GPG detached signature
    - [ ] Verify snapshot produces SBOMs (signing skipped in snapshot)
- [ ] Task: Packages and package managers
    - [ ] nFPM deb/rpm with man/completions; `homebrew_casks`, `scoops`, `winget` with `skip_upload: auto` for prereleases
    - [ ] Verify snapshot emits deb/rpm, cask, Scoop and winget manifests; `dpkg -c` / `rpm -qlp` contents check
- [ ] Task: Reproducibility check
    - [ ] CI job: two snapshot builds of same commit → identical linux binaries (sha256 compare)
- [ ] Task: Changelog with git-cliff
    - [ ] `cliff.toml` groups/exclusions per spec; generate `CHANGELOG.md`; notes-for-tag command documented
    - [ ] Verify against sample history (feat, fix, `!` breaking, excluded types)
- [ ] Task: PR CI snapshot job
    - [ ] Add `goreleaser check` + snapshot build to CI; actionlint passes
- [ ] Task: Conductor - User Manual Verification 'Phase 3: GoReleaser and Changelog' (Protocol in workflow.md)

## Phase 4: Release Workflow and Install Scripts

- [ ] Task: One-time external setup (manual, maintainer)
    - [ ] Create `<owner>/homebrew-tap`, `<owner>/scoop-bucket`, fork `microsoft/winget-pkgs`; fine-grained `PACKAGES_PAT`; GPG key + publish to keys.openpgp.org; `release` environment with secrets; enable Pages
- [ ] Task: `.github/workflows/release.yml`
    - [ ] Tag guard (strict SemVer, commit on `main`), least-privilege permissions, pinned action SHAs, test gate, tool installs, GPG import + cleanup, git-cliff notes, GoReleaser release, attest-build-provenance, dispatch smoke workflow
    - [ ] Verify: actionlint; guard step unit-tested via script tests (valid/invalid tags)
- [ ] Task: Install scripts
    - [ ] Write tests: shell test harness (local HTTP fixture server) for `install.sh` — OS/arch detection, `--version`, `--bin-dir`, checksum mismatch aborts, optional cosign verify; Pester tests for `install.ps1` equivalents
    - [ ] Implement `scripts/install.sh`, `scripts/install.ps1`; shellcheck + PSScriptAnalyzer in CI
- [ ] Task: Conductor - User Manual Verification 'Phase 4: Release Workflow and Install Scripts' (Protocol in workflow.md)

## Phase 5: Smoke Tests, README, Release Checklist, First Release

- [ ] Task: `.github/workflows/smoke.yml`
    - [ ] Binary matrix (6 runners): download, verify checksum/cosign/GPG/attestation, `--version` matches tag+commit, `--help`, unauthenticated `auth status` exits 4, optional authenticated checks with `SMOKE_ADO_TOKEN`
    - [ ] Channel jobs (stable only): brew cask, Scoop, deb, rpm (fedora container), install.sh, install.ps1, `go install`
    - [ ] actionlint passes
- [ ] Task: README install section
    - [ ] Every channel install/upgrade/uninstall, completions, verification steps incl. GPG fingerprint, docs site link
- [ ] Task: Release checklist
    - [ ] Write `docs/releasing.md` per spec; update `conductor/workflow.md` Release Workflow to reference it
- [ ] Task: Dry-run release
    - [ ] Tag `v0.1.0-rc.1`: confirm prerelease assets, signatures, attestations, notes, no package-manager updates, binary smoke green
    - [ ] Fix issues found; record outcome in plan
- [ ] Task: Conductor - User Manual Verification 'Phase 5: Smoke Tests, README, Release Checklist, First Release' (Protocol in workflow.md)
