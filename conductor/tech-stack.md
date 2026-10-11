# Tech Stack

## Language & Runtime
- **Go** (latest stable; `go.mod` pins minimum `go 1.25.8`, required by huh v2). Single static binary, `CGO_ENABLED=0` where possible. Never import `github.com/cli/cli/v2` (needs go 1.27).
- Module path: `github.com/snake-in-prod/ado-gh`; binary name: `ado`.

## Azure DevOps API
- **`github.com/microsoft/azure-devops-go-api/azuredevops/v7`** pinned at `v7.1.0`: typed clients for Git (repos, PRs), Work Item Tracking, Work (iterations, boards), Build/Pipelines, Core (projects, teams). Work Item Search goes through the `almsearch` host. Clients are built as `<pkg>.ClientImpl` over `azuredevops.NewClientWithOptions(..., WithHTTPClient(shared))` with an empty `AuthorizationString`; auth lives only in ado's RoundTripper (the SDK is mostly dormant and has no transport hook).
- **`net/http`** raw client for `ado api` and for endpoints the SDK lacks. It shares auth, user-agent and debug-logging transport with the SDK.
- Every request sends `X-TFS-FedAuthRedirect: Suppress`; 401 and 203 (sign-in page) map to exit code 4.
- Target: Azure DevOps Services (`dev.azure.com`, `*.visualstudio.com`). Host is configurable to leave room for Azure DevOps Server later.

## CLI & UX Libraries
- **`spf13/cobra`**: command tree, help, completion (bash/zsh/fish/PowerShell).
- **`cli/go-gh/v2`** packages: `pkg/term` helpers only (`IsTerminal`/`IsColorDisabled`/`IsColorForced`, not `term.FromEnv`), `pkg/tableprinter`, `pkg/jq`, `pkg/template`, `pkg/browser` (launcher passed explicitly), `pkg/ssh` (SSH alias resolution). No `GH_*` env or gh config is read.
- **`itchyny/gojq`**: `--jq` (through go-gh).
- **`aymanbagabas/go-udiff`** v0.4.1 (MIT/BSD-3, a copy of `golang.org/x/tools/internal/diff`, no dependencies): unified diffs for `ado pr diff` outside a local clone.
  - *2026-10-11:* chosen for git-compatible unified hunks without a git binary; see `archive/pr_parity_20261010/research.md` q7.
- **`charm.land/huh/v2`**: interactive prompts on TTY, gated on `IOStreams.CanPrompt()`.
  - *2026-10-10:* chose huh v2 over `AlecAivazis/survey/v2` (archived); see `archive/mvp_core_20261010/research.md` q10.
- **`github.com/charmbracelet/glamour`** v0.10.0 (same version as gh / go-gh): renders README markdown in `ado repo view` on a TTY. Called directly, not through go-gh `pkg/markdown`; style from colour state plus `GLAMOUR_STYLE`, wrap width `min(terminal width, 120)`.
  - *2026-10-11:* added for `repo view`; `CGO_ENABLED=0` build passes, `bin/ado` grew from 25,183,608 B to 34,379,098 B (+9.2 MB, mostly chroma syntax highlighting); see `archive/repo_browse_20261010/research.md` o6/o7.
- **`golang.org/x/net/html`** (already in the module graph): small in-house HTML→text converter for work item fields stored as HTML (`<div>` lines, `<ol>` numbering, links, tables). **`github.com/yuin/goldmark`** v1.7.8 (already pulled in by glamour): Markdown→HTML when writing to a field kept in HTML format. Both become direct dependencies; no new modules.
  - *2026-10-11:* chosen over `k3a/html2text` (collapses ADO `<div>` lines, ignores tables) and `jaytaylor/html2text` (unpinned, extra deps); see `tracks/workitems_20261010/research.md` o9/o7.
- **`golang.org/x/sync`** (`errgroup`): bounded parallel blob downloads in `ado pr diff` outside a clone.
  - *2026-10-11:* already an indirect dependency; promoted to direct by the pr_parity review fixes.
- **`MakeNowJust/heredoc`**: help text and examples.

## Auth & Credentials
- **PAT** via `ado auth login --with-token` / prompt; env `ADO_TOKEN`, `AZURE_DEVOPS_EXT_PAT`.
- **Microsoft Entra ID** via **`AzureAD/microsoft-authentication-library-for-go`** (MSAL). Device-code and interactive browser flows, Azure DevOps resource scope `499b84ac-1321-427f-aa17-267ca6975798/.default`, MSAL token cache stored AES-GCM-encrypted in a 0600 file with only its key in the keychain.
  - *2026-10-10:* keychains cap item size (Windows 2560 B, macOS ~3 KB), so the cache can't live in one keychain item; see `archive/mvp_core_20261010/research.md` q2/o4.
- Entra client ID: built-in default (empty until the app is registered), overridable via `ADO_ENTRA_CLIENT_ID` / config `entra_client_id`.
- **`zalando/go-keyring`**: OS keychain storage (Windows Credential Manager, macOS Keychain, Secret Service); every call wrapped in a 60 s timeout with a 0600 config fallback; `--insecure-storage` skips the keychain.

## Config & Context
- YAML config at `$ADO_CONFIG_DIR`, else `$XDG_CONFIG_HOME/ado/config.yml` (`%AppData%\ado` on Windows) via `gopkg.in/yaml.v3`. Holds defaults, aliases, per-host settings; per-host keys use the original remote host (no normalization to dev.azure.com).
- Git context from shelling out to `git` (`git remote -v`, `git rev-parse`), same as gh. Parses `https://dev.azure.com/{org}/{project}/_git/{repo}`, `git@ssh.dev.azure.com:v3/{org}/{project}/{repo}`, `https://{org}.visualstudio.com/...`.
- Default repo per checkout via `remote.<name>.ado-resolved = base` (gh's mechanism, set by `ado repo set-default`); resolution order `-R` > `ADO_REPO` > `ado-resolved` > `origin` > `upstream` > first ADO remote. Global config `org` and per-host `git_protocol` (`https`|`ssh`).
- Project-scoped commands (`workitem`, `sprint`, `board`) also accept `-R ORG/PROJECT`; per-project `team` config key under the hosts-map scope `dev.azure.com/ORG/PROJECT` (`ado config set team X --project ORG/PROJECT`).
- Extensions: `ado-<name>` executables found on `PATH` / extension dir, gh model.

## Testing & Quality
- **`go test`** + **`stretchr/testify`**.
- HTTP stubbing via `net/http/httptest` and a gh-style `httpmock` registry (request matchers + canned responses). No live network in unit tests.
- IO streams abstraction (`iostreams`-style) so tests can force TTY/non-TTY and capture stdout/stderr.
- **`golangci-lint`**, `go vet`, `gofmt`. Coverage gate in CI.

## Build, Release & CI
- **GitHub Actions**: lint + test matrix (linux/macos/windows).
- **GoReleaser**: cross-compiled binaries (windows/darwin/linux × amd64/arm64), checksums, GitHub Releases, Homebrew tap, Scoop bucket, winget manifest.
- Version injected via `-ldflags`.

## Architecture (gh-inspired layout)
- `cmd/ado/`: main.
- `pkg/cmd/<group>/<verb>/`: one package per command (options struct + `RunE` + tests).
- `pkg/cmdutil/`: Factory (lazy IOStreams, config, HTTP/SDK clients, git context).
- `pkg/iostreams/`, `internal/config/`, `internal/auth/`, `internal/adocontext/` (remote parsing), `api/` (SDK wrappers + raw client).
