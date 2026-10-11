# Plan: Work Items and Boards — gh issue Parity

Track: `workitems_20261010` · Spec: [spec.md](./spec.md) · Research: [research.md](./research.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

Every new `workitem`/`sprint`/`board` command is classified in `webCmds`/`noWebCmds` (pkg/cmd/root/root_test.go) in the task that adds it; `TestWebFlagAudit` fails on unclassified commands.

Live checks (every phase's manual verification) run in snake-in-prod/ado-gh: test items get the tag `ado-live-test` and an `[ado-live]` title prefix; each phase ends by destroying them (`ado wi delete <ids> --permanent -y`, fallback `ado api -X DELETE '{project}/_apis/wit/workitems/<id>?destroy=true'`) and checking `{project}/_apis/wit/recyclebin` is empty.

---

## Phase 1: Work Item Foundation [checkpoint: 684a976]

- [x] Task: Shared helpers refactor `684a976`
    - [x] Write tests: body flags/`ReadBody` from `pkg/cmdutil`; Markdown rendering from the shared package (TTY detection, style, wrap width); `WorkItemClient` relation add/remove with `(rel, url, attributes)`; existing `pr` and `repo view` tests still pass unchanged
    - [x] Implement: move `BodyFlags`/`AddBodyFlags`/`ReadBody` from pkg/cmd/pr/shared/body.go to pkg/cmdutil; extract `isRealTerminal`/`markdownStyle`/`renderMarkdown` from pkg/cmd/repo/view/view.go to a shared package (e.g. pkg/markdown); move `PullRequestArtifactURL`/`LinkWorkItem`/`UnlinkWorkItem` from api/pr.go to `WorkItemClient`, generalised to `(rel, url, attributes)`, with `PRClient` and pkg/cmd/pr/edit/links.go calling them
- [x] Task: Test fixtures for process templates `684a976`
    - [x] Capture system-process types and states (Agile, Scrum, Basic, CMMI) from the org-level process API with `ado api` (`_apis/work/processes` → each process's work item types with states; no hard-coded process IDs), converted to the project-scoped `wit` response shape, scrubbed of identities/GUIDs/org, into `testdata/live_<process>_*.json`
    - [x] Hand-write from Learn docs: categories, fields, link types, boards/backlogs per process, and one inherited custom process (custom state, type and field)
    - [x] Add pkg/httpmock/wit.go: `WITLocations` slice (types, states, categories, fields, type-fields, relation types, workitems, workitemsbatch, wiql, queries, comments, recyclebin, classification nodes), work and core locations, `StubResourceAreas` for search; distinct org name per test (SDK discovery is cached per base URL); JSON collections use `"value"`
- [x] Task: Reference parsing and API layer (`api/workitem.go`, `pkg/cmd/workitem/shared`) `684a976`
    - [x] Write tests: `123`, `#123`, URL forms, invalid refs; batch get in chunks of 200 with `errorPolicy=Omit`, nil-Id entries skipped, WIQL order restored, never `fields` together with `$expand`; `$top` always sent; VS402337 → "narrow filters / use -L" message; `IsRevConflict` (TypeKey `TestPatchOperationFailedException`, then `VS403351`/status fallback); `multilineFieldsFormat` decoded from raw JSON; not-found error
    - [x] Implement `WorkItemClient{Wit, Work, Core, SDK, Project}` built from `NewSDKClient`, own JSON structs via `toX` mappers, SDK wrappers (WIT client: get/batch/create/update/delete, comments, updates, relations, link types, queries, classification nodes); carry `TypeKey` into `HTTPError`; decode `multilineFieldsFormat` via `SDK.Send`
- [x] Task: Process metadata resolver `684a976`
    - [x] Write tests per process fixture: resolver order exact name (case-insensitive) → category default → alias as type name → error listing valid types; aliases `bug/story/task/feature/epic` via categories; disabled and Hidden-category types excluded; Basic `--type bug` uses whatever `Microsoft.BugCategory` resolves to (likely Issue), else error; state categories per type; `open`/`closed` expansion; `--resolve` picks the Resolved-category state first, then a state named "Resolved" (case-insensitive), else error; `--removed` errors on Basic/CMMI/Agile Bug; reopen falls back to the first In Progress state when there is no Proposed state (Agile Issue, Scrum Impediment); per-type `/states` fallback if the types list lacks `states`; fetched once per invocation
    - [x] Implement resolver
- [x] Task: Field resolver and value coercion `684a976`
    - [x] Write tests: friendly vs reference names, `isIdentity`/`isPicklist` checked before `type`, int/double/bool/datetime/identity/picklist coercion, allowed values fetched per type/field with `$expand=allowedValues` (never `All`), `Name=` clears, unknown field → close matches, bad value → error naming field and type
    - [x] Implement using `GetWorkItemFields` for types
- [x] Task: Identity, path and team resolution `684a976`
    - [x] Write tests: `@me`, email, display name, ambiguous → candidates (reusing MVP resolver), new resolver method returns the matched identity (account/display name) for `System.AssignedTo`; list filters use WIQL `@Me`; area/iteration validated against classification nodes; `@current` resolved via `GetTeamIterations($timeframe=current)` to a concrete iteration path; team resolution returns a concrete team: `--team` > config `team` > `core.TeamProject.DefaultTeam`
    - [x] Write tests for the `team` config key: stored under a hosts-map scope key `dev.azure.com/ORG/PROJECT`, set with `ado config set team X --project ORG/PROJECT`, `Get` falls back to a global `team`, `config get team --project` reads it back
    - [x] Write tests for project context: `-R ORG/PROJECT` accepted by project-scoped commands only (workitem, sprint, board) outside a git repo; repo-scoped commands still require ORG/PROJECT/REPO; git remote and `ADO_REPO` still resolve as before
    - [x] Implement resolvers; add the `team` config key with the project scope; accept `-R ORG/PROJECT` for project-scoped commands in internal/adocontext
- [x] Task: Body format and rendering `684a976`
    - [x] Record in `tech-stack.md`: own HTML→text converter on `golang.org/x/net/html` (glamour is already recorded); promote `golang.org/x/net` (and `goldmark`, for Markdown→HTML) to direct dependencies with `go get`; reword spec.md:96 (k3a/html2text → own converter)
    - [x] Write tests: value op and `multilineFieldsFormat` op in one patch; reactive fallback: a 400 mentioning the format op retries once as HTML (Markdown→HTML); format-preserving default: Markdown→HTML for HTML fields, Markdown for Markdown or empty fields, forced switch when asked; Markdown vs HTML detection case-insensitive with missing key = HTML; read path renders Markdown with glamour on TTY; HTML → text handles `<div>` lines, `<ol>` numbering, links, tables; raw when piped
    - [x] Implement
- [x] Task: WIQL builder `684a976`
    - [x] Write tests: each filter → clause; `[System.TeamProject] = @project` always present; quote-doubling for `O'Brien`, `' OR 1=1` payloads, backslash, `]`, `@Me` literal stay literal; one `[System.Tags] CONTAINS` clause per tag joined with AND; `UNDER` for paths; `@current` emitted as `UNDER '<path>'` (resolved path); `@Me` macro; `-S` IDs formatted as ints; length ≤ 32,000 chars; field names only from resolved reference names; `ORDER BY [System.ChangedDate] DESC, [System.Id] DESC`
    - [x] Implement
- [x] Task: Conductor - User Manual Verification 'Phase 1: Work Item Foundation' (Protocol in workflow.md)
    - [x] Detect the live process: `ado api '_apis/projects/{project}?includeCapabilities=true' --jq .capabilities.processTemplate`, then `ado api "_apis/work/processes/<templateTypeId>"`
    - [x] Create `ado-live-test` items with `ado api`; check `@current` resolution and `-R ORG/PROJECT` outside a git repo; destroy items, empty recycle bin

## Phase 2: `create`, `list`, `view`, Aliases [checkpoint: cb0f253]

- [x] Task: Command group and aliases `cb0f253`
    - [x] Write tests: `ado workitem`, `ado wi`, `ado issue` resolve to the same group; help lists subcommands
    - [x] Implement group
- [x] Task: `ado workitem create` `cb0f253`
    - [x] Write tests: all flags map to JSON Patch ops, `--parent` adds hierarchy link, `--field` passthrough, TTY prompts + Submit/Add metadata/Cancel, non-TTY missing `--type`/`--title` errors, prints URL
    - [x] Write `-w` tests: literal `[Ref.Name]=` keys, path-escaped type, `%20`/`%2B`/`%5C`; every given flag prefilled (title, body, assignee, tags, area, iteration, priority, `--field`) with a warning that `--parent` is ignored; body sent to `System.Description` as Markdown as-is; 8192-byte guard "cannot open in browser: maximum URL length exceeded"
    - [x] Implement `create`; add `ProjectURL`/`WorkItemURL` and move `queryEscape` into internal/adocontext/weburl.go, with `browse` using them; classify in `webCmds`
- [x] Task: `ado workitem list` `cb0f253`
    - [x] Write tests: each filter in generated WIQL, defaults (open, limit 30, changed-date order), TTY table vs TSV, `--json` fields incl. `fields`, empty message
    - [x] Write `-S` tests: search ordered by changed date; pages of ≤ 1000 IDs intersected by `[System.Id] IN (...)` until `--limit` filtered items are found, `$skip` by `len(results)`, stop on short page or `count`; ID parsing from lowercase `fields["system.id"]`; infoCode ≠ 0 → stderr warning; 404/missing search area or Azure DevOps Server → "work item search not available" error
    - [x] Write `-w` tests: opens the ad-hoc query URL `_queries/query/?wiql=...` with the generated WIQL; falls back to the Work Items hub `{project}/_workitems` over the URL length limit
    - [x] Implement `list` (search client via a shared `areaClient(ctx, areaID)` helper generalised from `imsClient`); classify in `webCmds`
- [x] Task: `ado workitem view` `cb0f253`
    - [x] Write tests: header and fields, grouped relations with linked titles, `-c` comments, `--history` updates, JSON incl. lazy `comments`/`history`, `-w`
    - [x] Implement `view`; classify in `webCmds`
- [x] Task: Conductor - User Manual Verification 'Phase 2: create, list, view, Aliases' (Protocol in workflow.md)
    - [x] Live checks with `ado-live-test` items: `@Me`, `@CurrentIteration('[P]\T')`, tag CONTAINS semantics, `-S` with an Entra token on almsearch (item a few minutes old), `create -w` prefill URL incl. Markdown-mode Description; destroy items, empty recycle bin

## Phase 3: `edit`, `close`, `reopen`, `comment`, `assign` [checkpoint: f843b18]

- [x] Task: `ado workitem edit` `f843b18`
    - [x] Write tests: every flag → patch op, multiple IDs, `test /rev` conflict detected with `IsRevConflict` and its message; tags computed client-side (`replace` with the joined set, `add` when none, `replace ""` when all removed); `--add-link`/`--remove-link` with relation removes in descending index order inside one `test /rev` patch; `--body` keeps the field's current format, `--markdown` forces the switch to Markdown; TTY field picker, non-TTY no flags error, partial failure summary exit 1
    - [x] Implement `edit` (incl. `--markdown`); optionally drop the 412 assumption in api/pr_workitems_test.go:119; classify in `noWebCmds`
- [x] Task: `ado workitem close` / `reopen` `f843b18`
    - [x] Write tests per process fixture: default Completed state, `--resolve` (category, then state named "Resolved", else error), `--removed`, `-r` pre-checked against the type-level `System.Reason` allowedValues while still relying on the server; TF401320 Required fixture (Resolved Reason) shown verbatim with a `-f` hint; `--state` override; `-c` comment sent as `/multilineFieldsFormat/System.History` = Markdown in the same patch, falling back to a separate `AddWorkItemComment(format=markdown)` if rejected; already-in-state warning exit 0; multiple IDs; `reopen` → Proposed state, falling back to the first In Progress state
    - [x] Implement; classify in `noWebCmds`
- [x] Task: `ado workitem comment` `f843b18`
    - [x] Write tests: body sources, editor on TTY, non-TTY missing `--body`, `AddWorkItemComment`/`UpdateWorkItemComment` with `format=markdown` (never `AddComment`/`UpdateComment`), `--edit-last`/`--delete-last` via `GetComments(order=desc)` filtered by the authenticated user's id with paging, "no own comment found", `--delete-last` confirm/`--yes` (soft delete; SDK returns only `error`), prints URL
    - [x] Implement (reuse shared body input helper); classify in `noWebCmds`
- [x] Task: `ado workitem assign` `f843b18`
    - [x] Write tests: default `@me`, `--to`, `--unassign`, multiple IDs
    - [x] Implement; classify in `noWebCmds`
- [x] Task: Conductor - User Manual Verification 'Phase 3: edit, close, reopen, comment, assign' (Protocol in workflow.md)
    - [x] Live checks with `ado-live-test` items: edit/close/reopen/comment/assign round-trips, `close -c` History Markdown; destroy items, empty recycle bin

## Phase 4: `link`, `unlink`, `delete`, `restore` [checkpoint: 541a2bd]

- [x] Task: Link target parsing `541a2bd`
    - [x] Write tests: `--parent/--child/--related`, `--link Type=id` looked up in live `GetRelationTypes` (friendly or reference name, case-insensitive; custom types), `--pr` / `--commit` / `--branch` artifact URIs from repo context (branch URL-encoded after `GB`, short sha resolved to 40 chars), `--url` hyperlink, invalid type error
    - [x] Implement shared parser (also used by `edit --add-link/--remove-link`); commit and branch artifact builders beside `PullRequestArtifactURL`
- [x] Task: `ado workitem link` / `unlink` `541a2bd`
    - [x] Write tests: adds relations; replacing a parent is one patch `test /rev` + `remove` + `add`, confirms on TTY / needs `--yes` non-TTY; TF201036/TF201035 matched by code (TF201035 = success); no auto-replace for Duplicate; unlink matches by `rel` + `/workItems/{id}` suffix, not exact URL; no match error
    - [x] Implement; classify in `noWebCmds`
- [x] Task: `ado workitem delete` `541a2bd`
    - [x] Write tests: soft delete via `DeleteWorkItem` + restore hint `ado wi restore 123`, `--permanent` via `DeleteWorkItem{Destroy: true}` (not `DestroyWorkItem`), 403 hint naming "Delete and restore work items" / "Permanently delete work items", TTY confirm lists IDs/titles, `-y` skips, non-TTY without `--yes` refuses, multiple IDs loop over single `DeleteWorkItem` calls with a partial-failure summary; httpmock routes for DELETE with `destroy` and `workItemRelationTypes`
    - [x] Implement; classify in `noWebCmds`
- [x] Task: `ado workitem restore` `541a2bd`
    - [x] Write tests: `ado workitem restore <id>...` restores from the recycle bin (`PATCH recyclebin/{id}` `isDeleted=false`), multiple IDs with partial-failure summary, not-in-recycle-bin error, prints URL
    - [x] Implement `RestoreWorkItem` wrapper and command; classify in `noWebCmds`
- [x] Task: Conductor - User Manual Verification 'Phase 4: link, unlink, delete, restore' (Protocol in workflow.md)
    - [x] Live checks with `ado-live-test` items: `link --pr` with PR !1, `--commit`/`--branch` from this repo, delete + restore, `delete --permanent` with a Read & write PAT; destroy items, empty recycle bin

## Phase 5: `workitem query` [checkpoint: 02d8e99]

- [x] Task: Query input resolution `02d8e99`
    - [x] Write tests: GUID → saved ID, `SELECT…` → WIQL, other text → saved path (My Queries / Shared Queries) via `GetQuery(path, $expand=wiql)` then `QueryById(id, Team)`, folder → "not a query" error, `--wiql-file` file and stdin, macros passed through, team-scoped WIQL endpoint with `--team`; mocks stub the SDK OPTIONS lookup
    - [x] Implement
- [x] Task: Result rendering `02d8e99`
    - [x] Write tests: flat results use query columns (`columns[].referenceName`) via the Phase 1 batch fetcher; trees built from `workItemRelations` (no source = root, grouped by `source.id`, `rel` as link-type column), tree and one-hop rendered nested on TTY; flat `--limit` → `$top`; link queries send no `$top` and trim to `--limit` roots client-side; JSON flat array with known fields mapped to `wi list` names and others under a `fields` map keyed by reference name, nested link shape; note when results reach `--limit` or 20,000; `-w` for saved queries only
    - [x] Implement; classify in `webCmds`
- [x] Task: `ado workitem query list` `02d8e99`
    - [x] Write tests: folder tree with paths/IDs, recursion with `GetQuery(folderId, $depth=2)` for folders with `hasChildren` and no `children`, `--folder` filter, JSON fields
    - [x] Implement; classify in `noWebCmds`
- [x] Task: Conductor - User Manual Verification 'Phase 5: workitem query' (Protocol in workflow.md)
    - [x] Setup: shared flat and tree queries; live checks: `%2F`-encoded query paths, flat/tree results; delete test queries and `ado-live-test` items, empty recycle bin

## Phase 6: `sprint`, `board`, Web, Docs [checkpoint: f9b5be1]

- [x] Task: `ado sprint list` / `current` `f9b5be1`
    - [x] Write tests: `--timeframe` filtered client-side on `attributes.timeFrame` (only `current` reaches `$timeframe`; past/future never appear in the query string), JSON fields, working days left from team settings minus `GetTeamDaysOff` for the iteration (fake clock), no current sprint message, `-w`
    - [x] Implement (add `work.ClientImpl` wrapper); classify `sprint current` in `webCmds`, `sprint list` in `noWebCmds` with reason
- [x] Task: `ado sprint view` `f9b5be1`
    - [x] Write tests: default current iteration, grouping by state category, parent/child nesting, titles/assignees via the batch fetcher, `-a`/`--type` filters, JSON, `-w`
    - [x] Implement; classify in `webCmds`
- [x] Task: Column renderer (shared by `sprint board` and `board`) `f9b5be1`
    - [x] Write tests: side-by-side columns at given widths, stacked fallback when narrow, card truncation, WIP header `Active 3/5`, split Doing/Done
    - [x] Implement
- [x] Task: `ado sprint board` `f9b5be1`
    - [x] Write tests: taskboard columns + item mapping with a concrete team always passed, titles via the batch fetcher, TTY render, non-TTY TSV, JSON
    - [x] Implement; classify in `noWebCmds` with reason
- [x] Task: `ado board` `f9b5be1`
    - [x] Write tests: default level from `GetBacklogConfigurations().RequirementBacklog` (or `GetBacklogs` `type == "requirement"`), `features`/`epics`/level name, `GetBoard(name)` columns and fields; cards from a team-scoped WIQL (category via IN GROUP, `@TeamAreas`, `State <> Removed`, completed items limited by recent ChangedDate) plus a batch call with the WEF column/done fields; cards placed by `fields.columnField`/`doneField`, not `System.BoardColumn`; `itemLimit` 0 = no limit; nil `isSplit` = false; `-a` filter, JSON shape, `-w`
    - Note (review 2026-10-11): cards are filtered by the board's state-mapped work item types and states instead of `IN GROUP` + `State <> Removed`, so bugs shown on the backlog appear too.
    - [x] Implement; classify in `webCmds`
- [x] Task: Web audit and docs `f9b5be1`
    - [x] Sprint/board URL builders in internal/adocontext/weburl.go (taskboard `{team}/{project}/{encodeURIComponent(rest)}`, board `_boards/board/t/{team}/{backlogLevelId}`)
    - [x] Check every new `workitem`/`sprint`/`board` command is classified in `webCmds`/`noWebCmds`
    - [x] README PAT scopes: add "Work Items (Read & write)" and "Project and Team (Read)"; mention Work Items (Full) only if the live destroy check with Read & write fails
    - [x] Update `docs/gh-differences.md` with every divergence in the spec, incl. `-S` uses ADO search syntax (`a:`, `c:`, `s:`, `t:`, `tags:`) and `-R ORG/PROJECT` for project-scoped commands
    - [x] Verify help text/examples for `workitem *`, `sprint *`, `board`
- [x] Task: Conductor - User Manual Verification 'Phase 6: sprint, board, Web, Docs' (Protocol in workflow.md)
    - [x] Setup: give one iteration dates covering today, selected by the default team; live checks: work-area routing, sprint taskboard and board URLs, days off; clear test iteration dates, destroy `ado-live-test` items, empty recycle bin
