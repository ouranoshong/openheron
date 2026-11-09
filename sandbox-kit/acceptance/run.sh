#!/usr/bin/env bash
#
# The openheron Kit acceptance run.
#
# ★ What this script is for: it is the repeatable form of the measurement in
#   docs/sandbox-kit-design.md §6. Every claim that section makes has a check
#   here, and a check that fails fails the script — the run is not a report
#   that says "looks fine".
#
# ★ What it does NOT do: it does not build the Kit. Building is a separate,
#   slower step (a clean build pulls a 100 MB base image), and the Kit under
#   test is whatever `make kit` last produced. Pass --build to do both.
#
# Usage:
#   sandbox-kit/acceptance/run.sh [--build] [--out FILE] [--keep]
#
# Environment:
#   SANDBOX_NAME   the sandbox's name       (default: openheron-accept)
#   WORKSPACE      the host workspace       (default: ~/.openheron-accept-ws)
#   MIXINS         the mixin directories, space separated
#                  (default: sandbox-kit/openheron-deepseek)
#   KIT_DIR        where the Kit sources live (default: sandbox-kit)
#
# Exit code: 0 when every check passed, 1 otherwise.

set -uo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
KIT_DIR=${KIT_DIR:-$REPO_ROOT/sandbox-kit}
MIXINS=${MIXINS:-$KIT_DIR/openheron-deepseek}
SANDBOX_NAME=${SANDBOX_NAME:-openheron-accept}
WORKSPACE=${WORKSPACE:-$HOME/.openheron-accept-ws}
OUT=""
DO_BUILD=0
KEEP=0

while [ $# -gt 0 ]; do
	case "$1" in
	--build) DO_BUILD=1 ;;
	--out) OUT=$2; shift ;;
	--keep) KEEP=1 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
	shift
done

PASS=0
FAIL=0
FAILED_CHECKS=()

# Every line the script prints is also the record, so --out captures the run
# rather than a summary of it.
emit() {
	printf '%s\n' "$*"
	[ -n "$OUT" ] && printf '%s\n' "$*" >>"$OUT"
}

say() { emit "$*"; }
head2() { emit ""; emit "=== $* ==="; }

# check <name> <expected> <actual>
check() {
	local name=$1 expected=$2 actual=$3
	if [ "$expected" = "$actual" ]; then
		PASS=$((PASS + 1))
		emit "  PASS  $(printf '%-52s' "$name") $actual"
	else
		FAIL=$((FAIL + 1))
		FAILED_CHECKS+=("$name")
		emit "  FAIL  $(printf '%-52s' "$name") expected $expected, got $actual"
	fi
}

# check_contains <name> <needle> <haystack>
check_contains() {
	local name=$1 needle=$2 haystack=$3
	case "$haystack" in
	*"$needle"*)
		PASS=$((PASS + 1))
		emit "  PASS  $(printf '%-52s' "$name") contains $needle"
		;;
	*)
		FAIL=$((FAIL + 1))
		FAILED_CHECKS+=("$name")
		emit "  FAIL  $(printf '%-52s' "$name") does not contain $needle"
		;;
	esac
}

http_code() { curl -s -m "${2:-8}" -o /dev/null -w '%{http_code}' "$1"; }

published_port() {
	sbx ports "$SANDBOX_NAME" 2>/dev/null | awk '$1 == "127.0.0.1" { print $2; exit }'
}

# ── Preflight ─────────────────────────────────────────────────────────────

if [ -n "$OUT" ]; then
	mkdir -p "$(dirname "$OUT")"
	{
		echo "openheron Kit acceptance"
		echo "date:    $(date -Is)"
		echo "sbx:     $(sbx version 2>/dev/null | head -1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
		echo "sandbox: $SANDBOX_NAME"
		echo "kits:    $KIT_DIR/openheron $MIXINS"
		echo "workspace: $WORKSPACE"
		echo ""
	} >"$OUT"
fi

head2 "Preflight"

SBX_VERSION=$(sbx version 2>/dev/null | head -1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1)
check "sbx is installed" "yes" "$([ -n "$SBX_VERSION" ] && echo yes || echo no)"
say "        sbx version: $SBX_VERSION"

# 0.42.0 is the floor: it fixed CVE-2026-77179 and CVE-2026-79994, and the
# mitigation Docker documents is a mode openheron cannot use.
SBX_OK=$(printf '%s\n%s\n' "$SBX_VERSION" "0.42.0" | sort -V | head -1)
check "sbx is at least 0.42.0" "0.42.0" "$SBX_OK"

