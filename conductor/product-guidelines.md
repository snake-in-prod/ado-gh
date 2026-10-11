# Product Guidelines

## 1. gh Parity First
- Mirror `gh` command structure, verbs and flags: `ado <noun> <verb> [args] [flags]`.
- When `gh` has an equivalent, reuse its name and flag spelling (`-R/--repo`, `-w/--web`, `-L/--limit`, `-s/--state`, `--json`, `--jq`, `-t/--template`, `-y/--yes`).
- Diverge only where Azure DevOps concepts truly differ, and document every divergence:
  - Work items replace issues (`ado workitem`, alias `ado issue` for muscle memory).
  - PR votes (approve / approve-with-suggestions / wait / reject) extend `gh pr review`.
  - Org + project + repo hierarchy (gh has owner/repo only). `-R` takes `org/project/repo` or `project/repo`.
- `gh pr merge` maps to ADO "complete"; keep `merge` as the primary verb and add `complete` as an alias.

## 2. Output
- **TTY-aware:** color, tables, relative times and spinners only on an interactive terminal. When piped: plain, tab-separated, absolute timestamps, no color.
- Respect `NO_COLOR`, `CLICOLOR_FORCE` and `ADO_FORCE_TTY`.
- Data to stdout. Progress, warnings and prompts to stderr.
- Every list/view command supports `--json <fields>`, `--jq` and `--template`. Running `--json` with no fields prints the available field names.

## 3. Interactivity
- On a TTY, prompt for missing required input (title, body via `$EDITOR`, reviewers, target branch), like `gh pr create`.
- Never prompt when stdin/stdout isn't a TTY or `ADO_PROMPT_DISABLED` / `CI` is set. Fail fast with a message naming the missing flag.
- Every prompt has a flag equivalent, so every flow can be scripted.

## 4. Errors & Exit Codes
- Errors say what failed and what to do next, e.g. `authentication required; run: ado auth login`.
- Exit codes: `0` success, `1` failure, `2` cancelled (user abort), `4` authentication required, `8` checks still pending (`ado pr checks`). Same as `gh`.
- Surface ADO API error messages but trim stack noise. Full details go behind `ADO_DEBUG=1` / `--debug`.

## 5. Safety
- Destructive or irreversible actions (abandon PR, delete repo/branch, merge/complete, cancel pipeline, delete work item) ask for confirmation on a TTY.
- `--yes` skips confirmation. In non-interactive mode without `--yes`, refuse and say why.

## 6. Security
- Tokens live only in the OS keychain (Windows Credential Manager, macOS Keychain, Secret Service). A plaintext config file is an explicit, warned fallback.
- `ADO_TOKEN` / `AZURE_DEVOPS_EXT_PAT` env vars are supported for CI.
- Tokens are never printed, logged or put in URLs. Debug/HTTP traces redact `Authorization` headers.
- `ado auth token` prints a token only on explicit request.

## 7. Compatibility Contract
- `--json` field names, exit codes and command/flag names are public API.
- Changes are additive only. Removal or rename needs at least one minor release of deprecation warnings.
- Follow SemVer. Record breaking changes in the changelog.

## 8. Voice & Help Text
- Terse, imperative, gh-style. Short description is one line, lowercase, no trailing period.
- Help leads with examples (`$ ado pr create --fill`). No marketing language.
- Messages use plain words: "Created pull request !123", not "Successfully created...".
