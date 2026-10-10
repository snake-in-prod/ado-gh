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

Entra login uses a multi-tenant public-client app registration. ado does not ship
a default client ID yet, so set one before logging in with Entra ID. The
environment variable wins over the config key:

```sh
export ADO_ENTRA_CLIENT_ID=<client-id>
# or
ado config set entra_client_id <client-id>
```

Without a client ID, Entra login fails and points you to these settings or to a PAT.
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

#### Registering the Entra app (maintainers, or your own tenant)

1. In the [Microsoft Entra admin center](https://entra.microsoft.com), go to
   **App registrations > New registration**.
2. Name: `ado`. Supported account types: **Accounts in any organizational directory**
   (multi-tenant).
3. Redirect URI: platform **Mobile and desktop applications**, URI `http://localhost`.
4. Under **Authentication**, set **Allow public client flows** to **Yes**
   (needed for device-code login).
5. Under **API permissions > Add a permission > Azure DevOps**, add the delegated
   permission **user_impersonation**.
6. Copy the **Application (client) ID** from **Overview**. Use it through
   `ADO_ENTRA_CLIENT_ID` / `entra_client_id`, or set it as `defaultEntraClientID`
   in `internal/auth/entra_client_id.go` and document it here.

## Development

```sh
gofmt -l . && go vet ./... && golangci-lint run && go test ./... -race -cover
```

## License

[MIT](LICENSE)
