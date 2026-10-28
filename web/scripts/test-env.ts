import fs from "node:fs"
import path from "node:path"

/**
 * 读仓库根的 .env.test —— 测试端口与路径的**唯一**来源。
 *
 * ★ 为什么冒烟脚本要读它：
 *   这些脚本打的是真 openheron，而真 openheron 现在由 `scripts/test-backend.sh`
 *   起在 :18099（独立配置、独立会话目录）。若脚本各自写死一个默认端口，
 *   就会出现「脚本连 :8099、那儿没有实例 → 连不上」，或者更糟：
 *   连上了某个开发实例，用真实会话跑冒烟、往里写垃圾。
 *
 *   与 vite.config.ts / playwright.config.ts 共读同一份，
 *   改端口只改 .env.test 一处。
 */
const REPO_ROOT = path.resolve(import.meta.dirname, "../..")

export function testEnv(): Record<string, string> {
  const file = path.join(REPO_ROOT, ".env.test")
  if (!fs.existsSync(file)) {
    throw new Error(`missing ${file} — 测试端口都定义在这里，见文件头注释`)
  }
  const out: Record<string, string> = {}
  for (const line of fs.readFileSync(file, "utf8").split("\n")) {
    const m = /^\s*([A-Z0-9_]+)\s*=\s*(.+?)\s*$/.exec(line)
    if (m) out[m[1]] = m[2].replace(/^["']|["']$/g, "")
  }
  return out
}

/** 测试后端地址。OPENHERON_BASE 可覆盖（指向别处时临时改）。 */
export function testBackendBase(): string {
  return process.env.OPENHERON_BASE ?? testEnv().OPENHERON_TEST_BACKEND_BASE ?? "http://127.0.0.1:18099"
}

/**
 * 启动前飞检：测试实例在不在。
 *
 * ★ 为什么值得单独加：
 *   实例没起时，`fetch` 连 :18099 会一直挂着直到调用方的超时 ——
 *   表现为命令「卡住」，而人看到卡住的第一反应是「测试环境有问题」，
 *   而不是「你忘了起实例」。实测 `smoke:interop` 在这种情况下挂死，
 *   退出码 124，屏幕上什么都读不出来。
 *
 *   这里在真正发请求前问一次健康端点，失败就**立刻**给出可执行的提示。
 */
export async function requireTestBackend(): Promise<void> {
  const base = testBackendBase()
  const health = `${base}/v2/health`
  try {
    const res = await fetch(health, { signal: AbortSignal.timeout(3000) })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
  } catch (err) {
    console.error(
      [
        `✗ 测试实例不可达：${health}`,
        `  原因：${err instanceof Error ? err.message : String(err)}`,
        "",
        "  起一个（独立配置 + 独立会话目录，与开发环境完全隔离）：",
        "      make test-env-backend",
        "",
        "  连别处请显式指定：",
        "      OPENHERON_BASE=http://127.0.0.1:8080 bun run smoke:interop",
      ].join("\n"),
    )
    process.exit(2)
  }
}
