#!/usr/bin/env bash
# Release tag guard, run by the release workflow before any secret is in reach.
# Usage: scripts/release-guard.sh <tag>
# Accepts vX.Y.Z and vX.Y.Z-<prerelease> (strict SemVer, no leading zeros, no
# +build metadata) whose commit is on origin/main. Needs full history.
set -euo pipefail

tag=${1-}

# SemVer 2.0.0 prerelease identifier: numeric without leading zeros, or alphanumeric.
id='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
re="^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-$id(\.$id)*)?\$"
if [[ ! $tag =~ $re ]]; then
	echo "::error::$(printf '%q' "$tag") is not a release tag: want vX.Y.Z or vX.Y.Z-<prerelease>" >&2
	exit 1
fi

# ^{commit} peels annotated tags.
if ! commit=$(git rev-parse --verify --quiet "refs/tags/$tag^{commit}"); then
	echo "::error::no tag $tag in this clone" >&2
	exit 1
fi

if ! git merge-base --is-ancestor "$commit" origin/main; then
	echo "::error::$tag ($commit) is not on origin/main: tag releases from main" >&2
	exit 1
fi

echo "$tag ($commit) is a release tag on main"
