# Pipeline fixtures

Response bodies for the Build API endpoints the pipeline commands call. Load them in tests with
`internal/pipelinefixtures` (`Read`) and serve them with `httpmock.JSONResponse(json.RawMessage(b))`
(or `httpmock.StatusJSONResponse(status, json.RawMessage(b))` for errors) on the routes in
`httpmock.PipelineLocations`.

All files are hand-written from the documented REST 7.1 shapes and the sources in
`conductor/tracks/pipelines_20261010/research.md` (q2, q3, q5, q7, q9): the org had no pipelines
when they were written, so nothing is captured live yet. Shapes marked *unverified* rest on
third-party code or inference; confirm or replace them with live captures during the Phase 1
manual verification. All files are scrubbed: org `org`, project `proj` with id
`00000000-0000-0000-0000-000000000001`, every other GUID `00000000-0000-0000-0000-0000000xxxxx`,
commit SHAs zero-padded, no identities. `TestFixturesScrubbed` in internal/pipelinefixtures
enforces this and that every file is listed below; the other tests there pin each file's shape.

## Timelines

`GET {project}/_apis/build/builds/{buildId}/timeline` (`records`, `lastChangedBy`,
`lastChangedOn`, `id`, `changeId`, `url`). Records carry the full field set (`previousAttempts`,
`id`, `parentId`, `type`, `name`, `startTime`, `finishTime`, `state`, `result`, `order`,
`errorCount`, `warningCount`, `log`, `task`, `attempt`, `identifier`; `queueId` on jobs,
`issues` only where there are any). Task records have `identifier: null`; Job records and every
Task that ran have a `log`; Stage, Phase and Checkpoint records have `log: null`.

| File | Build | Covers |
|---|---|---|
| `timeline_yaml_stages.json` | 101 | YAML Stage → Phase → Job → Task, three stages: Build succeeded, Test failed (`go test` failed with an error and a warning issue, then `Publish coverage` skipped: no times, no log), Deploy skipped with its job never started |
| `timeline_default_stage.json` | 102 | Stage-less YAML: one implicit stage named and identified `__default` (*unverified*, forgetop) holding two jobs, `Lint` and `Build` |
| `timeline_classic.json` | 103 | Classic designer build: no Stage records, Phase roots (`Agent job 1`, `Agent job 2`, `parentId: null`) → Job → Task (*inferred*) |
| `timeline_retried.json` | 104 | Test stage retried: its Stage, Phase and Job records are `attempt: 2` with one `previousAttempts` entry pointing at the attempt-1 record in another timeline; the base timeline holds the latest attempt only (*inferred*) |
| `timeline_checkpoint.json` | 105 | Deploy waiting: Stage `pending` with no start time → `Checkpoint` (`inProgress`) → `Checkpoint.Approval` (pending approval; `id` = `identifier` = approval id `...10599`) and `Checkpoint.TaskCheck` `Business Hours` (pending check). Types and nesting from third-party code, states and `id` = `identifier` *unverified* |
| `timeline_in_progress.json` | 106 | Running job: `Checkout` completed with `log: null` (log not attached yet), `go build` in progress, two steps pending |

## Definitions

