# Spec: Repository Commands and Browsing — gh Parity

## Overview
Add the `ado repo` command group (`list`, `view`, `clone`, `create`, `delete`, `restore`, `set-default`) and a top-level `ado browse`. Make sure every command whose object has an Azure DevOps web page supports `-w/--web`. Builds on the Factory, context resolution, config, exporter, prompter and confirmation helpers from `mvp_core_20261010`, which must be complete first.

## User Stories
1. As a developer, I run `ado repo list`, `ado repo view` and `ado repo clone api` to find and grab a repo without the web UI.
2. As a team lead, I run `ado repo create svc --add-readme --gitignore Go --clone` to start a new repo in one step.
3. As a developer with several remotes, I run `ado repo set-default` once so every command targets the right repo.
4. As anyone, I run `ado browse pkg/api.go:42`, `ado browse !123` or `ado browse #456` to jump to the right page, or use `-n` to just get the URL.

## Functional Requirements

### FR1: `ado repo list [<org>[/<project>]]`
- No argument: repos in the current project (from context). `<org>`: every repo across all projects in that org (the name column shows `project/repo`). `<org>/<project>`: that project.
- `-L/--limit` (default 30), sorted by name.
- Filters: `--disabled` / `--no-disabled` (ADO `isDisabled`; hidden aliases `--archived` / `--no-archived`), `--fork` / `--source` (`isFork`; mutually exclusive), `--match <glob>` (client-side on name).
- TTY table: name (`project/repo` when org-wide), default branch, info (`fork`, `disabled`, `empty` for a repo with no commits), size. Non-TTY: TSV. An empty repo has JSON `defaultBranch` `""` (o5).
- `--json/--jq/--template` fields: `id,name,project,defaultBranch,size,isDisabled,isFork,remoteUrl,sshUrl,webUrl`. `parentRepository` is only in `repo view` (the list API doesn't return it) (o4).
- Empty result: `no repositories match your search` to stderr, exit 0.

### FR2: `ado repo view [<repo>]`
- `<repo>` forms: `repo`, `project/repo`, `org/project/repo`, URL. Default: current repo.
- Shows name, project, default branch, size (human-readable), HTTPS/SSH clone URLs, web URL, fork parent, disabled marker.
- README: finds a root `README*` (case-insensitive) on the default branch (`-b/--branch` overrides); a markdown extension (`.md`, `.markdown`, `.mdown`, `.mkdown`) wins, then the first in sorted order (o8). Rendered with `charmbracelet/glamour` on TTY, raw when piped. Style from the colour state plus `GLAMOUR_STYLE`; wrap width `min(terminal width, 120)` (o7). Missing README: `This repository does not have a README`. Disabled repo: metadata only, with a note.
- `-w/--web`. `--json/--jq/--template` fields: FR1 fields plus `readme`.

### FR3: `ado repo clone <repo> [<directory>] [-- <gitflags>...]`
- `<repo>` forms as FR2. Bare `repo` resolves org/project from current context, then `ADO_ORG`/`ADO_PROJECT`, then the global config key `org` (`ado config set org <org>`) (o2). Fails naming the missing piece if it can't resolve.
- Protocol from config `git_protocol` (`https` | `ssh`, per host, default `https`). New config key, validated by `ado config set`. Full URLs are cloned as given.
- Everything after `--` is passed straight to `git clone`.
- Default directory: the decoded repo name (`My Repo`), passed to git explicitly; `<name>.git` with `--bare`/`--mirror` (o13). `.`/`..` and path separators in `<directory>` are rejected.
- An HTTPS URL that contains a password is refused with a credential-manager hint and is never echoed (o14).
- Fork: adds the parent as a remote (`-u/--upstream-remote-name`, default `upstream`) and fetches it. Unlike gh it sets no `ado-resolved` default and has no `--no-upstream` (o12).
- HTTPS auth relies on the user's git credential setup (e.g. Git Credential Manager). `ado` never puts tokens in clone URLs.

### FR4: `ado repo create [<name>]`
- `-p/--project` (default current project), `--add-readme`, `-g/--gitignore <template>` (from ADO's gitignore templates API; an unknown name errors and lists close matches), `--clone` (clone into `./<name>` after creation).
- README and/or .gitignore go in one initial commit on `main` via the Pushes API. README content is `# <name>\n` (o9); .gitignore uses the server-side template.
- Initial push failure: warning on stderr, web URL still printed, exit 1; the empty repo is kept (o10).
- TTY with no name: prompts for name, project, README, gitignore and clone. Non-TTY with no name: fails naming `<name>`.
- Prints the web URL. Name already taken (`TF400948`, HTTP 409): error, exit 1.

### FR5: `ado repo delete [<repo>]`
- Default: soft delete (moves the repo to the project recycle bin). Prints `Deleted repository project/repo (restorable for 30 days: ado repo restore <name>)` (o11).
- `--permanent`: also purges it from the recycle bin (irreversible).
- TTY: confirm by typing the repo name (gh style); a mismatch cancels with exit 2. `-y/--yes` skips the prompt. Non-interactive without `--yes` refuses.
- Default target: current repo.

### FR5a: `ado repo restore <repo>` (o11)
- Restores a soft-deleted repo from the project recycle bin, by name or ID. Project from context or `[org/]project/repo`.
- Not in the recycle bin: not-found error, exit 1. Permission errors (403) passed through. Prints the restored repo and its web URL.

### FR6: `ado repo set-default [<repo>]`
- Pins the default repo for this checkout in git config (`remote.<name>.ado-resolved = base`, gh's mechanism). `<repo>` must match a configured ADO remote. A single-segment argument is read as a remote name first, then as a repo name (o16). `-R/--repo` is rejected (o17).
- TTY with no argument and several ADO remotes: select prompt. `--view`: print the current default; with none set, print a message on stderr and exit 0 (o15). `-u/--unset`: remove it.
- Context resolution order (amends MVP FR3): `-R` > `ADO_REPO` > `ado-resolved` remote > `origin` > `upstream` > first ADO remote.

### FR7: `ado browse [<target>]`
- No target: repo home. `<target>`:
  - `!123`: PR page. Bare number `123` is also a PR.
  - `#456`: work item page (`_workitems/edit/456`).
  - 7–40 hex chars: commit page.
  - Path: file/folder view. Relative to cwd inside a clone, normalized to repo root. `path:42` highlights a line, `path:10-20` a range.
- `-b/--branch <name>`: view files on that branch. Without `-b`/`-c` file URLs carry no `version`, so the web UI uses the default branch and `-n` needs no API call (o20). `-c/--commit [sha]`: view at a commit (no value = local `HEAD`). `--branch` and `--commit` are mutually exclusive.
- Section flags (mutually exclusive with `<target>` and each other): `-s/--settings` (repo settings; one API call for the repo ID, falling back to the project-level settings page) (o18), `-w/--wiki` (o19), `--pipelines`, `--boards`, `--prs` (repo PR list), `--branches`.
- `-n/--no-browser`: print the URL to stdout instead of opening it. Otherwise prints `Opening <url> in your browser.` (full URL) to stderr on TTY (o3).
- Works with `-R` outside a clone. Path targets with `-R` are taken as repo-root paths.
- Browser: `ADO_BROWSER` env, then config `browser` key, then `BROWSER` env, then OS default (go-gh `pkg/browser`) (o1).

### FR8: `-w/--web` coverage
- Shared helper `cmdutil.OpenInBrowser(f, url)` with gh output behavior, used by every `--web` flag.
- Audit: every existing command whose object has a web page gets `-w/--web`: `repo view`, `pr list/view/create` (MVP), `pr diff/comment/checks` (pr_parity, if implemented). Commands without a natural page are excluded by exact command path with a reason: each `auth` and `config` subcommand, `api`, `completion`, pr commands without a page, `repo list/clone/create/delete/restore/set-default` (o21), and `browse`, which opens the browser by default and uses `-w` for `--wiki` (o22).
- A test walks the cobra tree and checks `--web` against an allowlist, so future groups (workitem, pipeline) must opt in or be added to the exclusion list.

## Non-Functional Requirements
- **New dependency:** `charmbracelet/glamour` v0.10.0 (same as gh), recorded in `tech-stack.md` before use (o6).
- **Output contract:** `repo list` and `repo view` support `--json/--jq/--template`. New field names are public API.
- **Safety:** `repo delete` confirms on TTY, needs `--yes` when non-interactive, and `--permanent` is spelled out in the confirmation text.
- **Security:** tokens never go into clone URLs, remote URLs or browse URLs.
- **Testing:** >90% coverage. HTTP via httpmock, git via the command-runner stub, browser via a stub launcher. TTY and non-TTY covered.
- **Docs:** `docs/gh-differences.md` updated: `repo list <org>` scope, disabled vs archived, missing visibility/description/topics, recycle-bin delete + `--permanent`, `browse` `!`/`#` prefixes and ADO section flags, no `repo fork`/`--source` create, clone auth via credential manager.

## Acceptance Criteria
- [ ] `ado repo list`, `ado repo list myorg --match 'svc-*' --source --json name,project` behave as specified.
- [ ] `ado repo view` shows rendered README on TTY and raw when piped. `-w` opens the repo.
- [ ] `ado repo clone proj/api -- --depth 1` clones over HTTPS. After `ado config set git_protocol ssh` it uses SSH. Cloning a fork adds `upstream`.
- [ ] `ado repo create svc --add-readme -g Go --clone` creates a repo with one initial commit and clones it.
- [ ] `ado repo delete svc` needs the typed name on TTY. Non-TTY without `-y` refuses. `--permanent` purges.
- [ ] `ado repo set-default` changes which remote commands use. `--view` and `--unset` work.
- [ ] `ado browse`, `browse pkg/a.go:42`, `browse !12`, `browse #456`, `browse abc1234`, `browse --wiki -n` produce correct URLs.
- [ ] Web-flag audit test passes. `docs/gh-differences.md` updated. Coverage >90%. CI green on linux/macos/windows.

## Out of Scope
- `repo fork`, `repo rename`, `repo edit`, `repo sync`, `repo archive` (disable/enable). (`repo restore` moved in scope by o11.)
- `repo create --source/--push/--remote` (publishing an existing local repo).
- `ado auth setup-git` / git credential helper.
- `repo gitignore list` / `repo license` subcommands.
