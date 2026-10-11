# Differences from `gh`

`ado` follows `gh`'s command layout, flag names and shorthands wherever Azure
DevOps has an equivalent. This page lists the places where it can't, or where
Azure DevOps works differently enough that copying `gh` would mislead you.

## Work items, not issues

Azure DevOps tracks work in **work items** (Bug, Task, User Story, ...), not
issues. There is no `ado issue` command. Work item commands are planned for a
later release; until then use `ado api`:

```sh
ado api '{project}/_apis/wit/workitems/42'
```

Work items link to pull requests by ID instead of through `Fixes #12` keywords
in the body. Pass them when you create the PR:

```sh
ado pr create --work-item 42 --work-item 43
```

## Votes, not reviews

A GitHub review is a submitted event (approve, comment or request changes).
An Azure DevOps reviewer has a **vote** that stays on the PR until they change
it. `ado pr review` sets your vote:

| `ado pr review` flag | Vote | Closest `gh pr review` flag |
|---|---|---|
| `-a`, `--approve` | 10 (Approved) | `--approve` |
| `--approve-with-suggestions` | 5 (Approved with suggestions) | none |
| `--wait`, `-r`, `--request-changes` | -5 (Waiting for author) | `--request-changes` |
| `--reject` | -10 (Rejected) | none |
| `--reset` | 0 (No vote) | none |

- Comments are separate from votes. `-c/--comment` with `-b/--body` adds a
  comment thread; it doesn't change your vote.
- Reviewers can be **required** or **optional**. `ado pr create -r` adds
  optional reviewers. Branch policies add required reviewers on the server.
- Teams and groups can be reviewers but can't vote. `ado pr view` shows them as
  non-voting, with the member vote they stand for (`votedFor`).
- Reviewers are resolved to Azure DevOps identities: an email, UPN, display
  name, `@me`, a GUID, or `[Project]\Team` for teams. A bare team name is
  rejected. The person must already be a member of the organization. Your PAT
  needs the **Identity (Read)** scope for this.

## Merge is "complete"

Azure DevOps calls merging a PR **completing** it. `ado pr merge` has the alias
`ado pr complete`.

- Strategies: `--merge` (merge commit, no fast-forward), `--squash`,
  `--rebase` (rebase and fast-forward) and `--rebase-merge` (rebase, then merge
  commit). Without one, the strategy already set on the PR is kept, else a
  merge commit; `gh` prompts for it instead.
- `-d/--delete-branch` deletes only the remote source branch; your local
  branch stays.
- Branch policies (required reviewers, builds, linked work items) can block
  completion. `ado pr merge` then fails and tells you to use `--auto` (complete
  when the policies pass) or `--bypass-policy --bypass-reason "<why>"` (needs
  the bypass permission). `gh` has `--admin` for the second case.
- `ado pr create --auto` creates the PR and then sets auto-complete. Azure
  DevOps can't set auto-complete in the create call, so this is two requests.
- PR states are `active`, `completed` and `abandoned` instead of `open`,
  `merged` and `closed`. `ado pr list -s` takes those names (default `active`,
  plus `all`).
- PRs are numbered per organization and written `!123`. `ado` accepts `123`,
  `!123`, a PR URL or a branch name.

## Close is "abandon"; `pr close`, `pr reopen` and `pr ready`

