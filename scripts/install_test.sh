#!/bin/sh
# Tests for scripts/install.sh. Serves fixture releases with python3 http.server
# on an ephemeral 127.0.0.1 port and runs install.sh under every available
# POSIX shell (dash, bash --posix, sh) with a sandboxed PATH: stubbed
# uname/sysctl/cosign plus links to only the tools the installer may use.
# Usage: sh scripts/install_test.sh
set -eu

here=$(cd "$(dirname "$0")" && pwd)
script="$here/install.sh"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/ado-install-test.XXXXXX")
server_pid=
cleanup() {
	if [ -n "$server_pid" ]; then kill "$server_pid" 2>/dev/null || true; fi
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

fails=0
passes=0

# --- fixtures -----------------------------------------------------------------

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

# release <version> [corrupt]: archives for linux/darwin x amd64/arm64 plus
# checksums.txt under srv/download/v<version>/. The fixture binary prints its
# version and target so tests can tell which archive was installed.
release() {
	d="$tmp/srv/download/v$1"
	mkdir -p "$d"
	for t in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
		mkdir -p "$tmp/build"
		printf '#!/bin/sh\necho "ado %s %s"\n' "$1" "$t" >"$tmp/build/ado"
		chmod 755 "$tmp/build/ado"
		echo license >"$tmp/build/LICENSE"
		(cd "$tmp/build" && tar -czf "$d/ado_$1_$t.tar.gz" ado LICENSE)
		rm -rf "$tmp/build"
		sum=$(sha256 "$d/ado_$1_$t.tar.gz")
		if [ "${2:-}" = corrupt ]; then
			sum=0000000000000000000000000000000000000000000000000000000000000000
		fi
		# Packages and SBOMs share the name prefix: the installer must pick the tarball.
		printf '%s  %s\n' "$sum" "ado_$1_$t.deb" "$sum" "ado_$1_$t.tar.gz" "$sum" "ado_$1_$t.tar.gz.sbom.json" >>"$d/checksums.txt"
	done
	echo '{}' >"$d/checksums.txt.sigstore.json"
}

release 1.2.3
release 1.0.0
release 9.9.9 corrupt
mkdir -p "$tmp/srv/latest/download"
cp "$tmp/srv/download/v1.2.3/checksums.txt" "$tmp/srv/latest/download/checksums.txt"

python3 -u -m http.server --bind 127.0.0.1 --directory "$tmp/srv" 0 >"$tmp/http.log" 2>&1 &
server_pid=$!
port=
i=0
while [ -z "$port" ] && [ "$i" -lt 100 ]; do
	port=$(sed -n 's/.*Serving HTTP on 127\.0\.0\.1 port \([0-9]*\).*/\1/p' "$tmp/http.log")
	[ -n "$port" ] || sleep 0.1
	i=$((i + 1))
done
if [ -z "$port" ]; then
	echo "http.server did not start:" >&2
	cat "$tmp/http.log" >&2
	exit 1
fi
base="http://127.0.0.1:$port"

# sandbox [tool-to-omit...]: a fresh case dir ($case_dir) with home, tmp and a
# bin dir ($sb) holding stubs and links to the real tools minus the omitted ones.
sandbox() {
	case_dir=$(mktemp -d "$tmp/case.XXXXXX")
	sb="$case_dir/bin"
	mkdir -p "$sb" "$case_dir/home" "$case_dir/tmp"
	for tool in mktemp rm mkdir tar gzip cp mv chmod awk sed cat curl wget sha256sum shasum openssl; do
		case " $* " in *" $tool "*) continue ;; esac
		p=$(command -v "$tool" 2>/dev/null) || continue
		ln -s "$p" "$sb/$tool"
	done
	cat >"$sb/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
-s) echo "$STUB_UNAME_S" ;;
-m) echo "$STUB_UNAME_M" ;;
*) echo "$STUB_UNAME_S" ;;
esac
EOF
	cat >"$sb/sysctl" <<'EOF'
#!/bin/sh
[ -n "${STUB_SYSCTL_ARM64:-}" ] || exit 1
echo "$STUB_SYSCTL_ARM64"
EOF
	chmod 755 "$sb/uname" "$sb/sysctl"
}

add_cosign() {
	cat >"$1/cosign" <<'EOF'
#!/bin/sh
echo "$@" >"$STUB_COSIGN_LOG"
exit "${STUB_COSIGN_EXIT:-0}"
EOF
	chmod 755 "$1/cosign"
}

