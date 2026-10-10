# Spec: Work Items and Boards — gh issue Parity

## Overview
Add work item and boards support as the Azure DevOps counterpart of `gh issue`: the `ado workitem` group (aliases `wi`, `issue`), `ado workitem query` for WIQL and saved queries, `ado sprint` iteration views and `ado board` for Kanban boards. Types and states come from the project's process (Agile, Scrum, Basic, CMMI, or inherited customizations) instead of hard-coded names. Builds on the Factory, config, exporter, prompter, editor, identity resolver and confirmation helpers from `mvp_core_20261010`, which must be complete first.

## User Stories
1. As a developer, I run `ado issue create --type bug -t "Crash on save" -a @me --iteration @current` or `ado wi list -a @me` without learning WIQL.
2. As a team lead, I run `ado wi query "Shared Queries/Team/Active Bugs" --json id,title` in a script, or `ado sprint view` / `ado board` during standup.
3. As a developer, I close a story with `ado wi close 123 -c "done in !45"` and get the right state for my process, whether that's `Closed`, `Done` or `Resolved`.

## Functional Requirements

### FR1: Shared foundation
- **References:** `123`, `#123`, or a work item URL.
- **Process-aware types:** `--type` accepts the exact type name (case-insensitive) or an alias resolved through the project's work item type categories, so custom/inherited processes work: `bug` → Bug category, `story` → Requirement category (User Story / Product Backlog Item / Issue / Requirement), `task`, `feature`, `epic`. An unknown type errors and lists valid types.
- **States:** each type's states come with categories (Proposed, InProgress, Resolved, Completed, Removed) from the states API. `open` = Proposed/InProgress/Resolved, `closed` = Completed/Removed.
- **Fields:** `--field Name=Value` accepts a friendly name ("Story Points") or reference name (`Microsoft.VSTS.Scheduling.StoryPoints`). Values are checked against the field type (int, double, bool, datetime, identity, picklist values). `Name=` clears a field. An unknown field errors with close matches.
- **Identities:** `@me`, email/UPN or display name, resolved through the MVP identity resolver. Ambiguous names error and list candidates.
- **Paths:** `--area` / `--iteration` are checked against classification nodes. `--iteration @current` = the team's current sprint.
- **Team:** `--team` > config `team` (per project) > project default team.
- **Body format:** multiline fields (Description, Repro Steps, Acceptance Criteria, comments) are sent as Markdown (`multilineFieldsFormat` / comment `format=markdown`). Orgs without Markdown support fall back to Markdown→HTML. On read, Markdown fields render with glamour on TTY, HTML fields are converted to plain text, and piped output stays raw.
- **Process metadata** (types, states, fields, categories) is fetched lazily once per invocation.

### FR2: `ado workitem create`
- `--type`, `-t/--title`, `-b/--body`, `-F/--body-file` (`-` = stdin), `-e/--editor`, `-a/--assignee`, `--area`, `--iteration`, `--tag` (repeatable/comma; alias `-l/--label`), `--parent <id>`, `-p/--priority 1-4`, `-f/--field Name=Value` (repeatable).
- TTY with missing input: prompts for type (select), title, body (`$EDITOR`), then gh-style "Submit / Add metadata / Cancel". Non-TTY: fails naming the missing `--type` or `--title`.
- `-w/--web` opens the new-work-item form prefilled with title/body.
- Prints the work item URL.

### FR3: `ado workitem list`
- `--type`, `-s/--state {open|closed|all|<state name>}` (default open), `-a/--assignee` (`@me`, user, `none`), `-A/--author`, `--area` / `--iteration` (under path; `@current`), `--tag` / `-l/--label` (all must match), `-S/--search <text>` (Work Item Search API, then intersected with the other filters through WIQL `ID IN`), `-L/--limit` (default 30), `-w/--web`.
- Scope: the whole project, sorted by changed date (newest first).
- Filters become WIQL with every value escaped. Items are fetched in batches of 200.
- TTY table: ID, type, state, title, assignee, tags, updated (relative). Non-TTY: TSV with RFC3339 times.
- `--json/--jq/--template` fields: `id,type,title,state,reason,assignedTo,createdBy,createdDate,changedDate,areaPath,iterationPath,tags,priority,parent,url,fields` (`fields` = all raw fields).
- Empty result: `no work items match your search` to stderr, exit 0.

