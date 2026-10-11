# Differences from `gh`

`ado` follows `gh`'s command layout, flag names and shorthands wherever Azure
DevOps has an equivalent. This page lists the places where it can't, or where
Azure DevOps works differently enough that copying `gh` would mislead you.

## Work items, not issues

Azure DevOps tracks work in **work items** (Bug, Task, User Story, ...), not
issues. `ado workitem` is the counterpart of `gh issue`; `ado wi` and
`ado issue` are aliases. A work item is `123`, `#123` or a work item URL.

Work items link to pull requests by ID instead of through `Fixes #12` keywords
in the body. Pass them when you create the PR, or link them afterwards:

```sh
ado pr create --work-item 42 --work-item 43
ado wi link 42 --pr 45
```

### Project, not repository

Work items, queries, sprints and boards belong to a **project**. So `-R` also
takes `ORG/PROJECT` for `ado workitem`, `ado sprint` and `ado board`, which then
work outside a git clone. `--pr`, `--commit` and `--branch` links still need a
repository (the current one or `-R ORG/PROJECT/REPO`).

### Teams

Sprints, boards, `--iteration @current` and the WIQL team macros depend on a
**team**. It comes from `--team`, else the `team` config key for the project,
else the project's default team. `gh` has no teams here.

```sh
ado config set team "Web Team" --project fabrikam/Fiber
```

### Types come from the process

Every work item has a **type**, and the types depend on the project's process
(Agile, Scrum, Basic, CMMI or an inherited one). `--type` takes a type name in
any case, or an alias resolved through the process's type categories, so it
works on customized processes too:

| Alias | Category | Agile | Scrum | Basic | CMMI |
|---|---|---|---|---|---|
| `bug` | Bug | Bug | Bug | Issue | Bug |
| `story` | Requirement | User Story | Product Backlog Item | Issue | Requirement |
| `task` | Task | Task | Task | Task | Task |
| `feature` | Feature | Feature | Feature | none | Feature |
| `epic` | Epic | Epic | Epic | Epic | Epic |

`bug` uses whatever type the project's Bug category holds (Issue on Basic).
An unknown type, or an alias the process has no type for, errors and lists the
valid types. `ado workitem create` needs `--type`; `gh
issue create` has no such thing.

### States and categories, not open/closed

GitHub issues are open or closed. A work item has a **state** of its type
(New, Active, Resolved, Closed, Done, ...), and each state belongs to a
category: Proposed, In Progress, Resolved, Completed or Removed. `ado` maps
open/closed onto the categories:

- `open` is Proposed, In Progress or Resolved; `closed` is Completed or
  Removed. `ado wi list -s` takes `open` (default), `closed`, `all` or a state
  name.
- `ado wi close` moves each item to the first Completed state of its type:
  Closed on Agile and CMMI, Done on Scrum and Basic.
  - `--resolve` uses the Resolved category instead, else a state named
    `Resolved`; it errors when the type has neither.
  - `--removed` uses the Removed category, the closest thing to `gh`'s
    `--reason "not planned"`.
  - `-r/--reason` sets the work item's **Reason** field (`Fixed`, `Cut`, ...),
    checked against the reasons the type allows. In `gh`, `-r` picks
    `completed` or `not planned`.
  - `--state <name>` overrides all of these.
  - Some processes require a field to close (for example Resolved Reason);
    set it with `-f`.
- `ado wi reopen` moves each item to the first Proposed state, else the first
  In Progress state, or `--state`.
- `-c/--comment` on `close` and `reopen` adds the comment in the same update.
- An item already in a target state is left alone with a warning (exit 0).
- `close`, `reopen`, `edit`, `assign` and `delete` take several IDs. When some
  fail, the others are still updated and `ado` exits 1 naming the failed ones.

### Fields, tags and assignees

- `-f/--field Name=Value` sets any field by friendly name (`"Story Points=5"`)
  or reference name (`Microsoft.VSTS.Scheduling.StoryPoints=5`), on `create`,
  `edit`, `close` and `reopen`. Values are checked against the field type and
  its allowed values; `Name=` clears the field. An unknown field errors with
  close matches. `gh` has no such flag.
