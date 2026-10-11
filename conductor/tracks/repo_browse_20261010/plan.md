# Plan: Repository Commands and Browsing — gh Parity

Track: `repo_browse_20261010` · Spec: [spec.md](./spec.md) · Research: [research.md](./research.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Repo Foundation, `repo list`, `repo view` [checkpoint: c2f44e1]

- [x] Task: `org` config key (o2) `c2f44e1`
    - [x] Write tests: global `org` key in `pkg/cmd/config/shared` Options (public, no per-host use), `ado config set/get/list org`, `TestFind` key list updated (options_test.go:26)
    - [x] Implement `org` Options entry
- [x] Task: Repo reference parsing and API layer (`internal/adocontext`, `api/repo.go`, `pkg/cmd/repo/shared`) `c2f44e1`
    - [x] Write tests: `ParseRepoRef(arg, base)` in `internal/adocontext` (reusing `parseRepoArg`/`validSegment`/`ParseRemoteURL`): parse `repo`, `project/repo`, `org/project/repo`, URLs (dev.azure.com, visualstudio.com, ssh); web URLs with `?path=`, `?version=`, a fragment or trailing sub-paths (`/pullrequest/N`, `/commit/<sha>`) cleaned before parsing; bare name inherits host/org/project from BaseRepo (incl. visualstudio.com) → `ADO_ORG`/`ADO_PROJECT` → config `org` (o2); missing piece error names it; input URL never echoed
    - [x] Write tests: repo and PR web URL helpers (`RepoURL`/`PRURL`) available from `internal/adocontext` so `repo` and `browse` don't import `pr/shared`
    - [x] Write tests: `api/repo.go` `RepoClient` following `PRClient`: `ListRepositories` via `GetRepositories` (nil Project = org-wide, no project segment), `Repository` via `GetRepositoryWithParent(IncludeParent=true)`, `RootItems(ctx, branch)` via `GetItems` (scopePath `/`, OneLevel, version descriptor only with `-b`), README content via existing `BlobContent(objectId)`; own JSON struct (nil → zero values), `refs/heads/` stripped
    - [x] Write tests: gitignore templates = raw GET `{project}/_apis/git/templates?type=gitignore&api-version=7.1` (or `sdk.Client.Send` with location f88d498e-52c3-422a-a5f2-994f4265a25b), names only; pushes via `CreatePush`; recycle bin wraps `DeleteRepository`, `DeleteRepositoryFromRecycleBin`, plus `GetRecycleBinRepositories` and `RestoreRepositoryFromRecycleBin` (PATCH `{"deleted": false}`) for `repo restore` (o11)
    - [x] Implement `ParseRepoRef` and move or wrap `prshared.RepoURL`/`PRURL` into `internal/adocontext`
    - [x] Implement `api/repo.go` wrappers; add recycle-bin location a663da97-81db-4eb3-8b83-287670f63073 (`{project}/_apis/git/recycleBin/repositories/{repositoryId}`) to httpmock
- [x] Task: Shared browser helper (`cmdutil.OpenInBrowser`) `c2f44e1`
    - [x] Write tests: TTY prints `Opening <url> in your browser.` with the full URL (o3) to stderr, non-TTY silent, launcher precedence `ADO_BROWSER` > config `browser` > `BROWSER` > OS default (o1), launcher errors surfaced (shared recording stub launcher)
    - [x] Write tests: pr `view`/`list`/`create`/`diff`/`comment`/`checks` `--web` keep their "Opening" assertions through `OpenInBrowser` and the shared stub
    - [x] Implement `OpenInBrowser(f, url)` in `pkg/cmdutil/browser.go` wrapping `f.Browser` (stdout-TTY check, message on stderr); the `browser` config key and go-gh `pkg/browser` launcher already exist
    - [x] Migrate the six pr `--web` call sites to `OpenInBrowser` and replace the per-file browser stubs with one shared recording stub
- [x] Task: `ado repo list` `c2f44e1`
    - [x] Write tests: current project default, `<org>` org-wide (project column, URL without project segment), `<org>/<project>`, `--limit`, name sort, filter → sort → limit client-side on one unpaged response, `--disabled/--no-disabled` + hidden `--archived` aliases, `--fork`/`--source` exclusive, `--match` glob, repos with null `isFork`/`isDisabled`/`size`/`defaultBranch`/`webUrl` render, empty repo shows `empty` in the info column and JSON `defaultBranch` `""` (o5), TTY table vs TSV, `--json` fields without `parentRepository` (o4), empty-result message
    - [x] Implement `repo list`
- [x] Task: Record glamour dependency `c2f44e1`
    - [x] Record `bin/ado` size before adding glamour (25,183,608 B)
    - [x] Update `tech-stack.md`: `github.com/charmbracelet/glamour` v0.10.0 (same as gh/go-gh), called directly, not through go-gh `pkg/markdown` (o6), with dated note; then `go get github.com/charmbracelet/glamour@v0.10.0`
    - [x] Check `CGO_ENABLED=0 go build ./...` passes and record `bin/ado` size after (34,379,098 B, +9.2 MB)
- [x] Task: Windows virtual terminal processing (`pkg/iostreams`) `c2f44e1`
    - [x] Write tests: VT processing enabled on stdout/stderr when colour is enabled on Windows; skipped when colour is disabled or output is not a TTY (stubbed console-mode hook)
    - [x] Implement VT enabling in iostreams (no-op on other platforms)
- [x] Task: `ado repo view` `c2f44e1`
    - [x] Write tests: metadata fields, human-readable size (small new formatter), README discovery (case-insensitive root `README*`, root folder entry filtered, markdown extension `.md`/`.markdown`/`.mdown`/`.mkdown` first, then first in sorted order (o8)), `--branch`, glamour on TTY vs raw piped, non-markdown README raw, BOM/CRLF stripped, style from colour state + `GLAMOUR_STYLE` only with detection only on a real TTY (o7), wrap `min(TerminalWidth, 120)` with no width override (o7), empty repo (nil `defaultBranch`, size 0) makes no Items call, listing 404 → missing README message, disabled repo metadata-only with no Items call, `--json` incl. `readme`, `-w` via `OpenInBrowser`
    - [x] Implement `repo view`
- [x] Task: Conductor - User Manual Verification 'Phase 1: Repo Foundation, repo list, repo view' (Protocol in workflow.md)

## Phase 2: `repo clone` and `repo create`

- [x] Task: `git_protocol` config key `29b9484`
    - [x] Write tests: per-host get/set (incl. the `ssh.dev.azure.com` vs `dev.azure.com` host-key case), default `https`, invalid value rejected by `ado config set`, `TestFind` key list updated
    - [x] Implement Options entry `{Key: "git_protocol", Default: "https", AllowedValues: ["https","ssh"]}`
- [x] Task: `ado repo clone` `9d7aba5`
    - [x] Write tests: `internal/git` streaming exec (stdin/stdout/stderr to IOStreams) with its own stub hook; `gittest.Runner` records the dir argument
    - [x] Write tests (stubbed git runner): HTTPS (`remoteUrl`, `org@` kept) vs SSH (`sshUrl`) by protocol, no token added to any URL, full URL passed through as given, HTTPS URL containing a password refused with a credential-manager hint and not echoed (o14), argv exactly `clone <gitflags> -- <url> <dir>` split via `ArgsLenAtDash`, repo names starting with `-`, optional `<directory>` (`.`/`..` and path separators rejected), default directory = decoded repo name passed explicitly (`My Repo`; `.git` suffix with `--bare`/`--mirror`) (o13), fork adds `upstream` (and `-u` rename) via `remote add -- <name> <url>` + `fetch <name>` in the clone dir with parent URL fallback via `RepositoryByID`, fork clone sets no `ado-resolved` and has no `--no-upstream` (o12), git failure → non-zero exit (1) with git's stderr shown and no URL echoed (`SilentError`)
    - [x] Implement streaming exec and Runner dir recording in `internal/git`
    - [x] Implement `repo clone`
- [x] Task: `ado repo create` `75d5691`
    - [x] Write tests: create in `--project`/current project, `--add-readme` and `-g` produce one initial push (one commit on `refs/heads/main`, 40-zero `oldObjectId`, README `newContent` rawtext `# <name>\n` (o9), .gitignore `newContentTemplate{name: "<T>.gitignore", type: "gitignore"}`), unknown gitignore template lists close matches (template names minus `.gitignore`, case-insensitive), `--clone` clones after create, name conflict (`TF400948` in `HTTPError.Message`) → exit 1, initial push failure → warning on stderr, URL still printed, exit 1, empty repo kept (o10), TTY prompts, non-TTY missing name fails naming `<name>`, prints web URL
    - [x] Implement `repo create`
    - [x] Note for Phase 2 manual verification: confirm `newContentTemplate` .gitignore expansion and the TF400948 HTTP status against a real organization — verified live 2026-10-11: Go template expanded server-side, README `# <name>\n`, TF400948 is HTTP 409, soft delete 204 → recycle bin
- [x] Task: Conductor - User Manual Verification 'Phase 2: repo clone and repo create' (Protocol in workflow.md)

## Phase 3: `repo delete`, `repo restore` and `repo set-default`

- [ ] Task: `ado repo delete`
    - [ ] Write tests: name → GUID via GetRepository, soft delete message points at `ado repo restore <name>` (o11), `--permanent` = soft delete then `DeleteRepositoryFromRecycleBin` with explicit Project, purge failure says the repo is still in the recycle bin, only-repo server error passed through (no pre-check), 200 and 204 accepted, disabled repos not blocked client-side, TTY typed-name confirmation (match / mismatch → exit 2), confirmation text mentions permanence, `-y` skips, non-TTY without `--yes` refuses, default current repo
    - [ ] Implement `repo delete`
- [ ] Task: `ado repo restore` (o11)
    - [ ] Write tests: restore from the recycle bin by name (looked up via `GetRecycleBinRepositories`) or by ID via `RestoreRepositoryFromRecycleBin` (PATCH `{"deleted": false}`), project from context or `[org/]project/repo`, not in recycle bin → not-found error, permission error (403) passed through, success message
    - [ ] Implement `repo restore`
- [ ] Task: Context resolution update
    - [ ] Write tests: `git.Remote.Resolved` and `git.Client.ResolvedRemotes` (Remotes + `config --get-regexp ^remote\..*\.ado-resolved$`, exit 1 = none, dotted remote names survive); `Resolved: "base"` rows in resolve_test; order `-R` > `ADO_REPO` > `ado-resolved` > `origin` > `upstream` > first ADO remote; `Remotes()` and `BaseRepoFromGit` unchanged
    - [ ] Add `Resolved`/`ResolvedRemotes`, update the MVP resolver and switch the `NewFactory` BaseRepo closure
- [ ] Task: `ado repo set-default`
    - [ ] Write tests (stubbed git config): `--unset-all remote.<n>.ado-resolved` on every resolved remote then plain `git config remote.<n>.ado-resolved base` (not `--add`; pre-2.46 `--get-regexp`/`--unset-all`), repo must match an ADO remote, single-segment argument read as remote name first, then `[[org/]project/]repo`/URL matched against ADO remotes (o16), single ADO remote auto-picked, TTY select prompt with several remotes, non-TTY without argument → FlagError, `-R/--repo` rejected with a FlagError (o17), `--view` prints `org/project/repo`, with no default prints a message on stderr and exits 0 (o15), `--unset`, outside a git repo → error
    - [ ] Move `PointsAt`/`MatchRemote` into `internal/adocontext` and implement `repo set-default`
- [ ] Task: Conductor - User Manual Verification 'Phase 3: repo delete, repo restore and repo set-default' (Protocol in workflow.md)

## Phase 4: `ado browse`, `--web` Audit, Docs

- [ ] Task: Browse target parsing and URL building
    - [ ] Write tests (table-driven): no target → repo home; `!123` and bare `123` → PR (numbers checked before the hex check, so `1234567` is a PR); `#456` → work item; 7–40 hex → commit (short-SHA handling); paths relative to cwd normalized to repo root; file URLs without `-b`/`-c` omit `version` (no API call, `-n` offline) (o20); `:42` → `line=42&lineEnd=43&lineStartColumn=1&lineEndColumn=1&lineStyle=plain`, `:10-20` → `lineEnd=21`; `-b` → `GB<branch>` version; `-c` with/without SHA → `GC<sha>` (local `HEAD`); `--branch`/`--commit` exclusive; number/SHA argument with `-b`/`-c` rejected; encoding of space/`&`/`+` in paths and `/` in branches (QueryEscape with `+` → `%20`), project/repo via PathEscape; URLs built from `RepoURL`/`PRURL`/`OrgURL`
    - [ ] Write tests: section flags `-s/--settings`, `-w/--wiki` (o19), `--pipelines/--boards/--prs/--branches` URLs and mutual exclusion, target argument rejected with a section flag; `--settings` does one authenticated GET for the repo ID → `_settings/repositories?repo=<id>`, falling back to project-level `_settings/repositories` when it fails (o18, httpmock); `-R` outside clone; path with `-R` treated as repo-root
    - [ ] Implement URL builder
- [ ] Task: `ado browse` command
    - [ ] Write tests: `-n` prints URL to stdout and does not launch, default launches via `OpenInBrowser` (stderr "Opening" line)
    - [ ] Implement `browse`
- [ ] Task: `--web` audit
    - [ ] Write tree-walking test in `pkg/cmd/root/root_test.go`: build with `NewCmdRoot(cmdutil.NewFactory(...))`, walk without Execute, runnable commands only, keyed by `CommandPath`; allowlist (`pr checks/comment/create/diff/list/view`, `repo view`) must expose `-w/--web`; exclusion list with reasons names every command by exact path, incl. each `auth` and `config` subcommand, `api`, `completion` (o21), pr commands without web, `repo list/clone/create/delete/restore/set-default`, and `ado browse` with reason "opens the browser by default (-n prints the URL)" (its `-w` is `--wiki`) (o22, o19); unlisted commands and stale entries fail
    - [ ] Add `-w/--web` where the allowlist requires it and confirm every `--web` path goes through `OpenInBrowser` (pr call sites migrated in Phase 1)
- [ ] Task: Docs
    - [ ] Update `docs/gh-differences.md` with every divergence in the spec, plus: "Opening" line on stderr with full URL (o3; gh uses stdout or stderr by command and strips the scheme); `ADO_BROWSER` precedence (o1); offline `-n` with no `version=` (o20); project-level `--wiki`/`--pipelines`/`--boards` and the `--settings` fallback (o18); browse `-w` means `--wiki` (o19); 30-day retention, `ado repo restore`, Delete repository permission, PAT `vso.code_manage`, only-repo rule; clone URL used as given, password URLs refused (o14), no credential-helper injection, no LFS over SSH, fork clone sets no default and has no `--no-upstream` (o12); set-default `-R` rejected (o17) and `--view` exit 0 (o15); `GLAMOUR_STYLE` only, width capped at 120 (o7)
    - [ ] Update spec.md for every divergence the research decisions create: FR1 `empty` info column (o5) and no `parentRepository` in `repo list` JSON (o4); FR2 README choice (o8) and glamour style/width (o7); FR3 global `org` config key replacing "config default org" (o2), default clone directory (o13), password URLs refused (o14), fork clone adds `upstream` only, no `ado-resolved`/`--no-upstream` (o12); FR4 README content (o9) and initial push failure behaviour (o10); FR5 restore hint `ado repo restore <name>` (o11), new `repo restore` requirement and its removal from Out of Scope (o11); FR6 `--view` with no default exits 0 (o15), argument order (o16), `-R` rejected (o17); FR7 browser precedence `ADO_BROWSER` > config `browser` > `BROWSER` > OS default (o1), full URL in the "Opening" line (o3), `-s/--settings` and `-w/--wiki` short flags (o19), `--settings` fallback (o18), no `version` without `-b`/`-c` (o20); FR8 exact-path audit lists and `browse` in the exclusion list (o21, o22); NFR glamour v0.10.0 (o6)
    - [ ] Verify help text/examples for `repo *` (incl. `repo restore`) and `browse`
- [ ] Task: Conductor - User Manual Verification 'Phase 4: ado browse, --web Audit, Docs' (Protocol in workflow.md)
