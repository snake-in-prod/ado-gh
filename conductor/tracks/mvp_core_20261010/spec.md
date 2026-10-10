# Spec: `ado` CLI Foundation and Core PR Loop

## Overview
Build the first usable version of `ado`, a `gh`-style CLI for Azure DevOps Services. After this track, a developer can authenticate once (PAT or Microsoft Entra ID), `cd` into a cloned Azure DevOps repo, and run the core pull request loop (create, list, view, checkout, review, merge) without passing org/project/repo flags. Every read command also supports stable, scriptable `--json` output, and `ado api` gives raw REST access.

## User Stories
1. **Auth + zero-config:** As a developer, I run `ado auth login` once, and afterwards every command in a cloned Azure DevOps repo resolves org/project/repo from the git remote.
2. **Core PR loop:** As a developer used to `gh`, I can `ado pr create`, `ado pr list`, `ado pr view 123`, `ado pr checkout 123`, `ado pr review 123 --approve`, and `ado pr merge 123` with gh-equivalent flags.
3. **Scripting:** As a CI/script author, I can call `ado pr list --json id,title,status --jq '.[].id'` and `ado api _apis/projects` and get stable, machine-readable output with correct exit codes.

## Functional Requirements

### FR1: CLI Scaffolding
- Binary `ado` built from `cmd/ado`. Root cobra command with `--version`, `help`, shell `completion`.
- gh-style architecture: `pkg/cmdutil.Factory` (lazy IOStreams, Config, HTTP client, SDK connection, git context), `pkg/iostreams` (TTY detection, color, pager-free), one package per command under `pkg/cmd/<group>/<verb>`.
- Global flags: `-R/--repo [ORG/]PROJECT/REPO`, `--debug` (also `ADO_DEBUG=1`).
- Exit codes: `0` success, `1` error, `2` cancelled, `4` auth required.

### FR2: Config
- YAML config at `$XDG_CONFIG_HOME/ado/config.yml` (`%AppData%\ado\config.yml` on Windows; override via `ADO_CONFIG_DIR`).
- Stores per-host default org, auth method (`pat` | `entra`), user, and `prompt`/`editor` preferences. Never stores tokens when the keychain is available.
- `ado config get/set/list`.

### FR3: Context Detection
- Parse Azure DevOps remotes (see research.md q9, o21–o23):
  - `https://dev.azure.com/{org}/{project}/_git/{repo}` (optional `{user}[:{secret}]@`, dropped and never echoed)
  - `git@ssh.dev.azure.com:v3/{org}/{project}/{repo}` and `ssh://git@ssh.dev.azure.com[:22]/v3/...`
  - `{org}@vs-ssh.visualstudio.com:v3/{org}/{project}/{repo}` (org from the path)
  - `https://{org}.visualstudio.com/[DefaultCollection/]{project}/_git/{repo}` (team segment dropped)
  - Project omitted (`dev.azure.com/{org}/_git/{repo}`, `{org}.visualstudio.com/_git/{repo}`): project = repo
  - `_git/_full/` and `_git/_optimized/` segments; SSH v3 team segment (project kept, team dropped)
  - Legacy v1/v2 SSH `ssh://{org}@vs-ssh.visualstudio.com[:22]/{project}/_ssh/{repo}` (org from the SSH username, like az devops)
  - SSH host aliases from `~/.ssh/config` (unknown host with a `v3/` path) resolved via `ssh -G`
  - URL-encoded names decoded per segment (`%20` → space; `%2F` cannot fake a segment)
- Rejected: lookalike hosts, `vs-ssh.visualstudio.com` over HTTPS, empty segments, control characters / backslash, ports other than 443/22, a team segment on dev.azure.com, SSH v3 without a project, non-ADO remotes.
- `Repo.Host` keeps the original host (no normalization to dev.azure.com); per-host config is keyed by it.
- Resolution precedence: `-R` flag > `ADO_REPO` env > git remotes (prefer `origin`, then `upstream`, then first ADO remote) > error "not an Azure DevOps repository; use -R".
- `ADO_ORG` / `ADO_PROJECT` fill partial `-R` values.

### FR4: Authentication
- `ado auth login`:
  - `--with-token` reads PAT from stdin (non-interactive).
  - Interactive TTY: choose PAT (masked prompt) or Microsoft Entra ID.
  - `--org` selects org; validates credentials with a call to the connection data / profile API before saving.
- **PAT:** stored in OS keychain via go-keyring (service `ado:<host>`). Sent as Basic auth (`:<pat>` base64).
- **Entra ID:** MSAL public client, device-code flow (default on non-GUI / `--device`) and interactive browser flow; scope `499b84ac-1321-427f-aa17-267ca6975798/.default`; token cache persisted in keychain; silent refresh on use. Sent as Bearer token.
- Env overrides for CI: `ADO_TOKEN` and `AZURE_DEVOPS_EXT_PAT` (PAT) take precedence over stored credentials.
- `ado auth status` (per host: user, method, token source, validity; masked token), `ado auth logout`, `ado auth token` (prints token only on explicit call).
- Plaintext fallback (config file) only when keychain unavailable, with a warning.
- Authenticated `http.RoundTripper` shared by SDK and raw client: sets auth header, `User-Agent: ado-cli/<version>`, and with `--debug` logs requests to stderr with `Authorization` redacted.

