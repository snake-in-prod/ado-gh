# Plan: Release Engineering and Distribution

Track: `release_dist_20261010` · Spec: [spec.md](./spec.md) · Research: [research.md](./research.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers). Workflow/config tasks are verified by snapshot builds, actionlint and dry runs instead of unit tests. Plan revised 2026-10-12 from research.md (changes c1–c23, decisions o1–o43).

---

## Phase 1: Build Metadata, `ado version`, Update Notifier

- [ ] Task: Record new dependencies and tools
    - [ ] Add to `tech-stack.md` with a dated note (2026-10-12) and pinned versions: GoReleaser v2.18.3 (exact, upgrade deliberately), cosign v3.1.3 via cosign-installer v4.1.2 (`cosign-release: v3.1.3`), actions/attest v4.2.2, syft via anchore/sbom-action/download-syft v0.24.3, git-cliff v2.14.2, MkDocs 1.6.1 + Material 9.7.7 (plugin-free; Material EOL 2026-11-05, move to Zensical later), actionlint v1.7.12, shellcheck, PSScriptAnalyzer, Pester 5, python3 `http.server` for script tests, Go toolchain go1.27.2, pinned runner labels
    - [ ] Add Go deps: `golang.org/x/mod/semver`, `spf13/cobra/doc` (go-md2man v2.0.6, blackfriday v2.1.0)
- [ ] Task: Build info (`internal/build`)
    - [ ] Write tests (`readBuildInfo` test seam): ldflags values win; `!ok` → DEV; any `vcs.*` setting present → DEV (incl. a local tagged clean or dirty build); `Main.Version` `(devel)`/empty → DEV; module-cache install (`go install …@vX.Y.Z`, no `vcs.*`) → `Main.Version`; pseudo-version → Commit and Date parsed with a small regex; Dirty always from `vcs.modified`; empty revision/time allowed for `go install`
    - [ ] Implement; remove `main.version`, `main.go` passes `build.Version` to `NewFactory`; fix `cmd/ado/main_test.go:23-25,336` and `root_test.go:38-43` in the same task
- [ ] Task: `ado version` / `--version`
    - [ ] Write tests: three-line output with release URL, commit line omitted when Commit is empty, dev build omits URL, `--json` fields, `--version` and `version` byte-identical; tighten the `strings.Contains` assertion in `TestRun`
    - [ ] Implement: `SetVersionTemplate` renders the same three lines as `ado version`; one repo-URL constant shared with `pkg/extension/create.go:29` `docsURL`
