# Releasing

How maintainers cut a release of `ado`: one-time setup, versioning rules, the
release-prep pull request, tagging and checking the published artifacts.

This checklist is still being written. Nothing is released yet.

## Changelog and release notes

`CHANGELOG.md` and the release notes come from Conventional Commits through
`git-cliff` (v2.14.2) and the repository's `cliff.toml`. Breaking changes
(`type!:` or a `BREAKING CHANGE:` footer), `feat`, `fix`, `perf` and `docs`
commits are listed. `chore`, `test`, `style`, `ci`, `conductor`, any
`(conductor)` scope, other types such as `refactor`, `build` and `revert`, and
non-conventional subjects are left out.

Release-prep pull request, committed as `chore(release): prepare vX.Y.Z`:

```sh
git cliff --tag vX.Y.Z -o CHANGELOG.md
```

Notes for the tag being released (body only: the version heading is dropped so
the notes match the `CHANGELOG.md` section whatever date was rendered):

```sh
# stable vX.Y.Z (HEAD at the tag, full history fetched)
git cliff --current --strip header | sed -e '1,/^## /d' -e '/./,$!d' > notes.md
# prerelease vX.Y.Z-rc.N
git cliff --unreleased --tag "$TAG" --strip header | sed -e '1,/^## /d' -e '/./,$!d' > notes.md
goreleaser release --clean --release-notes notes.md
```

`-rc.N` tags are ignored by `cliff.toml`, so a stable section lists everything
since the previous stable tag, and rc notes are cumulative across the rc
series. Never use `--current` or `--latest` on an rc tag: they render the
previous stable section. Never set `changelog.disable` in GoReleaser: it drops
the `--release-notes` file and publishes empty notes.
