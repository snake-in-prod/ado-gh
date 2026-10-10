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
- **Identity (Read)** (needed to resolve reviewers and authors)

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

## License

[MIT](LICENSE)
