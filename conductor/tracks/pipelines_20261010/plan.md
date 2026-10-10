# Plan: Pipelines and Releases — gh run/workflow Parity

Track: `pipelines_20261010` · Spec: [spec.md](./spec.md) · Depends on: `mvp_core_20261010`

Workflow: TDD (Red → Green → Refactor), >90% coverage, one commit per phase (one-line message, no attribution trailers).

---

## Phase 1: Pipeline Foundation

- [ ] Task: Fixtures
    - [ ] httpmock fixtures: YAML multi-stage run, classic run (no stages), retried stage attempt, pending approval + check, definitions (YAML/classic/disabled), artifacts (pipeline, container, file share)
- [ ] Task: API layer (`api/pipeline.go`, `pkg/cmd/pipeline/shared`)
    - [ ] Write tests: Build API wrappers (definitions, builds, timeline, logs, artifacts, update), Pipelines API runs, stage retry, approvals; errors trimmed
    - [ ] Implement wrappers
- [ ] Task: Pipeline and run references
    - [ ] Write tests: ID, name, folder path (both separators), URL; ambiguous name lists matches; run ID/URL; no-arg run → TTY prompt over recent runs / non-TTY error
    - [ ] Implement resolvers
- [ ] Task: Default pipeline resolution
    - [ ] Write tests: one definition for current repo → used; several → TTY select / non-TTY error listing names; none → error with hint
    - [ ] Implement
- [ ] Task: Status model and exit codes
    - [ ] Write tests: status/result → mark and color, result → exit code (0/1/2), partiallySucceeded → 1
    - [ ] Implement
- [ ] Task: Timeline tree model and renderer
    - [ ] Write tests: records nested Stage → Job → Task, classic without stages, attempts (latest shown), durations, issues collected as annotations, pending checkpoints, verbose vs default expansion, failed jobs auto-expanded, JSON tree shape
    - [ ] Implement (renderer shared with `release`)
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Pipeline Foundation' (Protocol in workflow.md)

## Phase 2: `list`, `view`, `logs`

- [ ] Task: Command group and `run` alias
    - [ ] Write tests: `ado pipeline` and `ado run` resolve to the same group
    - [ ] Implement group
- [ ] Task: `ado pipeline list`
    - [ ] Write tests (runs): each filter → query param, `@me`, branch normalization, `--limit` default 20, TTY table vs TSV, JSON fields, empty message
    - [ ] Write tests (definitions): `-d`, `--folder`, `--match`, `-a` includes disabled, type/repo/last result columns, JSON fields
    - [ ] Implement `list`
- [ ] Task: Shared log fetcher
    - [ ] Write tests: fetch per log ID, `job<TAB>task<TAB>` prefixing, `--job`/`--task`/`--failed` filters, `--output` layout with sanitized names, traversal attempts confined to dir
    - [ ] Implement (used by `view --log`, `logs`, `release logs`)
- [ ] Task: `ado pipeline view`
    - [ ] Write tests: summary fields, tree default/`-v`, Annotations and Waiting sections, `--log`, `--log-failed`, `--job`, `--exit-status`, JSON incl. `stages`/`pendingApprovals`, `-w`
    - [ ] Implement `view`
- [ ] Task: `ado pipeline logs`
    - [ ] Write tests: stdout stream, filters, `-o` writes tree, `--follow` prints task logs as timeline records complete (fake clock) and stops at run completion
    - [ ] Implement `logs`
- [ ] Task: Conductor - User Manual Verification 'Phase 2: list, view, logs' (Protocol in workflow.md)

## Phase 3: `run`, `watch`, `cancel`, `rerun`

- [ ] Task: Watch loop (shared)
    - [ ] Write tests (fake clock): TTY redraw per interval with spinner and waiting line, non-TTY transition lines + final summary, completed run → immediate summary, Ctrl-C (context cancel) → exit 2, exit code per result
    - [ ] Implement (reused by `release watch`)
- [ ] Task: `ado pipeline watch`
    - [ ] Write tests: no-arg TTY prompt over in-progress runs, `-i` interval, `--exit-status` no-op
    - [ ] Implement
