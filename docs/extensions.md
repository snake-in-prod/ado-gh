# Writing `ado` extensions

An extension is an executable named `ado-<name>`. Once installed, `ado <name>`
runs it with the user's terminal, arguments and exit code, plus a few
environment variables that describe the Azure DevOps context. Extensions call
back into `ado` through `$ADO_EXECUTABLE` instead of linking a library.

There are three kinds:

- **Script**: a git repository with an executable `ado-<name>` at its root.
  `ado` clones it.
- **Precompiled**: binaries for each platform, published as a GitHub release or
  as an Azure Artifacts universal package. `ado` downloads the one for the
  user's platform and verifies its checksum.
- **Local**: a directory on the user's machine, linked by
  `ado extension install <dir>`. Handy while you develop.

## Quick start

```sh
ado extension create hello                     # script extension
ado extension create hello --precompiled=go    # Go program + release pipelines
ado extension create hello --precompiled=other # any language, you write script/build.sh

cd ado-hello
ado extension install .   # local install: edits take effect immediately
ado hello
```

`create` makes the directory `ado-hello`, writes the files below and makes an
initial git commit with the executable bit set.

## Naming

- The repository (and the local directory) is named `ado-<name>`. Users run
  `ado <name>`.
- `<name>` uses lowercase letters, digits, `.`, `_` and `-`, starts with a
  letter or digit and doesn't end with `.`.
- `<name>` can't be a core command or a built-in alias (`pr`, `repo`, `wi`,
  `issue`, `run`, `help`, ...). `ado extension install` refuses those.
- A user alias of the same name wins over your extension. Users can still run
  it with `ado extension exec <name>`.
- If you publish binaries to Azure Artifacts, also keep to lowercase letters,
  digits and dashes: universal package names allow nothing else, and the
  package is named `ado-<name>`.
- On GitHub, add the topic **`ado-extension`** so that
  `ado extension search` and `ado extension browse` find the repository.

## The manifest: `ado-extension.yml`

An optional file at the repository root. A plain script extension that needs
no token can do without it.

```yaml
name: hello                 # optional; must equal the repository name without "ado-"
description: Says hello     # shown in the install confirmation
token: false                # true asks the user for consent to get ADO_TOKEN
minAdoVersion: ""           # reserved; not checked yet
binaries:                   # omit for a script extension
  azure-artifacts:
    organization: myorg
    project: tools          # omit for an organization-scoped feed
    feed: ado-extensions
    package: ado-hello
```

| Field | Meaning |
|---|---|
| `name` | The extension name, with or without `ado-`. When set, it must match the repository name. |
| `description` | One line shown before install. Control characters are stripped. |
| `token` | `true` if the extension needs the user's Azure DevOps token as `ADO_TOKEN`. See [Token consent](#token-consent). |
| `binaries.github-releases` | `true` documents that binaries come from GitHub releases. `ado` detects release binaries on its own, so it's informational. |
| `binaries.azure-artifacts` | Install the binary from a universal package: `organization`, `feed`, `package` (required) and `project` (optional, empty = organization-scoped feed). |
| `minAdoVersion` | Reserved for a future version check. |

