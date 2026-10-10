# Spec: Pipelines and Releases — gh run/workflow Parity

## Overview
Add `ado pipeline` (alias `run`) to list, trigger, inspect, watch, log, cancel, rerun, download artifacts from, approve, and enable/disable Azure Pipelines. It covers YAML pipelines and classic build definitions. A separate `ado release` group covers Classic Release pipelines. Run listings, the timeline tree, log fetching and the watch loop are shared. Builds on the Factory, context resolution, exporter, prompter and confirmation helpers from `mvp_core_20261010`, which must be complete first.

## User Stories
1. As a developer, I run `ado run list -b feature/x` and `ado run view 812 --log-failed` to see why CI failed without opening a browser.
2. As a CI author, I run `ado pipeline run deploy -b main -p env=prod --watch` and branch on the exit code.
3. As a release manager, I run `ado pipeline approve 812 -c "ship it"`, or `ado release deploy 45 --environment prod`.
4. As a developer, I run `ado pipeline download 812 -n drop` to grab build output.

## Functional Requirements

### FR1: Shared foundation
- **APIs:** the Build API for definitions, runs, timeline, logs, artifacts, cancel and classic queueing. The Pipelines API for YAML runs (template parameters, resources) and approvals. Stage retry for YAML. The Release API (`vsrm` host, resolved through the SDK's resource-area lookup) for releases.
- **Pipeline refs:** ID, name, folder path (`\Folder\Name` or `Folder/Name`), or URL. Ambiguous names error and list matches.
- **Run refs:** run (build) ID or URL. With no run argument: TTY → select prompt over recent runs (the default pipeline's runs on the current branch when one resolves, otherwise the project's). Non-TTY → error naming the missing `<run>`.
- **Default pipeline:** definitions whose repository is the current repo. Exactly one: it's used. Several: TTY select prompt, non-TTY error listing names. None: error suggesting a pipeline argument.
- **Status model:** `status` (`notStarted|inProgress|cancelling|completed|postponed`) and `result` (`succeeded|partiallySucceeded|failed|canceled`), plus a display mark (✓ ✗ ! - ● ⏸).
- **Timeline tree:** Build timeline records nested as Stage → Phase/Job → Task (classic builds have no Stage level). Each node carries state, result, start/finish, duration, attempt, log ID, error/warning issues, and pending checkpoints (approvals/checks).
- **Exit codes (`watch`, `run --watch`, `view --exit-status`):** succeeded `0`, failed/partiallySucceeded `1`, canceled `2`. `--exit-status` is accepted on `watch` as a no-op for gh scripts.

### FR2: `ado pipeline list [<pipeline>]`
- **Runs (default):** `-b/--branch` (`refs/heads/` added if missing), `-s/--status`, `--result`, `-u/--user` (requested-for; `@me`), `-c/--commit`, `-e/--event` (reason: `manual|individualCI|batchedCI|pullRequest|schedule|resourceTrigger`), `-L/--limit` (default 20).
  - TTY: mark, build number, pipeline, branch, event, ID, elapsed, age. Non-TTY TSV.
  - JSON: `id,number,pipeline,status,result,sourceBranch,sourceVersion,reason,requestedBy,requestedFor,queueTime,startTime,finishTime,url`.
- **Definitions (`-d/--definitions`):** `--folder`, `--match <glob>`, `-a/--all` (include disabled/paused).
  - Columns: name, folder, type (`yaml|classic`), repository, queue status, last run result.
  - JSON: `id,name,path,type,repository,queueStatus,latestRun,url`.
- Empty result: message to stderr, exit 0.

### FR3: `ado pipeline run [<pipeline>]`
- `-b/--branch` (default: the pipeline repo's default branch, gh parity), `--commit <sha>`, `-p/--parameter key=value` (YAML template parameters; repeatable; rejected for classic), `-v/--variable key=value` (queue-time variables; non-settable variables surface the API error), `--watch`, `-w/--web`.
- YAML goes through the Pipelines API runs endpoint. Classic goes through Build queue.
- Prints the run URL and `ado pipeline watch <id>`. With `--watch`, it then runs FR5 and exits per FR1.

### FR4: `ado pipeline view [<run>]`
- Summary: pipeline, number, status/result, branch, commit, event, triggering PR, requested by/for, queued/started/finished, duration.
- Tree: stages and jobs with mark and duration. `-v/--verbose` adds tasks. Failed jobs always expand to their failed tasks. An **Annotations** section lists error/warning issues (gh parity). A **Waiting** section lists pending approvals/checks.
- `--log` prints full logs for all tasks (or `--job <name|id>`). `--log-failed` prints logs for failed tasks only. Lines are prefixed `job<TAB>task<TAB>` (gh style).
- `--exit-status`, `-w/--web`.
- JSON: FR2 run fields plus `stages` (tree: `name,type,state,result,startTime,finishTime,duration,attempt,logId,issues,children`) and `pendingApprovals`.

### FR5: `ado pipeline watch [<run>]`
- No argument: TTY prompt over in-progress runs.
- TTY: redraws the tree in place every `-i/--interval` seconds (default 3), with a spinner on running nodes and a waiting-approval line.
- Non-TTY: polls at the same interval and prints one line per stage/job state change, then a final summary.
- Run already finished: prints the summary and exits per FR1. Ctrl-C stops watching (the run continues) and exits 2.

### FR6: `ado pipeline logs [<run>]`
- Default: streams all completed task logs to stdout, prefixed as in FR4.
- `--job <name|id>`, `--task <name|id>`, `--failed` filter the logs.
- `-o/--output <dir>` writes `<dir>/<stage>/<job>/<NN>_<task>.log` (names sanitized, paths can't escape `<dir>`).
- `-f/--follow`: for in-progress runs, polls the timeline and prints each task's log as it completes. This isn't line-level live streaming, because the REST log API only exposes completed logs.

### FR7: `ado pipeline cancel [<run>]`
- Sets status to `cancelling`. Confirms on TTY. `-y/--yes` skips. Non-interactive without `--yes` refuses.
- Already completed: error, exit 1.

### FR8: `ado pipeline rerun [<run>]`
- Default: queues a new run of the same definition, branch, commit, template parameters and queue-time variables. Prints the new run URL.
- `--failed`: retries only failed stages of the same YAML run (stage retry API, failed jobs only). Classic: error `--failed is supported only for YAML pipelines`. No failed stages: error.

### FR9: `ado pipeline download [<run>]`
- `-n/--name` (repeatable), `-p/--pattern <glob>`, `-D/--dir` (default cwd; each artifact goes to `<dir>/<artifact>`).
- Supports pipeline artifacts and build container artifacts (zip download + extraction with zip-slip protection). File-share artifacts are skipped with a warning showing the UNC path.
- Existing target directory: error unless `--clobber`.

### FR10: `ado pipeline approve|reject <run>`
- Acts on pending approvals for the run where the signed-in user is an approver. `-c/--comment`.
- Several pending: TTY select, or `--stage <name>` / `--all`. Non-TTY with several requires one of them.
- None pending: message, exit 1. Not an approver: error naming the approvers.
- Other checks (business hours, invoke function, etc.) appear in `view`/`watch` as waiting but can't be acted on here.

### FR11: `ado pipeline enable|disable <pipeline>`
- Sets the definition `queueStatus` to `enabled` / `disabled` (GET + PUT with revision; concurrency conflict → retry message).
- Already in that state: message, exit 0.

### FR12: `ado release` (Classic Release)
- `list [<definition>]`: releases with `-s/--status {active|abandoned|draft}`, `-u/--user`, `-b/--branch` (primary artifact branch), `-L`. Also `-d/--definitions` (name, folder, environments). JSON: `id,name,definition,status,createdBy,createdOn,environments[{name,status}],url`.
- `view <release>`: environments with status, deploy steps tree (deploy phases → jobs → tasks), approvals, `--log`, `--log-failed`, `-w`. JSON with `environments` tree.
- `create <definition>`: `--artifact alias=version` (repeatable; default latest), `-v/--variable key=value` (overridable only), `--description`, `--watch`, `-w`. Prints the release URL.
- `deploy <release> --environment <name>`: manual/re-deploy. Confirms on TTY. `-y` required when non-interactive. `-c/--comment`.
- `watch <release> [--environment <name>]`: same behavior as FR5. Exit codes across the watched environments: all succeeded `0`, any rejected/failed/partiallySucceeded `1`, canceled `2`.
- `logs <release>`: `--environment`, `--failed`, `-o/--output` (same layout rules as FR6).
- `cancel <release> --environment <name>`: cancels the in-progress deployment. Confirms on TTY, `--yes` when non-interactive.
- `approve|reject <release>`: pending pre/post-deployment approvals for the user. `--environment`, `-c`, same multi-pending rules as FR10.

### FR13: Aliases & web
- `ado run` aliases `ado pipeline`.
- `-w/--web` on `pipeline run/view` and `release create/view`. Added to the web-flag audit allowlist if `repo_browse_20261010` exists.

## Non-Functional Requirements
- **Output contract:** read commands (`pipeline list/view`, `release list/view`) support `--json/--jq/--template`. Field names are public API.
- **Safety:** `pipeline cancel`, `release deploy`, `release cancel` confirm on TTY and require `--yes` when non-interactive.
- **Security:** artifact extraction and `--output` paths are confined to the target directory (zip-slip / path traversal tests). Tokens are never logged. Signed download URLs are never printed.
- **No new dependencies:** zip via `archive/zip`, polling via `time.Ticker` with an injectable clock for tests.
- **Testing:** >90% coverage. httpmock fixtures for YAML multi-stage runs, classic runs (no stages), retried stage attempts, pending approvals, and release environments. TTY and non-TTY watch output covered with a fake clock.
- **Docs:** `docs/gh-differences.md` covers `pipeline` vs `run`/`workflow`, `list -d`, `-p` vs `-v`, strict watch exit codes, `logs` command and `--follow` limits, `rerun --failed` YAML-only, approvals, `ado release`.

## Acceptance Criteria
- [ ] `ado run list -b main --result failed --json id,result` and `ado pipeline list -d` work. The default pipeline resolves inside a clone that has one pipeline.
- [ ] `ado pipeline run ci -b main -p env=prod -v foo=bar --watch` triggers a YAML run and exits 0/1/2 by result. Classic pipelines run with `-v` and reject `-p`.
- [ ] `ado pipeline view 812 -v`, `--log-failed` and `--json stages` show the tree and failed logs.
- [ ] `ado pipeline watch` redraws on TTY and prints transitions when piped.
- [ ] `ado pipeline logs 812 -o out/` writes the per-task tree. `--failed` filters.
- [ ] `cancel` confirms. `rerun` queues a copy. `rerun --failed` retries only failed stages of a YAML run.
- [ ] `download -n drop -D out` extracts safely. `approve 812 -c ok` approves. `enable`/`disable` toggle the queue status.
- [ ] `ado release list/view/create/deploy/watch/logs/cancel/approve/reject` work against fixture data.
- [ ] Docs updated. Coverage >90%. CI green on linux/macos/windows.

## Out of Scope
- Prompting for YAML template parameter values. Previewing or validating YAML (`gh workflow view --yaml`). Creating/editing pipeline definitions.
- Line-level live log streaming (SignalR). Retrying specific stages by name. Agent pools, environments, variable groups, service connections, secure files.
- Deleting runs or releases. Release definition editing. Release gates management.