- `--area` and `--iteration` set the area and iteration paths, checked against
  the project's paths. `--iteration @current` is the team's current sprint.
  `-p/--priority` takes 1 (highest) to 4. They replace `gh`'s `--milestone`
  and `--project`.
- Work items have **tags**, not labels. `--tag` (`-l/--label` is an alias),
  `--add-tag` and `--remove-tag` (aliases `--add-label`, `--remove-label`) take
  repeatable or comma-separated names. Azure DevOps creates a tag the first
  time it's used. `ado wi list --tag` matches items that have every tag.
- A work item has **one** assignee: `-a/--assignee` replaces `gh`'s
  `--add-assignee`/`--remove-assignee`, and `--unassign` clears it. `-a none`
  in `ado wi list` finds unassigned items. `ado wi assign <id>... [--to
  <user>]` (default `@me`) has no `gh` equivalent.

### Bodies are Markdown, sometimes HTML

- Description, Repro Steps and other long text fields, and comments, are sent
  as Markdown. If the organization doesn't support Markdown fields, `ado`
  retries once with the Markdown converted to HTML.
- `-b/--body` sets Description, except on bugs whose form shows Repro Steps
  instead (Agile, Scrum, CMMI): there it sets Repro Steps. Long text fields set
  with `-f Name=Value` are Markdown too.
- A field already stored as HTML stays HTML: `ado wi edit --body` (or `-f`)
  converts your Markdown to HTML for it. `--markdown` switches the field to
  Markdown, which Azure DevOps can't undo.
- `ado wi view` renders Markdown on a terminal and converts HTML to plain
  text. Piped output is the raw field value.

### `workitem list`

- Lists the whole **project**, most recently changed first, 30 items by
  default. `gh issue list` lists one repository.
- `-S/--search` runs **Azure DevOps Work Item Search**, not GitHub search.
  It takes the portal's search syntax: free text plus filters such as `a:`
  (assigned to), `c:` (created by), `s:` (state), `t:` (type) and `tags:`,
  e.g. `-S "crash a:@me t:Bug"`. The hits are then narrowed by the other
  flags. Work Item Search is only on Azure DevOps Services (or Azure DevOps
  Server with Search installed); otherwise `-S` fails with `work item search
  not available`. `-S` and `-w` can't be combined.
- `-A/--author`, `--area`, `--iteration` (sub-paths included) and `--type`
  filter too. There is no `--mention`, `--milestone` or `--app`.
- `-w` opens the filter as a query in the browser, or the Work Items hub when
  the query is too long for a URL.
- `--json` fields: `id,type,title,state,reason,assignedTo,createdBy,createdDate,changedDate,areaPath,iterationPath,tags,priority,parent,url,fields`.
  `id` replaces `number`, `assignedTo` is one user, and `fields` holds every
  raw field by reference name.
- No match prints `no work items match your search` on stderr and exits 0.

### `workitem create`, `view`, `edit` and `comment`

- `ado wi create -w` opens the new work item form prefilled with every flag
  given. `--parent` can't be prefilled; `ado` warns and ignores it.
- `ado wi view` shows the work item's links grouped as parent, children,
  related, other work items, pull requests, commits, branches, hyperlinks and
  attachments. `-c/--comments` shows comments oldest first; `--history` (no
  `gh` equivalent) shows the field change log. `--json` adds
  `description,relations,comments,history` to the `list` fields.
- `ado wi edit` takes several IDs and sends one update per item that fails if
  the item changed since it was read (`work item 123 changed since read;
  retry`). `--state`, `--add-link`/`--remove-link <type>:<target>` and `-f`
  have no `gh` equivalent. On a terminal without flags, you pick the fields to
  edit (one item only).
- `ado wi comment` works like `gh issue comment`, including `--edit-last` and
  `--delete-last` (with `-y/--yes` when not interactive). It prints the
  comment URL.

### Links

`ado wi link <id>` and `ado wi unlink <id>` manage links; `gh` has none.
Targets are `--parent` (one per item: replacing an existing parent asks first,
or needs `--yes` without a terminal), `--child`, `--related`, `--link
<type>=<id>` for any link type (`Predecessor=12`, `"Tested By=7"`), and `--pr`,
`--commit`, `--branch` and `--url`. `unlink` takes the same flags; if any of
them matches no link, nothing is removed.

