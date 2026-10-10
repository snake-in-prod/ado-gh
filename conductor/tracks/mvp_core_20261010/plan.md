# Plan: `ado` CLI Foundation and Core PR Loop

Track: `mvp_core_20261010` · Spec: [spec.md](./spec.md)

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Scaffolding & Core Infrastructure

- [ ] Task: Initialize Go module and repo hygiene
    - [ ] `go mod init`, `.gitignore`, `README.md` stub, `LICENSE` (MIT), `.golangci.yml`
    - [ ] GitHub Actions CI: `gofmt`, `go vet`, `golangci-lint`, `go test -race -cover` on ubuntu/macos/windows
- [ ] Task: IOStreams (`pkg/iostreams`)
    - [ ] Write tests: TTY vs non-TTY detection, `NO_COLOR` / `CLICOLOR_FORCE` / `ADO_FORCE_TTY`, color scheme on/off, test constructor capturing stdout/stderr
    - [ ] Implement IOStreams using go-gh `pkg/term`
- [ ] Task: Config (`internal/config`)
    - [ ] Write tests: config dir resolution per OS + `ADO_CONFIG_DIR`, load/save YAML round-trip, missing file defaults, per-host keys
    - [ ] Implement config read/write with `gopkg.in/yaml.v3`
- [ ] Task: Errors & exit codes (`pkg/cmdutil`)
    - [ ] Write tests: `SilentError`, `CancelError` → 2, `AuthError` → 4, generic → 1, flag errors print usage hint
    - [ ] Implement error types and `main` exit-code mapping
- [ ] Task: Factory and root command (`pkg/cmdutil`, `pkg/cmd/root`, `cmd/ado`)
    - [ ] Write tests: `ado --version` prints version (ldflags-injected), `ado help` lists groups, unknown command errors, `--debug` / `ADO_DEBUG` toggles debug
    - [ ] Implement lazy Factory (IOStreams, Config, HttpClient, Remotes, BaseRepo), root cobra command, `completion` subcommand
- [ ] Task: `ado config get/set/list`
    - [ ] Write tests: set then get value, list output TTY/non-TTY, unknown key error
    - [ ] Implement config commands
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Scaffolding & Core Infrastructure' (Protocol in workflow.md)

## Phase 2: Context Detection

- [ ] Task: Remote URL parser (`internal/adocontext`)
    - [ ] Write table-driven tests: `dev.azure.com` HTTPS (with/without user@), `ssh.dev.azure.com:v3`, `*.visualstudio.com` (with/without `DefaultCollection`), URL-encoded names, non-ADO remotes rejected
    - [ ] Implement `ParseRemoteURL(string) (Repo, error)` returning `{Host, Org, Project, Name}`
- [ ] Task: Git remotes reader (`internal/git`)
    - [ ] Write tests: parse `git remote -v` output, current branch, not-a-git-repo error (via stubbed command runner)
    - [ ] Implement git command runner and remote/branch helpers
- [ ] Task: Base repo resolution
    - [ ] Write tests: precedence `-R` > `ADO_REPO` > origin > upstream > first ADO remote; partial `-R` filled by `ADO_ORG`/`ADO_PROJECT`; actionable error when nothing resolves
    - [ ] Implement `-R/--repo` global flag and `Factory.BaseRepo`
- [ ] Task: Conductor - User Manual Verification 'Phase 2: Context Detection' (Protocol in workflow.md)

## Phase 3: Authentication

- [ ] Task: Credential store (`internal/auth`)
    - [ ] Write tests: keychain set/get/delete via `keyring.MockInit()`, plaintext fallback with warning when keychain errors, env `ADO_TOKEN` / `AZURE_DEVOPS_EXT_PAT` precedence
    - [ ] Implement token store and source resolution (env > keychain > config fallback)
- [ ] Task: Authenticated HTTP transport (`api`)
    - [ ] Write tests: Basic header for PAT, Bearer for Entra, `User-Agent`, `--debug` request log with `Authorization` redacted, 401 → `AuthError` (exit 4)
    - [ ] Implement `RoundTripper` and shared `http.Client`; SDK connection built on same transport
