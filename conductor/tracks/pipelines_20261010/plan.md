# Plan: Pipelines and Releases — gh run/workflow Parity

Track: `pipelines_20261010` · Spec: [spec.md](./spec.md) · Research: [research.md](./research.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Pipeline Foundation

- [x] Task: Fixtures `10e36f1`
    - [x] httpmock `BuildLocations`, `PipelinesLocations`, `ApprovalLocations` (in the same `StubLocations` call as build/pipelines), `ReleaseLocations` and `StubVSRM` (modelled on `StubSearch`), with a test driving the real SDK clients like `TestWorkItemLocations`; one org name per test (SDK caches discovery per base URL)
    - [x] Timeline fixtures with real shapes: YAML Stage → Phase → Job → Task, stage-less `__default`, classic Phase roots (no stages), retried stage attempt, Stage → `Checkpoint` → `Checkpoint.Approval` + `Checkpoint.TaskCheck` (pending approval + check), completed task with `log: null`, skipped task
    - [x] Definition fixtures: `process` 1 / 2 + `yamlFilename`, `repository` TfsGit + `defaultBranch`, `queueStatus` paused and disabled, `latestCompletedBuild`, two same-named definitions in different folders
    - [x] Artifact fixtures: Container (`#/<id>/<name>`, `%24format=zip`), PipelineArtifact (artprod URL, `artifactsize`), FilePath (`file://` UNC)
    - [x] Error fixtures: build-queue 400 with `customProperties.ValidationResults`, `PipelineValidationException`, definition 409 revision conflict
- [x] Task: API layer (`api/pipeline.go`, `pkg/cmd/pipeline/shared`) `d025517`
    - [x] Write tests: `build`/`pipelines`/`pipelinesapproval` `ClientImpl`s from `NewSDKClient`; Build API wrappers (definitions, builds, timeline, logs, artifacts, `UpdateBuild` for cancel and `retry=true`), Pipelines API runs, stage retry via `UpdateStage{state:retry, forceRetryAllJobs:false}` keyed by the stage `identifier` (no `retryDependencies`), approvals; raw `SDK.Send` full-definitions read (`includeAllProperties` + `includeLatestBuilds`, `[]build.BuildDefinition`, continuation paging); raw-map definition GET/PUT that changes only `queueStatus`; logs streamed via `GetBuildLog` (never `GetBuildLogLines`); errors trimmed, `mapErr` includes `customProperties.ValidationResults[].message`; build-specific revision-conflict check (409, or 400 mentioning revision)
    - [x] Write tests (shared transport): `--debug` logs every request URL without its query string, for all hosts; download errors unwrap `*url.Error` so no URL is printed; a redirect from artprod to `*.blob.core.windows.net` carries no Authorization header and no `sig` appears in the debug log
    - [x] Implement wrappers and transport fix
- [x] Task: Pipeline and run references `10e057e`
    - [x] Write tests: ID, name, folder path (both separators; normalizer turns `/` into `\` and adds a leading `\`), server `name=` pre-filter then exact case-insensitive name/folder match; ambiguous name lists matches; URL forms `_build?definitionId`, `_build/index?definitionId`, `_build/results?buildId`, `_build/index?buildid` (case-insensitive key, extra params ignored) and REST `_apis/build/definitions/N`, `_apis/build/builds/N`, `_apis/pipelines/P/runs/N`; errors never echo the ref; run ID/URL; no-arg run → TTY prompt over recent runs / non-TTY error
    - [x] Implement resolvers; factor org/project splitting out of `pkg/cmd/workitem/shared/ref.go`; add `PipelineURL`/`RunURL` to `internal/adocontext/weburl.go`
- [x] Task: Default pipeline resolution `688dff7`
    - [x] Write tests: current repo GUID via `RepoClient.Repository`, then `GetDefinitions(repositoryId=<guid>, repositoryType=TfsGit)`; disabled definitions excluded, paused ones kept (they can still be queued manually); one definition → used; several → TTY select / non-TTY error listing names; none → error with hint
    - [x] Implement
- [x] Task: Status model and exit codes `713afc4`
    - [x] Write tests: status/result → mark and color (failure mark "X", matching `pr checks` and the work item commands), result → exit code (0/1/2), partiallySucceeded → 1
    - [x] Implement
- [x] Task: Timeline tree model and renderer `7f08306`
    - [x] Write tests: tree rebuilt from flat records by `parentId`, siblings sorted by `order`; Stage → Job → Task with the Phase level collapsed; missing/null parent → root (classic without stages); implicit `__default` stage hidden, its jobs shown at the top level as with classic builds; retried nodes deduped by `identifier` (fallback `name`) keeping the highest `attempt`, latest attempt only with "attempt N" next to the node (no older-attempt fetches); durations; issues collected as annotations; pending from non-completed `Checkpoint.*` records keyed by `identifier` with `id` fallback; Checkpoint records hidden from the tree; unknown `Checkpoint.*` shown as "waiting: <name>"; verbose vs default expansion; failed jobs auto-expanded; JSON tree shape
    - [x] Implement (renderer shared with `release`); promote `timeOf` from `pkg/cmd/pr/shared/checks.go:237`; copy the `buildTree`/`walk` pattern from `pkg/cmd/workitem/query/render.go`
- [x] Task: Live test environment: setup and teardown `0000000`
    - [x] Note: steps that change the org (classic toggles, agent registration, throwaway repo) are run by the main session during manual verification, with the user's prior approval (granted in o23–o25), not by swarm workers. Credential: Entra login.
    - [x] Setup prerequisites: self-hosted agent registered in WSL in the `Default` pool (temporary; done 2026-10-12, user starts `run.sh`); org-level "Disable creation of classic build/release pipelines" toggles turned off for the test
    - [x] Setup repo: throwaway repo `ado-live` in project `ado-gh` holding `azure-pipelines.yml`
    - [x] Setup via `ado api`: YAML pipeline (raw JSON with `configuration.path` and `repository`), definition variable `foo` with allowOverride, environment + Approval check (`resource.type: environment`, approver = the user) + pipeline permission, classic build definition, agentless (`runOnServer`) release definition with two environments and a pre-deployment approval on the second
    - Teardown (runs in Phase 6 verification), in order: release definition `forceDelete` → build retention leases → build definitions (YAML and classic, with their builds) → environment and check → throwaway repo `ado-live` → classic toggles turned back on → agent unregistered, pool registration removed, its registration PAT revoked
- [x] Task: Conductor - User Manual Verification 'Phase 1: Pipeline Foundation' (Protocol in workflow.md)
    - [x] Set up the live test environment; confirm live and correct fixtures if needed: timeline type strings, Checkpoint states while waiting, approval `id` vs `identifier`, `__default` stage, retry layout, the "No hosted parallelism" message

## Phase 2: `list`, `view`, `logs`

- [ ] Task: Command group and `run` alias
    - [ ] Write tests: `ado pipeline` and `ado run` resolve to the same group
    - [ ] Implement group
- [ ] Task: `ado pipeline list`
    - [ ] Write tests (runs): each filter → query param, `queryOrder=queueTimeDescending` sent, `-u`/`@me` resolved via `IdentityResolver.Resolve`, branch normalization, `-b` matches `sourceBranch` only (PR-validation runs need `-e pullRequest`), `-c` filtered client-side while paging with the continuation token, narrowed by `-b`/definition in the server query, page cap and a warning when results may be incomplete (matches spanning pages), `--limit` default 20, TTY table vs TSV, JSON fields, empty message
    - [ ] Write tests (definitions): `-d` via the full-definitions read, `--folder`, `--match`, `-a` includes disabled, type/repo/last result (`latestCompletedBuild.result`) columns, JSON fields
    - [ ] Implement `list`; classify in `TestWebFlagAudit`
- [ ] Task: Shared log fetcher
    - [ ] Write tests: record `log.id` → streamed `GetBuildLog`, Task-record logs only (job logs duplicate them), `job<TAB>task<TAB>` prefixing via `bufio.Reader` (a >64 KiB line), `--job`/`--task`/`--failed` filters, `--output` layout written through the shared `os.Root` helper after name sanitization (Windows reserved names, `:*?"<>|`, control characters, trailing dots/spaces), traversal attempts confined to dir
    - [ ] Implement (used by `view --log`, `logs`, `release logs`)
- [ ] Task: `ado pipeline view`
    - [ ] Write tests: summary fields, tree default/`-v`, Annotations and Waiting sections (Waiting lists non-approval `Checkpoint` children by timeline name and state, timeline only, no checks API), `--log`, `--log-failed`, `--job`, `--exit-status`, JSON incl. `stages`/`pendingApprovals`, `-w`
    - [ ] Implement `view`; classify in `TestWebFlagAudit`
- [ ] Task: `ado pipeline logs`
    - [ ] Write tests: stdout stream, filters, `-o` writes tree, `--follow` (inside a `testing/synctest` bubble with a `time.Ticker`, as `pr checks` and `api/throttle_test.go` do) prints task logs as timeline records complete, keeps polling completed records with `log: null` until they or their parent job get a `log.id`, and stops at run completion
    - [ ] Implement `logs`; classify in `TestWebFlagAudit`
- [ ] Task: Conductor - User Manual Verification 'Phase 2: list, view, logs' (Protocol in workflow.md)

## Phase 3: `run`, `watch`, `cancel`, `rerun`

- [ ] Task: Watch loop (shared)
    - [ ] Write tests (`testing/synctest` bubble, `time.Ticker`): TTY redraw per interval with spinner and waiting line, non-TTY transition lines + final summary, completed run → immediate summary, Ctrl-C (context cancel) → exit 2, exit code per result; only the timeline is polled per tick and the build is fetched once at the end
    - [ ] Implement (reused by `release watch`); spinner is a tiny frame index; no throttle code (the transport handles Retry-After/X-RateLimit)
- [ ] Task: `ado pipeline watch`
    - [ ] Write tests: no-arg TTY prompt over in-progress runs, `-i` interval, `--exit-status` no-op
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado pipeline run`
    - [ ] Write tests: `queueStatus` pre-check from the definition GET (disabled → error, paused → warning, then queue), YAML via Pipelines API (branch, commit as `resources.repositories.self`, template parameters), classic via Build queue (branch, commit, variables as a JSON-encoded `map[string]string`), `-p` rejected for classic, no `-b` → branch from `definition.repository.defaultBranch` (fallback: the Git repo's default branch) sent explicitly, non-settable variable error surfaced (`PipelineValidationException` fixture, 400 assumed), `ValidationResults` details shown, prints URL (`_build/results?buildId=`, not `Run.url`) + watch hint, `--watch` exit codes, `-w`
    - [ ] Implement `run`; classify in `TestWebFlagAudit`
- [ ] Task: `ado pipeline cancel`
    - [ ] Write tests: GET first; completed → exit 1 without PATCH; already `cancelling` → message and exit 0 without PATCH; PATCH `cancelling`, TTY confirm/decline → exit 2, `-y`, non-TTY without `--yes` refuses
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado pipeline rerun`
    - [ ] Write tests: full rerun from one Build GET (`templateParameters` + decoded `parameters`, `sourceBranch`, `sourceVersion`) to `RunPipeline` (YAML) or Build queue (classic), prints new URL; `--failed` only once the run is completed, otherwise error "run is still in progress"; YAML `--failed` retries `failed` and `canceled` stages via `UpdateStage` with stageRefName = `identifier`, a failed stage depending on another failed stage, no failed or canceled stages → error; classic `--failed` sends `UpdateBuild` with `retry=true`; server error via `mapErr`
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: Conductor - User Manual Verification 'Phase 3: run, watch, cancel, rerun' (Protocol in workflow.md)
    - [ ] Confirm live: downstream behaviour of a 7.1 stage retry, classic `retry=true` behaviour

## Phase 4: `download`, `approve`/`reject`, `enable`/`disable`

- [ ] Task: `ado pipeline download`
    - [ ] Write tests: `-n` / `-p` selection; `resource.downloadUrl` fetched with the authenticated client, streamed to a temp file, then `zip.OpenReader`, no `Client.Timeout`; case-insensitive type match; pipeline and container artifacts extracted as in gh: a single `-n` directly into `<dir>`, several (or `-p`, or none) into `<dir>/<artifact>`; one top-level folder equal to the artifact name stripped; file-share (`FilePath` or nil type with id 0) skipped showing the UNC path, other types skipped with a warning; shared extraction helper (artifact name validated, `\` rejected, `fs.ValidPath` + `filepath.Localize`, `os.OpenRoot`, symlink/non-regular entries skipped, bytes capped per entry); zip-slip entries rejected (`../x`, `/abs`, `a\..\b`, `C:x`, `NUL`, symlink); existing `<dir>/<artifact>` errors without `--clobber`, and `--clobber` removes it and re-extracts into a fresh directory; single `-n` into `<dir>`: existing files that would be overwritten error without `--clobber` and are overwritten with it (`<dir>` itself is never removed); never `$expand=signedContent`, signed URLs never printed or in JSON
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado pipeline approve` / `reject`
    - [ ] Write tests: timeline → `QueryApprovals(ids, $expand=steps)` → pending; single pending approval acted on with comment, several → TTY select / `--stage` / `--all` (one PATCH) / non-TTY error, none pending → exit 1; `inSequence` steps `uninitiated`; approve with `minRequiredApprovers > 1` not yet met → exit 0 with "Approved (1 of 2 required); still waiting"; not approver: PATCH sent and a 4xx wrapped with the approver names from `steps[].assignedApprover`; 401/403 scope hint per operation (`vso.build` for query, `vso.build_execute`/`vso.pipelineresources_use` for update)
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado pipeline enable` / `disable`
    - [ ] Write tests: raw-map GET + PUT with revision changing only `queueStatus`, one asserted api-version; enable turns paused or disabled into enabled, disable turns anything into disabled, no pause support; already in state (exact value) → exit 0; revision conflict (409 fixture) → GET and PUT retried once automatically, then fail with the conflict message
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: Conductor - User Manual Verification 'Phase 4: download, approve/reject, enable/disable' (Protocol in workflow.md)
    - [ ] Confirm live: artifact zip layout and whether download redirects, definition conflict body, secret variables surviving the enable/disable PUT, approval `id` vs `identifier`

## Phase 5: `ado release` (Read)

- [ ] Task: Release API layer and fixtures
    - [ ] Fixtures: release definitions, releases with multiple environments, deploy steps, pending pre/post approvals; ResourceAreas stubbed on the org and Locations on the vsrm base, plus an org without a release area (on-prem fallback)
    - [ ] Write tests: `api/release.go` builds `release.ClientImpl` via the unexported `areaClient(..., release.ResourceAreaId)`, resolving the `vsrm` host for dev.azure.com and visualstudio.com orgs; wrappers for list/get/logs/approvals; shared pager stores string tokens and `Atoi`s them for releases/approvals
    - [ ] Implement
- [ ] Task: `ado release list`
    - [ ] Write tests: filters (`-u` resolved to an identity for `createdBy`), `-d` definitions, TTY/TSV, JSON fields
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado release view` / `logs`
    - [ ] Write tests: GET release with `$expand=tasks`, environments + deploy steps tree via shared renderer, approvals section, `--log`, `--log-failed` (`GetTaskLog` with `ReleaseDeployPhase.Id` + `ReleaseTask.Id`), `logs --environment/--failed/-o` (optional `GetLogs` zip for `-o`), JSON, `-w`
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado release watch`
    - [ ] Write tests: shared watch loop over environments, `--environment` filter, exit codes across watched environments (`rejected`/`partiallySucceeded` → 1, `canceled` → 2); a `rejected` environment exits 1 whether from a failed deployment or an approval rejection, and the output tells them apart via deploy-step `operationStatus` (fixture with the latest deploy step `<failed, phaseFailed>`); fall back to deploy-step `status`/`operationStatus`
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: Conductor - User Manual Verification 'Phase 5: ado release (Read)' (Protocol in workflow.md)
    - [ ] Confirm live: `ReleaseDeployPhase.Id` for task logs, environment status for failed deployments, Entra bearer token on vsrm

## Phase 6: `ado release` (Write), Web, Docs

- [ ] Task: `ado release create`
    - [ ] Write tests: `--artifact alias=version`; without `--artifact` no artifacts sent and the versions chosen by the server printed from the returned release; overridable `-v` only (checked against the definition's `$expand=variables` `allowOverride`), `--description`, prints URL, `--watch`, `-w`
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado release deploy` / `cancel`
    - [ ] Write tests: environment status PATCH (`inProgress` / `canceled`), TTY confirm, `-y`, non-TTY refuses without `--yes`, unknown environment lists valid ones, `-c`
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: `ado release approve` / `reject`
    - [ ] Write tests: pre/post approvals, `--environment`, multi-pending rules, not approver error
    - [ ] Implement; classify in `TestWebFlagAudit`
- [ ] Task: Web audit and docs
    - [ ] Confirm every `pipeline`/`release` subcommand is classified in `webCmds` or `noWebCmds` in `TestWebFlagAudit`
    - [ ] Update `docs/gh-differences.md` with every divergence in the spec, plus: `-b` not matching PR-validation runs (use `-e pullRequest`), rerun not preserving secret variables, the "X" failure mark, classic release creation disabled by default in new orgs
    - [ ] README PAT list: Build (Read / Read & execute), Release (Read / Read, write & execute / manage), Pipeline Resources (Use)
    - [ ] Update `spec.md` (main session) for the divergences decided in research: FR8 classic `--failed` uses `retry=true` instead of erroring (o1); FR9 a single `-n` extracts directly into `<dir>` (o13); FR1 failure mark "X" instead of ✗ (o2); FR11 enable also resumes paused definitions, no pause support (o19); FR12 `release create` without `--artifact` sends none and prints the chosen versions (o21); NFR Security: `--debug` logs every request URL without its query string (o15)
    - [ ] Verify help text/examples for `pipeline *` and `release *`
- [ ] Task: Conductor - User Manual Verification 'Phase 6: ado release (Write), Web, Docs' (Protocol in workflow.md)
    - [ ] Confirm live: the release `status: canceled` payload; then tear down the live test environment
