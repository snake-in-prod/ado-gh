# Plan: Pull Request Command Group — Full gh Parity

Track: `pr_parity_20261010` · Spec: [spec.md](./spec.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: PR State Changes (close, reopen, ready)

- [ ] Task: Shared PR status update helper (`pkg/cmd/pr/shared`)
    - [ ] Write tests: update status/isDraft via SDK `UpdatePullRequest`, optional pre-comment thread, state guard errors (completed PR, already in target state → warning)
    - [ ] Implement helper reusing the MVP PR finder and API layer
- [ ] Task: `ado pr close`
    - [ ] Write tests: abandons PR, `--comment` posts thread before abandon, TTY confirmation (accept / decline → exit 2), non-TTY without `--yes` refuses, `-y` skips prompt, already abandoned → warning exit 0, completed → exit 1
    - [ ] Write tests: `-d` deletes remote source branch (refs update to zero OID), deletes local tracking branch, switches to default branch when it's checked out (stubbed git runner), skips remote delete for fork PRs with a warning
    - [ ] Implement `pr close`
- [ ] Task: `ado pr reopen`
    - [ ] Write tests: abandoned → active, `--comment`, already active → warning exit 0, completed → exit 1, deleted source branch → actionable error
    - [ ] Implement `pr reopen`
- [ ] Task: `ado pr ready`
    - [ ] Write tests: draft → published, `--undo` → draft, already in target state → message exit 0, abandoned/completed → exit 1
    - [ ] Implement `pr ready`
- [ ] Task: Conductor - User Manual Verification 'Phase 1: PR State Changes (close, reopen, ready)' (Protocol in workflow.md)

## Phase 2: `ado pr edit`

- [ ] Task: Field edits (title, body, base)
    - [ ] Write tests: `-t`, `-b`, `-F` (file and stdin), `-B` retargets `targetRefName`, only changed fields sent in PATCH
    - [ ] Implement core edit command
- [ ] Task: Reviewers
    - [ ] Write tests: `--add-reviewer`/`--remove-reviewer` (repeatable + comma lists), users and `[Project]\Team` groups resolved via MVP identity resolver, `--add-required-reviewer` adds new and promotes existing optional, unknown identity error
    - [ ] Implement reviewer add/remove/required via PR reviewers API
- [ ] Task: Work items and labels
    - [ ] Write tests: `--add-work-item`/`--remove-work-item` link/unlink artifact links, `--add-label` (creates tag on demand)/`--remove-label`, not-found label removal warns
    - [ ] Implement via work item relations + PR labels APIs
- [ ] Task: Interactive mode and partial failure reporting
    - [ ] Write tests: TTY with no flags → multi-select prefilled with current values, non-TTY with no flags → error listing flags, partial failure exits 1 naming applied vs failed edits, prints PR URL
    - [ ] Implement prompts and result reporting
- [ ] Task: Conductor - User Manual Verification 'Phase 2: ado pr edit' (Protocol in workflow.md)

## Phase 3: `ado pr comment` and Thread Visibility

- [ ] Task: Body input helper
    - [ ] Write tests: `-b`, `-F` file / `-` stdin, `-e` editor, TTY with no body opens editor, non-TTY with no body fails naming `--body`, empty body rejected
    - [ ] Implement (reuse MVP `pr create` editor/body-file code; extract to `shared` if not already)
- [ ] Task: New thread, reply, thread status
    - [ ] Write tests: new thread, `--thread <id>` + body replies, `--thread <id> --status fixed` updates status (with and without body), `--status` without `--thread` usage error, invalid status value error, prints comment URL, `-w` opens browser
    - [ ] Implement via threads/comments APIs
- [ ] Task: Inline file comments
    - [ ] Write tests: `--file --line n` and `n:m` build `threadContext` (right/left side), `iterationContext` from latest iteration, `--line` without `--file` usage error, bad range error
    - [ ] Implement inline thread creation
- [ ] Task: `--edit-last` / `--delete-last`
    - [ ] Write tests: finds signed-in user's most recent non-deleted comment across threads, edit replaces content, delete confirms on TTY, non-TTY without `--yes` refuses, no prior comment → error
    - [ ] Implement
- [ ] Task: Extend `ado pr view --comments`
    - [ ] Write tests: thread ID, status, `file:line` anchor and replies in TTY output; `threads` JSON field shape (`id,status,filePath,line,comments[...]`); system threads (vote/policy) hidden
    - [ ] Implement view extension
- [ ] Task: Conductor - User Manual Verification 'Phase 3: ado pr comment and Thread Visibility' (Protocol in workflow.md)

## Phase 4: `ado pr diff`

- [ ] Task: Record diff dependency
    - [ ] Add Go unified-diff library (e.g. `aymanbagabas/go-udiff`) to `tech-stack.md` with dated note, then `go get`
- [ ] Task: Local clone path
    - [ ] Write tests (stubbed git runner): fetches source and target refs from the ADO remote, diffs merge-base..source, `--name-only` uses `--name-only`, `--patch` uses `format-patch --stdout`, `--color` auto/always/never maps to git `--color`, `NO_COLOR` respected
    - [ ] Implement clone path
- [ ] Task: API fallback path
    - [ ] Write tests (httpmock): latest iteration changes → per-file unified hunks from base/source blobs, add/delete/rename/edit headers, binary detection message, `--name-only` from change list, `--patch` outside clone errors, color applied only on TTY/`always`
    - [ ] Implement API path and ANSI colorizer for generated diffs
- [ ] Task: `-w/--web` and path selection
    - [ ] Write tests: `--web` opens Files tab URL; clone path chosen when cwd repo matches PR repo, API path otherwise
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 4: ado pr diff' (Protocol in workflow.md)

## Phase 5: `ado pr checks`, `ado pr status`, Docs

- [ ] Task: Exit code 8
    - [ ] Write tests: `PendingError` → exit 8 in main exit-code mapping
    - [ ] Implement; add code 8 to `product-guidelines.md` exit-code table
- [ ] Task: Checks aggregation (`pkg/cmd/pr/shared/checks.go`)
    - [ ] Write tests: policy evaluations (artifact ID `vstfs:///CodeReview/CodeReviewId/{projectId}/{prId}`) and PR statuses merged; statuses deduped to latest per `genre/name`; state → bucket table (approved/succeeded → pass, rejected/broken/failed/error → fail, queued/running/notSet/pending/expired → pending, notApplicable → skipping); `isRequired` from policy `isBlocking`
    - [ ] Implement aggregation (used by `checks` and `status`)
- [ ] Task: `ado pr checks`
    - [ ] Write tests: TTY table and non-TTY TSV, `--required` filter, exit 0/1/8 fixtures, no checks → message exit 0, `--json` fields, `--web`
    - [ ] Write tests: `--watch` polls with `--interval` (fake clock), stops when nothing pending, `--fail-fast` stops on first failure, TTY redraw vs non-TTY final-only output
    - [ ] Implement `pr checks`
- [ ] Task: `ado pr status`
    - [ ] Write tests: current-branch section, created-by-me, needs-review (direct + group membership, unvoted only), `--project` widens to all repos and shows repo name, `-c` merge status, checks summary + review decision per row, empty sections messages, `--json` object `{currentBranch, createdBy, needsReview}`
    - [ ] Implement `pr status`
- [ ] Task: Docs
    - [ ] Update `docs/gh-differences.md` with every divergence listed in the spec
    - [ ] Verify help text/examples for all eight new commands (`ado pr <cmd> --help`)
- [ ] Task: Conductor - User Manual Verification 'Phase 5: ado pr checks, ado pr status, Docs' (Protocol in workflow.md)
