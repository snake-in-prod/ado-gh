# Work item process fixtures

Response bodies for the work item metadata endpoints, one set per process: the system
processes Agile, Scrum, Basic and CMMI, plus `inherited`, a process inherited from Agile.
Load them in tests with `internal/witfixtures` (`Read`, `Process`, `TypeStates`) and serve them
with `httpmock.JSONResponse(json.RawMessage(b))` on the routes in `httpmock.WorkItemLocations`.

`live_*` files are captured from Azure DevOps Services (read-only `ado api` GETs, October 2026);
the rest are hand-written from Microsoft Learn (process guides, "How workflow category states are
used", the REST 7.1 samples), using the live files as the shape. All files are scrubbed: org
`org`, project id `00000000-0000-0000-0000-000000000001`, team id `...0002`, every other GUID
replaced by `00000000-0000-0000-0000-0000000xxxxx`, the board extension id in `WEF_..._Kanban.*`
by zeros, and no identities. `TestFixturesScrubbed` in internal/witfixtures enforces this and
that every file is listed below.

## Types and states

`{project}/_apis/wit/workitemtypes` shape (`count`/`value`; each type has `name`,
`referenceName`, `description`, `color`, `icon`, `isDisabled`, `states` of
`{name, color, category}` in workflow order, `url`; `fields`, `transitions`, `xmlForm` and
`fieldInstances` are left out). `witfixtures.TypeStates` derives the per-type
`.../workitemtypes/{type}/states` response from these.

| File | Source |
|---|---|
| `live_agile_types.json` | live, converted |
| `live_scrum_types.json` | live, converted |
| `live_basic_types.json` | live, converted |
| `live_cmmi_types.json` | live, converted |
| `inherited_types.json` | `live_agile_types.json` plus: Bug customised (reference name `AgileCustom.Bug`, custom state `Triaged`/Proposed after `New`), custom type `Customer Request` (`AgileCustom.CustomerRequest`: New/Proposed, In Review/InProgress, Closed/Completed), Issue disabled |

The system-process files come from the org-level process API, so no project per process is
needed and no process ID is hard-coded:

```sh
ado api _apis/work/processes --jq '.value[] | select(.customizationType == "system") | .typeId'
ado api "_apis/work/processes/<typeId>/workitemtypes?\$expand=states"
```

Conversion to the project shape: `stateCategory` → `category`, states sorted by `order`,
hidden states dropped, `icon` string → `{id, url}`, `url` rebuilt as a project URL. The process
API omits the hidden types every project also has (Shared Steps, Shared Parameter, Code Review
Request/Response, Feedback Request/Response); they appear only in the Hidden category. A live
project (Basic) list also includes `states`, so the per-type `/states` call is a fallback.

## Categories

`{project}/_apis/wit/workitemtypecategories` shape.

| File | Source |
|---|---|
| `live_basic_categories.json` | live, Basic project. Answers o3: `Microsoft.BugCategory` exists, default and only member `Issue`; `Microsoft.EpicCategory` exists |
| `agile_categories.json` | hand-written: Requirement = User Story |
| `scrum_categories.json` | hand-written: Requirement = Product Backlog Item |
| `cmmi_categories.json` | hand-written: Requirement = Requirement |
| `inherited_categories.json` | hand-written: Requirement = User Story (default) and Customer Request |

The hand-written files add `Microsoft.FeatureCategory` and take the test, code review, feedback,
task and Hidden categories from the Basic capture (they are the same in every process).

## Fields and link types

| File | Endpoint | Source |
|---|---|---|
| `fields.json` | `{project}/_apis/wit/fields` | live Basic project fields (`supportedOperations` dropped) plus hand-written Story Points, Severity, Repro Steps, Acceptance Criteria, Value Area, Business Value and the inherited picklist `Custom.CustomerImpact`. Note `System.AssignedTo` is `type: string` with `isIdentity: true` |
| `live_basic_type_issue_fields.json` | `{project}/_apis/wit/workitemtypes/Issue/fields?$expand=allowedValues` | live; `System.State`, `System.Reason` and `Microsoft.VSTS.Common.Priority` carry allowed values, identity fields an empty list |
| `inherited_type_customerrequest_fields.json` | `{project}/_apis/wit/workitemtypes/Customer Request/fields?$expand=allowedValues` | hand-written; `Custom.CustomerImpact` allowed values High, Medium, Low |
| `live_relationtypes.json` | `_apis/wit/workitemrelationtypes` | live (org-level, same for every process) |

## Backlogs and boards

Team-scoped work API shapes.

| File | Endpoint | Source |
|---|---|---|
| `live_basic_backlogs.json` | `{project}/{team}/_apis/work/backlogs` | live: Epics, Issues, Tasks |
| `agile_backlogs.json` | same | hand-written: Epics, Features, Stories, Tasks |
| `scrum_backlogs.json` | same | hand-written: Epics, Features, Backlog items, Tasks |
| `cmmi_backlogs.json` | same | hand-written: Epics, Features, Requirements, Tasks |
| `inherited_backlogs.json` | same | hand-written: as Agile, Stories also holds Customer Request |
| `live_basic_boards.json` | `{project}/{team}/_apis/work/boards` | live: Issues, Epics |
| `live_basic_board.json` | `{project}/{team}/_apis/work/boards/Issues` | live (`_links` dropped): To Do / Doing (WIP 5) / Done |
| `agile_board.json` | `.../boards/Stories` | hand-written: New / Active / Resolved / Closed |
| `scrum_board.json` | `.../boards/Backlog items` | hand-written: New / Approved / Committed / Done |
| `cmmi_board.json` | `.../boards/Requirements` | hand-written: Proposed / Active / Resolved / Closed |
| `inherited_board.json` | `.../boards/Stories` | hand-written: Agile columns, Customer Request mapped too |

Hand-written boards map only the requirement backlog's types (bugs-on-backlog is a team
setting) and use the live board's defaults: WIP limit 5 on in-progress columns, `isSplit` false.
