# Tech Stack

## Language & Runtime
- **Go** (latest stable; `go.mod` pins minimum version). Single static binary, `CGO_ENABLED=0` where possible.
- Module path: `github.com/<owner>/ado-gh`; binary name: `ado`.

## Azure DevOps API
- **`github.com/microsoft/azure-devops-go-api/azuredevops/v7`**: typed clients for Git (repos, PRs), Work Item Tracking, Build/Pipelines, Core (projects).
- **`net/http`** raw client for `ado api` and for endpoints the SDK lacks. It shares auth, user-agent and debug-logging transport with the SDK.
- Target: Azure DevOps Services (`dev.azure.com`, `*.visualstudio.com`). Host is configurable to leave room for Azure DevOps Server later.

## CLI & UX Libraries
- **`spf13/cobra`**: command tree, help, completion (bash/zsh/fish/PowerShell).
- **`cli/go-gh/v2`** packages: `pkg/term` (TTY detection), `pkg/tableprinter`, `pkg/jq`, `pkg/template`, `pkg/browser`, plus gh-compatible output behavior.
- **`itchyny/gojq`**: `--jq` (through go-gh).
- **`charm.land/huh/v2`**: interactive prompts on TTY, gated on `IOStreams.CanPrompt()`.
  - *2026-10-10:* chose huh v2 over `AlecAivazis/survey/v2` (archived); see `tracks/mvp_core_20261010/research.md` q10.
- **`MakeNowJust/heredoc`**: help text and examples.

## Auth & Credentials
- **PAT** via `ado auth login --with-token` / prompt; env `ADO_TOKEN`, `AZURE_DEVOPS_EXT_PAT`.
- **Microsoft Entra ID** via **`AzureAD/microsoft-authentication-library-for-go`** (MSAL). Device-code and interactive browser flows, Azure DevOps resource scope `499b84ac-1321-427f-aa17-267ca6975798/.default`, MSAL token cache persisted in the keychain.
- **`zalando/go-keyring`**: OS keychain storage (Windows Credential Manager, macOS Keychain, Secret Service).

## Config & Context
- YAML config at `$XDG_CONFIG_HOME/ado/config.yml` (`%AppData%\ado` on Windows) via `gopkg.in/yaml.v3`. Holds defaults, aliases, per-host settings.
- Git context from shelling out to `git` (`git remote -v`, `git rev-parse`), same as gh. Parses `https://dev.azure.com/{org}/{project}/_git/{repo}`, `git@ssh.dev.azure.com:v3/{org}/{project}/{repo}`, `https://{org}.visualstudio.com/...`.
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
