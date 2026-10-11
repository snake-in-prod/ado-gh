# Plan: Pull Request Command Group — Full gh Parity

Track: `pr_parity_20261010` · Spec: [spec.md](./spec.md) · Research: [research.md](./research.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: PR State Changes (close, reopen, ready) [checkpoint: 3906359]

- [x] Task: Shared test helpers (`internal/git/gittest`, `pkg/httpmock`) `3906359`
    - [x] Write tests: `gittest` stub runner maps joined args to stdout/error, fails unanswered `rev-parse`/`symbolic-ref`/`config --get` with exit 1, records calls, `Mutations()` filters repo-changing calls; `httpmock.StubIMS(org)` answers the identity lookups now copied in list_test.go/create_test.go
    - [x] Implement: hoist checkout's `coGit` into new `internal/git/gittest`, move the identity stub block into `pkg/httpmock` (`StubIMS(org)`), switch checkout/list/create tests to them
    - [x] Implement: add the missing location IDs to `httpmock.GitPRLocations` (refs, PR statuses, iterations, iteration changes, labels, PR work items, items, blobs, thread comments, PR commits, wit update)
- [x] Task: Shared PR status update helper (`pkg/cmd/pr/shared`) `3906359`
    - [x] Write tests (httpmock): `PRClient.Ref` (GetRefs `heads/<b>`, exact match on `refs/heads/<b>`, nil when absent) and `DeleteRef` (UpdateRefs with current tip as `oldObjectId`, 40-zero `newObjectId`; `succeeded`/`succeededCorruptRef` → success, `succeededNonExistentRef` → already deleted; `writePermissionRequired`, `forcePushRequired`, `locked`, `rejectedByPolicy`, `rejectedByPlugin`, `staleOldObjectId`, `invalidRefName` → mapped errors; both an HTTP error and a 200 `success:false` response)
    - [x] Write tests: update status/isDraft via SDK `UpdatePullRequest`, optional pre-comment thread, client-side guards from fetched `pr.Status`/`pr.IsDraft` (completed → exit 1; already in target state → yellow `!` warning on stderr, exit 0; ready on non-active → exit 1)
    - [x] Implement `Ref`/`DeleteRef` in `api/pr.go`
    - [x] Implement helper in new `pkg/cmd/pr/shared/state.go` (guards, optional `CreateThread`, then `UpdatePullRequest`) reusing the MVP PR finder and API layer; move the copied `deref` from list/merge/view there
- [x] Task: `ado pr close` `3906359`
    - [x] Write tests: abandons PR, `--comment` posts thread before abandon, TTY confirmation (accept / decline → exit 2), non-TTY without `--yes` refuses via `cmdutil.FlagErrorf("--yes required when not running interactively")` (usage printed, exit 1, like `pr merge`), `-y` skips prompt, already abandoned → warning exit 0, completed → exit 1
    - [x] Write tests: `-d` abandons first, then deletes the remote source branch (refs update to zero OID, tip from `Ref`, not `LastMergeSourceCommit`); deletes every local branch whose `branch.<name>.merge` is `refs/heads/<source>` (covers `pr checkout -b` and fork-prefixed names); switches to the default branch (from `c.Repository`) when one of them is checked out and reports a checkout failure (stubbed git runner); skips remote delete for fork PRs with a warning; remote delete failure after abandon (e.g. `writePermissionRequired`) → exit 1 naming the branch and the missing Force push permission, PR stays abandoned; remote branch already gone → reported, not an error
    - [x] Implement `pr close`: copy merge's `--yes`/`CanPrompt`/`Confirm`/`CancelError` pattern; local branch steps inline via `f.GitClient.Exec` (find branches by `branch.<name>.merge`, `checkout <default>`, `branch -D`), no new `internal/git` wrappers; register close/reopen/ready in pr.go
- [x] Task: `ado pr reopen` `3906359`
    - [x] Write tests: abandoned → active, `--comment`, already active → warning exit 0, completed → exit 1, deleted source branch (empty GetRefs stub, fork repo when `forkSource` is set) → actionable error with push hint and no PATCH sent
    - [x] Implement `pr reopen`: `Ref` pre-check in the source (or fork) repo before PATCH `status=active`
- [x] Task: `ado pr ready` `3906359`
    - [x] Write tests: draft → published, `--undo` → draft, `isDraft` sent in its own PATCH, already in target state → message exit 0, abandoned/completed → exit 1
    - [x] Write tests: `--undo` on a PR with votes confirms on TTY (accept / decline → exit 2), non-TTY without `--yes` refuses via `FlagErrorf`, `-y` skips the prompt; no votes → no prompt
    - [x] Implement `pr ready`
- [x] Task: Conductor - User Manual Verification 'Phase 1: PR State Changes (close, reopen, ready)' (Protocol in workflow.md)

## Phase 2: `ado pr edit` [checkpoint: 4f3ae83]

- [x] Task: Field edits (title, body, base) `4f3ae83`
    - [x] Write tests: `-t`, `-b`, `-F` (file and stdin), body over 4000 characters rejected, `-B` sends only `TargetRefName` (`refs/heads/` prefix) after checking the branch exists and isn't the source branch, unchanged `TargetRefName` in the response → error (retargeting disabled), no merge-status polling after retarget, only changed fields sent in PATCH (never reviewers/labels/work items)
    - [x] Implement core edit command
- [x] Task: Reviewers `4f3ae83`
    - [x] Write tests: `--add-reviewer`/`--remove-reviewer` (repeatable + comma lists), users and `[Project]\Team` groups resolved via MVP identity resolver, `--add-required-reviewer` adds new and promotes existing optional (added vs promoted from `pr.Reviewers`), promotion body has `isRequired:true` and no `vote`, promotion that resets an existing vote → promoted with a warning that the vote was reset, removing a non-reviewer → local warning, policy-required reviewer removal → partial failure, unknown identity error
    - [x] Implement reviewer add/remove/required via PR reviewers API: single-reviewer `CreatePullRequestReviewer` method (`IsRequired: true`, no `Vote`) and `RemoveReviewer` (`DeletePullRequestReviewer`) in `api/pr.go`
- [x] Task: Work items and labels `4f3ae83`
    - [x] Write tests: `--add-work-item` adds an ArtifactLink relation (`vstfs:///Git/PullRequestId/{projectGUID}%2F{repoGUID}%2F{prId}` from `pr.Repository`), "Relation already exists." = success; `--remove-work-item` removes only the PR ArtifactLink (branch/commit links kept) via `GetWorkItem` `Expand=Relations`, first case-insensitive match, `test /rev` + `remove /relations/{i}`; `--add-label` (creates tag on demand)/`--remove-label`, not-found label removal warns (from `pr.Labels`)
    - [x] Implement via a work item tracking client on the org URL (same SDK module) + PR labels APIs (`CreatePullRequestLabel`/`DeletePullRequestLabels`)
- [x] Task: Interactive mode and partial failure reporting `4f3ae83`
    - [x] Write tests: `Prompter.MultiSelect` in accessible mode (toggle numbers, finish with `0`)
    - [x] Write tests: TTY with no flags → multi-select prefilled with current values, non-TTY with no flags → error listing flags, partial failure exits 1 naming applied vs failed edits, prints PR URL
    - [x] Implement `Prompter.MultiSelect`
    - [x] Implement prompts and result reporting
- [x] Task: Conductor - User Manual Verification 'Phase 2: ado pr edit' (Protocol in workflow.md)
    - [x] Mandatory live check: promote an existing optional reviewer who has voted to required; confirm it becomes required and whether the vote is kept (the reset warning must fire only if it is not) — verified 2026-10-11: promotion resets the vote, warning is correct

## Phase 3: `ado pr comment` and Thread Visibility [checkpoint: 3e8fad2]

- [x] Task: Body input helper `3e8fad2`
    - [x] Write tests: `-b`, `-F` file / `-` stdin, `-e` editor, editor prefilled with an initial value, TTY with no body opens editor, non-TTY with no body fails naming `--body`, empty body rejected
    - [x] Implement: build a `$EDITOR` helper from scratch (no editor code exists in the MVP) that accepts an initial value; extract `readBody` from `pr create` into `shared`
- [x] Task: New thread, reply, thread status `3e8fad2`
    - [x] Write tests: new thread, `--thread <id>` + body replies with `ParentCommentId` = the thread's root comment, `--thread <id> --status fixed` updates status (with and without body; with body = CreateComment then UpdateThread, partial failure reported), `--status` mapped case-insensitively to `active|fixed|wontFix|closed|byDesign|pending`, `--status` without `--thread` usage error, invalid status value error, prints comment URL `<PR URL>?discussionId=<threadId>`, `-w` opens browser
    - [x] Implement via threads/comments APIs: `api/pr.go` wrappers CreateComment, UpdateComment, DeleteComment, UpdateThread (status only), and a CreateThread that takes a thread context
- [x] Task: Inline file comments `3e8fad2`
    - [x] Write tests: `--file --line n` and `n:m` build `threadContext` (`--side` right/left picks Right/Left positions, start offset 1), path normalized to a leading `/` (strip `./`, `\` → `/`), `changeTrackingId` from the latest iteration's paged changes (`$top` 2000, `NextSkip`; match path or `OriginalPath`, renamed-file test), `iterationContext {N,N}`, file not in the PR → `<file> is not changed in pull request !N`, `--line` without `--file` usage error, bad range error
    - [x] Implement inline thread creation; add api `Iterations` and paged `IterationChanges` (nil = 0), reused by Phase 4
- [x] Task: `--edit-last` / `--delete-last` `3e8fad2`
    - [x] Write tests: shared thread helpers in `pkg/cmd/pr/shared` (drop deleted threads/comments, `system` comments and any thread with a `CodeReviewThreadType` property; `filePath`; line = Right start else Left start, with range)
    - [x] Write tests: finds the signed-in user's (`Resolve("@me")`) latest non-deleted non-system comment by `PublishedDate` across threads, including replies and inline comments in others' threads, targeted by (threadId, commentId); edit replaces content (`-b`/`-F`, or TTY editor prefilled + `Submit?`, decline → `Discarding...`), prints comment URL; `--create-if-none` (only with `--edit-last`) creates a new thread, TTY without it asks `No comments found. Create one?`; delete confirms on TTY (`! Deleted comments cannot be recovered.` + `Delete the comment: "..."?`), `Comment deleted` on stderr, `--yes` only valid with `--delete-last`, body flags rejected with `--delete-last`, non-TTY without `--yes` refuses; no prior comment → `no comments found for current user`
    - [x] Implement: move `threads()`/`filePath()`/line helper from view.go into `shared` and add the `CodeReviewThreadType` check
    - [x] Implement `--edit-last` / `--delete-last`
- [x] Task: Extend `ado pr view --comments` `3e8fad2`
    - [x] Write tests: TTY thread header `#<id> <status> • <file>:<line>` (`<line>-<endLine>` for ranges), replies marked; `line:` in non-TTY `-c` records; `threads` JSON field shape (`id,status,filePath,line,comments[{id,author,content,publishedDate}]`, `author` an IdentityRef object like PR `author`); `comments` JSON field becomes an alias of the `threads` shape (breaking; nothing released yet); threads fetched only when `comments`/`threads` requested or `-c`; system threads (vote/policy) hidden; fixture with `rightFileStart.line` and a reply
    - [x] Implement view extension on the shared thread helpers
- [x] Task: Conductor - User Manual Verification 'Phase 3: ado pr comment and Thread Visibility' (Protocol in workflow.md)

## Phase 4: `ado pr diff` [checkpoint: 6eee251]

- [x] Task: Record diff dependency `6eee251`
    - [x] Update `tech-stack.md`: add `github.com/aymanbagabas/go-udiff` v0.4.1 (MIT/BSD-3, copy of `x/tools/internal/diff`, go 1.24, no deps, already in go.sum, becomes a direct require) with dated note, then `go get`
- [x] Task: Local clone path `6eee251`
    - [x] Write tests (stubbed git runner): fetches source and target refs from the ADO remote (checkout's remote matching, fork fetch-by-URL), diffs the latest iteration's `commonRefCommit`..`sourceRefCommit` after `cat-file -e` checks, missing SHA → fetch by SHA → `git merge-base` fallback, git always run with `--no-color --no-ext-diff`, `--name-only` adds `--name-only` and disables `--patch`, `--patch` uses `format-patch --stdout <target>..<source>`, `--color` auto/always/never drives the ado colorizer (gh codes), `NO_COLOR` respected
    - [x] Implement clone path and the ANSI colorizer shared by every diff path
- [x] Task: API fallback path `6eee251`
    - [x] Write tests (httpmock): latest iteration changes (multi-page) → per-file unified hunks from base/source blobs, `changeType` parsed as comma-separated flags; header writer (`diff --git`, mode 100644, 7-char `index`, rename from/to without similarity, `/dev/null` only in `---`/`+++`, headers for empty added files); `ItemContentAt(path, commit)` fallback when `originalObjectId` is missing; fork new-side blobs from `forkSource.repository.id`; binary = NUL in first 8000 bytes; blob over 5 MB (size via `GetBlob`) → `file too large to diff` notice; golden go-udiff fixtures (add, delete, rename-only, rename+edit, binary add, empty new file, no trailing newline), not byte comparison with git; `--name-only` from change list, `--patch` outside clone errors, color applied only on TTY/`always`
    - [x] Write tests: blob downloads use 4 workers with deterministic output order; backoff on 429 `Retry-After` and on 200 responses carrying `Retry-After` or `X-RateLimit-Remaining=0`
    - [x] Implement api `BlobContent`, `Blob` (size), `ItemContentAt`; reuse Phase 3's `Iterations`/`IterationChanges`
    - [x] Implement Retry-After / X-RateLimit backoff in the api HTTP client (`api/http_client.go`)
    - [x] Implement API path (bounded 4-worker pool) reusing the colorizer
- [x] Task: `-w/--web` and path selection `6eee251`
    - [x] Write tests: `--web` opens Files tab URL; clone path chosen when cwd repo matches PR repo, API path otherwise
    - [x] Implement
- [x] Task: Conductor - User Manual Verification 'Phase 4: ado pr diff' (Protocol in workflow.md)
    - [x] Check once on a real PR whether the latest iteration's `commonRefCommit` still matches the Files tab after only the target branch moved — verified 2026-10-11: matches

## Phase 5: `ado pr checks`, `ado pr status`, Docs [checkpoint: 8b2b1b5]

- [x] Task: Exit code 8 `8b2b1b5`
    - [x] Write tests: `pending` and `wrapped pending` rows in errors_test.go → exit 8 with empty stderr
    - [x] Implement `ExitPending = 8` and a `PendingError` sentinel in `pkg/cmdutil/errors.go`, handled in `HandleError` before printing; main.go unchanged
    - [x] Update `product-guidelines.md` exit-code table with code 8
- [x] Task: Checks aggregation (`pkg/cmd/pr/shared/checks.go`) `8b2b1b5`
    - [x] Write tests: `PRClient.Statuses` (`GetPullRequestStatuses`, httpmock); policy evaluations (artifact ID `vstfs:///CodeReview/CodeReviewId/{projectId}/{prId}`, no `includeNotApplicable`) and PR statuses merged; statuses deduped by (genre, name), highest id, tie-break `updatedDate`; matching statuses folded into status-policy rows (`targetUrl`/`description`), others as optional rows; rows named by type GUID + settings; state → bucket table (approved/succeeded → pass, rejected/broken/failed/error → fail, queued/running/notSet/pending/expired → pending, notApplicable PR statuses → skipping); expired = `status == "expired"` or `context.isExpired`; build links from `context.buildId`; `isRequired` from policy `isBlocking`; only build/status policies and PR statuses count toward the exit code (reviewer, work-item, comment and other policies listed only)
    - [x] Implement aggregation (used by `view`, `checks` and `status`); move the policy mapping out of view.go
- [x] Task: `ado pr checks` `8b2b1b5`
    - [x] Write tests: TTY table and non-TTY TSV, `--required` filter, exit 0/1/8 fixtures, reviewer/work-item/comment policies don't affect the exit code, no checks → "no checks reported" exit 0, `--json` fields with exit code following the buckets, `--web`, `--fail-fast` without `--watch` rejected, `--watch` with `--json` rejected (gh parity)
    - [x] Write tests: `--watch` polls with `--interval` (fake clock: `testing/synctest`, fallback package-level `sleep`/`now` vars if it conflicts with the SDK HTTP path), stops when nothing pending, stops with a requeue warning when only expired checks remain, `--fail-fast` stops on first failure, TTY redraw vs non-TTY final-only output
    - [x] Implement an iostreams screen-refresh helper
    - [x] Implement `pr checks`
- [x] Task: `ado pr status` `8b2b1b5`
    - [x] Write tests: current-branch section via exported `FindForBranch` returning `(nil, nil)` when nothing matches, `ErrNotOnAnyBranch` → "There is no current branch", section hidden with `-R`; created-by-me via `CreatorId`; needs-review from active PRs in scope (capped ~200) filtered client-side against me + my expandedUp groups (new `IdentityResolver` method: `ReadIdentities` expandedUp, descriptors mapped to container reviewer IDs; httpmock stubs), unvoted only, not declined, not my own PR; optional group reviewer dropped once the group's rolled-up vote is non-zero, required group reviewer kept until its policy passes; drafts included and marked as draft; `--project` via `GetPullRequestsByProject` (when `c.Repo == ""`) with per-PR repo URLs and repo name; sections capped at 10 with limit+1 "And more"; gh row format (`#N  <title> [<branch>]`, checks/review/merge-state line, `-c` merge status) plus a draft marker, checks summary from `shared/checks.go`; empty sections messages; `--json` takes PR fields filtered inside each section of `{currentBranch, createdBy, needsReview}`
    - [x] Implement `FindForBranch` export, the expandedUp identity method, project-scoped `ListPullRequests` with per-PR URLs in `ToJSON`/`PRURL`, per-section JSON field filtering
    - [x] Implement `pr status`
- [x] Task: Docs `8b2b1b5`
    - [x] Update `docs/gh-differences.md` with every divergence listed in the spec, plus: `ready --undo` clears all votes (and confirms when votes exist); diffs use ADO's iteration common commit; the API fallback skips blobs over 5 MB instead of the web's 0.5 MB / 5 MB behaviour; generated diffs lack `@@` function names, similarity percentages and mode changes; `format-patch` skips merge commits; `pr checks` no-checks exit 0, `--json` exit codes, expired-build `--watch` stop, notApplicable policies not requested, reviewer/work-item/comment policies excluded from the exit code; `pr status` row format and draft marker
    - [x] Update `spec.md` for the divergences decided in research: close's `--yes` refusal prints usage (o2); `ready --undo` confirms when votes exist (o4); `comments` JSON is an alias of `threads` (o9); comment URL `<PR URL>?discussionId=<threadId>` (o10); `--create-if-none` (o12); clone diff base is the iteration `commonRefCommit` (o14); expired builds pending with a `--watch` requeue stop (o18); no `includeNotApplicable` (o19); exit code from build/status policies and PR statuses only (o21); `pr status --json` takes PR fields inside sections, acceptance criterion changed (o22); gh row format plus draft marker (o23); group-vote rule (o25); drafts in needs-review (o26)
    - [x] Verify help text/examples for all eight new commands (`ado pr <cmd> --help`)
- [x] Task: Conductor - User Manual Verification 'Phase 5: ado pr checks, ado pr status, Docs' (Protocol in workflow.md)
    - [x] Capture a real PR's policy evaluations JSON (unmet reviewer policy, expired build) into fixtures — done 2026-10-11 on PR !1: status policy + unmet minimum-reviewers policy in `pkg/cmd/pr/shared/testdata/live_*.json` (identities scrubbed); expired build not possible (no pipeline in the project)
    - [x] Verify expandedUp group expansion against a team reviewer and a nested Entra group — done 2026-10-11: `queryMembership=expandedUp` call and team reviewer resolution verified live; nested Entra group and a second user's PR not available