| File | Source |
|---|---|
| `definitions.json` | `GET {project}/_apis/build/definitions?includeAllProperties=true&includeLatestBuilds=true` (full `BuildDefinition`s, *unverified* that the list returns them): `ado-gh` (id 1, `\`, YAML `process {type: 2, yamlFilename}`, enabled, `latestBuild` 106 in progress, `latestCompletedBuild` 101 failed); `nightly` (id 2, `\Infra`, classic `process {type: 1, phases}`, paused); `deploy` (id 3, `\Infra`, YAML, disabled, no builds); `deploy` (id 4, `\Apps\Web`, YAML, repo `web` with default branch `refs/heads/develop`). Every repository is `TfsGit` with a full-ref `defaultBranch` |
| `definition_yaml.json` | `GET {project}/_apis/build/definitions/1`: definition 1 above without the latest builds, revision 3, a CI trigger, variable `foo` (allowOverride) and secret `token` (value `null`), for the raw GET/PUT round-trip |

## Live captures

Captured on 2026-10-12 from a throwaway YAML pipeline (stages Build → Gate (agentless) → Fail, Build → Deploy to an environment with an approval check) run on a self-hosted agent. The org name and agent name are replaced and every GUID is mapped consistently to `00000000-0000-0000-0000-0000000009xx`.

| File | Source |
|---|---|
| `live_timeline_pending_approval.json` | `GET {project}/_apis/build/builds/{id}/timeline` while Deploy waits: `Checkpoint` → `Checkpoint.Approval` (its `identifier` is the approval id) + completed `Checkpoint.Authorization`; Deploy's `Phase` has no `Job` yet; jobs use `<stage>.<job>.__default` identifiers; tasks have no `identifier` and repeat names across jobs |
| `live_timeline_failed.json` | The same run after approval: `completed`/`failed`, the Fail stage's task carries an `error` issue `Bash exited with code '1'.` |

## Artifacts

| File | Source |
|---|---|
| `artifacts.json` | `GET {project}/_apis/build/builds/101/artifacts`: `drop` (`Container`, `data` `#/<containerId>/drop`, `downloadUrl` the build endpoint plus `&%24format=zip`); `webapp` (`PipelineArtifact`, dedup id in `data`, `properties.artifactsize`, `downloadUrl` on `artprodcus3.artifacts.visualstudio.com`); `share` (`FilePath`, UNC `data`, `file://` `downloadUrl`) |

## Classic Release

`GET https://vsrm.dev.azure.com/{org}/{project}/_apis/release/...` (the `vsrm` host comes from the
org's ResourceAreas). Serve them on the routes in `httpmock.ReleaseLocations` after
`httpmock.StubVSRM`. Captured on 2026-10-12 from release definition 1 `ado-live-release`
(`\ado-live`; environments Dev, automatic, and Prod with a manual pre-deployment approval; one
agentless job running a Delay task; artifact `_ado-live-classic` from build definition 2) and its
release 1. Scrubbed like the live captures above, with the same GUID map (the project is `...0903`,
the Delay task `...0914`); the user is `Test User`, avatars and the identity `uniqueName`,
`descriptor` and `imageUrl` are dropped, the artifact's commit SHA is zero-padded.

| File | Source |
|---|---|
| `release_definitions.json` | `GET {project}/_apis/release/definitions`: definition 1 (no `$expand`, so no environments) |
| `releases.json` | `GET {project}/_apis/release/releases`: release 1 `Release-1`, `active`, without environments |
| `release.json` | `GET {project}/_apis/release/releases/1?$expand=tasks`: Dev `succeeded` (automated pre/post approvals 1 and 3 approved; one deploy step `succeeded`/`Approved` → deploy phase `id` 1 `Agentless job` → job `Release` → task `Delay` with `id` 0 and an empty `logUrl`, as agentless tasks have no log); Prod `inProgress` waiting on pending manual `preDeploy` approval 4, its deploy step `notDeployed`/`Pending` with no deploy phases |
| `release_approvals.json` | `GET {project}/_apis/release/approvals` (default `statusFilter=pending`): approval 4 above, with its release, definition and environment references |
| `release_pending_post.json` | *Hand-edited* from `release.json`: release 2 `Release-2`, Dev `inProgress` after its deploy step succeeded, waiting on pending manual `postDeploy` approval 13; Prod `notStarted` with no approvals or deploy steps. The environment status while a post-deployment approval is pending is *unverified* |
| `release_failed_task.json` | *Hand-written* (minimal fields): release 3 `Release-3` for the view and logs commands. Dev `succeeded` (automated approvals; deploy phase `id` 6 with two deployment jobs, every task with a `logUrl`); QA `inProgress`, its deploy phase 8 `notStarted` with no deployment jobs yet; Prod `rejected` after a manual pre-deployment approval (12, `ship it`): two deploy steps, the latest (`attempt` 2) `failed`/`PhaseFailed` with a step-level error issue → deploy phase 7 → job `Agent job` → `Initialize job` succeeded, `Run script` failed with an `Error` issue (repeated on the job), `Publish` skipped with no times and an empty `logUrl` |

## Errors

The HTTP status is not part of the body; serve each file with the status listed.

| File | Status | Source |
|---|---|---|
| `error_queue_validation.json` | 400 | `POST build/builds` validation failure: `BuildRequestValidationFailedException` with the details in `customProperties.ValidationResults[].message` |
| `error_pipeline_validation.json` | 400 (*assumed*) | `POST pipelines/{id}/runs` with a variable not settable at queue time: `PipelineValidationException`, message as documented |
| `error_definition_conflict.json` | 409 | `PUT build/definitions/{id}` with a stale revision. Status, message and `typeKey` are *placeholders* (undocumented); the message mentions the revision so the 400-mentioning-revision check matches too. Replace with the live body |