Set either `github-releases` or `azure-artifacts`, not both. The
`azure-artifacts` values become ArtifactTool arguments, so they can't start
with `-` or contain `/`, `\`, control characters or (except `project`) spaces.

## Environment

`ado` runs the extension with its own environment plus these variables. A
context variable is empty when `ado` couldn't resolve it (for example outside
a clone); that is not an error.

| Variable | Value |
|---|---|
| `ADO_HOST` | The API host: `dev.azure.com`, or `<org>.visualstudio.com` for old-style clones. Never `ssh.dev.azure.com`. |
| `ADO_ORG` | Organization. |
| `ADO_PROJECT` | Project. |
| `ADO_REPO_NAME` | Repository name, when the repository came from the git remote. |
| `ADO_REPO` | `ORG/PROJECT/REPO`, only when the user passed `-R` before the extension name, or already had `ADO_REPO` set. |
| `ADO_EXTENSION_NAME` | The extension's name, without `ado-`. |
| `ADO_EXECUTABLE` | Absolute path of the running `ado`. Call it instead of `ado` on `PATH`. |
| `ADO_DEBUG` | `1` when the user passed `--debug` before the extension name. |
| `ADO_TOKEN` | Only with token consent: the user's token for `ADO_ORG` (unset when there is no organization). |
| `ADO_TOKEN_TYPE` | `bearer` when `ADO_TOKEN` is a Microsoft Entra ID access token; unset for a PAT. |

Why `ADO_REPO` is usually unset: a nested `ado` call reads `ADO_REPO` as an
override, which turns off its git-remote logic. So unless the user chose a
repository with `-R`, `ado` exports `ADO_REPO_NAME` next to `ADO_HOST`,
`ADO_ORG` and `ADO_PROJECT`, and nested `ado` calls resolve the repository from
the git remote as usual. To get the repository either way:

```sh
repo="${ADO_REPO:-$ADO_ORG/$ADO_PROJECT/$ADO_REPO_NAME}"
```

The organization URL is `https://dev.azure.com/$ADO_ORG/`, or
`https://$ADO_HOST/` when `ADO_HOST` ends in `.visualstudio.com`.

`-R` before the extension name (`ado -R org/proj/repo hello`) is read by `ado`.
Everything after the name, `-R` included, is passed to the extension
unchanged.

### Terminal and colour

`ado` doesn't export its own TTY or colour decisions. stdin, stdout and stderr
are the user's, and these variables are inherited as the user set them:

`ADO_FORCE_TTY`, `NO_COLOR`, `CLICOLOR`, `CLICOLOR_FORCE`,
`ADO_PROMPT_DISABLED`, `CI`, `TERM`, `COLORTERM`, `GLAMOUR_STYLE`.

Check whether your own stdout is a terminal and honour these the way `ado`
does: colour off with `NO_COLOR` or `CLICOLOR=0`, forced with
`CLICOLOR_FORCE`; no prompts with `ADO_PROMPT_DISABLED` or `CI`; a nested
`$ADO_EXECUTABLE` call sees the same streams and variables, so it formats its
output the same way.

### Calling `ado`

A nested `$ADO_EXECUTABLE` call uses the user's login and the same context,
**without** token consent:

```sh
"$ADO_EXECUTABLE" pr list --json id,title
"$ADO_EXECUTABLE" api '{project}/_apis/git/repositories'
```

You need `token: true` only to call the REST API or git yourself.

## Token consent

With `token: true`, `ado extension install` asks the user a separate yes/no
question (default no), or takes `--allow-token`. The answer is stored in the
install record (`extensions/.records/<name>.yml`), outside your repository, so
a repository can't ship its own consent. `ado extension upgrade` asks again
when a new version starts requesting the token.

With consent, the extension gets:

- `ADO_TOKEN`, the token `ado` uses for `ADO_ORG`, plus `ADO_TOKEN_TYPE=bearer`
  when the user signed in with Microsoft Entra ID. Send a PAT as
  `Authorization: Basic base64(":" + token)` and an Entra token as
  `Authorization: Bearer <token>`. A nested `ado` handles both.
- Entra tokens expire after about an hour. A long-running extension can get a
  fresh one with `"$ADO_EXECUTABLE" auth token`.

Without consent:

- `ADO_TOKEN`, `AZURE_DEVOPS_EXT_PAT` and `ADO_TOKEN_TYPE` are **removed** from
  the extension's environment, even when the user exported them, and `ado`
  prints one warning on stderr naming the variables it withheld.
- `ado auth token` refuses to run when `ADO_EXTENSION_NAME` names an extension
  without consent.
- Extensions on `PATH` that weren't installed never get a token.

