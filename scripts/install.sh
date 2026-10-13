#!/bin/sh
# Installs ado from GitHub Releases.
#
#   curl -fsSL https://snake-in-prod.github.io/ado-gh/install.sh | sh
#   curl -fsSL https://snake-in-prod.github.io/ado-gh/install.sh | sh -s -- --version 1.2.3 --bin-dir ~/bin
#
# Verifies the archive against checksums.txt (SHA-256), and checksums.txt
# against its cosign bundle when cosign is installed. No sudo needed.
#
# Everything is in functions and `main "$@"` is the last line, so a truncated
# download runs nothing.
set -eu

repo=snake-in-prod/ado-gh

usage() {
	cat <<'EOF'
Install ado from GitHub Releases.

Usage: install.sh [--version VERSION] [--bin-dir DIR]

Options:
  --version VERSION  release to install, e.g. 1.2.3 (default: latest)
  --bin-dir DIR      install directory (default: ~/.local/bin)
  -h, --help         show this help
EOF
}

say() { echo "ado-install: $*" >&2; }
die() {
	say "error: $*"
	exit 1
}

detect_os() {
	case $(uname -s) in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		die "on Windows use install.ps1 in PowerShell instead:
  irm https://snake-in-prod.github.io/ado-gh/install.ps1 | iex"
		;;
	*) die "unsupported operating system: $(uname -s)" ;;
	esac
}

# detect_arch <os>: a Darwin x86_64 shell may run under Rosetta on Apple
# silicon; install the native arm64 binary then.
detect_arch() {
	case $(uname -m) in
	x86_64 | amd64 | x64)
		if [ "$1" = darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
			echo arm64
		else
			echo amd64
		fi
		;;
	aarch64 | arm64) echo arm64 ;;
	*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

# download <url> <file>. Only the default GitHub URL is forced to https;
# ADO_INSTALL_BASE_URL is a test-only override that may be plain http.
download() {
	if command -v curl >/dev/null 2>&1; then
		if [ "$https_only" = 1 ]; then
			curl --proto '=https' --tlsv1.2 -fsSL -o "$2" "$1"
		else
			curl -fsSL -o "$2" "$1"
		fi
	elif command -v wget >/dev/null 2>&1; then
		if [ "$https_only" = 1 ] && wget_has_https_only; then
			wget --https-only --secure-protocol=TLSv1_2 -q -O "$2" "$1"
		else
			wget -q -O "$2" "$1"
		fi
	else
		die "curl or wget is required"
	fi
}

# BusyBox wget (Alpine and other minimal images) has neither --https-only nor
# --secure-protocol and fails on them; the default URL is https there anyway.
wget_has_https_only() {
	case $(wget --help 2>&1 || true) in
	*--https-only*) return 0 ;;
	esac
	return 1
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		die "sha256sum, shasum or openssl is required to verify the download"
	fi
}

# checksum_for <checksums.txt> <file name>
checksum_for() {
	awk -v f="$2" '{n = $2; sub(/^\*/, "", n)} n == f {print $1; exit}' "$1"
}

# latest_asset <checksums.txt> <suffix>: the ado_<version><suffix> name.
latest_asset() {
	awk -v s="$2" '{n = $2; sub(/^\*/, "", n)}
		substr(n, 1, 4) == "ado_" && length(n) > length(s) + 4 && substr(n, length(n) - length(s) + 1) == s {print n; exit}' "$1"
}

main() {
	version=
	bin_dir=
	while [ $# -gt 0 ]; do
		case $1 in
		--version | --bin-dir)
			[ $# -ge 2 ] || die "$1 needs a value"
			case $1 in --version) version=$2 ;; *) bin_dir=$2 ;; esac
			shift 2
			;;
		--version=*) version=${1#*=} && shift ;;
		--bin-dir=*) bin_dir=${1#*=} && shift ;;
		-h | --help)
			usage
			return 0
			;;
		*) die "unknown option: $1 (see --help)" ;;
		esac
	done

	os=$(detect_os)
	arch=$(detect_arch "$os")
	suffix="_${os}_${arch}.tar.gz"
	[ -n "$bin_dir" ] || bin_dir="${HOME:?HOME is not set}/.local/bin"

	if [ -n "${ADO_INSTALL_BASE_URL:-}" ]; then
		base=$ADO_INSTALL_BASE_URL
		https_only=0
	else
		base="https://github.com/$repo/releases"
		https_only=1
	fi

	tmp=$(mktemp -d "${TMPDIR:-/tmp}/ado-install.XXXXXX")
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 130' INT TERM HUP

	if [ -n "$version" ]; then
		version=${version#v}
		case $version in
		'' | *[!0-9A-Za-z.-]*) die "invalid version: $version" ;;
		esac
		download "$base/download/v$version/checksums.txt" "$tmp/checksums.txt" ||
			die "cannot download checksums.txt for v$version (does the release exist?)"
	else
		download "$base/latest/download/checksums.txt" "$tmp/checksums.txt" ||
			die "cannot download checksums.txt of the latest release"
		asset=$(latest_asset "$tmp/checksums.txt" "$suffix")
		[ -n "$asset" ] || die "latest release has no ${os}/${arch} archive"
		version=${asset#ado_}
		version=${version%"$suffix"}
	fi
	tag="v$version"
	asset="ado_${version}${suffix}"

	if command -v cosign >/dev/null 2>&1; then
		say "verifying checksums.txt with cosign"
		download "$base/download/$tag/checksums.txt.sigstore.json" "$tmp/checksums.txt.sigstore.json" ||
			die "cannot download checksums.txt.sigstore.json"
		cosign verify-blob --bundle "$tmp/checksums.txt.sigstore.json" \
			--certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$tag" \
			--certificate-oidc-issuer https://token.actions.githubusercontent.com \
			"$tmp/checksums.txt" >&2 ||
			die "cosign verification of checksums.txt failed for $tag; nothing was installed. Update cosign and retry; if it still fails, the download may have been tampered with: report it at https://github.com/$repo/issues"
	fi

	want=$(checksum_for "$tmp/checksums.txt" "$asset")
	[ -n "$want" ] || die "$asset is not listed in checksums.txt"
	say "downloading $asset"
	download "$base/download/$tag/$asset" "$tmp/$asset" || die "cannot download $asset"
	got=$(sha256 "$tmp/$asset")
	[ "$got" = "$want" ] || die "checksum mismatch for $asset: got $got, want $want"

	mkdir -p "$tmp/x"
	tar -xzf "$tmp/$asset" -C "$tmp/x" ado
	mkdir -p "$bin_dir"
	# Copy next to the target, then rename: replacing a running binary is safe.
	cp "$tmp/x/ado" "$bin_dir/.ado.new"
	chmod 755 "$bin_dir/.ado.new"
	mv -f "$bin_dir/.ado.new" "$bin_dir/ado"
	say "installed ado $version to $bin_dir/ado"

	case ":$PATH:" in
	*":$bin_dir:"*) ;;
	*) say "$bin_dir is not on your PATH. Add it, e.g. in ~/.profile:
  export PATH=\"$bin_dir:\$PATH\"" ;;
	esac
}

main "$@"
