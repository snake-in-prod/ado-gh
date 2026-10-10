# Project Tracks

This file tracks all major tracks for the project. Each track has its own detailed plan in its respective folder.

---

- [ ] **Track: Build the `ado` CLI foundation and core PR loop: scaffolding, PAT + Entra ID auth, git-remote context detection, `ado api`, `--json/--jq/--template` output, and `ado pr create/list/view/checkout/review/merge`**
  *Link: [./tracks/mvp_core_20261010/](./tracks/mvp_core_20261010/)*

---

- [ ] **Track: Complete the pull request command group to full gh parity: `ado pr diff/comment/close/reopen/edit/ready/status/checks`, `--json/--jq/--template` on read commands, confirmed destructive actions, updated `docs/gh-differences.md`**
*Link: [./tracks/pr_parity_20261010/](./tracks/pr_parity_20261010/)*

---

- [ ] **Track: Add repository commands and browsing with gh parity: `ado repo list/view/clone/create/delete/set-default`, top-level `ado browse` (repo, file:line, branch, commit, !PR, #work item, --no-browser), and `-w/--web` on every command with a web page**
*Link: [./tracks/repo_browse_20261010/](./tracks/repo_browse_20261010/)*

---

- [ ] **Track: Add work item and boards support (gh issue equivalent): `ado workitem` (aliases `wi`, `issue`) create/list/view/edit/close/reopen/comment/assign/link/unlink/delete, `ado workitem query` (WIQL + saved queries), `ado sprint` and `ado board` views, process-aware types/states, `--json/--jq/--template` on read commands**
*Link: [./tracks/workitems_20261010/](./tracks/workitems_20261010/)*

---

- [ ] **Track: Add pipeline commands mirroring gh run/workflow: `ado pipeline` (alias `run`) list/run/view/watch/logs/cancel/rerun/download/approve/reject/enable/disable for YAML and classic builds, default pipeline per repo, `ado release` group for Classic Release pipelines, `--json/--jq/--template` on read commands**
*Link: [./tracks/pipelines_20261010/](./tracks/pipelines_20261010/)*

---

- [ ] **Track: Add user aliases and an extension system matching gh: `ado alias set/list/delete/import` ($1 placeholders, --shell, no shadowing, config.yml) and `ado extension install/list/upgrade/remove/exec/create/search/browse` (GitHub/ADO/local sources, script + GitHub Release + Azure Artifacts binaries, checksums, pinning, allowlist, token consent), `ado-<name>` dispatch with context env vars, author contract docs**
*Link: [./tracks/extensions_20261010/](./tracks/extensions_20261010/)*

---

- [ ] **Track: Set up release engineering and distribution: GoReleaser (6 targets, ldflags metadata, checksums, SBOM, cosign + GPG, attestations), tag-triggered release workflow, GitHub Releases + Homebrew tap + Scoop + winget + deb/rpm + install scripts + go install, git-cliff changelog, `ado --version` metadata, opt-out update notifier, `ado help reference` + manpages + docs site, README install docs, release checklist and smoke tests**
*Link: [./tracks/release_dist_20261010/](./tracks/release_dist_20261010/)*
