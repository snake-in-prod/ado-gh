# Spec: Pull Request Command Group — Full gh Parity

## Overview
Complete the `ado pr` command group so a `gh` user finds every `gh pr` verb they rely on. Adds `diff`, `comment`, `close`, `reopen`, `edit`, `ready`, `status` and `checks` on top of the MVP loop (`create/list/view/checkout/review/merge`). Builds on the Factory, context resolution, PR reference parsing, identity resolution, exporter and confirmation helpers from track `mvp_core_20261010`. That track must be complete first.

## User Stories
1. As a reviewer, I run `ado pr diff 123` and `ado pr comment 123 --file a.go --line 40 -b "nit"` without opening the browser.
2. As an author, I run `ado pr status` to see my branch's PR, PRs I created, and PRs waiting on my review.
3. As a CI author, I run `ado pr checks 123 --required --watch` and branch on the exit code.
4. As an author, I fix a PR with `ado pr edit 123 --add-reviewer alice --add-label bug --add-work-item 42`, publish it with `ado pr ready`, or abandon it with `ado pr close -d`.

## Functional Requirements

All commands take an optional PR reference (number, `!123`, URL or branch; default: PR for current branch) and honor `-R`.

### FR1: `ado pr diff [<ref>]`
- Default: unified diff from the merge base of target and source to source. Matches the ADO "Files" tab.
- **In a local clone:** fetch the PR's source and target refs from the ADO remote, then `git diff <commonRefCommit>..<source>`, where `commonRefCommit` is the latest iteration's common commit from the API (falls back to fetch-by-SHA, then `git merge-base`). git runs with `--no-color --no-ext-diff`; ado colors every path itself.
- **Outside a clone (`-R`):** rebuild the diff from the API. Take the latest iteration's changes, fetch base and source blob contents (4 parallel downloads, backing off on `Retry-After` / `X-RateLimit-Remaining=0`), and run a Go unified-diff library. Binary files print `Binary files a/x and b/x differ`; blobs over 5 MB print `file too large to diff`. Renames, adds and deletes produce the matching git headers.
- `--name-only`: changed file paths only.
- `--patch`: `git format-patch --stdout` style, one patch per commit. Needs a local clone; outside one it fails with `--patch requires a local clone`.
- `--color {auto|always|never}` (default auto: color only on TTY; respects `NO_COLOR`/`CLICOLOR_FORCE`).
- `-w/--web`: open the PR Files tab.
- No `--json` (raw output, gh parity).

### FR2: `ado pr comment [<ref>]`
- New thread: `-b/--body`, `-F/--body-file` (`-` = stdin), `-e/--editor`. On TTY with no body flag, open `$EDITOR`. On non-TTY with no body, fail naming `--body`.
- The editor is `ADO_EDITOR`, else the `editor` config key, else `VISUAL`, else `EDITOR`, else `nano` (`notepad` on Windows); `GH_EDITOR` is ignored.
- Reply: `--thread <id>` + body adds a reply to an existing thread.
- Thread status: `--thread <id> --status {active|fixed|wontFix|closed|byDesign|pending}` (may be combined with a body).
- Inline: `--file <path> --line <n>[:<m>] [--side {right|left}]` (default right) anchors a new thread to diff lines in the latest iteration. `--line` without `--file` is a usage error.
- `--edit-last`: replace the body of your most recent non-system comment, including replies and inline comments (needs a body flag or opens the editor). `--create-if-none` creates a new thread when you have none (gh parity); on TTY without it, ado asks `No comments found. Create one?`.
- `--delete-last`: delete your most recent comment. Confirms on TTY, needs `--yes` in non-interactive mode.
- `-w/--web`: open the PR in the browser instead.
- Prints the comment URL `<PR URL>?discussionId=<threadId>`.
- **`ado pr view --comments`** is extended to show thread ID, status, file:line anchor and replies. It adds a `threads` JSON field (`id,status,filePath,line,comments[{id,author,content,publishedDate}]`, `author` an IdentityRef object) to `pr view --json`; the existing `comments` field becomes an alias of the `threads` shape (breaking, before the first release).

### FR3: `ado pr close [<ref>]` (abandon)
- Sets status to `abandoned`. `-c/--comment <text>` posts a comment first.
- `-d/--delete-branch`: deletes the remote source branch (abandon happens first). Deletes every local branch whose upstream (`branch.<name>.merge` plus `branch.<name>.remote`) is the source branch in the PR's repository (or its fork), plus the same-name local branch when it has no upstream, switching to the default branch first when one is checked out. Local branches are left alone when the repository comes from `-R` or `ADO_REPO`. Skips remote deletion for cross-fork PRs, with a warning. If the remote delete fails (e.g. no Force push permission), exits 1 naming the branch; the PR stays abandoned.
- Confirms on TTY (`Abandon pull request !123 "title"?`). `-y/--yes` skips the prompt. Non-interactive without `--yes` refuses: `--yes required when not running interactively`, with usage printed and exit 1 (like `pr merge`).
- Already abandoned: warning to stderr, exit 0. Completed: error, exit 1.

### FR4: `ado pr reopen [<ref>]`
- Sets an abandoned PR back to `active`. `-c/--comment` is optional.
- Already active: warning, exit 0. Completed: error, exit 1. Deleted source branch: error naming the branch and suggesting a push.

