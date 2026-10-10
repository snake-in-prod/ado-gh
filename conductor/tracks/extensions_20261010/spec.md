# Spec: Aliases and Extensions — gh Parity

## Overview
Add user aliases (`ado alias`) and an extension system (`ado extension`, alias `ext`) that work like gh's. Aliases expand to `ado` commands or shell snippets and are stored in `config.yml`. Extensions are `ado-<name>` executables run as `ado <name>`. They are installed from GitHub or Azure DevOps (script, GitHub Release binaries, or Azure Artifacts universal packages) or from a local directory. Each one receives context through env vars and gets the auth token only with explicit consent. Builds on the Factory, config, auth, prompter and git runner from `mvp_core_20261010`, which must be complete first.

## User Stories
1. As a developer, I run `ado alias set prs 'pr list -a @me'` and `ado alias set --shell co 'ado pr list --json id -q ".[0].id" | xargs ado pr checkout'`.
2. As a team, we publish `ado-lint` on Azure DevOps with binaries in an Azure Artifacts feed. Teammates run `ado extension install https://dev.azure.com/org/tools/_git/ado-lint` and then `ado lint`.
3. As an enterprise admin, I set `extension_sources` so only approved hosts can be installed from.
4. As an author, I run `ado extension create mytool --precompiled=go`, follow `docs/extensions.md`, and publish.

## Functional Requirements

### FR1: `ado alias`
- `alias set <name> <expansion>` (`-` reads the expansion from stdin):
  - `$1..$n` are replaced by arguments. Arguments no placeholder uses are appended (gh behavior).
  - A non-shell expansion must start with a valid `ado` command.
  - `-s/--shell` stores the alias with a `!` prefix (gh format) and runs it through the shell. `$1..$n` are positional parameters.
  - `--clobber` overwrites an existing alias.
- **Shell:** config `alias_shell`. Defaults: `sh` on Unix. On Windows, `sh` if on PATH (Git for Windows), else `pwsh`, then `powershell -NoProfile`. For PowerShell, `ado` replaces `$1..$n` with single-quoted escaped literals and exposes `$args`.
- **Shadowing:** names that match a built-in command or built-in alias (`pr`, `wi`, `issue`, `run`, …) are refused. Shadowing an installed extension gives a warning.
- `alias list`: TTY table, TSV when piped, `--json name,expansion,shell`.
- `alias delete <name>` / `--all`.
- `alias import [<file>|-]`: gh-format YAML map. Each entry is validated. `--clobber`. Prints imported and skipped entries with reasons.
- Stored under `aliases:` in `config.yml`. Expansion happens once (no recursion).

### FR2: Dispatch
- Order: built-in command > alias > installed extension > `ado-<name>` on `PATH`.
- `ado help` / `ado --help` list aliases and extensions in their own sections. Shell completion includes alias and extension names.
- Global `-R` given before the extension name is passed on as `ADO_REPO`. Arguments after the name pass through untouched. stdio is inherited and the exit code is passed through.