check "docker is available" "yes" \
	"$(docker version --format '{{.Server.Version}}' >/dev/null 2>&1 && echo yes || echo no)"

check "the deepseek credential is bound on the host" "yes" \
	"$(grep -q 'api.deepseek.com' "$HOME/.config/sbx/credentials.yaml" 2>/dev/null && echo yes || echo no)"

# ── Build (optional) ──────────────────────────────────────────────────────

if [ "$DO_BUILD" = "1" ]; then
	head2 "Build"
	# A builder of its own means an empty build cache: the base image and the
	# release asset are fetched again, so the recipe is proven to work from
	# scratch rather than from whatever a previous round left behind.
	make -C "$REPO_ROOT" kit kit-mixin KIT_BUILDER=kit-acceptance \
		&& say "  built" || { say "  build failed"; exit 1; }
fi

# ── A clean sandbox ───────────────────────────────────────────────────────

head2 "Start"

sbx rm -f "$SANDBOX_NAME" >/dev/null 2>&1
rm -rf "$WORKSPACE"
mkdir -p "$WORKSPACE"

kit_args=()
for m in $MIXINS; do kit_args+=(--kit "$m"); done

# ★ The agent session is what launches the workload's entrypoint. A detached
#   run creates the sandbox and runs the startup hooks, but nothing starts the
#   server — so the session is opened here, and closed again further down to
#   show that long-running@1 keeps the server up on its own.
sbx run "$KIT_DIR/openheron" "${kit_args[@]}" --name "$SANDBOX_NAME" "$WORKSPACE" \
	>"/tmp/${SANDBOX_NAME}.session.log" 2>&1 &
SESSION_PID=$!

PORT=""
for _ in $(seq 1 60); do
	PORT=$(published_port)
	[ -n "$PORT" ] && break
	sleep 2
done
check "the runtime published the port itself" "yes" \
	"$([ -n "$PORT" ] && echo yes || echo no)"
say "        published: 127.0.0.1:$PORT -> 8080/tcp"

# The port is published at start time; the server needs a moment more.
HEALTH=""
for _ in $(seq 1 30); do
	HEALTH=$(http_code "http://127.0.0.1:${PORT:-0}/v2/health")
	[ "$HEALTH" = "200" ] && break
	sleep 2
done
check "/v2/health answers 200 from the host" "200" "$HEALTH"

# ── The configuration lands on the mount ───────────────────────────────────

head2 "Configuration on the mount"

for d in agents llm mcps server state; do
	check "the mount holds .openheron/$d" "yes" \
		"$([ -d "$WORKSPACE/.openheron/$d" ] && echo yes || echo no)"
done

# ── The credential ────────────────────────────────────────────────────────

head2 "Credential"