Consent is a convention, not a security boundary. Extensions run with the
user's privileges, so one without consent can still:

- call any other `$ADO_EXECUTABLE` command, which uses the stored login;
- unset `ADO_EXTENSION_NAME` and run `ado auth token`;
- read the OS keychain, or the plaintext token in `config.yml` when no keychain
  is available;
- ask Git Credential Manager for a credential.

Users should install only extensions they trust, as with any program.

## Script extensions

The repository root holds an executable `ado-<name>` starting with a shebang
(`#!/bin/sh`, `#!/usr/bin/env python3`, ...).

- **Executable bit.** Install requires git mode `100755` on Linux and macOS.
  Commits made on Windows lose it, so set it in the index:

  ```sh
  git update-index --chmod=+x ado-hello
  git commit -m "Make ado-hello executable"
  ```

  On Windows, `ado` also accepts a file starting with `#!`, or an
  `ado-<name>.ps1` / `ado-<name>.cmd` next to it.

- **Line endings.** A CRLF shebang line fails to run (`/bin/sh^M: bad
  interpreter`). `ado` clones with `core.autocrlf=false` and `core.eol=lf`, but
  your `.gitattributes` wins over that, so add:

  ```gitattributes
  ado-* text eol=lf
  *.cmd text eol=crlf
  ```

  Keep the `*.cmd` line after `ado-*`: for a file both match, the later line
  wins.

- **Windows.** `ado` runs the first of these in the extension directory:
  1. `ado-<name>.exe`
  2. `ado-<name>.ps1` through `pwsh` (else `powershell`) with
     `-NoProfile -ExecutionPolicy Bypass -File`. Group Policy still overrides
     the execution policy.
  3. `ado-<name>.cmd`
  4. `ado-<name>` through `sh` from Git for Windows.

- **`.cmd` limits.** Go can't quote arguments for `cmd.exe`, so `ado` refuses
  to pass an argument containing `&`, `|`, `<`, `>`, `^`, `%` or `"` to a
  `.cmd`/`.bat` extension. Ship an `.exe` or `.ps1` if users need those.

- **`pwsh -File` caveats.** Ctrl+C ends a `.ps1` extension with exit code 0,
  so `ado` can't tell the user it was interrupted. And arguments starting with
  `-` bind to the script's declared parameters: with `param($Name)`, an
  argument `-Name` is taken by PowerShell. Read `$args` (and declare no
  parameters) if you parse arguments yourself.

- **Versions.** Users install the default branch, or a tag or commit with
  `--pin`. `ado extension upgrade` runs `git fetch origin HEAD` and
  `git merge --ff-only FETCH_HEAD`, so it follows a renamed default branch, but
  rewritten history makes it fail until the user passes `--force` (which resets
  to the remote). Don't force-push the default branch.

- **Private Azure DevOps repositories** are cloned with the user's login over
  HTTPS, which needs git 2.31 or later on the user's machine. Submodules are
  not fetched.

## Precompiled extensions

### Binary names

One file per platform, named with Go's `GOOS` and `GOARCH`:

```text
ado-<name>-linux-amd64        ado-<name>-linux-arm64
ado-<name>-darwin-amd64       ado-<name>-darwin-arm64
ado-<name>-windows-amd64.exe  ado-<name>-windows-arm64.exe
```

`ado` picks the exact name first, then any file ending in
`<os>-<arch>[.exe]` (the `gh-extension-precompile` naming). On Apple silicon it
falls back to `darwin-amd64` (Rosetta 2) with a message. With no match it
errors and lists the platforms you published.

Archives (`.tar.gz`, `.zip`) aren't unpacked: publish the bare binaries.

### Checksums

Publish a file named exactly **`checksums.txt`** next to the binaries, in
`sha256sum` format:

```text
3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b  ado-hello-linux-amd64
```

