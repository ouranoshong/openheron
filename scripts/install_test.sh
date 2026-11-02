#!/bin/sh
# Test the release installer against a local server that mimics GitHub.
#
# ★ The installer downloads a binary and runs it. Testing that for real means
#   either a real release or a machine that can run the result — so this
#   harness serves a fake release from a local HTTP server and points the
#   installer at it with OPENHERON_BASE_URL.
#
# What it checks:
#   1. the latest release is resolved and the right archive is downloaded
#   2. the archive is verified, installed, and the installed binary reports
#      the version that was asked for
#   3. a pinned version is honoured
#   4. an archive already on disk is verified and installed without a download
#   5. **a corrupted checksum installs nothing and fails**
#   6. an unsupported platform is refused
#
# Environment: none — everything is temporary.

set -eu

here=$(cd "$(dirname "$0")" && pwd)
installer="$here/install.sh"
repo="ouranoshong/openheron"
version="v1.2.3"

work=$(mktemp -d)
server_pid=""
cleanup() {
	[ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

fail() {
	printf 'install_test: %s\n' "$*" >&2
	exit 1
}

pass() {
	printf 'install_test: ok — %s\n' "$*"
}

command -v python3 >/dev/null 2>&1 || fail "python3 is needed to serve the fake release"

# ── the fake release ─────────────────────────────────────────────────────────
#
# ★ The layout under the server root mirrors GitHub's: the API path and the
#   /<owner>/<repo>/releases/download/<tag>/<asset> path.
root="$work/server"
mkdir -p "$root/api/repos/$repo/releases"
mkdir -p "$root/$repo/releases/download/$version"

binary_name="openheron_${version}_linux_amd64"
binary_dir="$work/build/$binary_name"
mkdir -p "$binary_dir"
cat > "$binary_dir/openheron" <<EOF
#!/bin/sh
if [ "\${1:-}" = "version" ]; then echo "$version"; exit 0; fi
echo "fake openheron"
EOF
chmod 0755 "$binary_dir/openheron"

tar -czf "$root/$repo/releases/download/$version/$binary_name.tar.gz" \
	-C "$work/build" "$binary_name"

cat > "$root/api/repos/$repo/releases/latest" <<EOF
{"tag_name":"$version","name":"$version","assets":[]}
EOF

(
	cd "$root/$repo/releases/download/$version"
	sha256sum "$binary_name.tar.gz" > checksums.txt
) || (
	cd "$root/$repo/releases/download/$version"
	shasum -a 256 "$binary_name.tar.gz" > checksums.txt
)

# ── the server ───────────────────────────────────────────────────────────────
port=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)
python3 -m http.server "$port" --directory "$root" >"$work/server.log" 2>&1 &
server_pid=$!

# wait for it to answer
i=0
while [ "$i" -lt 50 ]; do
	if curl -fsS -o /dev/null "http://127.0.0.1:$port/api/repos/$repo/releases/latest" 2>/dev/null; then
		break
	fi
	i=$((i + 1))
	sleep 0.2
done
[ "$i" -lt 50 ] || fail "the fake server did not come up: $(cat "$work/server.log")"

base="http://127.0.0.1:$port"

# ── 1 + 2. resolve the latest release, verify, install ───────────────────────
install_dir="$work/install"
env OPENHERON_BASE_URL="$base" OPENHERON_REPO="$repo" INSTALL_DIR="$install_dir" \
	sh "$installer" >"$work/run1.log" 2>&1 ||
	{ cat "$work/run1.log"; fail "the installer failed on the happy path"; }

[ -x "$install_dir/openheron" ] || fail "the binary was not installed"
got=$("$install_dir/openheron" version)
[ "$got" = "$version" ] || fail "the installed binary reports '$got', expected '$version'"
pass "the latest release is resolved, verified and installed"

# ── 3. a pinned version ──────────────────────────────────────────────────────
install_dir="$work/install-pinned"
env OPENHERON_BASE_URL="$base" OPENHERON_REPO="$repo" OPENHERON_VERSION="$version" \
	INSTALL_DIR="$install_dir" sh "$installer" >"$work/run2.log" 2>&1 ||
	{ cat "$work/run2.log"; fail "the installer failed with a pinned version"; }
[ -x "$install_dir/openheron" ] || fail "the pinned install did not happen"
pass "a pinned version is honoured"

# ── 4. an archive that is already on disk ────────────────────────────────────
install_dir="$work/install-local"
env OPENHERON_BASE_URL="$base" OPENHERON_REPO="$repo" \
	OPENHERON_ASSET="$root/$repo/releases/download/$version/$binary_name.tar.gz" \
	OPENHERON_CHECKSUMS="$root/$repo/releases/download/$version/checksums.txt" \
	INSTALL_DIR="$install_dir" sh "$installer" >"$work/run3.log" 2>&1 ||
	{ cat "$work/run3.log"; fail "the installer failed with a local asset"; }
[ -x "$install_dir/openheron" ] || fail "the local-asset install did not happen"
pass "an archive on disk is verified and installed without a download"

# ── 5. a corrupted checksum installs nothing ──────────────────────────────────
#
# ★ The checksums file stays the **original** one: recomputing it here would
#   make the archive match again, and the installer would be right to accept
#   it — the test would then be measuring tar's opinion of a broken file
#   instead of the checksum gate.
bad_dir="$work/bad"
mkdir -p "$bad_dir"
cp "$root/$repo/releases/download/$version/$binary_name.tar.gz" "$bad_dir/"
cp "$root/$repo/releases/download/$version/checksums.txt" "$bad_dir/"
printf 'corrupted\n' >> "$bad_dir/$binary_name.tar.gz"

install_dir="$work/install-bad"
if env OPENHERON_BASE_URL="$base" OPENHERON_REPO="$repo" \
	OPENHERON_ASSET="$bad_dir/$binary_name.tar.gz" \
	OPENHERON_CHECKSUMS="$bad_dir/checksums.txt" \
	INSTALL_DIR="$install_dir" sh "$installer" >"$work/run4.log" 2>&1; then
	fail "a corrupted archive was accepted — that is the one failure that must never happen"
fi
[ ! -e "$install_dir/openheron" ] || fail "a corrupted archive was installed anyway"
grep -q "checksum mismatch" "$work/run4.log" || fail "the failure did not say what went wrong"
pass "a corrupted archive is refused and nothing is installed"

# ── 6. an unsupported platform is refused ────────────────────────────────────
fakebin="$work/fakebin"
mkdir -p "$fakebin"
cat > "$fakebin/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
-s) echo "Plan9" ;;
-m) echo "x86_64" ;;
*) echo "Plan9" ;;
esac
EOF
chmod 0755 "$fakebin/uname"
if env PATH="$fakebin:$PATH" OPENHERON_BASE_URL="$base" OPENHERON_REPO="$repo" \
	INSTALL_DIR="$work/install-os" sh "$installer" >"$work/run5.log" 2>&1; then
	fail "an unsupported operating system was accepted"
fi
grep -q "unsupported operating system" "$work/run5.log" || fail "the refusal did not say why"
pass "an unsupported platform is refused"

printf 'install_test: all checks passed\n'
