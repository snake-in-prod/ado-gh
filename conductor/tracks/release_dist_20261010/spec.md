# Spec: Release Engineering and Distribution

## Overview
Ship `ado` as a signed, reproducible, single static binary for windows/darwin/linux × amd64/arm64. Publish to GitHub Releases, a Homebrew tap, a Scoop bucket, winget, `.deb`/`.rpm`, install scripts and `go install`. Generate the changelog from Conventional Commits. Enrich `ado --version`, add an opt-out update notifier, generate command reference docs and manpages from the cobra tree, publish a docs site, and document install, release and verification. Extends the basic `--version` and CI from `mvp_core_20261010`.

## User Stories
1. As a user, I install with `brew`, `scoop`, `winget`, `apt`/`dnf` from a downloaded package, `curl | sh`, or `go install`, and can verify what I installed.
2. As a user, `ado` tells me at most once a day when a newer release exists and how to upgrade with the tool I installed it with. I can turn that off.
3. As a maintainer, I push `vX.Y.Z` and get tested, signed binaries, packages, release notes, package-manager updates, a docs site and a smoke-test report without manual steps (winget moderation aside).

## Functional Requirements

### FR1: Build metadata and `ado version`
- `internal/build` holds `Version`, `Commit`, `Date`, set via ldflags (`-s -w -X …`), built with `-trimpath`, `CGO_ENABLED=0`, and `mod_timestamp` = commit time (reproducible builds).
- Fallback for `go install` builds: `debug.ReadBuildInfo()` (module version, `vcs.revision`, `vcs.time`, `vcs.modified`). Local builds report `DEV`.
- `ado --version` and `ado version`:
  ```
  ado version 1.2.3 (2026-10-10)
  commit abc1234 · go1.x · linux/amd64
  https://github.com/<owner>/ado-gh/releases/tag/v1.2.3
  ```
- `ado version --json` fields: `version,commit,date,dirty,goVersion,os,arch`.

### FR2: Update notifier
- Runs in a background goroutine with a 2 s network timeout and prints to stderr after the command finishes. It never delays a command beyond that timeout, and failures are silent (visible only with `--debug`).
- Checks GitHub `releases/latest` (stable only) at most once per 24 h. State lives in `$XDG_STATE_HOME/ado/state.yml` (`%LocalAppData%\ado` on Windows): last check time and latest version/URL. Only notifies when the latest release is a newer SemVer.
- Disabled when: `ADO_NO_UPDATE_NOTIFIER` is set, `CI`/`TF_BUILD`/`BUILD_NUMBER`/`RUN_ID`/`GITHUB_ACTIONS` is set, stdout or stderr is not a TTY, it's a dev build, or it's running inside an extension (`ADO_EXTENSION_NAME`).
- Message: `A new release of ado is available: 1.2.3 → 1.3.0`, then an upgrade command matched to how `ado` was installed (detected from the resolved executable path plus build info: `go install` builds are recognised by `Main.Sum`, so a custom GOBIN works):
  - Homebrew: `brew upgrade --cask ado` (Caskroom path); `brew upgrade ado` for a formula install (Cellar path)
  - Scoop: `scoop update ado`
  - winget: `winget upgrade <Owner>.Ado`
  - `go install`: `go install …@latest`
  - install script: re-run command
  - anything else: release URL.
- Never sends the ADO token. GitHub calls send `GH_TOKEN`/`GITHUB_TOKEN` as a Bearer token only to api.github.com when one is set, otherwise they are unauthenticated.

### FR3: Command reference and manpages
- `ado help reference`: prints the full command reference as Markdown to stdout (gh parity).
  - `--dir <path>` writes one Markdown page per command (MkDocs layout).
  - `--man <dir>` writes section-1 manpages.
  - Built on `cobra/doc` with the auto-gen date tag off, so output is deterministic.
  - Hidden commands, aliases and extensions are excluded.
- The release build generates manpages and shell completions (bash, zsh, fish, PowerShell) into archives, packages and the Homebrew cask.