### FR4: `ado workitem view <id>`
- Header (type, ID, title, state + reason), then assignee, area, iteration, priority, tags, created/changed by and date, description.
- Relations grouped as parent, children, related, other work item links, PRs, commits, branches, hyperlinks and attachments, with titles/state shown for linked work items.
- `-c/--comments`: comments (newest last). `--history`: field change log from the updates API.
- `-w/--web`. JSON: FR3 fields plus `description,relations,comments,history` (`comments`/`history` fetched only when requested).

### FR5: `ado workitem edit <id>...`
- Takes several IDs, so the same edits can hit many items.
- `-t`, `-b`, `-F`, `-e`, `--state <name>`, `-a/--assignee`, `--unassign`, `--area`, `--iteration`, `-p/--priority`, `--add-tag` / `--remove-tag` (aliases `--add-label` / `--remove-label`), `--add-link` / `--remove-link <type>:<target>` (same targets as FR8), `-f/--field Name=Value`.
- One JSON Patch per item, with a `test /rev` check. A concurrency conflict errors with `work item 123 changed since read; retry`.
- TTY with no flags: interactive field picker prefilled with current values. Non-TTY with no flags: error listing the flags.
- With several IDs, a failure on some exits 1 and says which items succeeded and which failed.

### FR6: `ado workitem close <id>...` / `reopen <id>...`
- `close`: first Completed-category state of the item's type. `--resolve` uses the Resolved category instead (errors if the type has none). `--removed` uses the Removed category (≈ gh "not planned"). `-r/--reason <text>` sets `System.Reason` (validated against allowed values). `--state <name>` overrides all of these.
- `reopen`: first Proposed-category state, or `--state`.
- `-c/--comment` adds a comment in the same operation.
- Already in a target-category state: warning, exit 0.

### FR7: `ado workitem comment <id>` / `assign <id>...`
- `comment`: `-b`, `-F`, `-e` (TTY with no body opens the editor; non-TTY fails naming `--body`), `--edit-last`, `--delete-last` (confirms on TTY, `--yes` when non-interactive). Prints the comment URL.
- `assign`: `--to <user>` (default `@me`), `--unassign`. Takes several IDs.

### FR8: `ado workitem link <id>` / `unlink <id>`
- Targets (repeatable): `--parent <id>` (one only; replaces the existing parent after confirmation on TTY), `--child <id>`, `--related <id>`, `--link <type>=<id>` (any link type by friendly or reference name, e.g. `Predecessor=12`, `Tested By=7`), `--pr <ref>` (PR artifact link in the current/`-R` repo), `--commit <sha>`, `--branch <name>`, `--url <url>`.
- `unlink` takes the same flags and removes the matching relation. No match: error.

### FR9: `ado workitem delete <id>...`
- Default: soft delete to the recycle bin, printing a restore hint. `--permanent`: destroy (irreversible, named in the prompt).
- TTY confirmation lists the IDs and titles. `-y/--yes` skips it. Non-interactive without `--yes` refuses.

### FR10: `ado workitem query`
- `ado workitem query <wiql | path | id>`: a GUID runs a saved query by ID. Text starting with `SELECT` (case-insensitive) runs as WIQL. Anything else is a saved query path (`"Shared Queries/Team/Active Bugs"`, `"My Queries/x"`). `--wiql-file <file|->` reads WIQL from a file or stdin.
- Macros (`@Me`, `@Today`, `@CurrentIteration`, `@Project`, `@TeamAreas`) are passed through. Team-scoped macros use the FR1 team (`--team`).
- Flat queries: table built from the query's own columns. Tree / one-hop (`FROM WorkItemLinks`) queries: nested, indented on TTY, with a link-type column for one-hop.
- `-L/--limit` (default 200), `-w/--web` (saved queries only).
- JSON: flat = array of items with the selected fields. Link queries = nested `[{...fields, links: [{rel, item}]}]`.
- `ado workitem query list [--folder <path>]`: saved query tree (My Queries / Shared Queries) with paths and IDs. JSON fields `id,name,path,isFolder,queryType,isPublic`.