- `ado pr close` **abandons** the PR. It asks for confirmation on a terminal
  (`gh pr close` doesn't). `-y/--yes` skips the prompt. Without a terminal and
  without `--yes` it refuses with `--yes required when not running
  interactively`, prints the usage and exits 1, like `ado pr merge`.
- Closing an already abandoned PR prints a warning and exits 0. A completed PR
  can't be abandoned (exit 1).
- `close -d/--delete-branch` deletes the remote source branch after abandoning,
  then the local branch with the same name (as `gh` does) and **every** local
  branch whose upstream (`branch.<name>.merge`) is that branch, so branches made
  with `pr checkout -b` or fork-prefixed names go too. Remote deletion is
  skipped with a warning for PRs from a fork. Deleting a branch needs the
  **Force push** permission, which Contributors usually only have on branches
  they created. When the delete fails, the PR stays abandoned and `ado` exits 1
  naming the branch and the missing permission.
- `ado pr reopen` fails when the source branch no longer exists, naming the
  branch and asking you to push it again. The web UI can restore it; `ado`
  doesn't.
- `ado pr ready --undo` marks the PR as a draft again. Azure DevOps then
  **removes every vote**, so `ado` asks for confirmation when the PR has votes
  and needs `-y/--yes` without a terminal. `gh` never prompts.

## `pr edit` reviewers, work items and labels

- `--add-reviewer`/`--remove-reviewer` take users (email, UPN, name, GUID,
  `@me`) and teams (`[Project]\Team`). Reviewers added by a branch policy can't
  be removed; that edit fails while the others are applied.
- `--add-required-reviewer` has no `gh` equivalent. It adds a required
  reviewer, or promotes an optional one. Promotion **resets the reviewer's
  vote**, and `ado` warns when that happens.
- `--add-work-item`/`--remove-work-item` link and unlink work items by ID.
  Removing only drops the PR link; the work item's branch and commit links stay.
- `--add-label`/`--remove-label` edit the PR's **tags**. Azure DevOps creates
  a tag the first time it's used, so unknown labels aren't an error.
- `-B/--base` needs the PR retargeting feature. When the server ignores the
  change, the edit is reported as failed.
- Descriptions are limited to 4000 characters.
- When some edits fail, the others are still applied, and `ado` exits 1 naming
  which edits applied and which failed.
- `gh`'s `--add-assignee`, `--remove-assignee`, `--add-project`,
  `--remove-project`, `--milestone` and `--remove-milestone` don't exist:
  Azure DevOps PRs have no assignees, projects or milestones.

## `pr comment` works on threads

Azure DevOps comments live in **threads** that have a status and can be
anchored to file lines. `ado pr comment` adds flags `gh` doesn't have:

- `--thread <id> -b ...` replies to a thread.
- `--thread <id> --status <s>` sets the thread status (`active`, `fixed`,
  `wontFix`, `closed`, `byDesign` or `pending`), with or without a reply.
- `--file <path> --line <n>[:<m>] [--side right|left]` starts a thread on lines
  of a changed file in the latest iteration.

`--edit-last`, `--delete-last`, `--create-if-none` and `--yes` work like in
`gh`, but `--edit-last`/`--delete-last` pick your latest comment in **any**
thread, replies and file comments included. The printed URL is
`<PR URL>?discussionId=<thread ID>`; it points at the thread, not the comment.
Reactions aren't supported.

`ado pr view --comments` shows each thread's ID, status, `file:line` anchor
and replies. In `ado pr view --json`, `threads` and its alias `comments` are
arrays of threads (`id,status,filePath,line,comments[{id,author,content,publishedDate}]`),
not `gh`'s flat list of comments. Vote and policy system threads are hidden.

## `pr diff` matches the Files tab

- The diff starts at the **common commit** Azure DevOps stores for the latest
  iteration, so it matches the PR's Files tab. If that commit can't be fetched,
  `ado` falls back to `git merge-base`. In a clone, `ado` fetches the source and
  target branches; GitHub-style `refs/pull/<n>/head` refs don't exist.
- Outside a clone (for example with `-R`), the diff is **generated from the
  REST API** by downloading every changed file. That output isn't byte for byte
  what git prints:
  - hunk headers have no function names after `@@`;
  - renames have no `similarity index` line, and renames may be paired
    differently than git would;
  - file modes are always `100644`; mode changes aren't shown;
  - files over 5 MB print `file too large to diff (over 5 MB)` instead of a
    diff. The web UI truncates files from 0.5 MB and stops at 5 MB.
- `--patch` needs a local clone; it runs `git format-patch --stdout`, which
  leaves out merge commits.
- Colour comes from `ado`, not your git config; `--color` and `NO_COLOR` apply
  to every path. There is no pager and no `--exclude` flag.

## `pr checks` shows policies and statuses

Azure DevOps has no check runs. `ado pr checks` lists the PR's **branch policy
evaluations** (build validation, minimum and required reviewers, work item
linking, comment resolution, merge strategy, status policies) and the latest
**PR status** per context (`genre/name`).

- Buckets are `pass`, `fail`, `pending` and `skipping`; there is no `cancel`.
  `--required` keeps only blocking (required) checks.
- **Only build and status policies and PR statuses decide the exit code** and
  the summary counts. Reviewer, work item, comment and other policies are
  listed but never make `ado pr checks` fail or pend, so a PR waiting for
  reviews doesn't exit 1.
- Policies that don't apply (for example a build path filter that doesn't
  match) aren't shown, like in the web UI. `skipping` only comes from PR
  statuses set to `notApplicable`.
- An **expired** build counts as pending. Expired builds may never be requeued,
  so `--watch` stops with a warning to requeue them when only expired checks
  are pending (exit 8).
- No checks: `no checks reported` on stderr and **exit 0**. `gh` exits 1.
- With `--json`, the exit code still follows the buckets (0, 1 or 8). `gh`
  exits 0 whenever checks exist.

## `pr status`

- Rows use `gh`'s format (`#N  title [branch]`, then checks, review and, with
  `-c`, merge status), plus a `(draft)` marker that `gh` doesn't print.
- "Requesting a code review from you" also covers PRs where a **team or group
  you belong to** is the reviewer. An optional group stops counting once any
  member voted for it; a required group counts until its policy passes. `ado`
  scans the newest 200 active PRs for this. Drafts are included.
- Azure DevOps returns no total count, so a full section ends with `And more`
  instead of `And N more`.