- [ ] Task: Install-method detection
    - [ ] Write tests for pure `detectInstallMethod(exe, goos, getenv, buildInfo)` (table-driven, runs on Linux CI for every OS; `\`→`/` and lowercase normalization): Homebrew Caskroom segment and `/Cellar/ado/` (both → `brew upgrade ado`), Scoop user and global, WinGet Packages (user/machine) plus Links fallback, `go install` from `Main.Sum` after checking ldflags values (custom GOBIN), `~/.local/bin` and `%LocalAppData%/Programs/ado` (install script), `/usr/bin` → release URL, unknown → release URL
    - [ ] Implement; `EvalSymlinks` happens in the notifier, not in `Factory.Executable`
    - [ ] Update spec FR2 (line 28): detection is path plus build info (go install from `Main.Sum`), and Homebrew matches Caskroom and Cellar
- [ ] Task: Update notifier
    - [ ] Write tests (fake clock, fast fake server, temp state dir): once per 24 h; `config.StateDir()` table test next to `TestDataDir`; atomic state-file round-trip; `checked_for_update_at` written only after a successful check (failures and rate limits retry next command); `v`-prefix before `semver.Compare`, newer-only; stable only (`-rc` never offered); notify as soon as releases/latest changes (no per-channel grace); disabled by `ADO_NO_UPDATE_NOTIFIER`, CI vars incl. explicit `TF_BUILD`, non-TTY, DEV build (explicit skip), and `ADO_EXTENSION_NAME` (an ado started inside an extension); parent ado still prints after dispatching an extension or alias; notice printed only when the command succeeded; 2 s timeout never blocks longer; errors silent (debug only); message per install method; uses `pkg/extension` `GitHubClient`: `GH_TOKEN`/`GITHUB_TOKEN` sent as Bearer only to api.github.com when set, otherwise unauthenticated, ADO token never sent
    - [ ] Implement and wire into `run()` in `cmd/ado/main.go` (covers errors, aliases and extensions): start before `ExecuteC` with a 2 s `context.WithTimeout` and buffered channel, read `f.Debug` after `ExecuteC`, print after `HandleError` only on success
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Build Metadata, ado version, Update Notifier' (Protocol in workflow.md)

## Phase 2: Reference Docs and Docs Site

- [ ] Task: `ado help reference`
    - [ ] Write tests: stdout is one gh-style Markdown document (custom walker, raw always, also on a TTY) covering every visible command; hidden commands, aliases and extensions excluded; `--dir <path>` writes one page per command plus `reference/index.md`; `--man <dir>` writes section-1 pages with a fixed `GenManHeader` (`.TH` date = Phase 1 build commit date, fixed Source and Manual); two runs byte-identical incl. with `ADO_DEBUG` set (no autogen tag, fixed `--debug` DefValue); `help reference` always wins over an `ado-reference` PATH or installed extension (an extension named `reference` stays allowed); "Unknown help topic" kept
    - [ ] Implement: custom help via `SetHelpCommand` with a `reference` child, group `additional`; custom walker for stdout, `cobra/doc` for `--dir`/`--man`; `DisableAutoGenTag` on every command; fix the `help <name>` rewrite in `pathExtensionArgs` (`cmd/ado/main.go:64-71`); `go mod tidy`
- [ ] Task: Docs site skeleton
    - [ ] `mkdocs.yml` (plugin-free; `validation:` block with anchors and unrecognized_links warn, absolute_links relative_to_docs; `not_in_nav: reference/*`; nav: install, getting started, auth, gh differences, extensions, releasing, Reference → `reference/index.md`); `docs/requirements.txt` with two exact pins `mkdocs==1.6.1`, `mkdocs-material==9.7.7`
    - [ ] Create `docs/index.md`, `docs/install.md`, `docs/getting-started.md`, `docs/auth.md` and a `docs/releasing.md` stub; move auth and getting-started content out of README into the docs pages (README keeps a summary and link, Phase 5); keep `docs/extensions.md` in place
    - [ ] Gitignore `docs/reference/` and `/site/`; document generating the reference before a local `mkdocs build --strict`
- [ ] Task: Docs workflows
    - [ ] PR CI: build `ado`, generate reference, `mkdocs build --strict` with `NO_MKDOCS_2_WARNING=true`
    - [ ] `docs.yml`: `workflow_call` (called from release.yml for stable tags) plus `workflow_dispatch`; build job (generate, build, copy install scripts to site root) and deploy job on SHA-pinned upload-pages-artifact v5.0.0 and deploy-pages v5.0.1; `pages: write` + `id-token: write` on deploy only; `github-pages` environment inside docs.yml; concurrency group
    - [ ] Update spec FR4: docs deploy is a reusable workflow called from release.yml plus manual dispatch, not `release: published` (GITHUB_TOKEN-created releases don't trigger workflows)
    - [ ] actionlint passes
- [ ] Task: Conductor - User Manual Verification 'Phase 2: Reference Docs and Docs Site' (Protocol in workflow.md)

## Phase 3: GoReleaser and Changelog

- [ ] Task: Pin the Go toolchain
    - [ ] Add `toolchain go1.27.2` to `go.mod` (keep `go 1.25.8` minimum); setup-go `go-version-file: go.mod` and `GOTOOLCHAIN=local` in snapshot, reproducibility and release jobs
- [ ] Task: `.goreleaser.yaml` builds and archives
    - [ ] `version: 2`; 6 targets, `CGO_ENABLED=0`, `-trimpath`, ldflags `-s -w -X …/internal/build.Version={{.Version}} …Commit={{.FullCommit}} …Date={{.CommitDate}}`, `mod_timestamp: "{{ .CommitTimestamp }}"`; `gomod.proxy` off; before-hooks generate manpages and completions; gitignore `/manpages/` and `/completions/`; archives with LICENSE/README/man/completions; `report_sizes: true`
    - [ ] Verify: `goreleaser check`, `goreleaser release --snapshot --clean --skip=sign`, archive contents listed, `ado version` from a snapshot binary not dirty
- [ ] Task: Checksums, SBOM, signing config
    - [ ] `checksums.txt`; `sboms: [{artifacts: archive}]` (syft SPDX per archive); two `signs` entries on `artifacts: checksum`: id `cosign` (`sign-blob --bundle=${signature} ${artifact} --yes`, signature `${artifact}.sigstore.json`) and id `gpg` (`-u {{ .Env.GPG_FINGERPRINT }} --output ${signature} --detach-sign ${artifact}`, key fed via `signs.stdin` from the temporary GNUPGHOME setup)
    - [ ] Verify snapshot with `--skip=sign` produces SBOMs (snapshot does not skip signing by itself)
- [ ] Task: Packages and package managers
    - [ ] Homebrew cask token `ado` in `snake-in-prod/homebrew-tap`: `binaries`, `manpages`, static bash/zsh/fish completions plus `generate_completions_from_executable` for pwsh, `hooks.post.install` xattr quarantine removal in `if OS.mac?` plus `caveats`, `skip_upload: auto`; no Linuxbrew channel
    - [ ] Scoop (`snake-in-prod/scoop-bucket`, empty `directory`, zip, `skip_upload: auto`); winget `package_identifier: SnakeInProd.Ado` with `publisher`, `license`, `short_description`, `commit_msg_template`, fork `repository` `snake-in-prod/winget-pkgs`, `pull_request.base: microsoft/winget-pkgs@master`, `pull_request.token` from `WINGET_PR_PAT`, `skip_upload: auto`
    - [ ] nFPM deb/rpm with man/completions contents and an explicit `file_name_template` (no `~`)
    - [ ] Verify snapshot emits deb/rpm, cask, Scoop and winget manifests; `dpkg -c` / `rpm -qlp` contents check
- [ ] Task: Reproducibility check
    - [ ] CI job: second snapshot build of the same commit in a `git worktree` at another path with an empty GOCACHE; select all 6 binaries via `dist/artifacts.json`; diff sha256 lists; on mismatch diff version output/strings (not `go version -m`)
- [ ] Task: Changelog with git-cliff
    - [ ] `cliff.toml`: groups per spec, `docs(conductor)` scope rule, rc `ignore_tags` with stable-only `tag_pattern`, newline-safe preprocessor, `protect_breaking_commits`; ungrouped types (refactor, build, revert) dropped silently; generate `CHANGELOG.md`; prep commit convention `chore(release): prepare vX.Y.Z`; notes-for-tag command documented (stable `--current`, rc `--unreleased --tag "$TAG"`, version heading dropped from notes.md)
    - [ ] Verify against sample history (feat, fix, `!` breaking, excluded types, rc after a stable tag)
    - [ ] Update spec FR5: pass only `--release-notes`, never `changelog.disable`; notes body (no heading) matches the CHANGELOG.md section
- [ ] Task: PR CI snapshot job
    - [ ] `goreleaser check` + `goreleaser release --snapshot --clean --skip=sign` (GoReleaser v2.18.3, syft installed); assert cask, Scoop and winget manifests exist in `dist/`
    - [ ] Pin and bump ci.yml actions to SHAs (checkout v7.0.1, setup-go v7.0.0, golangci-lint-action v9.3.0); actionlint passes
- [ ] Task: Conductor - User Manual Verification 'Phase 3: GoReleaser and Changelog' (Protocol in workflow.md)

## Phase 4: Release Workflow and Install Scripts

- [ ] Task: One-time external setup (manual, maintainer)
    - [ ] Make GitHub canonical: push main and history to `snake-in-prod/ado-gh`; the Azure DevOps remote becomes a mirror or is dropped
    - [ ] Create `snake-in-prod/homebrew-tap`, `snake-in-prod/scoop-bucket`, fork `microsoft/winget-pkgs` as `snake-in-prod/winget-pkgs`; sign the Microsoft CLA; `winget search ado` to confirm `SnakeInProd.Ado` is free
    - [ ] Tokens: fine-grained `PACKAGES_PAT` (Contents read/write on tap, bucket, fork; optional Workflows on fork) plus classic `public_repo` `WINGET_PR_PAT` for `pull_request.token`
    - [ ] GPG: certify-only primary kept offline, signing subkey (1–2-year expiry) exported with `--export-secret-subkeys FPR!` as the only CI secret, offline revocation cert; publish to keys.openpgp.org (web upload, email verification)
    - [ ] Enable Pages (GitHub Actions); `release` environment with secrets; `v*` tag rules on `github-pages` and `release` environments plus a `v*` tag ruleset
    - [ ] Update spec line 71 (FR7 secrets): split `PACKAGES_PAT` / `WINGET_PR_PAT`, signing subkey only
- [ ] Task: `.github/workflows/release.yml`
    - [ ] Write tests for `scripts/release-guard.sh` (valid/invalid tag table): accepts `vX.Y.Z` and `vX.Y.Z-<prerelease>`, rejects `+build`, leading zeros and non-`v` tags; rejects commits not ancestors of `origin/main`
    - [ ] Implement guard script; `guard` job (no secrets) and `release` job (`needs: guard`, `environment: release`); `permissions: {}` at top, per-job `contents: write`, `id-token: write`, `attestations: write`; concurrency; `fetch-depth: 0`, `persist-credentials: false`; pinned action SHAs; test gate; setup-go `go-version-file` + `GOTOOLCHAIN=local`
    - [ ] Tools: GoReleaser v2.18.3, cosign-installer with `cosign-release: v3.1.3`, `download-syft`; GPG via plain `gpg --batch` import into a temporary GNUPGHOME, fed to GoReleaser via `signs.stdin`, `if: always()` cleanup step (no ghaction-import-gpg)
    - [ ] Notes: git-cliff stable `--current` / rc `--unreleased --tag "$TAG"`; check the heading matches the tag and the body is non-empty, then drop the heading line from notes.md; GoReleaser `--release-notes`
    - [ ] Provenance with `actions/attest@v4` (`subject-checksums: dist/checksums.txt` plus `subject-path: dist/checksums.txt`); upload the attestation bundle (`bundle-path` output) as a release asset
    - [ ] Call docs.yml (`if: ${{ !contains(github.ref_name, '-') }}`) and smoke.yml via `workflow_call`; add a smoke.yml stub so the call resolves
    - [ ] Update spec: provenance via `actions/attest@v4` (not attest-build-provenance), bundle uploaded as an asset; FR7 dispatches docs/smoke via `workflow_call`
    - [ ] Verify: actionlint
- [ ] Task: Install scripts
    - [ ] Write tests: `scripts/install_test.sh` (python3 `http.server` fixture, stubbed uname/sysctl, run under dash and `bash --posix`) for `install.sh` — OS/arch detection incl. Rosetta, `--version`, `--bin-dir`, checksum mismatch aborts, optional cosign verify, Git Bash/MSYS2/Cygwin exits with a message pointing to install.ps1; Pester 5 tests under powershell 5.1 and pwsh for `install.ps1` — `-Version`/`-BinDir` with `ADO_VERSION`/`ADO_INSTALL_DIR` env fallback, `-NoModifyPath` leaves HKCU untouched, checksum mismatch aborts, ARM64 arch from HKLM
    - [ ] Implement `scripts/install.sh`: functions with bare `main "$@"` last, `set -eu`, EXIT plus INT/TERM/HUP traps, uname mapping incl. Rosetta check, sha256sum/shasum/openssl fallback, https-only curl/wget, test-only base-URL override
    - [ ] Implement `scripts/install.ps1`: `-UseBasicParsing`, `$ProgressPreference`, Tls12 only when not SystemDefault, HKLM registry arch, `-NoModifyPath`, raw HKCU PATH write keeping REG_EXPAND_SZ plus `WM_SETTINGCHANGE`
    - [ ] CI: shellcheck in the lint job; PSScriptAnalyzer settings with `PSUseCompatibleSyntax` 5.1, fail on any result; Pester in CI only (locally only the sh harness runs)
- [ ] Task: Conductor - User Manual Verification 'Phase 4: Release Workflow and Install Scripts' (Protocol in workflow.md)

## Phase 5: Smoke Tests, README, Release Checklist, First Release

- [ ] Task: `.github/workflows/smoke.yml`
    - [ ] Triggers: `workflow_call` + `workflow_dispatch(version)`; update spec FR9 accordingly
    - [ ] Binary matrix pinned to `ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-26` (darwin/arm64), `macos-15-intel` (darwin/amd64, drop the row when GitHub retires Intel runners), `windows-2025`, `windows-11-arm`: download, verify checksum, cosign (SHA-pinned cosign-installer, exact identity), GPG (check or install gpg on Windows), attestation (`GH_TOKEN: ${{ github.token }}`, plus `--bundle` from the release asset); `--version` matches tag+commit, `--help`, unauthenticated `auth status` exits 4, optional authenticated checks with `SMOKE_ADO_TOKEN`
    - [ ] Channel jobs (stable only, x64 only, no winget, no Linuxbrew): brew on macOS `--cask --yes snake-in-prod/tap/ado` plus quarantine-gone assertion, failing on any Homebrew deprecation warning; Scoop via plain `irm get.scoop.sh | iex` and install in one step (or GITHUB_PATH); deb via `gh release download -p '*_linux_amd64.deb'` + `apt-get install ./`; rpm in a pinned Fedora container from the real asset name; install.sh; install.ps1 (env-var form with `-NoModifyPath`); `go install` with setup-go `cache: false`, no checkout, exact tag with 3× retry, default GOPROXY, version-only assertion
    - [ ] actionlint passes
- [ ] Task: README install section
    - [ ] New Install section after the intro (Build moves to Development), drop "Nothing is released yet"; every channel install/upgrade/uninstall (`brew install --cask snake-in-prod/tap/ado`, Scoop, `winget install SnakeInProd.Ado`, deb/rpm, install.sh, install.ps1 with both `-Version`/`-BinDir` scriptblock and env-var forms, `go install`), completions, docs site link
    - [ ] Verification: cosign ≥ 3.1.3 with both exact identity and anchored regex, `gh attestation verify --bundle` (no login) and the logged-in form (gh ≥ 2.93.0), GPG fingerprint, macOS quarantine note and security note for the xattr hook
    - [ ] README summarises auth and getting started with links to the docs pages; rewrite `docs/...` and `LICENSE` links where text moved into docs
- [ ] Task: Release checklist
    - [ ] Write `docs/releasing.md` per spec: git-cliff prep PR, push one tag at a time, winget PR check, CLA, Defender false positives and `@wingetbot run`, Intel macOS runner retirement (fall 2027), `[bump] breaking_always_bump_major = false` if `--bumped-version` is mentioned, user-facing fix subjects (no "address … review findings" commits)
    - [ ] Shrink `conductor/workflow.md` Release Workflow to a pointer to `docs/releasing.md` plus a short checklist; replace "CHANGELOG updated" with the git-cliff prep PR; reword the hotfix flow: merge hotfixes to main first and tag from main
- [ ] Task: Dry-run release
    - [ ] Tag `v0.1.0-rc.1`: confirm prerelease assets, signatures, attestations and bundle asset, non-empty notes, no package-manager updates, binary smoke green
    - [ ] Fix issues found; record outcome in plan
- [ ] Task: Conductor - User Manual Verification 'Phase 5: Smoke Tests, README, Release Checklist, First Release' (Protocol in workflow.md)