- [ ] Task: PAT login (`ado auth login --with-token` + interactive PAT)
    - [ ] Write tests: stdin token validated against profile/connectionData (httpmock), invalid token rejected, masked TTY prompt, non-TTY without `--with-token` fails
    - [ ] Implement PAT login flow and org selection
- [ ] Task: Entra ID login via MSAL
    - [ ] Write tests: device-code flow messages to stderr, token cache persisted to store, silent refresh on expiry, `--device` forces device code (MSAL client behind interface, faked)
    - [ ] Implement MSAL public client (device code + interactive browser), token provider used by transport
- [ ] Task: `ado auth status`, `logout`, `token`
    - [ ] Write tests: status shows user/method/source with masked token, exit 4 when not logged in, logout clears store, token prints raw token only
    - [ ] Implement commands
- [ ] Task: Conductor - User Manual Verification 'Phase 3: Authentication' (Protocol in workflow.md)

## Phase 4: Output & `ado api`

- [ ] Task: JSON/jq/template exporter (`pkg/cmdutil/json_flags`)
    - [ ] Write tests: `--json f1,f2` field filtering, `--json` with no fields lists fields and exits 1, `--jq` via go-gh `pkg/jq`, `--template` via go-gh `pkg/template`, mutually exclusive flags
    - [ ] Implement `AddJSONFlags` + `Exporter`
- [ ] Task: Table printer helpers
    - [ ] Write tests: TTY aligned colored table with relative time, non-TTY TSV with RFC3339
    - [ ] Implement wrapper over go-gh `pkg/tableprinter`
- [ ] Task: `ado api` command
    - [ ] Write tests: placeholder substitution, default `api-version`, `-X`, `-H`, `-f`/`-F` typed fields → JSON body, `--input`, `-i`, `--jq`, non-2xx → stderr + exit 1, `--paginate` follows continuation tokens and merges `value` arrays
    - [ ] Implement `ado api`
- [ ] Task: Conductor - User Manual Verification 'Phase 4: Output & ado api' (Protocol in workflow.md)

## Phase 5: Pull Request Commands

- [ ] Task: PR API layer and reference resolution (`api/pr.go`, `pkg/cmd/pr/shared`)
    - [ ] Write tests: resolve PR by number, `!123`, URL, branch name, current branch; not-found error
    - [ ] Implement SDK wrappers (git client: get/list/create/update PR, reviewers, threads) and finder
- [ ] Task: `ado pr list`
    - [ ] Write tests: state/author/base/head/reviewer/draft filters map to search criteria, `--limit`, TTY table, non-TTY TSV, `--json` fields, `--web` opens URL, empty-result message
    - [ ] Implement `pr list`
- [ ] Task: `ado pr view`
    - [ ] Write tests: current-branch default, reviewers with vote labels, policy summary, `--comments`, `--json`, `--web`
    - [ ] Implement `pr view`
- [ ] Task: `ado pr create`
    - [ ] Write tests: flags-only creation, `--fill` from commits, default base = repo default branch, `--draft`, reviewers resolved to identities, `--work-item` links, push prompt when branch not on remote, TTY prompts, non-TTY missing `--title` error, prints URL
    - [ ] Implement `pr create`
- [ ] Task: `ado pr checkout`
    - [ ] Write tests: fetch + create tracking branch, existing branch fast-forward, `--branch` override, `--force` (stubbed git runner)
    - [ ] Implement `pr checkout`
- [ ] Task: `ado pr review`
    - [ ] Write tests: each vote flag maps to correct value (10/5/-5/-10/0), `--comment` + `--body` creates thread, mutually exclusive flags, non-TTY with no flag errors
    - [ ] Implement `pr review`
- [ ] Task: `ado pr merge` (alias `complete`)
    - [ ] Write tests: strategy flags map to merge strategy, `--delete-branch`, `--auto` sets auto-complete, merge message, `--bypass-policy` requires `--bypass-reason`, TTY confirmation, non-TTY without `--yes` refuses, cancel → exit 2
    - [ ] Implement `pr merge`
- [ ] Task: gh-differences doc
    - [ ] Write `docs/gh-differences.md` covering work items vs issues, votes vs reviews, merge→complete, `-R` hierarchy
- [ ] Task: Conductor - User Manual Verification 'Phase 5: Pull Request Commands' (Protocol in workflow.md)