### FR5: `ado api`
- `ado api <path> [flags]`: path relative to `https://dev.azure.com/{org}/` (or absolute URL on an ADO host); `{org}`, `{project}`, `{repo}` placeholders filled from context.
- Flags (gh parity): `-X/--method`, `-H/--header`, `-f/--raw-field`, `-F/--field` (typed), `--input <file|->`, `-i/--include`, `--jq`, `-t/--template`, `--paginate` (follows `x-ms-continuationtoken` / `continuationToken`).
- Default `api-version` query param added when absent (configurable via `--api-version`).
- Non-2xx → print response body to stderr and exit `1`.

### FR6: Output Formatting
- Shared exporter: `--json [fields]`, `--jq <expr>`, `-t/--template <tmpl>` on every list/view command. `--json` without fields lists available fields and exits `1`.
- TTY: aligned colored table, relative times. Non-TTY: tab-separated, absolute RFC3339 times, no color. Respect `NO_COLOR`, `CLICOLOR_FORCE`.
- JSON field names are camelCase and treated as public API.

### FR7: Pull Request Commands
| Command | Behavior (gh parity) |
|---|---|
| `ado pr list` | `-s/--state {active\|completed\|abandoned\|all}` (default active), `-A/--author`, `-B/--base`, `-H/--head`, `--reviewer`, `-L/--limit` (default 30), `--draft`, `-w/--web`, JSON fields: `id,title,status,isDraft,author,sourceRefName,targetRefName,createdDate,url,reviewers,mergeStatus` |
| `ado pr view [<id>\|<branch>]` | Default: PR for current branch. Shows title, status, author, branches, reviewers + votes, description, policies/checks summary. `-c/--comments`, `-w/--web`, JSON |
| `ado pr create` | `-t/--title`, `-b/--body`, `-F/--body-file`, `-B/--base` (default repo default branch), `-H/--head` (default current branch), `-d/--draft`, `-r/--reviewer` (repeatable), `--work-item` (link IDs), `-f/--fill` (from commits), `-w/--web`. Pushes branch if not on remote (prompt on TTY). Prompts for title/body/reviewers on TTY; fails naming the missing flag when non-TTY. Prints PR URL |
| `ado pr checkout <id>` | Fetches source branch and checks it out locally (creates tracking branch; `-b/--branch` override, `-f/--force`) |
| `ado pr review [<id>]` | `-a/--approve` (vote 10), `--approve-with-suggestions` (5), `--wait` (-5, alias `-r/--request-changes`), `--reject` (-10), `--reset` (0), `-c/--comment` + `-b/--body` adds a comment thread |
| `ado pr merge [<id>]` (alias `complete`) | `--merge`/`--squash`/`--rebase`/`--rebase-merge` strategy, `-d/--delete-branch`, `--auto` (set auto-complete), `--subject`/`--body` merge message, `--bypass-policy` + `--bypass-reason`. Confirmation on TTY unless `-y/--yes`; non-TTY without `--yes` refuses |

- PR references accept number, `!123`, URL, or branch name.

## Non-Functional Requirements
- **Cross-platform:** Windows, macOS, Linux. CI test matrix on all three (GitHub Actions). Path, keychain, and browser-open handled per OS.
- **Coverage:** >90% per workflow. No live network in unit tests (httpmock registry).
- **Security:** tokens never printed (except `auth token`), logged, or put in URLs; redaction covered by tests.
- **gh parity:** flag names/shorthands match gh where an equivalent exists; divergences listed in `docs/gh-differences.md`.

## Out of Scope (future tracks)
- Work item commands, pipeline commands, `repo` commands beyond what PR flows need.
- Aliases and extensions.
- GoReleaser / package-manager distribution.
- Azure DevOps Server (on-prem).

## Acceptance Criteria
- [ ] `ado auth login --with-token < pat.txt` then `ado auth status` shows a valid user; `ado auth login` with Entra ID device code works against a real org.
- [ ] Inside a cloned ADO repo, `ado pr list` works with no flags; outside, `-R org/project/repo` works.
- [ ] Full loop: `ado pr create --fill`, `ado pr view`, `ado pr checkout <id>`, `ado pr review <id> --approve`, `ado pr merge <id> --squash -d -y` succeed against a test repo.
- [ ] `ado pr list --json id,title --jq '.[0].id'` prints a bare number; piping produces no color.
- [ ] `ado api '_apis/projects' --jq '.value[].name'` works; `--paginate` follows continuation tokens.
- [ ] Missing auth exits `4` with `run: ado auth login`; non-TTY `pr create` without `--title` fails naming the flag.
- [ ] CI green on linux/macos/windows with >90% coverage.
