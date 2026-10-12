#!/bin/sh
# Reproducibility check: build a second GoReleaser snapshot of HEAD in a git
# worktree at another path with an empty GOCACHE, then compare the sha256 of
# all binaries listed in dist/artifacts.json. Run from the repository root
# with goreleaser and jq on PATH.
set -eu

tmp=$(mktemp -d)
trap 'git worktree remove --force "$tmp/b" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

sums() {
	jq -r '.[] | select(.type == "Binary") | .path' dist/artifacts.json | sort | xargs sha256sum
}

goreleaser build --snapshot --clean >&2
sums >"$tmp/a.sum"

git worktree add --detach "$tmp/b" HEAD >&2
mkdir "$tmp/gocache"
(cd "$tmp/b" && GOCACHE="$tmp/gocache" goreleaser build --snapshot --clean >&2 && sums) >"$tmp/b.sum"

n=$(wc -l <"$tmp/a.sum")
if [ "$n" -ne 6 ]; then
	echo "expected 6 binaries in dist/artifacts.json, got $n" >&2
	exit 1
fi

if diff "$tmp/a.sum" "$tmp/b.sum"; then
	echo "reproducible: $n binaries match"
	exit 0
fi

# Mismatch: show version output and a strings diff per differing binary
# (go version -m hides ldflags under -trimpath).
diff "$tmp/a.sum" "$tmp/b.sum" | sed -n 's/^< [0-9a-f]*  //p' | while read -r p; do
	echo "== $p"
	"./$p" --version 2>&1 || true
	"$tmp/b/$p" --version 2>&1 || true
	strings "$p" >"$tmp/a.str"
	strings "$tmp/b/$p" >"$tmp/b.str"
	diff "$tmp/a.str" "$tmp/b.str" | head -n 100 || true
done
exit 1