# run <shell> <sandbox> [args...]: runs install.sh, output in $out, status in $rc.
# Each case gets its own HOME and TMPDIR so leftovers are detectable.
run() {
	sh_cmd=$1
	sb=$2
	shift 2
	rc=0
	# shellcheck disable=SC2086 # $sh_cmd may be "bash --posix"
	out=$(env -i PATH="$sb" HOME="$case_dir/home" TMPDIR="$case_dir/tmp" \
		ADO_INSTALL_BASE_URL="$base" \
		STUB_UNAME_S="${STUB_UNAME_S:-Linux}" STUB_UNAME_M="${STUB_UNAME_M:-x86_64}" \
		STUB_SYSCTL_ARM64="${STUB_SYSCTL_ARM64:-}" STUB_COSIGN_EXIT="${STUB_COSIGN_EXIT:-0}" \
		STUB_COSIGN_LOG="$case_dir/cosign.log" \
		$sh_cmd "$script" "$@" 2>&1) || rc=$?
}

pass() { passes=$((passes + 1)); }
fail() {
	fails=$((fails + 1))
	echo "FAIL [$shell_name] $name: $1" >&2
	echo "$out" | sed 's/^/    /' >&2
}

expect_ok() { if [ "$rc" -eq 0 ]; then pass; else fail "exit $rc, want 0"; fi; }
expect_fail() { if [ "$rc" -ne 0 ]; then pass; else fail "exit 0, want non-zero"; fi; }
expect_out() {
	case "$out" in *"$1"*) pass ;; *) fail "output lacks: $1" ;; esac
}
# expect_bin <file> <want-output>
expect_bin() {
	if [ -x "$1" ] && [ "$("$1")" = "$2" ]; then pass; else fail "$1 missing or not '$2'"; fi
}
expect_no_file() { if [ ! -e "$1" ]; then pass; else fail "$1 exists"; fi; }
expect_clean_tmp() {
	if [ -z "$(ls -A "$case_dir/tmp")" ]; then pass; else fail "temp files left: $(ls -A "$case_dir/tmp")"; fi
}

# --- cases --------------------------------------------------------------------

shells=
command -v dash >/dev/null 2>&1 && shells="dash"
command -v bash >/dev/null 2>&1 && shells="$shells bash_posix"
shells="$shells sh"

for shell_name in $shells; do
	case $shell_name in
	dash) sh_cmd="$(command -v dash)" ;;
	bash_posix) sh_cmd="$(command -v bash) --posix" ;;
	sh) sh_cmd=/bin/sh ;;
	esac

	name="arch detection"
	for row in Linux:x86_64::linux_amd64 Linux:amd64::linux_amd64 Linux:aarch64::linux_arm64 \
		Linux:arm64::linux_arm64 Darwin:arm64::darwin_arm64 Darwin:x86_64::darwin_amd64 \
		Darwin:x86_64:0:darwin_amd64 Darwin:x86_64:1:darwin_arm64; do
		IFS=: read -r STUB_UNAME_S STUB_UNAME_M STUB_SYSCTL_ARM64 want <<EOF