### FR4: Docs site
- MkDocs Material site from `docs/`: install, getting started, auth, gh differences, extensions, releasing/verification, and the generated `reference/`. Python deps pinned in `docs/requirements.txt`.
- `docs.yml` workflow: on each published stable release (and manual dispatch) it builds `ado`, runs `ado help reference --dir docs/reference`, runs `mkdocs build --strict`, and deploys to GitHub Pages. Generated reference pages are gitignored. `install.sh` / `install.ps1` are copied to the site root for stable URLs.
- PR CI runs `mkdocs build --strict` (broken links fail the build).

### FR5: GoReleaser (`.goreleaser.yaml`, v2)
- **Builds:** linux/darwin/windows × amd64/arm64.
- **Archives:** `ado_<version>_<os>_<arch>` as tar.gz (zip on Windows), with LICENSE, README, manpages and completions.
- **Integrity:** `checksums.txt` (SHA-256). SPDX SBOM per archive via syft.
- **Signing:** cosign keyless `sign-blob` on `checksums.txt` (`checksums.txt.sigstore.json` bundle), plus a GPG detached signature `checksums.txt.sig`.
- **Packages:** nFPM `.deb` / `.rpm` (amd64/arm64) installing `/usr/bin/ado`, manpages and completions.
- **Homebrew:** `homebrew_casks` → `<owner>/homebrew-tap` (binary, manpages, completions).
- **Scoop:** `scoops` → `<owner>/scoop-bucket`.
- **winget:** `winget` → PR to `microsoft/winget-pkgs` from `<owner>/winget-pkgs` (package `<Owner>.Ado`).
- **Prereleases:** `prerelease: auto`. Prerelease tags (`-rc.N`) publish to GitHub Releases only (no tap/bucket/winget).
- **Release notes:** from git-cliff (FR6), passed to GoReleaser (its own changelog disabled).
- PR CI runs `goreleaser check` and `goreleaser release --snapshot --clean` so config breakage shows up before release.

### FR6: Changelog from Conventional Commits
- `cliff.toml` (git-cliff):
  - Groups: ⚠ Breaking Changes (`!` / `BREAKING CHANGE`), Features, Bug Fixes, Performance, Documentation.
  - Excluded types: `chore`, `test`, `style`, `ci`, `conductor`.
- `CHANGELOG.md` is regenerated in a release-prep PR before tagging. The release workflow renders the notes for the tag from the same config, so the file and the release page always match.

### FR7: Release workflow (`.github/workflows/release.yml`)
- **Trigger:** tags `v*`. A guard step rejects tags that aren't strict SemVer and tags whose commit isn't on `main`.
- **Permissions:** `contents: write`, `id-token: write`, `attestations: write`. Secrets come from a `release` GitHub environment: `PACKAGES_PAT` (fine-grained: tap, bucket, winget fork), `GPG_PRIVATE_KEY`, `GPG_PASSPHRASE`.
- **Steps:**
  1. Checkout with full history, `setup-go`.
  2. Gate: `go test ./... -race`.
  3. Install syft, cosign and git-cliff; import the GPG key.
  4. Render release notes.
  5. `goreleaser release --clean`.
  6. `actions/attest-build-provenance` for archives, packages and checksums.
  7. Trigger the smoke workflow (FR9).
- No secret value is ever printed. GPG key material is cleaned up at job end.

### FR8: Install scripts
- `scripts/install.sh` (POSIX sh) and `scripts/install.ps1`:
  - Detect OS/arch. `--version` (default latest) and `--bin-dir` (default `~/.local/bin`; on Windows `%LocalAppData%\Programs\ado`, added to the user PATH).
  - Download the archive and `checksums.txt` and verify SHA-256 (`sha256sum`/`shasum`/`Get-FileHash`). Also verify the cosign bundle when `cosign` is installed.
  - Install without sudo and print a PATH hint.
- Linted in CI (shellcheck, PSScriptAnalyzer).