### Delete goes to the recycle bin

- `ado wi delete <id>...` moves work items to the project's **recycle bin**
  and prints `Restore with: ado wi restore <id>...`. `ado wi restore` has no
  `gh` equivalent.
- `--permanent` destroys them instead; this can't be undone.
- On a terminal, `ado` lists the IDs and titles and asks for a yes/no
  confirmation; `gh` asks you to type the issue number. `-y/--yes` skips it.
  Without a terminal and without `--yes` it refuses.
- Deleting needs the project permission **Delete and restore work items**;
  `--permanent` needs **Permanently delete work items**.

### Queries

`ado wi query` runs **WIQL** or a **saved query**; `gh` has no equivalent.

- Text starting with `SELECT` is WIQL, a GUID is a saved query ID, anything
  else is a saved query path (`"Shared Queries/Team/Active Bugs"`).
  `--wiql-file <file|->` reads WIQL from a file or stdin.
- Macros (`@Me`, `@Today`, `@Project`, `@CurrentIteration`, `@TeamAreas`) are
  evaluated by Azure DevOps; the team macros use the team above.
- Flat queries print the query's own columns. Tree and one-hop queries nest
  linked items under their parents, and `--json` gives each item a
  `links: [{rel, item}]` array. `-L/--limit` (default 200) counts top-level
  items for those.
- `-w` works for saved queries only.
- `ado wi query list [--folder <path>]` lists the saved query tree (My Queries
  and Shared Queries) with paths and IDs.

### Not supported

`gh issue`'s `develop`, `pin`/`unpin`, `transfer`, `lock`/`unlock` and
`status` have no counterpart. Attachments, templates and capacity are out of
scope.

## Sprints and boards

`ado sprint` and `ado board` have no `gh` equivalent. They are read-only and
use the team above.

- `ado sprint list [--timeframe past|current|future|all]` lists the team's
  sprints with dates and time frame.
- `ado sprint current` shows the current sprint and its working days left:
  today through the finish date, on the team's working days, minus the team's
  days off.
- `ado sprint view [<iteration>]` lists the sprint's work items grouped by
  state category, children nested under their parent (`-a`, `--type`).
- `ado sprint board [<iteration>]` shows the taskboard columns side by side
  (stacked on a narrow terminal), cards `#id title (assignee)`. Piped, it
  prints `column, id, title, assignee, parent` separated by tabs. A team that
  never customized its taskboard gets one column per state of its task type.
  There is no capacity or burndown.
- `ado board [<backlog>]` shows the team's Kanban board for the requirement
  backlog, or `features`, `epics` or another level by name. Columns show WIP
  limits (`Active 3/5`) and Doing/Done halves of split columns. Completed
  items changed in the last 30 days are shown. Cards can't be moved; use
  `ado wi edit --state` or `-f` with the board column field.
- `-w` on `sprint current` and `sprint view` opens the sprint's taskboard,
  and on `board` the board. `sprint list` and `sprint board` have no `-w`.
- `sprint list`, `sprint current`, `sprint view`, `sprint board` and `board`
  all take `--json/--jq/--template`.

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
- `close -d/--delete-branch` abandons the PR, then deletes the local branch
  with the same name (as `gh` does) and **every** local branch whose upstream is
  the source branch, so branches made with `pr checkout -b` or fork-prefixed
  names go too, and then the remote source branch. A local branch counts only
  when its `branch.<name>.remote` points at the repository holding the source
  branch (the fork, for a PR from a fork); a branch tracking a same-named
  branch of another repository is kept, and so is a same-name branch that does.
  With `-R` or `ADO_REPO`, local branches are left alone (`gh` only checks
  `--repo`). Remote deletion is skipped with a warning for PRs from a fork. Deleting a branch needs the
  **Force push** permission, which Contributors usually only have on branches
  they created. When the delete fails, the PR stays abandoned and `ado` exits 1
  naming the branch and the missing permission.
- `ado pr reopen` fails when the source branch no longer exists, naming the
  branch and asking you to push it again (to the fork, for a PR from a fork). The web UI can restore it; `ado`
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
  a tag the first time it's used, so unknown labels aren't an error. Tags are
  case-insensitive: a name repeated in another case is applied once.
