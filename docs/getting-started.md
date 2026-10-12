# Getting started

[Install](install.md) `ado` and [sign in](auth.md) first. Inside a clone of an
Azure DevOps repository, `ado` takes the organization, project and repository
from the git remote. Elsewhere, pass `-R ORG/PROJECT/REPO` (or `-R ORG/PROJECT`
for project-level commands).

## Work items, sprints and boards

```sh
ado issue create --type bug -t "Crash on save" -a @me --iteration @current
ado wi list -a @me --iteration @current
ado wi close 123 -c "Fixed in !45"
ado wi query "Shared Queries/Team/Active Bugs" --json id,title
ado sprint view
ado board
```

`ado workitem` has the aliases `wi` and `issue`. These commands need a project,
not a repository, so outside a clone use `-R ORG/PROJECT`. Team-scoped commands
(`--iteration @current`, `sprint`, `board`) use `--team`, else the `team` config
key, else the project's default team:

```sh
ado config set team "Web Team" --project fabrikam/Fiber
```

See [Differences from `gh`](gh-differences.md#work-items-not-issues) for how
they differ from `gh issue`.

## Pipelines and releases

```sh
ado run list -b main --result failed
ado pipeline run deploy -b main -p env=prod --watch
ado run view 812 --log-failed
ado pipeline approve 812 -c "ship it"
ado pipeline download 812 -n drop -D out
ado release deploy 45 --environment Prod
```

`ado pipeline` (alias `run`) covers YAML and classic build pipelines; `ado release`
covers Classic Release pipelines. See
[Differences from `gh`](gh-differences.md#pipelines-not-workflows) for how they
differ from `gh run` and `gh workflow`.

## REST API

```sh
ado api _apis/projects --jq '.value[].name'
ado api '{project}/_apis/git/repositories/{repo}/pullrequests?$top=100' --paginate
```

Paths are relative to the organization URL; `{org}`, `{project}` and `{repo}` come
from the current repository. `--paginate` follows continuation tokens (and `$skip`
when you pass `$top`) and merges `value` arrays into one result. See `ado api --help`.

## Next

- [Command reference](reference/index.md) for every command and flag.
- [Extensions](extensions.md) to add your own commands.
