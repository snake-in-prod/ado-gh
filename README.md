# ado

`ado` is a command-line tool for Azure DevOps, modeled on the GitHub CLI (`gh`).
It brings pull requests, repositories and other Azure DevOps concepts to the terminal.

> Status: early development. Nothing is released yet.

## Build

```sh
go build -o bin/ado ./cmd/ado
```

## Authentication

`ado auth login` signs in with a personal access token (PAT) or Microsoft Entra ID.

```sh
ado auth status   # user, method, token source (masked token); exit 4 if not logged in
ado auth token    # print the raw token
ado auth logout   # remove the stored PAT and the Entra ID account and token cache
```

They act on `--org`, else the current git remote, else `ADO_ORG`.

### Personal access token

Create an **organization-scoped** PAT (global PATs stop working on 2026-12-01) with these scopes:

- **Code (Read & write)**
- **Identity (Read)** (needed to resolve reviewers, authors and assignees)
- **Work Items (Read & write)** (work items, queries, sprints and boards)
- **Project and Team (Read)** (teams, sprints and boards)
- **Build (Read)** to list, view, watch and download runs; **Build (Read & execute)**
  to run, cancel, rerun, enable and disable pipelines and to approve runs
- **Pipeline Resources (Use)** to approve or reject runs, if you'd rather not grant
  Build (Read & execute)
- **Release (Read)** to list, view, watch and read releases; **Release (Read, write &
  execute)** to create, deploy and cancel them; **Release (Read, write, execute &
  manage)** to approve or reject them

Work Items (Read & write) is expected to cover `ado workitem delete --permanent`
too: the permanent delete was checked with an Entra ID login, not a PAT. If it
fails with a PAT, add **Work Items (Full)**.

```sh
ado auth login --with-token < pat.txt
```

### Microsoft Entra ID

Entra login uses ado's multi-tenant public-client app (client ID
`41d205f0-a5e5-4a1f-813a-46eec13d0249`). Use your own app registration instead with
the environment variable or the config key (the variable wins):

```sh
export ADO_ENTRA_CLIENT_ID=<client-id>
# or
ado config set entra_client_id <client-id>
```

The first sign-in in a tenant asks you to consent to ado reading Azure DevOps as you.
If your tenant only lets users consent to apps from verified publishers, you'll see
"Need admin approval": ask a tenant admin to grant consent once for everyone at
`https://login.microsoftonline.com/<tenant-id>/adminconsent?client_id=41d205f0-a5e5-4a1f-813a-46eec13d0249`,
or use your own app registration or a PAT.

Orgs backed by personal Microsoft accounts (MSA) can't use Entra login; use a PAT.

```sh
ado auth login                        # choose "Microsoft Entra ID": opens your browser
ado auth login --org contoso --device # device code flow
```

The browser flow is the default. ado falls back to the device code flow over SSH,
on Linux without a display, or when no browser can be opened. Without `--org` or a
git remote, ado lists your organizations to pick from. Tokens are cached in an
AES-GCM-encrypted `msal_cache.bin` in the config directory, with its key in the
system credential store; without a credential store the cache is a plain-text
`msal_cache.json` (mode 0600) and ado warns once.

#### Registering your own Entra app

1. In the [Microsoft Entra admin center](https://entra.microsoft.com), go to
   **App registrations > New registration**.
2. Name: `ado`. Supported account types: **Accounts in any organizational directory**
   (multi-tenant).
3. Redirect URI: platform **Mobile and desktop applications**, URI `http://localhost`.
4. Under **Authentication**, set **Allow public client flows** to **Yes**
   (needed for device-code login).
5. Under **API permissions > Add a permission > Azure DevOps**, add the delegated
   permission **user_impersonation**.
6. Copy the **Application (client) ID** from **Overview** and use it through
   `ADO_ENTRA_CLIENT_ID` / `entra_client_id`.

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

See [docs/gh-differences.md](docs/gh-differences.md) for how they differ from
`gh issue`.

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
covers Classic Release pipelines. See [docs/gh-differences.md](docs/gh-differences.md)
for how they differ from `gh run` and `gh workflow`.

## REST API

```sh
ado api _apis/projects --jq '.value[].name'
ado api '{project}/_apis/git/repositories/{repo}/pullrequests?$top=100' --paginate
```

Paths are relative to the organization URL; `{org}`, `{project}` and `{repo}` come
from the current repository. `--paginate` follows continuation tokens (and `$skip`
when you pass `$top`) and merges `value` arrays into one result. See `ado api --help`.

## Development

```sh
gofmt -l . && go vet ./... && golangci-lint run && go test ./... -race -cover
```

The docs site lives in `docs/` (MkDocs, see `mkdocs.yml`). The command reference
is generated and gitignored, so generate it before a strict build:

```sh
go run ./cmd/ado help reference --dir docs/reference
pip install -r docs/requirements.txt   # in a virtualenv
mkdocs build --strict
```

## License

[MIT](LICENSE)
