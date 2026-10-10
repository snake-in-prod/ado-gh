# Plan: `ado` CLI Foundation and Core PR Loop

Track: `mvp_core_20261010` · Spec: [spec.md](./spec.md) · Research: [research.md](./research.md)

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Scaffolding & Core Infrastructure [checkpoint: 60a7046]

- [x] Task: Initialize Go module and repo hygiene `60a7046`
    - [x] `go mod init` with `go 1.25.8` as the floor (huh v2; above go-gh's 1.25.0), `.gitignore`, `README.md` stub, `LICENSE` (MIT), `.golangci.yml`; never import `github.com/cli/cli/v2` (go 1.27)
    - [x] GitHub Actions CI: `gofmt`, `go vet`, `golangci-lint`, `go test -race -cover` on ubuntu/macos/windows
- [x] Task: IOStreams (`pkg/iostreams`) `60a7046`
    - [x] Write tests: TTY vs non-TTY detection, `NO_COLOR` / `CLICOLOR_FORCE` / `ADO_FORCE_TTY` (parsed by ado; `GH_FORCE_TTY` ignored), terminal width, color scheme on/off, test constructor capturing stdout/stderr
    - [x] Implement IOStreams using only go-gh `pkg/term` helpers `IsTerminal` / `IsColorDisabled` / `IsColorForced` (not `term.FromEnv`); parse `ADO_FORCE_TTY` ourselves; width via `golang.org/x/term.GetSize`
- [x] Task: Config (`internal/config`) `60a7046`
    - [x] Write tests: config dir resolution per OS + `ADO_CONFIG_DIR`, load/save YAML round-trip, missing file defaults, per-host keys
    - [x] Implement config read/write with `gopkg.in/yaml.v3`
- [x] Task: Errors & exit codes (`pkg/cmdutil`) `60a7046`
    - [x] Write tests: `SilentError`, `CancelError` → 2, `AuthError` → 4, generic → 1, flag errors print usage hint
    - [x] Implement error types and `main` exit-code mapping
- [x] Task: HTTP mock for tests (`pkg/httpmock`) `60a7046`
    - [x] Write tests: registry matches and verifies stubs, REST/String/JSON responders, helpers that stub the SDK's OPTIONS `{base}/_apis` and `_apis/ResourceAreas` discovery calls
    - [x] Implement by copying gh's stdlib-only `registry.go` plus REST/String/JSON responders (keep the MIT notice); add the SDK discovery stub helpers
- [x] Task: Factory and root command (`pkg/cmdutil`, `pkg/cmd/root`, `cmd/ado`) `60a7046`
    - [x] Write tests: `ado --version` prints version (ldflags-injected), `ado help` lists groups, unknown command errors, `--debug` / `ADO_DEBUG` toggles debug
    - [x] Write tests: browser-launcher resolver precedence `ADO_BROWSER` > ado config `browser` > `BROWSER`; `GH_BROWSER` and gh's config are ignored; resolved launcher is passed explicitly to go-gh `browser.New`
    - [x] Write tests: prompter refuses to prompt when `!IOStreams.CanPrompt()`; buffer-driven prompts via huh `WithInput` / `WithOutput` / `WithAccessible(true)`
    - [x] Implement lazy Factory (IOStreams, Config, HttpClient, Remotes, BaseRepo, Browser, Prompter), root cobra command, `completion` subcommand
    - [x] Implement browser-launcher resolver (also used later as MSAL `WithOpenURL`)
    - [x] Implement thin prompter over `charm.land/huh/v2`, gated on `IOStreams.CanPrompt()`
    - [x] Update tech-stack.md: pick `charm.land/huh/v2` as the prompt library, drop `AlecAivazis/survey/v2` (archived)
- [x] Task: `ado config get/set/list` `60a7046`
    - [x] Write tests: set then get value, list output TTY/non-TTY, unknown key error
    - [x] Implement config commands
- [x] Task: Conductor - User Manual Verification 'Phase 1: Scaffolding & Core Infrastructure' (Protocol in workflow.md)
    - [x] Include a manual huh v2 prompt check in Windows cmd.exe and PowerShell (moved to Phase 3: no command prompts before `auth login`)

## Phase 2: Context Detection [checkpoint: 72f054b]

- [x] Task: Remote URL parser (`internal/adocontext`) `72f054b`
    - [x] Write table-driven tests (accepted forms): `dev.azure.com` HTTPS (with/without `user[:secret]@`; userinfo dropped and never echoed), `ssh.dev.azure.com:v3` scp-style, `ssh://git@ssh.dev.azure.com[:22]/v3/...`, `{org}@vs-ssh.visualstudio.com:v3/...` (org from the path, not the username), `*.visualstudio.com` (with/without `DefaultCollection`, case-insensitive), project omitted (`dev.azure.com/{org}/_git/{repo}`, `{org}.visualstudio.com/[DefaultCollection/]_git/{repo}` → project = repo), `_git/_full|_optimized/{repo}` (also in SSH v3), visualstudio.com team segment (dropped), SSH v3 team segment (take project, drop team), per-segment `url.PathUnescape` (no double decode, `%2F` can't fake a segment), `.git` suffix kept, trailing slash tolerated, case-insensitive host and `_git`/`v3`/`_ssh` markers
    - [x] Write tests: legacy v1/v2 SSH `ssh://{org}@vs-ssh.visualstudio.com[:22]/{project}/_ssh/{repo}` and `{org}/{project}/_ssh/{repo}` accepted with the org from the SSH username, like az devops
    - [x] Write tests (reject list): lookalike hosts (`dev.azure.com.evil.com`, `notvisualstudio.com`), `vs-ssh.visualstudio.com` as an HTTPS host, empty segments, control chars / backslash, ports other than 443/22, team segment on dev.azure.com, SSH v3 without a project, non-ADO remotes
    - [x] Write tests: `Repo.Host` keeps the original host (no normalization to dev.azure.com); per-host config lookup keyed by the original host (e.g. `{org}.visualstudio.com`)
    - [x] Write tests: SSH host alias (unknown host, path starts with `v3/`, e.g. `git@devops_fabrikam:v3/...`) resolved via go-gh `pkg/ssh` `ssh -G` (stubbed)
    - [x] Implement `ParseRemoteURL(string) (Repo, error)` returning `{Host, Org, Project, Name}`, with go-gh's scp-style → `ssh://` rewrite pattern and per-segment unescape
    - [x] Implement SSH alias translation via go-gh `pkg/ssh` for unknown hosts with `v3/` paths
    - [x] Update spec.md FR3 (spec.md:25-29) with the accepted forms, the reject list, the original-host rule, legacy `_ssh` acceptance and SSH alias resolution
- [x] Task: Git remotes reader (`internal/git`) `72f054b`
    - [x] Write tests: parse `git remote -v` output, current branch, not-a-git-repo error (via stubbed command runner)
    - [x] Implement git command runner and remote/branch helpers
- [x] Task: Base repo resolution `72f054b`
    - [x] Write tests: precedence `-R` > `ADO_REPO` > origin > upstream > first ADO remote; partial `-R` filled by `ADO_ORG`/`ADO_PROJECT`; actionable error when nothing resolves
    - [x] Implement `-R/--repo` global flag and `Factory.BaseRepo`
- [x] Task: Conductor - User Manual Verification 'Phase 2: Context Detection' (Protocol in workflow.md)

## Phase 3: Authentication [checkpoint: 51916f6]

- [x] Task: Prerequisite - Entra app registration and auth docs (manual) `51916f6`
    - [~] Register ado's multi-tenant public-client app ("Accounts in any organizational directory", "Allow public client flows" on, redirect `http://localhost` (mobile/desktop), delegated Azure DevOps `user_impersonation`) and record its client ID as the default constant (deferred: app not registered yet; `ADO_ENTRA_CLIENT_ID` / config override in place)
    - [x] Document the client ID and the `ADO_ENTRA_CLIENT_ID` / config override in README; README and `auth login` PAT guidance list the scopes "Code (Read & write)" + "Identity (Read)"
- [x] Task: Credential store (`internal/auth`) `51916f6`
    - [x] Write tests: keychain set/get/delete via `keyring.MockInit()`; PAT keys per org `ado:<host>/<org>` (e.g. `ado:dev.azure.com/<org>`) with per-host Entra token as fallback; env `ADO_TOKEN` / `AZURE_DEVOPS_EXT_PAT` precedence
    - [x] Write tests: timeout wrapper (60s, injectable in tests) around go-keyring Set/Get/Delete returning a timeout error; any Set error (incl. `ErrSetDataTooBig`, timeout, `keyring.MockInitWithError`) falls back to the 0600 config file with a stderr warning; Get: `ErrNotFound` = nothing stored, other error = unusable, both continue to the config source; token source (env / keychain / config) exposed for `auth status`
    - [x] Write tests: insecure-storage option skips the keychain and writes the 0600 plaintext file with the warning
    - [x] Implement token store and source resolution (env > keychain > config fallback) with the timeout wrapper, per-org keys and insecure-storage option
- [x] Task: Authenticated HTTP transport (`api`) `51916f6`
    - [x] Write tests: Basic header for PAT, Bearer for Entra, `User-Agent` overwritten in the RoundTripper (same for SDK and raw requests), `X-TFS-FedAuthRedirect: Suppress` always sent, `--debug` request log with `Authorization` redacted, 401 and 203 → `AuthError` (exit 4)
    - [x] Write tests: a typed SDK call (git `GetRepository` via httpmock with the OPTIONS `_apis` stub) carries the RoundTripper's Authorization / User-Agent and is logged
    - [x] Implement `RoundTripper` (on a cloned `http.DefaultTransport`, never the global one) and shared `http.Client`; pin `azuredevops/v7 v7.1.0`; build typed clients as `&<pkg>.ClientImpl{Client: *azuredevops.NewClientWithOptions(conn, baseURL, azuredevops.WithHTTPClient(shared))}` with an empty `AuthorizationString`, auth set only in the RoundTripper
    - [~] Live check that git/core/wit/build/pipelines live under the org URL on dev.azure.com and `*.visualstudio.com`; add a `GetResourceAreas` fallback through the shared client only if not (deferred to Phase 5 verification: first SDK use is `pr list`)
- [x] Task: PAT login (`ado auth login --with-token` + interactive PAT) `51916f6`
    - [x] Write tests: stdin token validated only via org-scoped `_apis/connectionData` (httpmock, no profile API), anonymous user id `aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa` rejected, 203 sign-in page rejected, invalid token rejected, no PAT length check, org required before validating (`--org`, git remote, `ADO_ORG`, prompt) and non-TTY without an org fails, masked TTY prompt, non-TTY without `--with-token` fails, `--insecure-storage` stores the PAT in plaintext 0600 with a warning, help/error text mentions the 2026-12-01 global-PAT cutoff (use an org-scoped PAT or Entra)
    - [x] Implement PAT login flow, org selection and a shared `validate(org, transport)` helper; add `--insecure-storage` to `ado auth login`
    - [x] Update spec.md FR4 (spec.md:37): PAT validation via `connectionData` only (drop "profile"); PAT credential key per org
- [x] Task: Entra ID login via MSAL `51916f6`
    - [x] Write tests: tenant discovery - unauthenticated HEAD `https://dev.azure.com/{org}` → `WWW-Authenticate` `authorization_uri` tenant, then `X-VSS-ResourceTenant` (first non-zero); cached per org; fake-server cases: normal tenant, all-zeros (MSA-backed) tenant → error pointing to a PAT, 404 → "org not found", unknown → `organizations`
    - [x] Write tests: one MSAL `public.Client` with `organizations` authority and `WithTenantID` on device-code, interactive and silent calls; default client ID overridable via `ADO_ENTRA_CLIENT_ID` / config; `HomeAccountID` stored in per-host config; silent refresh via `AcquireTokenSilent(WithSilentAccount)`; silent errors → `AuthError` (exit 4) "run: ado auth login" (MSAL client behind interface, faked)
    - [x] Write tests: login-method selection (env-driven) - default interactive browser via `WithOpenURL` backed by the ado browser resolver; `--device` forces device code; `SSH_CONNECTION`/`SSH_TTY`, or Linux outside WSL without `DISPLAY`/`WAYLAND_DISPLAY` → device code; openURL error → device-code fallback; device-code messages to stderr; interactive login runs in a cancellable ctx with a timeout
    - [x] Write tests: interactive `ado auth login` menu offers Entra ID first, PAT second
    - [x] Write tests: MSAL cache accessor - cache AES-GCM-encrypted in a 0600 file with only its 32-byte key in go-keyring; `keyring.MockInit` round-trip; undecryptable cache → empty; no keyring → Export falls back to a plaintext 0600 file with a one-time warning instead of failing the login; default ctx timeout; atomic write (temp + rename)
    - [x] Write tests: org selection - `profile/me` → `accounts?memberId`, org URL built as `https://dev.azure.com/{accountName}`, manual entry fallback on error or empty list
    - [x] Write tests: error/help text - device code blocked by Conditional Access (suggest browser or PAT), MSA-backed org (suggest PAT)
    - [x] Implement tenant discovery
    - [x] Implement MSAL public client (interactive browser by default, device-code fallback and `--device`), login-method selection and login menu, token provider used by transport
    - [x] Implement encrypted MSAL cache accessor
    - [x] Implement Entra org selection
    - [~] Measure `len(Marshal())` of the MSAL cache once against a real org (deferred until the Entra app is registered)
    - [x] Update spec.md FR4 (spec.md:39): MSAL cache AES-GCM-encrypted in a 0600 file with its key in go-keyring (plaintext 0600 + warning without a keyring); interactive browser is the default flow with device-code fallback; Entra first in the login menu
    - [x] Update tech-stack.md:21: MSAL cache stored as an encrypted 0600 file, only its key in go-keyring
- [x] Task: `ado auth status`, `logout`, `token` `51916f6`
    - [x] Write tests: status shows user/method/source with masked token and reuses the shared `connectionData` validation, exit 4 when not logged in, logout clears store (Entra: `RemoveAccount`, delete cache file and keyring key), token prints raw token only
    - [x] Implement commands
- [x] Task: Conductor - User Manual Verification 'Phase 3: Authentication' (Protocol in workflow.md)
    - [x] Manual huh v2 prompt check in Windows cmd.exe and PowerShell (carried over from Phase 1)

## Phase 4: Output & `ado api`

- [ ] Task: JSON/jq/template exporter (`pkg/cmdutil/json_flags`)
    - [ ] Write tests: `--json f1,f2` field filtering, `--json` with no fields lists fields and exits 1, coloured indented `--json` on TTY, `--jq` via go-gh `pkg/jq`, `--template` via go-gh `pkg/template`, mutually exclusive flags
    - [ ] Implement `AddJSONFlags` + `Exporter`; coloured `--json` via `jq.EvaluateFormatted(r, w, ".", "  ", color)` (no jsoncolor port)
- [ ] Task: Table printer helpers
    - [ ] Write tests: TTY aligned colored table with relative time, non-TTY TSV with RFC3339
    - [ ] Implement wrapper over go-gh `pkg/tableprinter`
- [ ] Task: `ado api` command
    - [ ] Write tests: placeholder substitution, default `api-version`, `-X`, `-H`, `-f`/`-F` typed fields → JSON body, `--input`, `-i`, `--jq`, non-2xx → stderr + exit 1
    - [ ] Write tests (`--paginate`): GET only; `X-MS-ContinuationToken` header → `continuationToken` query param keeping `api-version` and other params; body `continuationToken` followed only when `isLastBatch != true` and `hasMore != false`; stop on no token or a repeated token; when the user passes `$top`, step `$skip` until an empty page; merge `value` arrays into one envelope with recomputed `count`, sequential pages when any page lacks `value`; `-i` prints headers per page; `--jq`/`--template` applied once to the merged result
    - [ ] Implement `ado api`, reusing `azuredevops.HeaderKeyContinuationToken`
    - [ ] Live check that PR list sends no continuation header
- [ ] Task: Conductor - User Manual Verification 'Phase 4: Output & ado api' (Protocol in workflow.md)

## Phase 5: Pull Request Commands

- [ ] Task: PR API layer and reference resolution (`api/pr.go`, `pkg/cmd/pr/shared`)
    - [ ] Write tests: resolve PR by number, `!123`, URL, branch name, current branch; not-found error; list wrapper sends `$top` and loops `$skip` until `--limit` or an empty page; client-side `--draft` filter; single-PR fetch via `GetPullRequestById`; error mapping handles both `WrappedError` and `*WrappedError`; PR model exposes `ForkSource`, `SourceRefName`, `LastMergeSourceCommit`; repo-by-id lookup (httpmock with OPTIONS `_apis` stubs)
    - [ ] Implement SDK wrappers (git client: get/list/create/update PR, reviewers, threads, repo by id) and finder; no created/closed date filters in MVP
- [ ] Task: Identity resolver (`api/identity.go`)
    - [ ] Write tests: GUID passthrough, `@me` via connectionData, IMS `General` lookup via SDK `ReadIdentities` (stubbed ResourceAreas + OPTIONS + identities), `DirectoryAlias`/`AccountName` fallback, same-domain disambiguation; ambiguous → prompt to pick on TTY, error listing candidates on non-TTY; no match → error "could not resolve <x>: user must be added to the organization"; `[Project]\Team` resolves (verify live); bare team name rejected with a hint to use `[Project]\Team` or a GUID; 401/403 → "Identity (Read)" scope hint; duplicate ids removed
    - [ ] Implement identity resolver shared by `pr create -r`, `pr list --author/--reviewer` and pr_parity `pr edit`
- [ ] Task: `ado pr list`
    - [ ] Write tests: state/author/base/head/reviewer/draft filters map to search criteria (author/reviewer via the identity resolver), `--limit`, TTY table, non-TTY TSV, `--json` fields, `--web` opens URL, empty-result message
    - [ ] Implement `pr list`
- [ ] Task: `ado pr view`
    - [ ] Write tests: current-branch default, reviewers with vote labels (teams shown as non-voting with `votedFor`), policy summary, `--comments`, `--json`, `--web`
    - [ ] Implement `pr view`
- [ ] Task: `ado pr create`
    - [ ] Write tests: flags-only creation, `--fill` from commits, default base = repo default branch, `--draft`, `-r` reviewers resolved via the identity resolver (optional reviewers only; required reviewers via pr_parity `pr edit`), `--work-item` links, `--auto` = create then `UpdatePullRequest` with `AutoCompleteSetBy` + `CompletionOptions` (assert the second call), push prompt when branch not on remote, TTY prompts, non-TTY missing `--title` error, prints URL
    - [ ] Implement `pr create`
    - [ ] Update spec.md `ado pr create` row: add `--auto` (set auto-complete after create)
- [ ] Task: `ado pr checkout`
    - [ ] Write tests (stubbed git runner): same-repo fetch `+refs/heads/<b>:refs/remotes/<remote>/<b> --no-tags` + `checkout -b <local> --track`; fork PR with a matching local remote uses the same-repo path; fork without a remote fetches by URL and sets `branch.<b>.remote`/`pushRemote`/`merge`; local name = source branch, prefixed `<forkRepo>/` only when it equals the default branch; unreadable fork → clear access error; existing branch fast-forward, `--branch` override, `--force` reset; branch names starting with `-` rejected; only `-b`/`-f` flags; never fetches `refs/pull/<id>/head`
    - [ ] Implement `pr checkout`
- [ ] Task: `ado pr review`
    - [ ] Write tests: each vote flag maps to correct value (10/5/-5/-10/0), `--comment` + `--body` creates thread, mutually exclusive flags, non-TTY with no flag errors
    - [ ] Implement `pr review`
- [ ] Task: `ado pr merge` (alias `complete`)
    - [ ] Write tests: strategy flags map to an explicit `MergeStrategy` (never `SquashMerge`), `Status: completed` sent with `LastMergeSourceCommit` from the fetched PR and existing completion options carried over, `--delete-branch`, `--auto` sets auto-complete, merge message, `--bypass-policy` requires `--bypass-reason`, completion blocked by policy → fails with a hint to use `--auto` or `--bypass-policy`, TTY confirmation, non-TTY without `--yes` refuses, cancel → exit 2
    - [ ] Implement `pr merge`
- [ ] Task: gh-differences doc
    - [ ] Write `docs/gh-differences.md` covering work items vs issues, votes vs reviews, merge→complete, `-R` hierarchy, no PR head ref (checkout fetches the source branch), fork pushes depend on fork permissions (no maintainer-can-modify), `ado api --paginate` output (merged `value` envelope vs gh's per-page / `--slurp`)
- [ ] Task: Conductor - User Manual Verification 'Phase 5: Pull Request Commands' (Protocol in workflow.md)
    - [ ] Live check (from Phase 3) that git/core/wit/build/pipelines SDK clients work under the org URL on dev.azure.com and `*.visualstudio.com`; add a `GetResourceAreas` fallback only if not
