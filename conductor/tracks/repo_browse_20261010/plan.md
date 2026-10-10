# Plan: Repository Commands and Browsing — gh Parity

Track: `repo_browse_20261010` · Spec: [spec.md](./spec.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Repo Foundation, `repo list`, `repo view`

- [ ] Task: Repo reference parsing and API layer (`api/repo.go`, `pkg/cmd/repo/shared`)
    - [ ] Write tests: parse `repo`, `project/repo`, `org/project/repo`, URLs (dev.azure.com, visualstudio.com, ssh); bare name filled from context → `ADO_ORG`/`ADO_PROJECT` → config default org; missing piece error names it
    - [ ] Implement parser and SDK wrappers (get/list repos by project and org, items, recycle bin, templates, pushes)
- [ ] Task: Shared browser helper (`cmdutil.OpenInBrowser`)
    - [ ] Write tests: TTY prints `Opening <url> in your browser.` to stderr, non-TTY silent, `BROWSER` env > config `browser` > OS default, launcher errors surfaced (stub launcher)
    - [ ] Implement with go-gh `pkg/browser`; add `browser` config key
- [ ] Task: `ado repo list`
    - [ ] Write tests: current project default, `<org>` org-wide (project column), `<org>/<project>`, `--limit`, name sort, `--disabled/--no-disabled` + hidden `--archived` aliases, `--fork`/`--source` exclusive, `--match` glob, TTY table vs TSV, `--json` fields, empty-result message
    - [ ] Implement `repo list`
- [ ] Task: Record glamour dependency
    - [ ] Add `charmbracelet/glamour` to `tech-stack.md` with dated note, then `go get`
- [ ] Task: `ado repo view`
    - [ ] Write tests: metadata fields, human-readable size, README discovery (case-insensitive root `README*`), `--branch`, glamour on TTY vs raw piped, missing README message, disabled repo metadata-only, `--json` incl. `readme`, `-w`
    - [ ] Implement `repo view`
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Repo Foundation, repo list, repo view' (Protocol in workflow.md)

## Phase 2: `repo clone` and `repo create`

- [ ] Task: `git_protocol` config key
    - [ ] Write tests: per-host get/set, default `https`, invalid value rejected by `ado config set`
    - [ ] Implement key and validation
- [ ] Task: `ado repo clone`
    - [ ] Write tests (stubbed git runner): HTTPS vs SSH URL by protocol, full URL passed through as given, optional `<directory>`, args after `--` forwarded, fork adds `upstream` (and `-u` rename) + fetch, no token in any URL, git failure exit code propagated
    - [ ] Implement `repo clone`
- [ ] Task: `ado repo create`
    - [ ] Write tests: create in `--project`/current project, `--add-readme` and `-g` produce one initial push on `main`, unknown gitignore template lists close matches, `--clone` clones after create, name conflict → exit 1, TTY prompts, non-TTY missing name fails naming `<name>`, prints web URL
    - [ ] Implement `repo create`
- [ ] Task: Conductor - User Manual Verification 'Phase 2: repo clone and repo create' (Protocol in workflow.md)

## Phase 3: `repo delete` and `repo set-default`

- [ ] Task: `ado repo delete`
    - [ ] Write tests: soft delete + restore hint message, `--permanent` purges from recycle bin, TTY typed-name confirmation (match / mismatch → exit 2), confirmation text mentions permanence, `-y` skips, non-TTY without `--yes` refuses, default current repo
    - [ ] Implement `repo delete`
- [ ] Task: `ado repo set-default`
    - [ ] Write tests (stubbed git config): sets `remote.<name>.ado-resolved base` and clears others, repo must match an ADO remote, TTY select prompt with several remotes, `--view` (exit 1 when unset), `--unset`
    - [ ] Implement `repo set-default`
- [ ] Task: Context resolution update
    - [ ] Write tests: order `-R` > `ADO_REPO` > `ado-resolved` > `origin` > `upstream` > first ADO remote
    - [ ] Update MVP resolver
- [ ] Task: Conductor - User Manual Verification 'Phase 3: repo delete and repo set-default' (Protocol in workflow.md)

## Phase 4: `ado browse`, `--web` Audit, Docs

- [ ] Task: Browse target parsing and URL building
    - [ ] Write tests (table-driven): no target → repo home; `!123` and bare `123` → PR; `#456` → work item; 7–40 hex → commit; paths relative to cwd normalized to repo root; `:42` and `:10-20` line params; `-b` → `GB<branch>` version; `-c` with/without SHA → `GC<sha>` (local `HEAD`); `--branch`/`--commit` exclusive; URL-encoded names
    - [ ] Write tests: section flags `--settings/--wiki/--pipelines/--boards/--prs/--branches` URLs and mutual exclusion; `-R` outside clone; path with `-R` treated as repo-root
    - [ ] Implement URL builder
- [ ] Task: `ado browse` command
    - [ ] Write tests: `-n` prints URL to stdout and does not launch, default launches via `OpenInBrowser`
    - [ ] Implement `browse`
- [ ] Task: `--web` audit
    - [ ] Write tree-walking test: allowlisted commands expose `-w/--web`, exclusion list documented, unknown new commands fail the test
    - [ ] Add `-w` where missing and migrate existing `--web` flags to `OpenInBrowser`
- [ ] Task: Docs
    - [ ] Update `docs/gh-differences.md` with every divergence in the spec
    - [ ] Verify help text/examples for `repo *` and `browse`
- [ ] Task: Conductor - User Manual Verification 'Phase 4: ado browse, --web Audit, Docs' (Protocol in workflow.md)