### FR5: `ado pr edit [<ref>]`
- `-t/--title`, `-b/--body`, `-F/--body-file`, `-B/--base <branch>` (retarget).
- `--add-reviewer`, `--remove-reviewer` (repeatable/comma-separated; users by email/UPN/name, teams/groups by `[Project]\Team`).
- `--add-required-reviewer`: adds a reviewer as required, or promotes an existing optional reviewer.
- `--add-work-item`, `--remove-work-item <id>` (link/unlink).
- `--add-label`, `--remove-label` (ADO PR tags; created on demand).
- **`ado pr view`** shows the PR's active labels (tags): a `Labels:` line on TTY, a `labels:` row in raw output, and a `labels` JSON field (`[{id,name}]`, fetched with one extra request; not offered by `pr list`/`pr status`).
- TTY with no flags: interactive multi-select of fields to edit, prefilled with current values (gh behavior). Non-TTY with no flags: error listing the flags.
- Prints PR URL. Any partial failure (e.g. one bad reviewer) exits 1 and names which edits applied and which didn't.

### FR6: `ado pr ready [<ref>]`
- Publishes a draft PR. `--undo` converts it back to draft; because that removes all votes, it confirms on TTY when the PR has votes and requires `-y/--yes` when non-interactive.
- Already in the target state: message, exit 0. Not active (abandoned/completed): error, exit 1.

### FR7: `ado pr status`
- Three sections: **Current branch** (PR whose source is the checked-out branch), **Created by you** (active, authored by the signed-in user), **Requesting a review from you** (active, including drafts marked as draft, where you or a group you belong to is a reviewer and you have not voted yet; an optional group reviewer drops out once the group's rolled-up vote is non-zero, a required group reviewer stays until its policy passes).
- Rows use gh's format (`#N  <title> [<branch>]`, then checks / review / merge-state summary) plus a draft marker. Sections show at most 10 rows, then "And more".
- `-c/--conflict-status`: show merge status (conflicts / succeeded).
- `--project`: widen the last two sections from the current repo to every repo in the project. Rows then show the repo name.
- `--json/--jq/--template`: object `{currentBranch, createdBy, needsReview}`; `--json` takes PR fields (`pr list` field set plus `checks`, `reviewDecision`) filtered inside each section (gh parity).

### FR8: `ado pr checks [<ref>]`
- Lists every **policy evaluation** (build validation, minimum reviewers, required reviewers, linked work items, comment resolution, merge strategy, status policies; notApplicable evaluations are not requested, matching the ADO UI) and every **PR status** (latest per context `genre/name`).
- Columns: bucket mark, name, required/optional, state, elapsed, link. Buckets: `pass` (approved/succeeded), `fail` (rejected/broken/failed/error), `pending` (queued/running/notSet/pending, expired build), `skipping` (notApplicable).
- `--required`: only blocking checks. `--watch` (`-i/--interval` seconds, default 10; `--fail-fast`): refresh until nothing is pending, or stop with a requeue warning when only expired build policies remain. `--fail-fast` requires `--watch`; `--watch` can't be combined with `--json`. Redraws in place on TTY, prints only the final state when not a TTY.
- `-w/--web`: open the PR overview.
- Exit codes (computed from build/status policies and PR statuses only; reviewer, work-item and comment policies are listed but don't count): `0` all pass/skip, `1` any failed, `8` none failed but some pending (gh parity). No checks: message `no checks reported`, exit 0. With `--json` the exit code still follows the buckets.
- `--json/--jq/--template` fields: `name,state,bucket,type,isRequired,description,link,startedAt,completedAt`.

## Non-Functional Requirements
- **Output contract:** every read command (`status`, `checks`, `view --comments`) supports `--json/--jq/--template`. New JSON field names are public API.
- **Safety:** destructive actions (`close`, `close -d`, `comment --delete-last`) confirm on TTY and require `-y/--yes` when non-interactive.
- **Exit code 8** (checks pending) is added to the project's exit-code table in `product-guidelines.md`.
- **New dependency:** a Go unified-diff library (e.g. `aymanbagabas/go-udiff`) is recorded in `tech-stack.md` before use.
- **Testing:** >90% coverage. All HTTP stubbed (httpmock). Git calls stubbed via the command-runner stub. Both TTY and non-TTY covered.
- **Docs:** `docs/gh-differences.md` updated with every divergence (diff fallback, inline/status comment flags, required reviewers, work-item links, labels = tags, policies in `checks`, `status --project`, missing gh flags such as `--add-assignee`/`--milestone`/`--add-project`).

## Acceptance Criteria
- [ ] `ado pr diff 123` matches the ADO Files tab, both in a clone and with `-R` outside one. `--name-only` and `--color never` work. Piped output has no ANSI codes.
- [ ] `ado pr comment 123 -b "x"`, `--thread 5 -b "y"`, `--thread 5 --status fixed`, `--file a.go --line 10 -b "z"`, `--edit-last`, `--delete-last -y` each work. `pr view 123 --comments --json threads` shows them.
- [ ] `ado pr close 123 -d -c "dup"` abandons the PR, comments and deletes branches after a TTY confirmation. Non-TTY without `-y` refuses with exit 1.
- [ ] `ado pr reopen 123` reactivates it. `ado pr ready 123` and `--undo` toggle draft.
- [ ] `ado pr edit 123 -t T --add-required-reviewer alice --add-label bug --add-work-item 42 -B main` applies all edits.
- [ ] `ado pr status` shows all three sections. `--project` widens them. `--json id,title` filters PR fields inside each section.
- [ ] `ado pr checks 123` exits 0/1/8 for pass/fail/pending fixtures. `--watch` stops when nothing is pending. `--required` filters.
- [ ] `docs/gh-differences.md` updated. Coverage >90%. CI green on linux/macos/windows.

## Out of Scope
- `pr comment` reactions/likes, comment `list` subcommand (covered by `pr view --comments`).
- Pager for `pr diff`. `pr diff --exclude`.
- `pr edit` assignees/milestones/projects (no ADO equivalent).
- Auto-requeue of expired build policies (`pr checks --requeue`), which belongs in a later track.
