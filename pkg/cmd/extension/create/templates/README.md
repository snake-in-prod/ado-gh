# ado-<% .Name %>

An extension for [ado](https://github.com/snake-in-prod/ado-gh), the Azure DevOps CLI.

## Install

    ado extension install <repository URL>

## Usage

    ado <% .Name %>
<%- if eq .Kind "other" %>

## Releases

`script/build.sh` builds one binary per platform into `dist/`, named
`ado-<% .Name %>-<os>-<arch>` (`.exe` on Windows) for `linux`, `darwin` and `windows` on
`amd64` and `arm64`, e.g. `dist/ado-<% .Name %>-darwin-arm64`. ado installs the binary for the
user's platform and checks it against its SHA-256 in `checksums.txt`.

Pushing a `v*` tag runs both pipelines. Each runs `script/build.sh`, writes
`dist/checksums.txt` and publishes `dist/`:

- `.github/workflows/release.yml` as a GitHub release of the tag
  (`binaries: {github-releases: true}` in `ado-extension.yml`).
- `azure-pipelines.yml` as an Azure Artifacts universal package whose version is the tag
  without its `v` (`binaries: {azure-artifacts: ...}` in `ado-extension.yml`).
<%- end %>

See <% .DocsURL %> for writing and publishing ado extensions.
