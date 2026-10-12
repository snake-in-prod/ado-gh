# Project Tracks

This file tracks all major tracks for the project. Each track has its own detailed plan in its respective folder.

---

- [x] **Track: Add user aliases and an extension system matching gh: `ado alias set/list/delete/import` ($1 placeholders, --shell, no shadowing, config.yml) and `ado extension install/list/upgrade/remove/exec/create/search/browse` (GitHub/ADO/local sources, script + GitHub Release + Azure Artifacts binaries, checksums, pinning, allowlist, token consent), `ado-<name>` dispatch with context env vars, author contract docs**
*Link: [./tracks/extensions_20261010/](./tracks/extensions_20261010/)*

---

- [ ] **Track: Set up release engineering and distribution: GoReleaser (6 targets, ldflags metadata, checksums, SBOM, cosign + GPG, attestations), tag-triggered release workflow, GitHub Releases + Homebrew tap + Scoop + winget + deb/rpm + install scripts + go install, git-cliff changelog, `ado --version` metadata, opt-out update notifier, `ado help reference` + manpages + docs site, README install docs, release checklist and smoke tests**
*Link: [./tracks/release_dist_20261010/](./tracks/release_dist_20261010/)*
