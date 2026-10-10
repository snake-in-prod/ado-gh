# Plan: Work Items and Boards — gh issue Parity

Track: `workitems_20261010` · Spec: [spec.md](./spec.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Work Item Foundation

- [ ] Task: Test fixtures for process templates
    - [ ] httpmock fixtures (types, categories, states, fields, link types) for Agile, Scrum, Basic, CMMI and one inherited custom process
- [ ] Task: Reference parsing and API layer (`api/workitem.go`, `pkg/cmd/workitem/shared`)
    - [ ] Write tests: `123`, `#123`, URL forms, invalid refs; batch get splits at 200; not-found error
    - [ ] Implement SDK wrappers (WIT client: get/batch/create/update/delete, comments, updates, relations, link types, queries, classification nodes)
- [ ] Task: Process metadata resolver
    - [ ] Write tests: type aliases (`bug/story/task/feature/epic`) resolve via categories per process fixture, exact names case-insensitive, unknown type lists valid ones; state categories per type; `open`/`closed` expansion; fetched once per invocation
    - [ ] Implement resolver
- [ ] Task: Field resolver and value coercion
    - [ ] Write tests: friendly vs reference names, int/double/bool/datetime/identity/picklist coercion, `Name=` clears, unknown field → close matches, bad value → error naming field and type
    - [ ] Implement
- [ ] Task: Identity, path and team resolution
    - [ ] Write tests: `@me`, email, display name, ambiguous → candidates (reusing MVP resolver); area/iteration validated against classification nodes; `@current` iteration; team precedence `--team` > config `team` > default team
    - [ ] Implement; add `team` config key
- [ ] Task: Body format and rendering
    - [ ] Record `charmbracelet/glamour` (if not already) and an HTML→text library in `tech-stack.md`, then `go get`
    - [ ] Write tests: Markdown sent with `multilineFieldsFormat`, fallback to Markdown→HTML when unsupported, read path renders Markdown with glamour on TTY, HTML → text, raw when piped
    - [ ] Implement
- [ ] Task: WIQL builder
    - [ ] Write tests: each filter → clause, single quotes escaped, injection attempts stay literal, `UNDER` for paths, `@Me`/`@CurrentIteration` macros, ordering by changed date
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Work Item Foundation' (Protocol in workflow.md)

## Phase 2: `create`, `list`, `view`, Aliases

- [ ] Task: Command group and aliases
    - [ ] Write tests: `ado workitem`, `ado wi`, `ado issue` resolve to the same group; help lists subcommands
    - [ ] Implement group
- [ ] Task: `ado workitem create`
    - [ ] Write tests: all flags map to JSON Patch ops, `--parent` adds hierarchy link, `--field` passthrough, TTY prompts + Submit/Add metadata/Cancel, non-TTY missing `--type`/`--title` errors, `-w` prefilled form URL, prints URL
    - [ ] Implement `create`
- [ ] Task: `ado workitem list`
    - [ ] Write tests: each filter in generated WIQL, defaults (open, limit 30, changed-date order), `-S` intersects search IDs with filters, TTY table vs TSV, `--json` fields incl. `fields`, empty message, `-w`
    - [ ] Implement `list` (incl. Work Item Search API client)
- [ ] Task: `ado workitem view`
    - [ ] Write tests: header and fields, grouped relations with linked titles, `-c` comments, `--history` updates, JSON incl. lazy `comments`/`history`, `-w`
    - [ ] Implement `view`
- [ ] Task: Conductor - User Manual Verification 'Phase 2: create, list, view, Aliases' (Protocol in workflow.md)

## Phase 3: `edit`, `close`, `reopen`, `comment`, `assign`

- [ ] Task: `ado workitem edit`
    - [ ] Write tests: every flag → patch op, multiple IDs, `test /rev` conflict message, add/remove tags merge correctly, `--add-link`/`--remove-link`, TTY field picker, non-TTY no flags error, partial failure summary exit 1
    - [ ] Implement `edit`
- [ ] Task: `ado workitem close` / `reopen`
    - [ ] Write tests per process fixture: default Completed state, `--resolve` (and error when no Resolved), `--removed`, `-r` validated reason, `--state` override, `-c` comment, already-in-state warning exit 0, multiple IDs; `reopen` → Proposed state
    - [ ] Implement
- [ ] Task: `ado workitem comment`
    - [ ] Write tests: body sources, editor on TTY, non-TTY missing `--body`, Markdown format param, `--edit-last`, `--delete-last` confirm/`--yes`, prints URL
    - [ ] Implement (reuse shared body input helper)
- [ ] Task: `ado workitem assign`
    - [ ] Write tests: default `@me`, `--to`, `--unassign`, multiple IDs
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 3: edit, close, reopen, comment, assign' (Protocol in workflow.md)

## Phase 4: `link`, `unlink`, `delete`

- [ ] Task: Link target parsing
    - [ ] Write tests: `--parent/--child/--related`, `--link Type=id` (friendly + reference names), `--pr` / `--commit` / `--branch` artifact URIs from repo context, `--url` hyperlink, invalid type error
    - [ ] Implement shared parser (also used by `edit --add-link/--remove-link`)
- [ ] Task: `ado workitem link` / `unlink`
    - [ ] Write tests: adds relations, replacing parent confirms on TTY / needs `--yes` non-TTY, unlink finds relation index by URL, no match error
    - [ ] Implement
- [ ] Task: `ado workitem delete`
    - [ ] Write tests: soft delete + restore hint, `--permanent` destroy, TTY confirm lists IDs/titles, `-y` skips, non-TTY without `--yes` refuses, multiple IDs
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 4: link, unlink, delete' (Protocol in workflow.md)

## Phase 5: `workitem query`

- [ ] Task: Query input resolution
    - [ ] Write tests: GUID → saved ID, `SELECT…` → WIQL, other text → saved path (My Queries / Shared Queries), `--wiql-file` file and stdin, macros passed through, team-scoped WIQL endpoint with `--team`
    - [ ] Implement
- [ ] Task: Result rendering
    - [ ] Write tests: flat results use query columns, tree and one-hop rendered nested on TTY with link type, `--limit` → `$top`, JSON flat array and nested link shape, `-w` for saved queries only
    - [ ] Implement
- [ ] Task: `ado workitem query list`
    - [ ] Write tests: folder tree with paths/IDs, `--folder` filter, JSON fields
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 5: workitem query' (Protocol in workflow.md)

## Phase 6: `sprint`, `board`, Web, Docs

- [ ] Task: `ado sprint list` / `current`
    - [ ] Write tests: `--timeframe` filter, JSON fields, working days left from team settings (fake clock), no current sprint message, `-w`
    - [ ] Implement
- [ ] Task: `ado sprint view`
    - [ ] Write tests: default current iteration, grouping by state category, parent/child nesting, `-a`/`--type` filters, JSON, `-w`
    - [ ] Implement
- [ ] Task: Column renderer (shared by `sprint board` and `board`)
    - [ ] Write tests: side-by-side columns at given widths, stacked fallback when narrow, card truncation, WIP header `Active 3/5`, split Doing/Done
    - [ ] Implement
- [ ] Task: `ado sprint board`
    - [ ] Write tests: taskboard columns + item mapping, TTY render, non-TTY TSV, JSON
    - [ ] Implement
- [ ] Task: `ado board`
    - [ ] Write tests: default requirement backlog per process, `features`/`epics`/level name, team board column fields place cards, `-a` filter, JSON shape, `-w`
    - [ ] Implement
- [ ] Task: Web audit and docs
    - [ ] Add new `--web` commands to the web-flag audit allowlist (if present)
    - [ ] Update `docs/gh-differences.md` with every divergence in the spec
    - [ ] Verify help text/examples for `workitem *`, `sprint *`, `board`
- [ ] Task: Conductor - User Manual Verification 'Phase 6: sprint, board, Web, Docs' (Protocol in workflow.md)