$row
EOF
		name="arch $row"
		sandbox
		run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
		expect_ok
		expect_bin "$case_dir/out/ado" "ado 1.2.3 $want"
	done
	STUB_UNAME_S=Linux STUB_UNAME_M=x86_64 STUB_SYSCTL_ARM64='' STUB_COSIGN_EXIT=0

	name="latest into default bin dir with PATH hint"
	sandbox
	run "$sh_cmd" "$sb"
	expect_ok
	expect_bin "$case_dir/home/.local/bin/ado" "ado 1.2.3 linux_amd64"
	expect_out "$case_dir/home/.local/bin"
	expect_out "PATH"
	expect_clean_tmp

	name="--version with and without v, --opt=value form"
	sandbox
	run "$sh_cmd" "$sb" --version 1.0.0 --bin-dir "$case_dir/a"
	expect_ok
	expect_bin "$case_dir/a/ado" "ado 1.0.0 linux_amd64"
	run "$sh_cmd" "$sb" --version=v1.0.0 --bin-dir="$case_dir/b"
	expect_ok
	expect_bin "$case_dir/b/ado" "ado 1.0.0 linux_amd64"

	name="upgrade replaces an existing binary"
	sandbox
	run "$sh_cmd" "$sb" --version 1.0.0 --bin-dir "$case_dir/out"
	run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
	expect_ok
	expect_bin "$case_dir/out/ado" "ado 1.2.3 linux_amd64"

	name="piped: curl | sh -s -- --version"
	sandbox
	rc=0
	# shellcheck disable=SC2086
	out=$(env -i PATH="$sb" HOME="$case_dir/home" TMPDIR="$case_dir/tmp" ADO_INSTALL_BASE_URL="$base" \
		STUB_UNAME_S=Linux STUB_UNAME_M=x86_64 \
		$sh_cmd -s -- --version 1.0.0 <"$script" 2>&1) || rc=$?
	expect_ok
	expect_bin "$case_dir/home/.local/bin/ado" "ado 1.0.0 linux_amd64"

	name="checksum mismatch aborts and installs nothing"
	sandbox
	run "$sh_cmd" "$sb" --version 9.9.9 --bin-dir "$case_dir/out"
	expect_fail
	expect_out "checksum"
	expect_no_file "$case_dir/out/ado"
	expect_clean_tmp

	name="unknown version fails"
	sandbox
	run "$sh_cmd" "$sb" --version 4.5.6 --bin-dir "$case_dir/out"
	expect_fail
	expect_no_file "$case_dir/out/ado"

	name="invalid version rejected"
	sandbox
	run "$sh_cmd" "$sb" --version '../../x' --bin-dir "$case_dir/out"
	expect_fail
	expect_out "invalid version"

	name="wget fallback"
	sandbox curl
	if [ -e "$sb/wget" ]; then
		run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
		expect_ok
		expect_bin "$case_dir/out/ado" "ado 1.2.3 linux_amd64"
	fi

	name="no downloader"
	sandbox curl wget
	run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
	expect_fail
	expect_out "curl or wget"

	name="shasum fallback"
	sandbox sha256sum
	if [ -e "$sb/shasum" ]; then
		run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
		expect_ok
		expect_bin "$case_dir/out/ado" "ado 1.2.3 linux_amd64"
	fi

	name="openssl fallback"
	sandbox sha256sum shasum
	if [ -e "$sb/openssl" ]; then
		run "$sh_cmd" "$sb" --version 9.9.9 --bin-dir "$case_dir/out"
		expect_fail
		expect_out "checksum"
		run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
		expect_ok
		expect_bin "$case_dir/out/ado" "ado 1.2.3 linux_amd64"
	fi

	name="no sha256 tool"
	sandbox sha256sum shasum openssl
	run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
	expect_fail
	expect_no_file "$case_dir/out/ado"

	name="cosign verifies checksums.txt when installed"
	sandbox
	add_cosign "$sb"
	run "$sh_cmd" "$sb" --version 1.0.0 --bin-dir "$case_dir/out"
	expect_ok
	expect_bin "$case_dir/out/ado" "ado 1.0.0 linux_amd64"
	log=$(cat "$case_dir/cosign.log" 2>/dev/null || true)
	out=$log
	expect_out "verify-blob"
	expect_out "--bundle"
	expect_out "checksums.txt.sigstore.json"
	expect_out "--certificate-identity https://github.com/snake-in-prod/ado-gh/.github/workflows/release.yml@refs/tags/v1.0.0"
	expect_out "--certificate-oidc-issuer https://token.actions.githubusercontent.com"

	name="cosign verification failure aborts"
	sandbox
	add_cosign "$sb"
	STUB_COSIGN_EXIT=1
	run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
	STUB_COSIGN_EXIT=0
	expect_fail
	expect_out "cosign"
	expect_no_file "$case_dir/out/ado"
	expect_clean_tmp

	name="cosign absent is skipped"
	sandbox
	run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
	expect_ok
	expect_no_file "$case_dir/cosign.log"

	for os in MINGW64_NT-10.0-26100 MSYS_NT-10.0-26100 CYGWIN_NT-10.0-26100 Windows_NT; do
		name="windows shell $os points to install.ps1"
		sandbox
		STUB_UNAME_S=$os
		run "$sh_cmd" "$sb" --bin-dir "$case_dir/out"
		expect_fail
		expect_out "install.ps1"
		expect_no_file "$case_dir/out/ado"
	done
	STUB_UNAME_S=Linux

	name="unsupported OS"
	sandbox
	STUB_UNAME_S=FreeBSD
	run "$sh_cmd" "$sb"
	STUB_UNAME_S=Linux
	expect_fail
	expect_out "unsupported operating system: FreeBSD"

	name="unsupported arch"
	sandbox
	STUB_UNAME_M=i686
	run "$sh_cmd" "$sb"
	STUB_UNAME_M=x86_64
	expect_fail
	expect_out "unsupported architecture: i686"

	name="unknown option"
	sandbox
	run "$sh_cmd" "$sb" --bogus
	expect_fail
	expect_out "unknown option: --bogus"

	name="missing option value"
	sandbox
	run "$sh_cmd" "$sb" --version
	expect_fail

	name="--help"
	sandbox
	run "$sh_cmd" "$sb" --help
	expect_ok
	expect_out "--bin-dir"
done

echo "install_test: $passes passed, $fails failed (shells: $shells)"
[ "$fails" -eq 0 ]
