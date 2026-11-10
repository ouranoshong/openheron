#!/usr/bin/env bash
# Start a **test-only** openheron instance: its own config, its own session
# directory, its own port.
#
# The three-way isolation from the development environment (ports / config /
# session data) is all defined in .env.test at the repository root.
# Usage:
#   scripts/test-backend.sh            start in the foreground
#   scripts/test-backend.sh --reset    clear the test directories, then start
#   scripts/test-backend.sh --sync     re-copy the development-side agent definitions, then start
#   scripts/test-backend.sh --prepare-only  build the directories only, do not start
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

set -a
# shellcheck disable=SC1091
. ./.env.test
set +a

# ★ Iterate over **all** arguments instead of looking at $1 alone — `--reset
#   --prepare-only` is a common combination, and reading only the first one
#   silently drops the rest (which once made `--prepare-only` start the server
#   anyway).
SYNC=0
PREPARE_ONLY=0
for arg in "$@"; do
  case "$arg" in
    --reset)        rm -rf "${OPENHERON_TEST_ROOT:?}"; echo "[test-backend] reset ${OPENHERON_TEST_ROOT}" ;;
    --sync)         SYNC=1 ;;
    --prepare-only) PREPARE_ONLY=1 ;;
    *) echo "usage: $0 [--reset] [--sync] [--prepare-only]" >&2; exit 2 ;;
  esac
done

TEST_ROOT="$REPO_ROOT/$OPENHERON_TEST_ROOT"
# The config root is named explicitly by OPENHERON_CONFIG_DIR (exported below),
# and the process cwd moves to $PROJECT_DIR. The config root **no longer
# searches upwards**, so the repository's own .openheron/ is structurally out of
# reach — port isolation and data isolation no longer depend on "a probe that
# happens to stop here", they depend on a named root.
PROJECT_DIR="$TEST_ROOT/project"
CONFIG_DIR="$PROJECT_DIR/.openheron"
STATE_DIR="$TEST_ROOT/state"

mkdir -p "$CONFIG_DIR/server" "$STATE_DIR"

# agents/ llm/ policy/ skills/ tools/ mcps/ are **inputs** (agent definitions,
# vendor protocols, permissions, tool configuration, MCP servers), not state.
# The test instance needs real agents to get through a whole turn, so they are
# copied over — and copied rather than symlinked so that a crashing test
# cannot damage the development configuration.
#
# ★ tools/ is in there too: it is a **configuration** directory (one file per
#   external tool, plus the knowledge base), and for it to behave the same it
#   has to match the development side. Leaving it out means the HTTP/search
#   tools are always on (or always off) in the test instance, which is not the
#   same thing as development.
#
# ★ mcps/ belongs to the same list and for the same reason, only the failure
#   is louder: an agent names the servers it mounts in its own frontmatter
#   (`mcps: [fetch]`), and the loader refuses an agent that names a server
#   nobody declared — "agent task lists the MCP server "fetch", which is not
#   declared under mcps/". Copy the agents without the servers and the test
#   instance refuses to start at all.
#
# ★ The list is the set of **configuration partitions**, and a partition that
#   is missing from it is a partition the test instance silently disagrees
#   with the development side about.
#
# Deliberately excludes server/ and state/: this script manages those itself.
copy_defs() {
  local src="$REPO_ROOT/.openheron" sub
  [[ -d "$src" ]] || { echo "[test-backend] no $src, skipping agent defs" >&2; return; }
  for sub in agents llm policy skills tools mcps; do
    [[ -d "$src/$sub" ]] || continue
    if [[ ! -d "$CONFIG_DIR/$sub" || "$SYNC" == 1 ]]; then
      rm -rf "${CONFIG_DIR:?:?}/$sub"
      cp -r "$src/$sub" "$CONFIG_DIR/$sub"
      echo "[test-backend] synced $sub/"
    fi
  done
}
copy_defs

# ★ This file's content **is** the config.Server struct — there is no `server:`
#   top-level key. Writing it as a nested structure fails with
#   "field server not found in type decl.Server".
if [[ ! -f "$CONFIG_DIR/server/config.yaml" ]]; then
  cat > "$CONFIG_DIR/server/config.yaml" <<'YAML'
# Test configuration. Kept apart from the development configuration so that test
# changes do not affect day-to-day use.

# The port actually comes from LISTEN_ADDR (see .env.test); this is only a
# fallback.
listen_addr: ":18099"

# The cap on one turn from submission to running state. Shortened for tests:
# failures must be fast, otherwise a single assertion can wait 60 seconds and
# bury the real error under a timeout.
start_timeout: 10s

# ★ The nesting-depth cap has to match the development-side agent topology.
#
#   agents/ is copied from the development side (the factory configuration is
#   main → [task]; the sample under .openheron-example/ is
#   main → [researcher, coder, reviewer] — one level of delegation), and with
#   the default cap of 1 the instance would **refuse to start**: "agent main has
#   a team nested to depth 2" — and that error looks nothing like "the test
#   environment is misconfigured", which makes it hard to diagnose.
agent:
  max_depth: 2
YAML
  echo "[test-backend] wrote default server config"
fi

# PROJECT_ROOT is deliberately not set — cd-ing into $PROJECT_DIR does the same
# job. projectRoot is taken from the working directory, so the cwd is it, which
# matches development. The config root itself is named by
# OPENHERON_CONFIG_DIR and is independent of projectRoot: projectRoot only
# decides where the "default root" would land, and what the workdir is.
#
# ★ OPENHERON_CONFIG_DIR is now the **only** config root (llm/ agents/ … live
#   directly inside it) — it used to be the "user-level layer" that was stacked
#   on top of the project level. The value semantics did not change, the role
#   did: once it is set, the project's .openheron/ is never consulted.
export OPENHERON_CONFIG_DIR="$CONFIG_DIR"
export DATA_DIR="$STATE_DIR"
export LISTEN_ADDR=":$OPENHERON_TEST_BACKEND_PORT"

if [[ "$PREPARE_ONLY" == 1 ]]; then
  echo "[test-backend] prepared (not started)"
  exit 0
fi

# ★ The binary is named by the build, and the build knows the platform: the
#   Taskfile builds `bin/openheron{{.EXE}}`, so the file is `openheron.exe`
#   on Windows and `openheron` everywhere else. A hardcoded name picks the
#   wrong file — and the wrong file is worse than a missing one, because a
#   stale binary from an earlier commit starts happily and fails on the
#   configuration it does not understand.
#   So the platform decides, the same way the build does.
BIN="${OPENHERON_BIN:-}"
if [[ -z "$BIN" ]]; then
  if [[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* || "$OSTYPE" == win32* ]]; then
    BIN="$REPO_ROOT/bin/openheron.exe"
  else
    BIN="$REPO_ROOT/bin/openheron"
  fi
fi
if [[ ! -x "$BIN" ]]; then
  echo "[test-backend] binary not found: $BIN (run 'task build-go' first)" >&2
  exit 1
fi

printf '[test-backend] base   : %s\n' "$OPENHERON_TEST_BACKEND_BASE"
printf '[test-backend] config : %s\n' "$OPENHERON_CONFIG_DIR"
printf '[test-backend] state  : %s\n' "$DATA_DIR"
printf '[test-backend] cwd    : %s\n' "$PROJECT_DIR"

cd "$PROJECT_DIR"
exec "$BIN"