- `--project` widens "Created by you" and "Requesting a code review from you"
  to every repository of the project and prefixes rows with the repository
  name. `gh` has no such flag.
- `--json` takes PR fields (the `pr list` fields plus `checks` and
  `reviewDecision`) and returns `{currentBranch, createdBy, needsReview}`, like
  `gh`.

## `-R` takes `[ORG/]PROJECT/REPO`

GitHub repositories are `OWNER/REPO`. Azure DevOps has three levels:
organization, project and repository.

```sh
ado pr list -R fabrikam/Fiber/web    # organization/project/repository
ado pr list -R Fiber/web             # organization from ADO_ORG
ado pr list -R web                   # organization from ADO_ORG, project from ADO_PROJECT
```

- The target repo comes from `-R`, then `ADO_REPO` (same format), then the git
  remotes (`origin`, then `upstream`, then the first Azure DevOps remote).
- `-R` and `ADO_REPO` always target `dev.azure.com`. Remotes on
  `{org}.visualstudio.com` keep their host.
- When a repo has the same name as its project, Azure DevOps remotes may omit
  the project. `ado` treats the project as equal to the repo name in that case.
- `ado` uses `ADO_*` environment variables and its own config only. It ignores
  `GH_REPO`, `GH_HOST`, `GH_BROWSER` and other `GH_*` variables.

## `pr checkout` fetches the source branch

GitHub publishes every PR's head at `refs/pull/<n>/head`, and `gh pr checkout`
fetches that ref. Azure Repos documents only `refs/pull/<id>/merge`, the
server's preview merge of the PR into its target branch, not the PR's own
commits. So `ado pr checkout` never fetches a pull ref. It fetches the PR's
**source branch** directly:

- **Same repository:** fetches the source branch from the matching remote and
  creates a local branch that tracks it.
- **From a fork, with a local remote for the fork:** same as above, using that
  remote.
- **From a fork, without a local remote:** fetches the branch from the fork's
  URL and sets `branch.<name>.remote`, `pushRemote` and `merge` to the fork, so
  `git pull` and `git push` go to the fork.
- If you can't read the fork, checkout fails with an access error. It doesn't
  fall back to the merge ref.

The local branch gets the source branch's name. A fork branch that has the same
name as the default branch (for example `main`) gets the fork's name as a
prefix: `<fork>/main`. `-b/--branch` sets a different name. `-f/--force`
resets an existing local branch to the PR's branch.

`gh`'s `--detach`, `--recurse-submodules` and `--worktree` flags aren't
available.

## Pushing to a fork depends on the fork's permissions

GitHub has "Allow edits from maintainers", which lets base repository
maintainers push to the PR branch in a contributor's fork. Azure DevOps has no
such setting. Forks exist only inside the same organization, and whether you
can push to a fork's PR branch depends only on your permissions on the fork
repository. If you don't have Contribute permission there, `git push` after
`ado pr checkout` fails.

## `ado api --paginate` merges pages

`gh api --paginate` prints each page as a separate JSON document, or wraps all
pages in an outer array with `--slurp`. Most Azure DevOps list endpoints return
an envelope like `{"count": 2, "value": [...]}`, so `ado api --paginate` merges
pages instead:

- When every page has a `value` array, the arrays are joined into **one**
  envelope with `count` recomputed. `--jq` and `--template` run once on the
  merged result, so `--jq '.value[]'` works the same with or without
  `--paginate`.
- When a page has no `value` array, pages are printed one after another (like
  `gh` without `--slurp`).
- There is no `--slurp` flag.
- Azure DevOps has no `Link` header. `--paginate` follows the
  `X-MS-ContinuationToken` response header or a `continuationToken` field in
  the body.
- Endpoints that page with `$top`/`$skip` only (pull requests, commits, teams)
  are stepped when the path sets `$top`, until an empty page:

  ```sh
  ado api '{project}/_apis/git/repositories/{repo}/pullrequests?$top=100' --paginate
  ```

- `--paginate` works with GET requests only.
- With `-i`, the response headers are printed for every page.

Other `ado api` differences:

- Paths are relative to the organization URL (`https://dev.azure.com/{org}/`),
  not to an API root. Absolute URLs must point to an Azure DevOps host. The
  placeholders are `{org}`, `{project}` and `{repo}`; `gh`'s are `{owner}`,
  `{repo}` and `{branch}`.
- An `api-version` query parameter is added when the path has none. Set
  another one with `--api-version`.
- There is no GraphQL endpoint.

## Authentication

- `ado auth login` signs in with a personal access token or Microsoft Entra ID
  instead of a GitHub OAuth app. PATs are stored per organization.
- CI tokens come from `ADO_TOKEN` or `AZURE_DEVOPS_EXT_PAT` instead of
  `GH_TOKEN`/`GITHUB_TOKEN`.
- Missing or rejected credentials exit with code `4`, as in `gh`.