# The exec container shares the sandbox's network, so this is the workload's
# own environment, not the exec container's.
ENV_DUMP=$(timeout 180 sbx exec "$SANDBOX_NAME" -- sh -c '
	echo "KEY=$DEEPSEEK_API_KEY"
	echo "MODE=$SBX_CRED_DEEPSEEK_MODE"
	echo "WS=$WORKSPACE_DIR"
' 2>/dev/null)

check_contains "the container holds the sentinel, not the key" \
	"KEY=proxy-managed" "$ENV_DUMP"
check_contains "WORKSPACE_DIR points at the host workspace" \
	"WS=$WORKSPACE" "$ENV_DUMP"

# The real key must not have been written anywhere the user can read.
if [ -n "${DEEPSEEK_API_KEY:-}" ]; then
	LEAK=$(grep -rl -- "$DEEPSEEK_API_KEY" "$WORKSPACE/.openheron" 2>/dev/null | head -1)
	check "the real key is nowhere in the configuration root" "none" "${LEAK:-none}"
fi

# ── A model call, and the stream's shape ──────────────────────────────────

head2 "Model call"

PROBE=$(mktemp /tmp/sse-probe.XXXXXX.py)
cat >"$PROBE" <<'PYEOF'
import json, sys, time, urllib.request

base, query = sys.argv[1], sys.argv[2]

def post(path, payload):
    req = urllib.request.Request(base + path, data=json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=300) as r:
        return json.load(r)

sess = post("/v2/sessions", {})
tid = sess.get("threadId") or sess.get("id")
req = urllib.request.Request(base + "/v2/run",
                             data=json.dumps({"threadId": tid, "query": query}).encode(),
                             headers={"Content-Type": "application/json",
                                      "Accept": "text/event-stream"})
t0 = time.monotonic()
frames, text = [], []
with urllib.request.urlopen(req, timeout=300) as r:
    for raw in r:
        line = raw.decode("utf-8", "replace").strip()
        if not line.startswith("data:"):
            continue
        dt = (time.monotonic() - t0) * 1000
        frames.append(dt)
        try:
            obj = json.loads(line[5:])
        except ValueError:
            continue
        if obj.get("type") == "TEXT_MESSAGE_CONTENT":
            text.append(obj.get("delta") or "")
print("FRAMES=%d" % len(frames))
print("FIRST_MS=%.0f" % (frames[0] if frames else -1))
print("LAST_MS=%.0f" % (frames[-1] if frames else -1))
print("SPAN_MS=%.0f" % ((frames[-1] - frames[0]) if len(frames) > 1 else 0))
print("TEXT=%s" % ("".join(text).replace("\n", " ")[:200]))
PYEOF

PROBE_OUT=$(python3 "$PROBE" "http://127.0.0.1:${PORT}" \
	"用一句话说明什么是 Docker Sandbox Kit。" 2>&1)
rm -f "$PROBE"

printf '%s\n' "$PROBE_OUT" | sed 's/^/        /' | while IFS= read -r line; do emit "$line"; done

FRAMES=$(printf '%s' "$PROBE_OUT" | sed -n 's/^FRAMES=//p')
FIRST=$(printf '%s' "$PROBE_OUT" | sed -n 's/^FIRST_MS=//p')
LAST=$(printf '%s' "$PROBE_OUT" | sed -n 's/^LAST_MS=//p')
SPAN=$(printf '%s' "$PROBE_OUT" | sed -n 's/^SPAN_MS=//p')
TEXT=$(printf '%s' "$PROBE_OUT" | sed -n 's/^TEXT=//p')

check "the model answered" "yes" \
	"$([ "${#TEXT}" -gt 5 ] && echo yes || echo no)"
check "the stream carried more than one frame" "yes" \
	"$([ "${FRAMES:-0}" -gt 1 ] && echo yes || echo no)"
# A span of at least half a second means the frames arrived as the answer was
# generated. A short span with a late last frame means the forwarder buffered
# the whole body and released it at the end.
check "the stream was not buffered (span >= 500 ms)" "yes" \
	"$([ "${SPAN:-0}" -ge 500 ] && echo yes || echo no)"

# ── The server outlives the client ────────────────────────────────────────

head2 "The workload outlives its client"

kill "$SESSION_PID" 2>/dev/null
wait "$SESSION_PID" 2>/dev/null
sleep 5

AFTER=$(http_code "http://127.0.0.1:${PORT}/v2/health")
check "the server still answers after the client is gone" "200" "$AFTER"

# ── The port, from every vantage point ────────────────────────────────────

head2 "Port connectivity"

check "host, IPv4 loopback" "200" "$(http_code "http://127.0.0.1:${PORT}/v2/health")"
check "host, IPv6 loopback" "200" "$(http_code "http://[::1]:${PORT}/v2/health")"
check "the daemon's own network namespace" "200" \
	"$(docker exec sbx-daemon curl -s -m 8 -o /dev/null -w '%{http_code}' \
		"http://127.0.0.1:${PORT}/v2/health" 2>/dev/null)"

# ★ The host's bridge gateway is a different address from the sandbox's: the
#   daemon resolves the sandbox's service address inside its own namespace,
#   while the host's 172.17.0.1 is the Docker bridge. Reaching it from the
#   host is neither expected to work nor needed — the published loopback port
#   is the supported path, and it is what the checks above use.
GATEWAY=$(http_code "http://172.17.0.1:${PORT}/v2/health")
say "        host bridge gateway 172.17.0.1:${PORT} -> $GATEWAY (not a supported path)"

# ── Result ────────────────────────────────────────────────────────────────

head2 "Result"
say "  passed: $PASS"
say "  failed: $FAIL"
if [ "$FAIL" -ne 0 ]; then
	for c in "${FAILED_CHECKS[@]}"; do say "    - $c"; done
fi
say ""
say "  published port: 127.0.0.1:$PORT -> 8080/tcp"
say "  frame span:     ${SPAN:-0} ms (first ${FIRST:-0} ms, last ${LAST:-0} ms)"

if [ "$KEEP" = "0" ]; then
	sbx rm -f "$SANDBOX_NAME" >/dev/null 2>&1
	rm -rf "$WORKSPACE"
	say "  sandbox removed (pass --keep to leave it up)"
fi

[ "$FAIL" -eq 0 ] || exit 1
exit 0
