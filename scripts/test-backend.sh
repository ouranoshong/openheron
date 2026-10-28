#!/usr/bin/env bash
# 起一个**测试专用**的 openheron 实例：独立配置、独立会话目录、独立端口。
#
# 与开发环境的三重隔离（端口 / 配置 / 会话数据）都定义在仓库根的 .env.test。
# 用法：
#   scripts/test-backend.sh            前台起
#   scripts/test-backend.sh --reset    清空测试目录后起
#   scripts/test-backend.sh --sync     重新拉取开发侧的 agent 定义后起
#   scripts/test-backend.sh --prepare-only  只建好目录，不启动
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

set -a
# shellcheck disable=SC1091
. ./.env.test
set +a

# ★ 遍历**所有**参数而不是只看 $1 —— `--reset --prepare-only` 是常见组合，
#   只看第一个会把后半段静默丢掉（曾经因此「--prepare-only」照样起了服务）。
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
# 让 probeUpwards 停在这里 —— 否则它会一路找到**仓库根那个** .openheron/，
# 于是测试实例读开发配置、写开发会话：端口隔离了而数据没隔离，等于没隔离。
PROJECT_DIR="$TEST_ROOT/project"
CONFIG_DIR="$PROJECT_DIR/.openheron"
STATE_DIR="$TEST_ROOT/state"

mkdir -p "$CONFIG_DIR/server" "$STATE_DIR"

# agents/ llm/ policy/ skills/ tools/ 是**输入**（agent 定义、厂商协议、权限、
# 工具配置），不是状态。测试实例要用真 agent 才跑得通一整轮，所以复制一份
# 过来 —— 复制而非软链，是为了测试跑崩时不会写坏开发配置。
#
# ★ tools/ 也在其中：它是**配置**目录（每个外部工具一个文件 + 知识库），
#   改了要生效就得跟开发侧一致。漏掉它的话，测试实例里的 HTTP/搜索工具
#   永远开着/关着，与开发侧不是一回事。
#
# 刻意不含 server/ 与 state/：这两个由本脚本自己管。
copy_defs() {
  local src="$REPO_ROOT/.openheron" sub
  [[ -d "$src" ]] || { echo "[test-backend] no $src, skipping agent defs" >&2; return; }
  for sub in agents llm policy skills tools; do
    [[ -d "$src/$sub" ]] || continue
    if [[ ! -d "$CONFIG_DIR/$sub" || "$SYNC" == 1 ]]; then
      rm -rf "${CONFIG_DIR:?:?}/$sub"
      cp -r "$src/$sub" "$CONFIG_DIR/$sub"
      echo "[test-backend] synced $sub/"
    fi
  done
}
copy_defs

# ★ 这个文件的内容**直接**就是 config.Server，没有 server: 顶层键 ——
#   写成嵌套结构会报 "field server not found in type config.Server"。
if [[ ! -f "$CONFIG_DIR/server/config.yaml" ]]; then
  cat > "$CONFIG_DIR/server/config.yaml" <<'YAML'
# 测试用配置。与开发配置分开存放，避免测试改动影响日常使用。

# 端口实际由 LISTEN_ADDR 提供（见 .env.test），这里只兜底。
listen_addr: ":18099"

# 一轮从提交到进入运行态的上限。测试里调短：失败要快，
# 否则一条断言能等 60 秒，把真正的报错埋在超时里。
start_timeout: 10s

# ★ 嵌套深度上限要与开发侧的 agent 拓扑对得上。
#
#   agent/ 是从开发侧复制过来的（main → {task, pm} → 三个工程师，两层），
#   上限还是默认的 1 的话实例**起不来**：「agent main has a team nested to
#   depth 2」—— 而这条报错与「测试环境没配好」长得完全不一样，难查。
agent:
  max_depth: 2
YAML
  echo "[test-backend] wrote default server config"
fi

# 不设 PROJECT_ROOT —— 用 cd 进 $PROJECT_DIR 代替。projectRoot 取的是工作
# 目录；设成绝对路径还得再走一次上溯判定，不如让 cwd 就是它，语义同开发时。
#
# ★ OPENHERON_CONFIG_DIR 现在是**唯一的配置根**（里面直接有 llm/ agents/ …）——
#   它以前是「用户级那一层」，与项目级叠加。值语义没变，角色变了：
#   设了它就不再看项目里的 .openheron/。
export OPENHERON_CONFIG_DIR="$CONFIG_DIR"
export DATA_DIR="$STATE_DIR"
export LISTEN_ADDR=":$OPENHERON_TEST_BACKEND_PORT"

if [[ "$PREPARE_ONLY" == 1 ]]; then
  echo "[test-backend] prepared (not started)"
  exit 0
fi

BIN="${OPENHERON_BIN:-$REPO_ROOT/bin/openheron}"
if [[ ! -x "$BIN" ]]; then
  echo "[test-backend] binary not found: $BIN (run 'make build-go' first)" >&2
  exit 1
fi

printf '[test-backend] base   : %s\n' "$OPENHERON_TEST_BACKEND_BASE"
printf '[test-backend] config : %s\n' "$OPENHERON_CONFIG_DIR"
printf '[test-backend] state  : %s\n' "$DATA_DIR"
printf '[test-backend] cwd    : %s\n' "$PROJECT_DIR"

cd "$PROJECT_DIR"
exec "$BIN"