### FR11: `ado sprint`
- `sprint list [--timeframe past|current|future|all]` (default all): name, path, start, finish, timeframe. JSON: `id,name,path,startDate,finishDate,timeFrame`.
- `sprint current`: name, dates, working days left (from the team's working days). JSON. `-w`.
- `sprint view [<iteration>]` (default current): sprint work items grouped by state category, children nested under parents. `-a/--assignee`, `--type`. JSON. `-w`.
- `sprint board [<iteration>]` (default current): taskboard columns side by side on TTY (stacked when the terminal is too narrow), cards `#id title (assignee)`. No capacity or burndown. Non-TTY: TSV `column,id,title,assignee,parent`. JSON.

### FR12: `ado board [<backlog>]`
- Team Kanban board for a backlog level (default: requirement backlog, e.g. Stories / Backlog items; also `features`, `epics` or the level name).
- Columns side by side on TTY with WIP limits (`Active 3/5`) and split Doing/Done sub-columns. Cards placed by the team's board column fields.
- `--team`, `-a/--assignee`, `-w/--web`, JSON (`columns[{name,isSplit,wipLimit,items[...]}]`).

### FR13: Aliases & web
- `ado wi` and `ado issue` alias `ado workitem`.
- `-w/--web` on create, list, view, query (saved), sprint current/view and board. Added to the web-flag audit allowlist if `repo_browse_20261010` exists.

## Non-Functional Requirements
- **Output contract:** every read command (`list`, `view`, `query`, `query list`, `sprint list/current/view/board`, `board`) supports `--json/--jq/--template`. Field names are public API.
- **Safety:** `delete` (and `--permanent`), `comment --delete-last` and parent replacement confirm on TTY and require `--yes` when non-interactive.
- **Security:** user values are escaped before going into generated WIQL (no WIQL injection through flags). Tokens are never logged.
- **Dependencies:** `charmbracelet/glamour` (shared with `repo_browse_20261010`) and a small HTML→text library (e.g. `k3a/html2text`), recorded in `tech-stack.md` before use.
- **Testing:** >90% coverage. httpmock fixtures for all four process templates (type categories, states, fields) plus one inherited custom process. TTY and non-TTY covered.
- **Docs:** `docs/gh-differences.md` covers `issue` → work items, `--type`, state categories vs open/closed, `--resolve`/`--removed`, `--field`, tags vs labels, WIQL/saved queries, sprint/board (no gh equivalent), recycle-bin delete.

## Acceptance Criteria
- [ ] `ado issue create --type story -t T` creates a User Story on Agile, a Product Backlog Item on Scrum, an Issue on Basic and a Requirement on CMMI.
- [ ] `ado wi list -a @me -s open --iteration @current --json id,title` works. `-S "crash"` narrows by text.
- [ ] `ado wi view 123 -c --history` shows relations, comments and history. Markdown renders on TTY and is raw when piped.
- [ ] `ado wi edit 123 124 --add-tag x -f "Story Points=5"` updates both items. Unknown fields list close matches.
- [ ] `ado wi close 123` reaches Closed (Agile), Done (Scrum/Basic). `--resolve` reaches Resolved where it exists. `reopen` restores a Proposed state.
- [ ] `ado wi link 123 --parent 100 --pr 45` and `unlink` work.
- [ ] `ado wi delete 123` confirms on TTY and refuses non-TTY without `-y`. `--permanent` destroys.
- [ ] `ado wi query "Shared Queries/Active Bugs"`, inline WIQL, `--wiql-file -`, tree queries and `query list` work.
- [ ] `ado sprint current`, `sprint view`, `sprint board` and `ado board` render on TTY and emit JSON.
- [ ] Docs updated. Coverage >90%. CI green on linux/macos/windows.

## Out of Scope
- Attachments upload/download, work item templates, bulk CSV import/export.
- Capacity, burndown, sprint/iteration creation or editing, team management, delivery plans, test plans.
- `wi develop` (create linked branch), `wi pin`, `wi transfer` (move between projects), follow/notifications.
- Moving cards on boards (`board move`). Use `wi edit --state` or `-f` with the board column field.
