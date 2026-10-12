# Plan: Aliases and Extensions — gh Parity

Track: `extensions_20261010` · Spec: [spec.md](./spec.md) · Research: [research.md](./research.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Aliases [checkpoint: 095dbed]

- [x] Task: Alias storage and expansion (`internal/config`, `pkg/cmd/alias/shared`) `095dbed`
    - [x] Write tests: `aliases:` round-trip in `config.yml`, a hand-written nested `aliases:` map and an `extension_sources:` YAML list load (typed `Aliases map[string]string` and `ExtensionSources []string` fields next to `Hosts`; the inline string map can't decode them today); `alias_shell` in `shared.Options`; `DataDir()` next to `dir(goos)` (`XDG_DATA_HOME` → `LocalAppData` on Windows → `~/.local/share`, goos parameter); `!` prefix marks shell aliases; expansion is shlex-split first, then `$N` substituted per token, every unused arg appended (spec rule, o4): args with spaces, `$2`-only, `$10`, literal `$` (e.g. a jq filter); missing args error; single expansion validated against built-ins only (no recursion, no alias chaining)
    - [x] Implement; promote `github.com/google/shlex` to a direct dependency
- [x] Task: Shell execution `095dbed`
    - [x] Write tests (stubbed runner, `systemBrowser`-style `goos`/`getenv`/`lookPath`/`run` injection so the Windows order is tested on every OS): `sh -c expansion -- args` on Unix; Windows sh lookup as gh's findsh (PATH `sh`, `<git dir>\..\bin\sh.exe`, scoop path), then `pwsh`, then `powershell`, both via `-NoProfile -NonInteractive -EncodedCommand` (UTF-16LE base64) of `& { $1=$args[0]; $2=$args[1]; …; <expansion> } '<a1>' '<a2>' …; exit $LASTEXITCODE` with index-assigned variables (one-arg case) and all five single-quote characters (U+0027, U+2018, U+2019, U+201A, U+201B) doubled; `alias_shell` override; exit code propagated; LookPath-guarded real-shell tests (sh and pwsh) incl. a one-arg PowerShell test and an exit-code test
    - [x] Write tests: `cmdutil.ExternalExitError` checked first and silently in `HandleError`, signal-killed child (-1) → 1; `cmd/ado/main_test.go` case where a fake `ado-hello` exits 3 → `run` returns 3 with empty stderr (shared with Phase 2 extensions)
    - [x] Implement; add `alias_shell` config key and `ExternalExitError`
- [x] Task: `ado alias set/list/delete/import` `095dbed`
    - [x] Write tests: built-in and built-in-alias shadowing refused, extension shadowing warns, nested names (`'pr mine'`) refused — top-level alias names only (o6), non-shell expansion must start with a valid command, `--clobber`, stdin expansion, list: table on a TTY, YAML when piped (round-trips into `alias import`), `--json` (o5); `delete --all`, `import` YAML (file/stdin) with per-entry validation report
    - [x] Implement commands
- [x] Task: Root dispatch for aliases `095dbed`
    - [x] Write tests: built-in > alias order, alias args forwarded, help shows aliases section, completion includes alias names; `help` reserved; the `alias` group added only when aliases exist (cobra panics on a GroupID without its group); raw-argv pre-scan peels leading `--debug` / `-R v` / `--repo=v` / `-Rv` before the command name
    - [x] Implement: `NewCmdRoot` reads config and registers alias commands (`GroupID: "alias"`, `DisableFlagParsing: true`) before extension commands (`GroupID: "extension"`), each group added only when non-empty; never register an extension whose name collides with a built-in, built-in cobra alias or user alias (duplicate cobra names resolve nondeterministically); raw-argv pre-scan in `cmd/ado/main.go` (also hosts the Phase 2 PATH `ado-<name>` fallback)
- [x] Task: Conductor - User Manual Verification 'Phase 1: Aliases' (Protocol in workflow.md)
    - [x] Windows host check (o28): cross-compile `ado.exe` and run shell aliases from the Windows host through WSL interop — Git `sh.exe` path, and the `powershell.exe` 5.1 fallback with `alias_shell` set (args with spaces/quotes, exit code)

## Phase 2: Extension Core (dispatch, env contract, list/remove/exec) [checkpoint: 6d9a5ed]

- [x] Task: Layout and manifests (`pkg/extension`) `6d9a5ed`
    - [x] Write tests: data dir per OS (`DataDir()`), author manifest `ado-extension.yml` parsing/validation, `ado-` name rule via one strict validator shared by install/remove/exec/upgrade (no `.`/`..`, `../` names refused, manifest name must equal the repo-derived name), core-name collision refused; kind derived from the entry (directory / symlink / pointer file); install record round-trip in a separate records directory `extensions/.records/<name>.yml` for every kind, never inside the entry (o10); `.records` skipped when listing entries; missing record → treated as no token consent
    - [x] Implement
- [x] Task: Dispatch and env contract `6d9a5ed`
    - [x] Write tests: order built-in > alias > installed > PATH; env vars set (`ADO_HOST` = API host — `dev.azure.com` or `<org>.visualstudio.com`, never a raw remote host like `ssh.dev.azure.com` — `ADO_ORG`, `ADO_PROJECT`, `ADO_EXTENSION_NAME`, `ADO_EXECUTABLE` from a Factory executable-path field filled by `os.Executable()`); `ADO_REPO` only when `-R` or an inherited `ADO_REPO` was given, otherwise new `ADO_REPO_NAME` alongside `ADO_HOST`/`ADO_ORG`/`ADO_PROJECT` (o7); `os.Environ()` and the real `*os.File` streams inherited, no exported TTY/colour state; pre-name `--debug` → `ADO_DEBUG=1`; unresolvable context → empty, not fatal; `-R` before and after the name; args/stdio/exit code passthrough via `ExternalExitError`; `signal.Notify` around `cmd.Run()` (not `signal.Ignore`)
    - [x] Write tests (token): `ADO_TOKEN` only with consent; without consent inherited `ADO_TOKEN` and `AZURE_DEVOPS_EXT_PAT` are filtered out of the child env (not overridden with empty values) and one stderr warning is printed when a token was withheld (o2); with consent and an Entra login the child gets the Entra token plus `ADO_TOKEN_TYPE=bearer` (o3); child side: env `ADO_TOKEN` with `ADO_TOKEN_TYPE=bearer` is sent as Bearer, unset → Basic (`internal/auth/store.go`, `pkg/cmd/auth/shared/credential.go`)
    - [x] Write tests (Windows launch): order `.exe` > `.ps1` via `pwsh` (fallback `powershell`) `-NoProfile -ExecutionPolicy Bypass -File` (o8) > `.cmd` > findsh `sh -c 'command "$@"' --`; a Windows `.cmd` metacharacter test (`&`, `|`, `"`, `%`) with an escape-or-refuse decision; helper-process fakes (editortest pattern) on all three OSes
    - [x] Implement, incl. `ADO_TOKEN_TYPE` handling in the credential lookup
- [x] Task: `ado auth token` speed bump `6d9a5ed`
    - [x] Write tests: refuses when `ADO_EXTENSION_NAME` set without consent, allowed with consent, unaffected outside extensions
    - [x] Implement
- [x] Task: Help and completion `6d9a5ed`
    - [x] Write tests: `ado help` extensions section, completion lists extension names; installed extensions registered eagerly (one `ReadDir`); PATH `ado-*` executables listed, scanned lazily only inside the help func and the root completion function, never on a normal run (o9); unknown-command fallback looks up only the given `ado-<name>` on PATH; `ado help <ext>` rewritten to `<ext> --help`
    - [x] Implement
- [x] Task: `ado extension list / remove / exec` (alias `ext`) `6d9a5ed`
    - [x] Write tests: list columns, TSV, JSON fields; remove Lstats the entry and deletes the dir, or only the local symlink/pointer file (not its target), plus its `.records/<name>.yml`; exec bypasses alias shadowing; unknown name errors
    - [x] Implement
- [ ] Task: Live test environment: setup and teardown
    - Note: steps that change the org or accounts (ADO repo `ado-hello`, GitHub repo `snake-in-prod/ado-hello`, Azure Artifacts feed `ado-live` with universal package versions, the temporary WSL agent and short-lived PATs) are run by the main session during manual verification, with the user's prior approval (granted in o26, o27, o29), not by swarm workers. Credential: Entra login. Work from a Linux-filesystem scratch dir (not /mnt/c) so the exec bit sticks.
    - [x] Setup ADO repo (Phase 3 verification): `ado repo create` `ado-hello` in `ado-gh`; push an executable `ado-hello` (`git update-index --chmod=+x`) and `ado-extension.yml`; a second push later for `upgrade`
    - [x] Setup GitHub repo (Phase 3 verification): public `snake-in-prod/ado-hello` via `gh repo create`; releases v0.1.0 and v0.2.0 with `ado-hello-<os>-<arch>[.exe]` assets and `checksums.txt`; `gh repo edit --add-topic ado-extension`
    - [x] Setup Azure Artifacts (Phase 4 verification, o27): project-scoped feed `ado-live` via `ado api -X POST https://feeds.dev.azure.com/snake-in-prod/ado-gh/_apis/packaging/feeds?api-version=7.1 -f name=ado-live`; publish universal package versions 0.1.0 and 0.2.0 (`ado-hello-linux-amd64`, `ado-hello-windows-amd64.exe`, `checksums.txt`) with the cached ArtifactTool, `--project ado-gh`, Entra token via `--patvar`; fallback a short-lived Packaging (Read & write) PAT
    - [x] Setup Windows host (o28): cross-compile `ado.exe` (`GOOS=windows`) and run it from the Windows host through WSL interop (Git `sh.exe`, `powershell.exe` 5.1, client execution policy)
    - [x] Setup agent (Phase 5 verification, o29): re-register the temporary WSL self-hosted agent in the `Default` pool for one run of the generated `azure-pipelines.yml` (the user starts `run.sh`; the main session cannot start long-running background processes)
    - Teardown (runs in Phase 5 verification): `ado repo delete ado-gh/ado-hello --permanent --yes` → feed `ado-live` `DELETE .../_apis/packaging/feeds/{id}` then `DELETE .../_apis/packaging/feedrecyclebin/{id}?api-version=7.1` → GitHub repo `gh repo delete snake-in-prod/ado-hello --yes` after the user grants `delete_repo` (`gh auth refresh -h github.com -s delete_repo`, o26) → agent unregistered, pool registration removed → short-lived PATs (agent registration, Packaging fallback) revoked
- [x] Task: Conductor - User Manual Verification 'Phase 2: Extension Core' (Protocol in workflow.md)
    - [x] Windows host check (o28): `.ps1` under the client's default execution policy, `.cmd`, and an `sh` script extension dispatched by the cross-compiled `ado.exe`

## Phase 3: Install — Local, Script, Trust Controls [checkpoint: 630b473]

- [x] Task: Source parsing and allowlist `630b473`
    - [x] Write tests: `owner/repo`, GitHub URL, ADO HTTPS/SSH/visualstudio.com URLs, local paths, all canonicalised to lowercase `host/org[/project]/repo` (ADO via `ParseRemoteURL`, separate GitHub parser; `.`/`..` rejected, `.git` stripped, parsed hosts compared); `extension_sources` glob matching incl. `local`, where `*` matches one segment and `**` any number (e.g. `dev.azure.com/myorg/**`, o24); bad patterns or config fail closed; GitHub API `full_name` matched after resolving (transfers redirect silently); refused sources error naming the allowlist; `ado config get/set extension_sources` reads and writes the YAML list as a comma-separated value (o1); host-aware httpmock matcher (inline `Matcher` on `req.URL.Host`, `httpmock.REST` ignores the host) for "ADO token never sent to non-ADO hosts" and "`GH_TOKEN` only to api.github.com"
    - [x] Implement; add `extension_sources` config key with `ado config get/set` support
- [x] Task: Local install `630b473`
    - [x] Write tests: symlink created on Unix; Windows always writes a pointer file (build-tagged `symlink_windows.go` / `symlink_other.go`, gh parity, o11); pointer-file entries read and dispatched on every OS; path made absolute and directory confirmed before linking; missing executable warns but keeps the link; dispatch follows link; `--force` replaces
    - [x] Implement
- [x] Task: Script install via git `630b473`
    - [x] Write tests (stubbed git): `Env []string` on `git.Client`, recorded by `gittest.Runner`; one real-git test that `GIT_CONFIG_COUNT/KEY_0/VALUE_0` sets `http.extraheader` (no network, all three OSes); `git version` < 2.31 with a private ADO HTTPS source fails with "git >= 2.31 required for private Azure DevOps extensions" (o14); clone into a temp dir inside the extensions root with `--` before the URL, `-c core.autocrlf=false -c core.eol=lf` (o12), no submodules, `GIT_ALLOW_PROTOCOL=https:ssh`, `GIT_PROTOCOL_FROM_USER=0`, `GIT_TERMINAL_PROMPT=0` off a TTY, then rename to `ado-<name>`; executable `ado-<name>` required: `git ls-files -s` mode 100755 on Unix, on Windows also a file with a shebang or a `.ps1`/`.cmd` sibling (o13); `--pin` checks out tag/commit, `-`-prefixed `--pin` rejected, resolved SHA (plus tag) recorded; private ADO clone passes a URL-scoped `http.<url>.extraheader` (Basic/Bearer header builder extracted from `api/http_client.go`) via `GIT_CONFIG_*` env, appended after an inherited `GIT_CONFIG_COUNT` (never argv or persisted config); ADO token never sent to GitHub; already-installed error without `--force`; real-git commit tests set `GIT_AUTHOR_*`/`GIT_COMMITTER_*`
    - [x] Implement
- [x] Task: Confirmation and token consent `630b473`
    - [x] Write tests: TTY summary (source, ref, kind, checksum status, token request), decline → exit 2, non-TTY requires `--yes`, token consent prompt default no, `--allow-token`, no consent → warning and record `tokenConsent: false` (in `.records/<name>.yml`); terminal control/escape sequences stripped from untrusted strings (manifest, tags, search results, README) before printing
    - [x] Implement
- [x] Task: Conductor - User Manual Verification 'Phase 3: Install — Local, Script, Trust Controls' (Protocol in workflow.md)
    - [x] Set up the ADO and GitHub parts of the live test environment; install `https://dev.azure.com/snake-in-prod/ado-gh/_git/ado-hello` (private clone with the Entra bearer header) and `snake-in-prod/ado-hello` as script extensions; check `ado hello` env contract, `-R`, `--pin`, `list`, `remove`
    - [x] Windows host check (o28): local install writes a pointer file and dispatches

## Phase 4: Binary Install and Upgrade [checkpoint: 3b57ab6]

- [x] Task: GitHub Releases binaries `3b57ab6`
    - [x] Move `LocalPath`, `SafeName`, `CreateFile`, `ExtractZip` from `pkg/cmd/pipeline/shared` into an `internal/` package (`git mv`, pipeline imports updated) before reuse
    - [x] Write tests (httpmock): release by tag/latest, asset selection exact `ado-<name>-<os>-<arch>[.exe]` first, then suffix `<os>-<arch>[.exe]` (o15); darwin-arm64 falls back to a darwin-amd64 asset with a message (o17); missing platform lists available ones; SHA-256 verify from `checksums.txt` or a single asset ending in `_checksums.txt` (o16); checksum parser (`  name`, ` *name`, basename, blank lines); mismatch aborts; missing checksums needs `--allow-unverified`; plain `f.HttpClient()` with Bearer (`GH_TOKEN` > `GITHUB_TOKEN`) only for api.github.com; `url.PathEscape` for path segments; 302 to the asset host without Authorization and no signed query in the log; 403/429 remaining-0 hint; latest 404; pinned prerelease; staged in an `O_EXCL` temp file, hashed while writing, `Chmod(0o755)`, renamed
    - [x] Implement
- [x] Task: Azure Artifacts binaries via ArtifactTool `3b57ab6`
    - [x] Record ArtifactTool in `tech-stack.md`: a proprietary Microsoft binary fetched at runtime from the user's org, never bundled or mirrored, with no integrity hash
    - [x] Write tests: ArtifactTool fetched from `_apis/clienttools/ArtifactTool/release` (clienttools area via `areaClient` and `StubResourceAreas`) with osName `Linux`/`Darwin`/`Windows` and arch, `uri` downloaded with plain `f.HttpClient()`, extracted + chmod, cached in `<cache>/artifacttool/<rid>_<version>`; darwin/windows arm64 without a native build falls back to amd64 with a one-line warning (o20); invoked (stubbed runner) with feed/package/version, `--project` when the manifest's optional `azure-artifacts.project` is set (empty = org-scoped, o18), token only in the `--patvar` env; manifest args validated (no leading `-`); version matches tag (`v` stripped) else latest = highest non-prerelease via `feed.GetPackages` exact match, same as ArtifactTool's `*` (o19); feed org may differ from the source repo's org only if it passes `extension_sources` (o25); non-zero exit = failure with Serilog Error/Critical lines; output read through `os.Root` accepting only expected regular files, confined to the install dir; checksum verify; Azure DevOps Server error test
    - [x] Implement
- [x] Task: `ado extension upgrade` `3b57ab6`
    - [x] Write tests: binary → latest version (old binary renamed aside, not the directory, on Windows); script → token env on `ls-remote`/`fetch`/`merge`, `git fetch origin HEAD` + `git merge --ff-only FETCH_HEAD` (follows a renamed default branch), non-ff fails with a `--force` hint, `--force` = `git reset --hard FETCH_HEAD`; local skipped, pinned skipped unless `--force`, `--dry-run` output (script: `ls-remote` vs `rev-parse HEAD`), new token request re-prompts consent, allowlist re-checked, `--all` partial failure summary exit 1
    - [x] Implement
- [x] Task: Conductor - User Manual Verification 'Phase 4: Binary Install and Upgrade' (Protocol in workflow.md)
    - [x] Set up the Azure Artifacts part of the live test environment (o27); install and upgrade binaries from GitHub releases v0.1.0 → v0.2.0 (checksum pass) and from feed `ado-live` 0.1.0 → 0.2.0 (confirm Entra-as-patvar and the osName spelling live); script `upgrade` after a second push to the ADO repo

## Phase 5: `create`, `search`, `browse`, Docs

- [ ] Task: `ado extension create`
    - [ ] Write tests: script/go/other templates file sets, `ado-extension.yml` contents, GitHub workflow and `azure-pipelines.yml` present, `git init` + initial commit (stubbed) with `git add --chmod=+x`, `.gitignore` with `/dist/`; generated `release.yml` is a plain script (setup-go `go-version-file`, six-target loop, `sha256sum` → `checksums.txt`, `gh release create`, prerelease for `-` tags) with build-provenance attestations (generation only, o23); `go.mod` written from the template with module path `dev.azure.com/<org>/<project>/_git/ado-<name>` from the ADO context, falling back to `ado-<name>` (o21), not `go mod init`; `azure-pipelines.yml` pins a full Go version, strips `v` for the package version, and passes a YAML parse/structure test; TTY prompts, non-TTY missing name error, next-steps output; real-git commit tests set `GIT_AUTHOR_*`/`GIT_COMMITTER_*`
    - [ ] Implement templates (`embed.FS`)
    - [ ] Smoke test: generated Go template compiles (`go build` in test with `-short` skip)
- [ ] Task: `ado extension search`
    - [ ] Write tests (httpmock): `topic:ado-extension` query composition, `--owner`, `--sort` validated (`stars|updated`), `--limit` capped at 1000, installed mark, TTY/TSV/JSON, `-w` URL; 403/429 remaining-0 message with reset time and `GH_TOKEN` hint
    - [ ] Implement
- [ ] Task: `ado extension browse`
    - [ ] Write tests: `-L/--limit` default 100 (o22), results filtered to `ado-`; select prompt over results, README via `/readme` with explicit Accept, 404 placeholder, in-memory cache; install/remove/open/back actions dispatch, non-TTY error
    - [ ] Implement
- [ ] Task: Docs
    - [ ] Write `docs/extensions.md` (author contract per spec): env contract incl. `ADO_REPO_NAME`, `ADO_TOKEN_TYPE` and token stripping; inherited TTY/colour variables (`ADO_FORCE_TTY`, `NO_COLOR`, `CLICOLOR`, `CLICOLOR_FORCE`, `ADO_PROMPT_DISABLED`, `CI`, `TERM`, `COLORTERM`, `GLAMOUR_STYLE`); `.gitattributes` `ado-* text eol=lf` (and `*.cmd text eol=crlf`); `git update-index --chmod=+x`; `checksums.txt` naming; Feed Publisher (Contributor) for build identities; lowercase-dash package names; Universal Packages on Azure DevOps Services only; consent bypass list; checksums = integrity not authenticity; `.cmd` escaping limit; `pwsh -File` caveats (Ctrl+C exit 0, `-`-prefixed args binding to script parameters)
    - [ ] Update `docs/gh-differences.md`: alias/extension dispatch order (gh: built-in > extension > alias), alias list output, top-level alias names only, no alias chaining, PowerShell shell aliases, `.ps1`/`.cmd` extensions, PATH extensions, git 2.31 minimum, `FETCH_HEAD` upgrade
    - [ ] Update `spec.md` (main session) for the decisions made in research: FR1 `alias list` prints a table on a TTY, YAML when piped, plus `--json` (o5), and the Shell bullet reworded for sh → `pwsh` → `powershell` via `-EncodedCommand` with `$1..$n` bound from `$args` (c3); FR3 `ADO_REPO` set only from `-R` or an inherited `ADO_REPO`, otherwise `ADO_HOST`/`ADO_ORG`/`ADO_PROJECT` plus new `ADO_REPO_NAME` (o7), `ADO_TOKEN`/`AZURE_DEVOPS_EXT_PAT` stripped from extensions without token consent with a one-time stderr warning (o2), and `ADO_TOKEN_TYPE=bearer` alongside an Entra `ADO_TOKEN` (o3); FR4 install records in `extensions/.records/<name>.yml` for every kind (o10) and an optional `project` field under `azure-artifacts` in `ado-extension.yml`, empty = org-scoped (o18); FR5 local installs always use a pointer file on Windows (o11), asset selection exact name then `<os>-<arch>[.exe]` suffix (o15), checksums from `checksums.txt` or a single `*_checksums.txt` asset (o16), allowlist examples with `*` = one segment and `**` = any number, e.g. `dev.azure.com/myorg/**` (o24)
    - [ ] Verify help text/examples for `alias *` and `extension *`
- [ ] Task: Conductor - User Manual Verification 'Phase 5: create, search, browse, Docs' (Protocol in workflow.md)
    - [ ] Confirm live: `search` and `browse` find `snake-in-prod/ado-hello` (topic indexing can lag minutes); re-register the temporary WSL agent and run the generated `azure-pipelines.yml` once (o29); then tear down the live test environment
