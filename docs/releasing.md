# Releasing

How maintainers cut a release of `ado`. Pushing a `vX.Y.Z` tag from `main`
runs [`release.yml`](https://github.com/snake-in-prod/ado-gh/blob/main/.github/workflows/release.yml):
it tests, builds, signs and publishes everything, updates the package managers,
smoke-tests the result and deploys this site. The maintainer prepares the
changelog, pushes the tag and follows the winget pull request.

## Checklist

1. `main` is green on CI (Linux, macOS, Windows, snapshot build, docs).
2. Pick the version ([Versioning](#versioning)).
3. Stable only: merge the [release-prep pull request](#release-prep-pull-request).
4. [Tag and push](#tag-and-push) one annotated tag from `main`.
5. [Monitor the workflow](#monitor-the-release) and check the smoke jobs.
6. Stable only: [follow the winget pull request](#follow-the-winget-pull-request).

## One-time setup

Done on 2026-10-13. Listed here so it can be checked or rebuilt.

- **Repositories:** GitHub `snake-in-prod/ado-gh` is canonical and public.
  `snake-in-prod/homebrew-tap` (cask `ado`) and `snake-in-prod/scoop-bucket`
  exist, and `microsoft/winget-pkgs` is forked as `snake-in-prod/winget-pkgs`
  (package `SnakeInProd.Ado`).
- **Microsoft CLA:** the account that owns `WINGET_PR_PAT` signs it once,
  through the CLA bot comment on its first winget-pkgs pull request. Until then
  the PR is labelled `Needs-CLA` and is not reviewed.
- **Tokens,** stored as secrets in the `release` environment:
    - `PACKAGES_PAT`, fine-grained, owner `snake-in-prod`, repositories
      `homebrew-tap`, `scoop-bucket` and `winget-pkgs`: Contents read/write
      (Workflows read/write on the fork if syncing it needs it). GoReleaser
      pushes the cask, the Scoop manifest and the winget branch with it.
    - `WINGET_PR_PAT`, classic, `public_repo` only. It opens the pull request
      into `microsoft/winget-pkgs`, which fine-grained tokens can't do.
    - Both expire. Put the expiry dates in a calendar and rotate the secrets
      before then: an expired token fails the release after the GitHub release
      is already published.
- **GPG:** the primary key `E8CC 041B D0BE BDC1 3ADB 967E EC1E F53E 7EF8 CA48`
  (`ado release signing <f.dziewiatowski@gmail.com>`) is certify-only and kept
  offline with its revocation certificate. It is published and email-verified
  on [keys.openpgp.org](https://keys.openpgp.org) (web upload; `gpg
  --send-keys` skips the email verification and GnuPG refuses keys without an
  identity). CI holds only the signing subkey
  `9236 1482 2D64 5B37 06B7 56E7 F621 5CEA 43F8 A59C`, exported with
  `gpg --armor --export-secret-subkeys 923614822D645B3706B756E7F6215CEA43F8A59C!`, as
  `GPG_PRIVATE_KEY`, with its passphrase as `GPG_PASSPHRASE`. The subkey
  expires two years after creation (October 2028): extend it with the offline
  primary (`gpg --quick-set-expire <primary> 2y <subkey>`), re-upload the
  public key, and replace the secret. The fingerprint users check stays the
  same.
- **Pages:** source "GitHub Actions". The `github-pages` and `release`
  environments allow `v*` tags (deployment branch and tag rules), and a `v*`
  tag ruleset blocks deleting and moving release tags.
- **Optional:** a `SMOKE_ADO_TOKEN` secret enables the authenticated smoke
  checks.

## Versioning

Tags are `vX.Y.Z` or `vX.Y.Z-<prerelease>` (use `-rc.N`), strict SemVer
without `+build` metadata. The workflow rejects anything else, and any tag
whose commit is not on `main`.

What counts as a breaking change for `ado`: removing or renaming a command,
flag or environment variable, changing a flag default, changing `--json` field
names or types, changing exit codes, or changing the config file format in a
way old files stop working. Adding commands, flags or `--json` fields is not
breaking, and neither is rewording human-readable (TTY) output.

| Change since the last stable tag | 1.0 and later | Before 1.0 (`0.y.z`) |
| --- | --- | --- |
| Breaking change | major | minor |
| New feature | minor | minor |
| Fixes, performance, docs only | patch | patch |

`git cliff --bumped-version` prints the version these rules give, from the
commits since the last stable tag. `cliff.toml` sets
`[bump] breaking_always_bump_major = false` so a breaking change before 1.0
bumps the minor version instead of jumping to `1.0.0`. Going to 1.0 is a
deliberate decision, not a side effect of a `!` commit.

## Commit subjects are release notes

Every `feat`, `fix`, `perf` and `docs` subject on `main` lands in
`CHANGELOG.md` and on the release page word for word. Write them for users:

- Describe the user-visible change: `fix(pr): keep the draft flag when creating
  from a template`, not `fix(pr): address review findings`. Fold review fixes
  into the commit they fix, or describe what they change.
- Mark breaking changes with `!` (`feat(pr)!: ...`) and add a `BREAKING
  CHANGE:` footer that says what to do instead.
- Use `chore`, `test`, `ci`, `style`, `refactor`, `build` or the `conductor`
  scope for work users don't see. These never reach the notes.

## Changelog and release notes

`CHANGELOG.md` and the release notes come from Conventional Commits through
`git-cliff` (v2.14.2) and the repository's `cliff.toml`. Breaking changes
(`type!:` or a `BREAKING CHANGE:` footer), `feat`, `fix`, `perf` and `docs`
commits are listed. `chore`, `test`, `style`, `ci`, `conductor`, any
`(conductor)` scope, other types such as `refactor`, `build` and `revert`, and
non-conventional subjects are left out.

Notes for the tag being released (body only: the version heading is dropped so
the notes match the `CHANGELOG.md` section whatever date was rendered). The
release workflow runs these itself; they are here to preview locally:

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

## Release-prep pull request

Stable releases only. Prereleases don't touch `CHANGELOG.md`.

```sh
git switch -c release/vX.Y.Z origin/main
git cliff --tag vX.Y.Z -o CHANGELOG.md
git commit -am "chore(release): prepare vX.Y.Z"
gh pr create --fill
```

Read the new section, but don't edit it by hand: the release notes are
rendered from the same commits and must match it. Merge once CI is green, and
merge nothing else before tagging: the tag must point at the prep commit, or
the release notes list commits the `CHANGELOG.md` section doesn't.

## Tag and push

```sh
git switch main
git pull --ff-only
git log -1 --oneline   # the prep commit (stable) or the commit to test (rc)
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

Push one tag per `git push`. GitHub creates no push events when more than three
tags are pushed at once, so `git push --tags` can silently release nothing.

## Monitor the release

Watch the run with `gh run watch` or on the Actions tab. Its jobs:

- **guard:** the tag is strict SemVer and on `main`; `go test ./... -race`.
  No secrets are in reach yet. If it fails, nothing was published.
- **release:** renders the notes (fails if they are empty or the heading
  doesn't match the tag), runs GoReleaser (archives, `.deb`/`.rpm`,
  `checksums.txt`, SBOMs, cosign and GPG signatures, GitHub release, Homebrew
  cask, Scoop manifest, winget pull request), then creates build provenance and
  uploads `attestations.jsonl`.
- **smoke:** downloads the release on all six platforms, verifies checksum,
  cosign, GPG and attestations, and runs `ado --version`, `ado --help` and
  `ado auth status`. Stable tags also install through every channel
  (Homebrew, Scoop, `.deb`, `.rpm`, `install.sh`, `install.ps1`,
  `go install`) and check `ado --version`.
- **docs** (stable only): rebuilds the reference and deploys this site.

Then check the release page: notes present, assets complete, prerelease flag
set only for `-rc` tags.

If the run fails before GoReleaser publishes, fix the cause on `main` and
re-run the failed jobs if the cause was transient; otherwise release the next
patch version. Once the GitHub release exists, don't re-run the release job:
finish the missing step by hand and re-run what follows it:

```sh
gh workflow run smoke.yml -f version=vX.Y.Z
gh workflow run docs.yml --ref vX.Y.Z
```

A failed winget pull request (for example an expired `WINGET_PR_PAT`) turns
the release job red after everything else was published. The branch
`ado-X.Y.Z` is already on the fork, so open the PR by hand, titled with the
branch's commit message:

```sh
gh pr create --repo microsoft/winget-pkgs --base master \
  --head snake-in-prod:ado-X.Y.Z --title "<commit message>" --body ""
```

## Follow the winget pull request

winget is the only channel that isn't automatic: every version is a pull
request into `microsoft/winget-pkgs`, validated by a bot and then reviewed by a
moderator. It can take days, and it's not smoke-tested.

- Find it with `gh pr list --repo microsoft/winget-pkgs --search SnakeInProd.Ado`.
- `Needs-CLA`: sign the Microsoft CLA from the bot's comment (once).
- Validation failures are labelled on the PR with a link to the log. If the
  failure looks transient (URL timeout, pipeline error), comment
  `@wingetbot run` to re-run validation.
- **Defender or antivirus false positives** block the PR outright, whatever
  the binary does. Unsigned Go binaries get flagged now and then. Submit the
  flagged file as a false positive at the
  [Microsoft Security Intelligence portal](https://www.microsoft.com/en-us/wdsi/filesubmission),
  link the submission on the PR, and comment `@wingetbot run` once the
  definitions are updated.
- The PR closes itself if it waits on the author for about a week: answer
  moderator questions promptly.
- After the first version (`New package: ...`) is accepted, change
  `commit_msg_template` in `.goreleaser.yaml` to
  `New version: {{ .PackageIdentifier }} version {{ .Version }}`.
- Once merged, check on Windows: `winget upgrade SnakeInProd.Ado` (or
  `winget install`) and `ado --version`.

## Prerelease flow

Tag `vX.Y.Z-rc.N` from `main`, starting at `rc.1`, with no prep pull request.
The release is marked as a prerelease and gets every asset, signature and
attestation, but the Homebrew tap, Scoop bucket, winget and the docs site are
not touched, and the channel smoke jobs are skipped. The notes list
everything since the last stable tag. When the rc is good, do the prep pull
request and tag `vX.Y.Z` the usual way.

## Hotfix flow

Tags must be on `main`, so hotfixes go through `main` too:

1. Write a failing test for the bug, fix it with a user-facing `fix:` subject,
   and merge to `main` through a pull request.
2. Release the next patch version from `main` (prep PR, tag, push). Anything
   else merged to `main` since the last release ships with it; if that's not
   ready, hold it back or revert it on `main` first.

For a leaked credential, revoke it first, then follow the same flow and call
it out in the changelog.

## Rollback policy

Never delete or move a release tag (the tag ruleset blocks it), and never
replace published assets: users, package managers and checksums already point
at them. To withdraw a bad release:

- Release a fixed patch version as soon as possible. The tap, bucket and a new
  winget PR then move users forward.
- Meanwhile, add a warning at the top of the bad release's notes
  (`gh release edit vX.Y.Z --notes-file ...`). If its winget PR is still open,
  close it.

## Maintenance

- **Intel macOS runners:** the darwin/amd64 smoke row runs on
  `macos-15-intel`. GitHub drops Intel macOS runners when the macOS 15 image
  is retired in fall 2027. Remove that row from `smoke.yml` then; the
  darwin/amd64 binary keeps shipping, just without a hosted smoke test.
- **Pinned tools:** GoReleaser is pinned to v2.18.3 (v2.19 deprecates the
  cask's `hooks.post.install`, and `goreleaser check` fails on deprecations).
  Bump tools and action SHAs on purpose, in their own pull request, and let
  the snapshot and reproducibility CI jobs run.
- **Tokens and GPG subkey:** rotate before they expire (see
  [One-time setup](#one-time-setup)).