`<hash> *<file>` and paths are accepted (only the base name counts). On GitHub
a single asset ending in `_checksums.txt` (GoReleaser's default
`<project>_<version>_checksums.txt`) also works.

`ado` hashes the binary while writing it and aborts on a mismatch. Without a
checksums file, users must pass `--allow-unverified`.

A checksum published in the same release proves **integrity, not
authenticity**: it catches a corrupted or truncated download, but whoever can
publish the release can replace both files. `ado` doesn't verify signatures or
attestations. The generated GitHub workflow creates build-provenance
attestations that users can check themselves with `gh attestation verify`.

### GitHub releases

`ado extension install owner/ado-<name>` installs the binary from the latest
release when that release has platform binaries, else it clones the repository
as a script extension (as `gh` does). `--pin v1.2.0` installs that release,
prereleases included. `ado` reads `ado-extension.yml` at the release tag for
`description` and `token`.

The workflow from `ado extension create --precompiled=go` builds the six
binaries, writes `checksums.txt` and runs `gh release create` when you push a
`v*` tag. A tag with a `-` (`v1.0.0-rc.1`) makes a prerelease, which `latest`
skips.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

GitHub's API allows 60 unauthenticated requests per hour. Users who hit the
limit can set `GH_TOKEN` or `GITHUB_TOKEN`; `ado` sends it to `api.github.com`
only.

### Azure Artifacts

Universal packages exist only in **Azure DevOps Services**, not Azure DevOps
Server.

Users install the **repository**, which holds `ado-extension.yml` with
`binaries.azure-artifacts`:

```sh
ado extension install https://dev.azure.com/myorg/tools/_git/ado-hello
```

`ado` clones it to read the manifest, then downloads the package with
Microsoft's ArtifactTool, which it fetches from the user's organization and
caches. The package holds the binaries and `checksums.txt` at its root, named
as above.

- **Versions.** A package version is SemVer without a `v`: tag `v1.2.3`
  publishes and installs version `1.2.3`. Without `--pin`, `ado` installs the
  highest version without a prerelease part, as ArtifactTool's `*` does.
- **Project or organization feed.** Feeds created inside a project are
  project-scoped: set `project`. Leave it out for an organization-scoped feed.
- **Permissions.** Users need read access to the feed. The pipeline's build
  identities need **Feed Publisher (Contributor)** on the feed (Feed settings >
  Permissions): both *Project Collection Build Service (`<org>`)* and
  *`<project>` Build Service (`<org>`)*.
- **Another organization.** A feed outside the repository's organization works
  only if the user's `extension_sources` allowlist (when set) allows that
  organization too.

The generated `azure-pipelines.yml` runs on `v*` tags: it builds with a pinned
Go version, writes `checksums.txt`, strips the `v` from the tag and publishes
`dist/` with `UniversalPackages@0`. Set `vstsFeedPublish` (`project/feed`, or
just `feed` for an organization feed) and the manifest's `azure-artifacts`
block to your feed.

## Exit codes

`ado <name>` exits with the extension's exit code. An extension killed by a
signal exits 1. Follow `ado`'s conventions where they fit: `0` success, `1`
error, `2` cancelled by the user, `4` authentication needed. Write errors to
stderr and data to stdout.

## How users find and manage extensions

```sh
ado extension search lint            # GitHub repos with the ado-extension topic
ado extension browse                 # pick one, read its README, install
ado extension install myorg/ado-lint --pin v1.2.0
ado extension list
ado extension upgrade --all --dry-run
ado extension remove lint
```

Admins can restrict sources with the `extension_sources` config list. `*`
matches one path segment and `**` any number; `local` allows local installs:

```yaml
extension_sources:
  - github.com/myco/*
  - dev.azure.com/myorg/**
  - local
```

`ado` also runs any `ado-<name>` executable on `PATH`, after built-in commands,
aliases and installed extensions. PATH extensions show up in `ado help` and
completion but never get a token.