- Adding and removing the same reviewer in one run is an error.
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

`ado pr view` also shows the PR's labels (tags): a `Labels:` line on a
terminal, a `labels:` row when piped, and a `labels` field in `--json`, an
array of `{id,name}` objects. The PR itself doesn't carry its labels, so they
cost one extra request; `pr list` and `pr status` don't offer `labels`.

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
- The "Current branch" section is hidden with `-R`, as in `gh`, and also when the
  repository comes from `ADO_REPO`, because the current directory may then be a
  different repository.

## `-R` takes `[ORG/]PROJECT/REPO`

GitHub repositories are `OWNER/REPO`. Azure DevOps has three levels:
organization, project and repository.

```sh
ado pr list -R fabrikam/Fiber/web    # organization/project/repository
ado pr list -R Fiber/web             # organization from ADO_ORG
ado pr list -R web                   # organization from ADO_ORG, project from ADO_PROJECT
ado wi list -R fabrikam/Fiber        # project-scoped commands: organization/project
```

- `ado workitem`, `ado sprint` and `ado board` act on a project, so their `-R`
  also takes `ORG/PROJECT`.

- The target repo comes from `-R`, then `ADO_REPO` (same format), then the git
  remotes: the one marked by `ado repo set-default`, then `origin`, then
  `upstream`, then the first Azure DevOps remote.
- `-R` and `ADO_REPO` always target `dev.azure.com`. Remotes on
  `{org}.visualstudio.com` keep their host.
- When a repo has the same name as its project, Azure DevOps remotes may omit
  the project. `ado` treats the project as equal to the repo name in that case.
- `ado` uses `ADO_*` environment variables and its own config only. It ignores
  `GH_REPO`, `GH_HOST`, `GH_BROWSER` and other `GH_*` variables.
- The text editor comes from `ADO_EDITOR`, then `ado config set editor`, then
  `VISUAL`, then `EDITOR`, else `nano` (`notepad` on Windows). `GH_EDITOR` and
  `gh`'s `editor` setting are ignored.

## Repositories

Azure DevOps repositories live in a **project**, and have no visibility,
description, topics, language or license of their own: visibility belongs to
the project. So `ado repo` has no `--visibility`, `--public`/`--private`/
`--internal`, `--topic`, `--language`, `--description` or `--license` flags,
and `--json` has no such fields. There is no `repo fork`, `repo rename`,
`repo edit`, `repo sync` or `repo archive`.

A `<repo>` argument is `REPO`, `PROJECT/REPO`, `ORG/PROJECT/REPO` or a URL
(clone URLs and URLs copied from the browser). Missing parts come from the
current repository, then `ADO_ORG`/`ADO_PROJECT`, then the `org` config key
(`ado config set org myorg`).

### `repo list`

- No argument lists the **current project**, not your own repositories.
  `ado repo list <org>` lists every repository of every project in the
  organization (names show as `project/repo`); `<org>/<project>` lists one
  project.
- Azure DevOps **disables** repositories instead of archiving them.
  `--disabled`/`--no-disabled` filter on that; `--archived`/`--no-archived` are
  hidden aliases.
- `--match <glob>` filters names (case-insensitive). The API doesn't page, so
  filters, sorting (by name) and `--limit` all run on the client.
- The info column shows `fork`, `disabled` and `empty` (no default branch; JSON
  `defaultBranch` is `""`).
- `--json` fields: `id,name,project,defaultBranch,size,isDisabled,isFork,remoteUrl,sshUrl,webUrl`.
  `parentRepository` is only in `repo view`; the list API doesn't return it.

### `repo view`

- Shows name, project, default branch, size, HTTPS and SSH clone URLs, the
  fork parent and a disabled marker, then the root `README*` (found
  case-insensitively; a markdown file wins, then the first by name).
  `-b/--branch` reads the README from another branch. A disabled repository
  shows only its metadata.
- `--json` takes the `repo list` fields plus `parentRepository` and `readme`.
- The README is rendered on a terminal and printed raw when piped. The style
  follows `GLAMOUR_STYLE` (else the terminal background), and the text wraps
  at the terminal width, **at most 120 columns**. There is no `GH_MDWIDTH`
  equivalent.

### `repo clone`

