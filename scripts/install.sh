#!/bin/sh
# Install the openheron binary that GitHub published as a release.
#
#   curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh
#
# What it does, in order: detect the platform, resolve the version, download the
# published archive, **verify its sha256 against the release's checksums.txt**,
# unpack it, install it, and check that the installed binary reports the version
# that was asked for.
#
# ★ The verification is not optional. A download that does not match the
#   checksum is not installed — that is the whole reason the release ships a
#   checksums file.
#
# Environment:
#   OPENHERON_VERSION   the tag to install (default: the latest release)
#   OPENHERON_REPO      owner/repo to install from (default: ouranoshong/openheron)
#   OPENHERON_BASE_URL  the GitHub base URL (default: https://github.com)
#   OPENHERON_TOKEN     a token for private repos or draft releases
#   OPENHERON_ASSET     an archive already on disk: skip the download, verify it
#   OPENHERON_CHECKSUMS the checksums file to verify against (default: next to the asset)
#   INSTALL_DIR          where to install (default: $HOME/.local/bin)

set -eu

REPO="${OPENHERON_REPO:-ouranoshong/openheron}"
BASE_URL="${OPENHERON_BASE_URL:-https://github.com}"
# ★ The API lives on another host than the web: `https://api.github.com`, not
#   `https://github.com/api`. Asking the web host for the API is a 404, and a
#   404 here means "no release" — which is how a working installer turns into
#   a broken one without a single line of the release being wrong.
API_URL="${OPENHERON_API_URL:-https://api.github.com}"
VERSION="${OPENHERON_VERSION:-}"
TOKEN="${OPENHERON_TOKEN:-}"
ASSET="${OPENHERON_ASSET:-}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

die() {
	printf 'openheron: %s\n' "$*" >&2
	exit 1
}

note() {
	printf 'openheron: %s\n' "$*"
}

# fetch <url> [out]
#
# ★ curl and wget are both optional: a minimal container often has one or
#   neither, and the installer should say which one it needs instead of
#   failing with "command not found".
fetch() {
	url="$1"
	out="${2:-}"
	if command -v curl >/dev/null 2>&1; then
		if [ -n "$TOKEN" ]; then
			if [ -n "$out" ]; then
				curl -fsSL -H "Authorization: Bearer $TOKEN" -o "$out" "$url"
			else
				curl -fsSL -H "Authorization: Bearer $TOKEN" "$url"
			fi
		else
			if [ -n "$out" ]; then
				curl -fsSL -o "$out" "$url"
			else
				curl -fsSL "$url"
			fi
		fi
	elif command -v wget >/dev/null 2>&1; then
		if [ -n "$TOKEN" ]; then
			if [ -n "$out" ]; then
				wget -q --header="Authorization: Bearer $TOKEN" -O "$out" "$url"
			else
				wget -q --header="Authorization: Bearer $TOKEN" -O - "$url"
			fi
		else
			if [ -n "$out" ]; then
				wget -q -O "$out" "$url"
			else
				wget -q -O - "$url"
			fi
		fi
	else
		die "neither curl nor wget is available — install one of them and retry"
	fi
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		die "no sha256 tool found (install coreutils or perl)"
	fi
}

# ── 1. the platform ──────────────────────────────────────────────────────────
#
# ★ The asset names come from the Taskfile (PLATFORMS and the naming rule), so
#   this script and `task release` must not drift apart.
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux) os=linux ;;
darwin) os=darwin ;;
*) die "unsupported operating system: $(uname -s) — Windows users should run install.ps1" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture: $arch" ;;
esac

# ── 2. the version ───────────────────────────────────────────────────────────
if [ -z "$VERSION" ]; then
	if [ -n "$ASSET" ]; then
		# ★ An archive already on disk carries its version in its name, so
		#   resolving "latest" is not just unnecessary — it is a network call
		#   this installation should not need.
		base=$(basename "$ASSET")
		VERSION=$(printf '%s' "$base" | sed -n 's/^openheron_\([^_]*\)_.*$/\1/p')
		[ -n "$VERSION" ] ||
			die "could not read the version from the asset name ($base) — pass OPENHERON_VERSION=<tag>"
	else
		note "resolving the latest release of $REPO"
		json=$(fetch "$API_URL/repos/$REPO/releases/latest") ||
			die "could not read the latest release of $REPO — pass OPENHERON_VERSION=<tag> instead"
		VERSION=$(printf '%s' "$json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
		[ -n "$VERSION" ] || die "could not resolve the latest release of $REPO — pass OPENHERON_VERSION=<tag> instead"
	fi
fi

name="openheron_${VERSION}_${os}_${arch}"
archive="$name.tar.gz"

# ── 3. the files ─────────────────────────────────────────────────────────────
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

if [ -n "$ASSET" ]; then
	[ -f "$ASSET" ] || die "OPENHERON_ASSET points at a file that does not exist: $ASSET"
	cp "$ASSET" "$tmp/$archive"
	checksums="${OPENHERON_CHECKSUMS:-$(dirname "$ASSET")/checksums.txt}"
	[ -f "$checksums" ] || die "no checksums file found next to the asset: $checksums"
	cp "$checksums" "$tmp/checksums.txt"
else
	note "downloading $archive (version $VERSION)"
	fetch "$BASE_URL/$REPO/releases/download/$VERSION/$archive" "$tmp/$archive" ||
		die "could not download $archive — the release page is $BASE_URL/$REPO/releases/tag/$VERSION"
	fetch "$BASE_URL/$REPO/releases/download/$VERSION/checksums.txt" "$tmp/checksums.txt" ||
		die "could not download checksums.txt for $VERSION"
fi

# ── 4. verify ────────────────────────────────────────────────────────────────
expected=$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")
[ -n "$expected" ] || die "checksums.txt has no entry for $archive"
actual=$(sha256_of "$tmp/$archive")
if [ "$expected" != "$actual" ]; then
	die "checksum mismatch for $archive — expected $expected, got $actual. Nothing was installed."
fi
note "checksum ok"

# ── 5. unpack ────────────────────────────────────────────────────────────────
tar -xzf "$tmp/$archive" -C "$tmp"
binary="$tmp/$name/openheron"
[ -f "$binary" ] || die "the archive does not contain $name/openheron"

# ── 6. install ───────────────────────────────────────────────────────────────
mkdir -p "$INSTALL_DIR"
if command -v install >/dev/null 2>&1; then
	install -m 0755 "$binary" "$INSTALL_DIR/openheron"
else
	cp "$binary" "$INSTALL_DIR/openheron"
	chmod 0755 "$INSTALL_DIR/openheron"
fi

# ── 7. check ─────────────────────────────────────────────────────────────────
got=$("$INSTALL_DIR/openheron" version 2>/dev/null || true)
[ "$got" = "$VERSION" ] ||
	die "the installed binary reports '$got' but $VERSION was expected — something is wrong with the archive"

case ":$PATH:" in
*":$INSTALL_DIR:"*) ;;
*) note "hint: $INSTALL_DIR is not on PATH — export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac

note "installed $INSTALL_DIR/openheron (version $VERSION)"