- [ ] Task: `ado pipeline run`
    - [ ] Write tests: YAML via Pipelines API (branch, commit as `resources.repositories.self`, template parameters), classic via Build queue (branch, commit, variables JSON), `-p` rejected for classic, default branch = repo default, non-settable variable error surfaced, prints URL + watch hint, `--watch` exit codes, `-w`
    - [ ] Implement `run`
- [ ] Task: `ado pipeline cancel`
    - [ ] Write tests: PATCH `cancelling`, TTY confirm/decline → exit 2, `-y`, non-TTY without `--yes` refuses, completed → exit 1
    - [ ] Implement
- [ ] Task: `ado pipeline rerun`
    - [ ] Write tests: full rerun copies definition/branch/commit/parameters/variables and prints new URL; `--failed` retries only failed stages (YAML), classic error, no failed stages error
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 3: run, watch, cancel, rerun' (Protocol in workflow.md)

## Phase 4: `download`, `approve`/`reject`, `enable`/`disable`

- [ ] Task: `ado pipeline download`
    - [ ] Write tests: `-n` / `-p` selection, pipeline and container artifacts extracted to `<dir>/<artifact>`, zip-slip entries rejected, file-share skipped with warning, existing dir error without `--clobber`, signed URLs never printed
    - [ ] Implement
- [ ] Task: `ado pipeline approve` / `reject`
    - [ ] Write tests: single pending approval acted on with comment, several → TTY select / `--stage` / `--all` / non-TTY error, none pending → exit 1, not approver → error naming approvers
    - [ ] Implement
- [ ] Task: `ado pipeline enable` / `disable`
    - [ ] Write tests: GET + PUT with revision, already in state → exit 0, revision conflict message
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 4: download, approve/reject, enable/disable' (Protocol in workflow.md)

## Phase 5: `ado release` (Read)

- [ ] Task: Release API layer and fixtures
    - [ ] Fixtures: release definitions, releases with multiple environments, deploy steps, pending pre/post approvals
    - [ ] Write tests: SDK release client resolves `vsrm` host for dev.azure.com and visualstudio.com orgs; wrappers for list/get/logs/approvals
    - [ ] Implement
- [ ] Task: `ado release list`
    - [ ] Write tests: filters, `-d` definitions, TTY/TSV, JSON fields
    - [ ] Implement
- [ ] Task: `ado release view` / `logs`
    - [ ] Write tests: environments + deploy steps tree via shared renderer, approvals section, `--log`, `--log-failed`, `logs --environment/--failed/-o`, JSON, `-w`
    - [ ] Implement
- [ ] Task: `ado release watch`
    - [ ] Write tests: shared watch loop over environments, `--environment` filter, exit codes across watched environments
    - [ ] Implement
- [ ] Task: Conductor - User Manual Verification 'Phase 5: ado release (Read)' (Protocol in workflow.md)

## Phase 6: `ado release` (Write), Web, Docs

- [ ] Task: `ado release create`
    - [ ] Write tests: `--artifact alias=version` and default latest, overridable `-v` only, `--description`, prints URL, `--watch`, `-w`
    - [ ] Implement
- [ ] Task: `ado release deploy` / `cancel`
    - [ ] Write tests: environment status PATCH (`inProgress` / `canceled`), TTY confirm, `-y`, non-TTY refuses without `--yes`, unknown environment lists valid ones, `-c`
    - [ ] Implement
- [ ] Task: `ado release approve` / `reject`
    - [ ] Write tests: pre/post approvals, `--environment`, multi-pending rules, not approver error
    - [ ] Implement
- [ ] Task: Web audit and docs
    - [ ] Add `--web` commands to the web-flag audit allowlist (if present)
    - [ ] Update `docs/gh-differences.md` with every divergence in the spec
    - [ ] Verify help text/examples for `pipeline *` and `release *`
- [ ] Task: Conductor - User Manual Verification 'Phase 6: ado release (Write), Web, Docs' (Protocol in workflow.md)