- The protocol comes from the per-host `git_protocol` key (`https` or `ssh`,
  default `https`). A git URL you pass is cloned **as given**.
- `ado` doesn't inject a credential helper the way `gh` does. HTTPS clones use
  your own git credential setup (for example Git Credential Manager), and SSH
  uses your keys. A URL with a password in it (`https://user:PAT@...`) is
  **refused**; store the credential in a credential manager instead.
- Azure Repos doesn't support Git LFS over SSH. Clone over HTTPS if the
  repository uses LFS.
- `<directory>` must be a new directory name without path separators; the
  default is the repository name (`My Repo`, not `My%20Repo`).
- Cloning a fork adds the parent as `upstream` (`-u` renames it) and fetches
  it. Unlike `gh`, it **doesn't make the parent the default repository**, and
  there is no `--no-upstream`. Commands then target the fork (`origin`); run
  `ado repo set-default upstream` to target the parent.
- When git fails, `ado` exits 1 after git's own error output.

### `repo create`

- Creates the repository in the current project or `-p/--project
  [ORG/]PROJECT`. There is no `--source`/`--push`/`--remote` (publishing an
  existing local repository) and no `--template`.
- `--add-readme` (README.md with `# <name>`) and `-g/--gitignore <template>`
  (one of the project's Azure DevOps gitignore templates; an unknown name lists
  close matches) go in **one initial commit on `main`**.
- If the repository is created but that commit fails, `ado` warns, still prints
  the URL, keeps the empty repository and exits 1.

### `repo delete` and `repo restore`

- `ado repo delete` **soft-deletes**: the repository goes to the project's
  recycle bin for **30 days**, then Azure DevOps removes it. Bring it back with
  `ado repo restore <name|id>` (no `gh` equivalent). `--permanent` also purges
  it from the recycle bin, which can't be undone.
- On a terminal you confirm by typing the repository name (a mismatch cancels
  with exit 2). Without a terminal, `--yes` is required.
- Deleting needs the **Delete repository** permission (by default only
  Project Administrators have it) and, with a PAT, the **Code (Read, write &
  manage)** scope (`vso.code_manage`); so does restoring.
- Azure DevOps refuses to delete the **only** repository of a project; `ado`
  passes the server's error through.

### `repo set-default`

- The choice is stored as `remote.<name>.ado-resolved = base` in the git
  config. `gh-resolved` is ignored.
- A single-word argument is read as a **remote name first**, then as a
  repository; `gh` tries `OWNER/REPO` first.
- `-R/--repo` is **rejected**; pass the repository as the argument.
- `--view` prints `org/project/repo`. With no default set it prints a message
  on stderr and exits 0, like `gh`.

## `ado browse`

- Targets: `!123` or `123` is a pull request, `#456` a **work item** (quote it
  in the shell), 7 to 40 hex characters a commit, anything else a path, with
  `:N` or `:N-M` to highlight lines. Paths are relative to the current
  directory inside a clone, and to the repository root with `-R`.
- Section flags: `-s/--settings`, `-w/--wiki`, `--pipelines` (`gh`'s
  `--actions`), `--boards` (`gh`'s `--projects`), `--prs` and `--branches`.
  There is no `--releases` or `--blame`.
- In `ado browse`, `-w` means `--wiki`, as in `gh browse`; elsewhere it is
  `--web`.
- `--wiki`, `--pipelines` and `--boards` open **project** pages
  (`_wiki`, `_build`, `_boards`), not repository pages.
- `--settings` makes one API call for the repository ID to open its settings
  page. If that fails, it opens the project's repository list instead.
- `-n/--no-browser` works **offline**: without `-b`/`-c`, file URLs have no
  `version=` and Azure DevOps shows the default branch. `gh` looks up the
  default branch even with `-n`. Only `--settings` needs the network.

## Opening the browser

- `ado browse` and every `-w/--web` flag print `Opening <url> in your
  browser.` on **stderr**, with the **full URL**, when stdout is a terminal.
  `gh` prints it on stdout or stderr depending on the command and drops the
  `https://`.
- The browser comes from `ADO_BROWSER`, then `ado config set browser`, then
  `BROWSER`, else the system default. `GH_BROWSER` and `gh`'s `browser`
  setting are ignored.

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
