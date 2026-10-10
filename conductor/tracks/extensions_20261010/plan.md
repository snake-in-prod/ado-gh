# Plan: Aliases and Extensions — gh Parity

Track: `extensions_20261010` · Spec: [spec.md](./spec.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Aliases

- [ ] Task: Alias storage and expansion (`internal/config`, `pkg/cmd/alias/shared`)
    - [ ] Write tests: `aliases:` round-trip in `config.yml`, `!` prefix marks shell aliases, `$1..$n` substitution, unused args appended, missing args error, single expansion (no recursion)
    - [ ] Implement
- [ ] Task: Shell execution
    - [ ] Write tests (stubbed runner): `sh -c expansion -- args` on Unix; Windows picks `sh` → `pwsh` → `powershell -NoProfile`; PowerShell literal substitution escapes quotes; `alias_shell` override; exit code propagated
    - [ ] Implement; add `alias_shell` config key
- [ ] Task: `ado alias set/list/delete/import`
    - [ ] Write tests: built-in and built-in-alias shadowing refused, extension shadowing warns, non-shell expansion must start with a valid command, `--clobber`, stdin expansion, list TTY/TSV/JSON, `delete --all`, `import` YAML (file/stdin) with per-entry validation report
    - [ ] Implement commands
- [ ] Task: Root dispatch for aliases
    - [ ] Write tests: built-in > alias order, alias args forwarded, help shows aliases section, completion includes alias names
    - [ ] Implement in root command
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Aliases' (Protocol in workflow.md)

## Phase 2: Extension Core (dispatch, env contract, list/remove/exec)

- [ ] Task: Layout and manifests (`pkg/extension`)
    - [ ] Write tests: data dir per OS, author manifest `ado-extension.yml` parsing/validation, install record `.ado-install.yml` round-trip, `ado-` name rule, core-name collision refused
    - [ ] Implement
- [ ] Task: Dispatch and env contract
    - [ ] Write tests: order built-in > alias > installed > PATH; env vars set (`ADO_HOST/ORG/PROJECT/REPO`, `ADO_EXTENSION_NAME`, `ADO_EXECUTABLE`, TTY/color); unresolvable context → empty, not fatal; `-R` before name → `ADO_REPO`; args/stdio/exit code passthrough; `ADO_TOKEN` only with consent; Windows `.ps1`/`.cmd`/`sh` launching
    - [ ] Implement
- [ ] Task: `ado auth token` speed bump
    - [ ] Write tests: refuses when `ADO_EXTENSION_NAME` set without consent, allowed with consent, unaffected outside extensions
    - [ ] Implement
- [ ] Task: Help and completion
    - [ ] Write tests: `ado help` extensions section, completion lists extension names
    - [ ] Implement
- [ ] Task: `ado extension list / remove / exec` (alias `ext`)
    - [ ] Write tests: list columns, TSV, JSON fields; remove deletes dir (and local symlink only, not target); exec bypasses alias shadowing; unknown name errors
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 2: Extension Core' (Protocol in workflow.md)

## Phase 3: Install — Local, Script, Trust Controls

- [ ] Task: Source parsing and allowlist
    - [ ] Write tests: `owner/repo`, GitHub URL, ADO HTTPS/SSH/visualstudio.com URLs, local paths; `extension_sources` glob matching incl. `local`; refused sources error naming the allowlist
    - [ ] Implement; add `extension_sources` config key
- [ ] Task: Local install
    - [ ] Write tests: symlink created, Windows pointer-file fallback, dispatch follows link, `--force` replaces
    - [ ] Implement
- [ ] Task: Script install via git
    - [ ] Write tests (stubbed git): clone to data dir, executable `ado-<name>` required, `--pin` checks out tag/commit and records it, private ADO clone passes token via `GIT_CONFIG_*` env (never argv), ADO token never sent to GitHub, already-installed error without `--force`
    - [ ] Implement
- [ ] Task: Confirmation and token consent
    - [ ] Write tests: TTY summary (source, ref, kind, checksum status, token request), decline → exit 2, non-TTY requires `--yes`, token consent prompt default no, `--allow-token`, no consent → warning and record `tokenConsent: false`
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 3: Install — Local, Script, Trust Controls' (Protocol in workflow.md)

## Phase 4: Binary Install and Upgrade

- [ ] Task: GitHub Releases binaries
    - [ ] Write tests (httpmock): release by tag/latest, asset `ado-<name>-<os>-<arch>[.exe]` selection, missing platform lists available ones, `checksums.txt` SHA-256 verify, mismatch aborts, missing checksums needs `--allow-unverified`, `GH_TOKEN`/`GITHUB_TOKEN` used, executable bit set
    - [ ] Implement
- [ ] Task: Azure Artifacts binaries via ArtifactTool
    - [ ] Record ArtifactTool (runtime-downloaded external binary) in `tech-stack.md`
    - [ ] Write tests: ArtifactTool fetched from `_apis/clienttools/ArtifactTool/release` per OS/arch and cached, invoked (stubbed runner) with feed/package/version and token via env, version matches tag else latest, extracted files confined to install dir, checksum verify
    - [ ] Implement
- [ ] Task: `ado extension upgrade`
    - [ ] Write tests: binary → latest version, script → fast-forward default branch, local skipped, pinned skipped unless `--force`, `--dry-run` output, new token request re-prompts consent, allowlist re-checked, `--all` partial failure summary exit 1
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 4: Binary Install and Upgrade' (Protocol in workflow.md)

## Phase 5: `create`, `search`, `browse`, Docs

- [ ] Task: `ado extension create`
    - [ ] Write tests: script/go/other templates file sets, `ado-extension.yml` contents, GitHub workflow and `azure-pipelines.yml` present, `git init` + initial commit (stubbed), TTY prompts, non-TTY missing name error, next-steps output
    - [ ] Implement templates (`embed.FS`)
    - [ ] Smoke test: generated Go template compiles (`go build` in test with `-short` skip)
- [ ] Task: `ado extension search`
    - [ ] Write tests (httpmock): `topic:ado-extension` query composition, `--owner`, `--sort`, `--limit`, installed mark, TTY/TSV/JSON, `-w` URL
    - [ ] Implement
- [ ] Task: `ado extension browse`
    - [ ] Write tests: select prompt over results, README render, install/remove/open/back actions dispatch, non-TTY error
    - [ ] Implement
- [ ] Task: Docs
    - [ ] Write `docs/extensions.md` (author contract per spec)
    - [ ] Update `docs/gh-differences.md`
    - [ ] Verify help text/examples for `alias *` and `extension *`
- [ ] Task: Conductor - User Manual Verification 'Phase 5: create, search, browse, Docs' (Protocol in workflow.md)