### FR9: Smoke tests (`.github/workflows/smoke.yml`)
- Runs after release and by manual dispatch (`version` input).
- **Binary matrix:** ubuntu x64/arm64, macos arm64/x64, windows x64/arm64. Each job:
  - Downloads the release archive.
  - Verifies checksum, cosign bundle (`--certificate-identity` = release workflow, issuer = GitHub OIDC), GPG signature and `gh attestation verify`.
  - Asserts `ado --version` matches the tag and commit, `ado --help` exits 0, and `ado auth status` without credentials exits 4 with the login hint.
  - If secret `SMOKE_ADO_TOKEN` is set, also runs `ado api _apis/projects` and `ado pr list -R <test repo>`.
- **Channel jobs (stable only):** `brew install --cask <owner>/tap/ado` (macOS), Scoop (Windows), `.deb` (ubuntu), `.rpm` (fedora container), `install.sh`, `install.ps1`, `go install …@<tag>`. Each asserts `ado --version`. winget is checked by hand (moderation queue).

### FR10: README and release checklist
- README "Install" covers every channel, plus upgrade and uninstall per channel, shell completions, verifying downloads (checksum, cosign, GPG fingerprint, `gh attestation verify`), and a link to the docs site.
- `docs/releasing.md`:
  - One-time setup: create tap/bucket repos, fork winget-pkgs, PAT scopes and expiry reminder, GPG key published to keys.openpgp.org with its fingerprint in README, enable Pages, create the `release` environment.
  - SemVer decision rules (pre-1.0 too).
  - Release-prep PR (git-cliff), annotated tag, monitoring the workflow, checking smoke results, following the winget PR.
  - Prerelease flow. Hotfix flow. Rollback policy: never delete or move tags; release a patch instead.
- `conductor/workflow.md` "Release Workflow" section updated to point to it.

## Non-Functional Requirements
- **Reproducible builds:** two snapshot builds of the same commit produce identical binaries (checked in CI on linux).
- **Security:** keyless signing (no long-lived signing key except GPG, which lives in the environment secret), least-privilege workflow permissions, pinned action SHAs, no tokens in logs.
- **Dependencies:** Go: `golang.org/x/mod/semver`, `spf13/cobra/doc` (+ `go-md2man`). CI-only tools: GoReleaser, syft, cosign, git-cliff, MkDocs Material, shellcheck, PSScriptAnalyzer. All recorded in `tech-stack.md`.
- **Testing:** >90% coverage for Go code (version, notifier with fake clock/HTTP/state dir, install-method detection, reference generator determinism and coverage of all commands). Workflows are validated with snapshot builds and actionlint.

## Acceptance Criteria
- [ ] `goreleaser release --snapshot --clean` produces 6 archives, deb/rpm, checksums, SBOMs, a cask, a Scoop manifest and a winget manifest locally.
- [ ] Pushing `v0.1.0-rc.1` creates a GitHub prerelease with signed checksums (cosign + GPG), attestations, SBOMs and notes, and does not touch tap/bucket/winget. Binary smoke passes on all 6 platforms.
- [ ] Pushing `v0.1.0` also updates the tap and bucket, opens the winget PR, deploys the docs site, and passes channel smoke jobs.
- [ ] `ado --version` shows version, date, commit and URL. `go install` builds show the module version.
- [ ] The notifier appears at most once per day, shows the right upgrade command per channel, and stays silent in CI, when piped, or with `ADO_NO_UPDATE_NOTIFIER=1`.
- [ ] `ado help reference` prints Markdown. `--dir`/`--man` generate a page per command, deterministically.
- [ ] README install and verification sections and `docs/releasing.md` exist. Tags off `main` or not SemVer are rejected.

## Out of Scope
- Windows Authenticode signing, macOS notarization.
- Container images, hosted apt/yum repositories, Chocolatey, Snap/Flatpak/AUR, homebrew-core submission.
- Self-update (`ado upgrade`).
- Versioned docs (only the latest release is published).