### FR3: Extension contract (env)
- Always set: `ADO_HOST`, `ADO_ORG`, `ADO_PROJECT`, `ADO_REPO` (empty when context can't be resolved; failing to resolve is not fatal), `ADO_EXTENSION_NAME`, `ADO_EXECUTABLE` (path of the running `ado`, for calls like `$ADO_EXECUTABLE api …`), plus the TTY/color env so the extension can match `ado`'s output behavior.
- `ADO_TOKEN` only if the extension's manifest declares `token: true` and the user consented (FR5).
- Speed bump: `ado auth token` refuses to run when `ADO_EXTENSION_NAME` is set and that extension has no consent. Documented as a convention, not a security boundary, because extensions run with the user's privileges.

### FR4: Extension layout & manifests
- Install dir: `$XDG_DATA_HOME/ado/extensions/ado-<name>/` (`%LocalAppData%\ado\extensions` on Windows).
- **Author manifest** `ado-extension.yml` at the repo root (optional for plain scripts): `name`, `description`, `token: bool`, `binaries: {github-releases: true} | {azure-artifacts: {organization, feed, package}}`, `minAdoVersion`.
- **Install record** `.ado-install.yml` written by `ado`: source URL, host, kind (`script|binary|local`), tag/commit/version, `pinned`, `tokenConsent`, verified checksum.
- Repo names must start with `ado-`. The extension name is the part after the prefix. Names that collide with core commands or built-in aliases are refused.

### FR5: `ado extension install <source>`
- **Sources:** `owner/repo` (github.com), GitHub URL, Azure DevOps URL (`dev.azure.com`, `ssh.dev.azure.com`, `*.visualstudio.com`), or a local path (`.`).
- **Kinds:**
  - **Script:** git clone. The repo root must contain an executable `ado-<name>`. On Windows: `ado-<name>.ps1` / `.cmd` if present, else run through `sh`.
  - **Binary (GitHub):** Release asset `ado-<name>-<os>-<arch>[.exe]` plus `checksums.txt` (SHA-256, goreleaser format).
  - **Binary (Azure DevOps):** Azure Artifacts universal package named in the manifest. The version matches the tag, otherwise the latest. Same asset names and `checksums.txt`. Downloaded through Microsoft's ArtifactTool, which `ado` fetches from `_apis/clienttools/ArtifactTool/release` into a per-OS/arch cache.
  - **Local:** symlink to the directory (on Windows without symlink rights, a pointer file). No checksum or confirmation.
- No binary for the current OS/arch: error listing the available ones.
- `--pin <tag|commit|version>` installs that ref and marks it pinned.
- **Checksums:** binaries are verified against `checksums.txt`, and a mismatch aborts. A missing `checksums.txt` refuses unless `--allow-unverified`, which also shows a warning on TTY.
- **Confirmation:** TTY shows source URL, resolved tag/commit/version, kind, checksum status and token request before installing. Non-interactive requires `--yes`.
- **Token consent:** if the manifest says `token: true`, TTY asks a separate yes/no question (default no). Non-TTY needs `--allow-token`. Without consent the extension installs with a warning that it may not work.
- **Allowlist:** config `extension_sources` (glob list, e.g. `github.com/myco/*`, `dev.azure.com/myorg/*`, `local`). When set, sources outside it are refused, including during upgrade.
- **Auth:** GitHub API calls use `GH_TOKEN`/`GITHUB_TOKEN` if set (rate limits). The ADO token is never sent to non-ADO hosts. Private ADO clones pass the token through `GIT_CONFIG_*` env vars (not argv, not persisted). ArtifactTool gets it through its env var.
- Already installed: error unless `--force`.

### FR6: `ado extension list | remove | exec`
- `list`: name, source, kind, version/commit, pinned marker, token marker. TSV when piped. `--json name,source,kind,version,pinned,tokenConsent,path`.
- `remove <name>`: deletes the install dir (no prompt, gh parity).
- `exec <name> [args…]`: runs an installed extension directly, even when an alias shadows it.

### FR7: `ado extension upgrade [<name>] | --all`
- Binary: latest release / package version. Script: fast-forward to the remote default branch HEAD. Local: skipped.
- Pinned: skipped with a note unless `--force`.
- `--dry-run` shows `name: v1 → v2` without changing anything. Same checksum, allowlist and confirmation rules as install. If a new version newly asks for the token, consent is asked again.
- With `--all`, any failure exits 1 with a per-extension summary.

### FR8: `ado extension create [<name>]`
- `--precompiled=go|other`. Default is a script extension.
- **Script:** `ado-<name>` with a shebang, `ado-extension.yml`, README.
- **Go:** `main.go` (reads the FR3 env vars, calls back through `ADO_EXECUTABLE`), `go.mod`, `ado-extension.yml`, `.github/workflows/release.yml` (cross-compile + checksums to GitHub Releases), and `azure-pipelines.yml` (cross-compile + checksums published as an Azure Artifacts universal package).
- **Other:** README describing asset naming plus the two pipelines without Go steps.
- Runs `git init` and makes an initial commit. Prompts for name and type on TTY; non-TTY without a name fails naming `<name>`. Prints next steps.

### FR9: `ado extension search [<query>]` / `browse`
- `search`: GitHub repo search `topic:ado-extension` plus the query. `--owner`, `--sort stars|updated`, `-L/--limit` (default 30), `-w/--web`. TTY table with an installed ✓ mark. `--json fullName,description,stars,updatedAt,installed,url`.
- `browse`: TTY-only interactive select over search results. Picking one shows its README (glamour) and offers install / remove / open in browser / back. Non-TTY: `browse requires a terminal; use 'ado extension search'`.

## Non-Functional Requirements
- **Security:** checksum verification, allowlist, confirmation, token consent, and no token in argv/URLs/logs or to non-ADO hosts. Tests cover each, including path traversal in archive/package contents.
- **Cross-platform:** dispatch, shell aliases, symlink fallback and script execution are tested on linux/macos/windows CI.
- **Dependencies:** no new Go modules. GitHub API via `net/http`. huh/glamour as already recorded. ArtifactTool is an external binary downloaded at runtime and is recorded in `tech-stack.md`.
- **Testing:** >90% coverage. httpmock for GitHub/ADO/Artifacts APIs, stubbed process runner for git/ArtifactTool/shell, `t.TempDir` data dirs.
- **Docs:** `docs/extensions.md` (author contract: naming, manifest schema, env vars, token consent, asset naming + checksums, GitHub Releases and Azure Artifacts publishing, exit codes, `ado-extension` topic, versioning/pinning) and `docs/gh-differences.md` updates (Windows shell aliases, token consent, Azure Artifacts binaries, allowlist, PATH extensions).

## Acceptance Criteria
- [ ] `ado alias set prs 'pr list -a $1'` then `ado prs @me` works. `--shell` aliases run on Linux (sh) and Windows (sh or PowerShell). `ado alias set pr x` is refused. `alias import` and `delete --all` work.
- [ ] Installing a GitHub script extension, a GitHub binary extension (checksum verified), an ADO-hosted extension with an Azure Artifacts binary, and a local dir each work. `ado <name>` runs and sees the context env vars.
- [ ] Token-requesting extensions get `ADO_TOKEN` only after consent. Without it, `ado auth token` refuses inside the extension.
- [ ] A checksum mismatch aborts. A missing checksum needs `--allow-unverified`. `extension_sources` blocks other hosts. Non-TTY install without `--yes` refuses.
- [ ] `upgrade --all --dry-run` lists changes. Pinned extensions are skipped.
- [ ] `create --precompiled=go` scaffolds a project that builds and that `install .` runs.
- [ ] `search` and `browse` work. Docs written. Coverage >90%. CI green on linux/macos/windows.

## Out of Scope
- A Go SDK library for extension authors (like go-gh). Extensions read env vars and call `$ADO_EXECUTABLE`.
- Extension sandboxing, signature/attestation verification (cosign, Sigstore), private search over Azure DevOps.
- Building Go-source extensions locally during install.
- Full split-pane TUI for `browse`.
