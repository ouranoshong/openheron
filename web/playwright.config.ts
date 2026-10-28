import fs from "node:fs"
import path from "node:path"
import { defineConfig, devices } from "@playwright/test"

/**
 * E2E 配置：真实浏览器 + 确定性假后端。
 *
 * ★ 为什么是假后端而不是真 openheron：
 *
 *   现有 10 个 bun 冒烟脚本全绿，界面却是坏的 —— 因为脚本结构上就
 *   看不见渲染。而真后端依赖模型是否配合（`smoke:hitl` 约 1/5 抖动，
 *   取决于模型这一次会不会调 `write_file`），用它做界面断言，失败时
 *   分不清是界面坏了还是模型没听话。
 *
 *   这里的取舍是：**牺牲与真实模型的端到端联调，换取界面断言的确定性。**
 *   与真实后端的协议层验证另有 `scripts/interop-check.ts` 负责。
 *
 * ★ 为什么不用真后端 + 真模型的 playwright：
 *   那类测试必须允许重试，于是失败变成噪音，三轮之后没人看它的结果。
 *
 * ★ 端口一律取自**仓库根的 .env.test**，与 scripts/test-backend.sh 共读
 *   同一份。这样 E2E 的端口也不占用开发环境（:5173 / :8080），
 *   开发时开着 vite 也不影响跑测试。
 */
// 本文件在 web/ 下，仓库根是上一级。
const repoRoot = path.resolve(import.meta.dirname, "..")

function envFile(): Record<string, string> {
  const file = path.join(repoRoot, ".env.test")
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

const env = envFile()
const FAKE_PORT = Number(env.OPENHERON_TEST_FAKE_PORT ?? 18180)
const FAKE_URL = `http://127.0.0.1:${FAKE_PORT}`
const FRONTEND_PORT = Number(env.OPENHERON_TEST_FRONTEND_PORT ?? 15173)
const FRONTEND_URL = `http://127.0.0.1:${FRONTEND_PORT}`

export default defineConfig({
  testDir: "./test/e2e",
  // 串行：所有用例共享同一个假后端的会话表，并行会互相污染。
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : [["list"]],
  timeout: 20_000,
  expect: { timeout: 7_000 },

  use: {
    baseURL: FRONTEND_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },

  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],

  /**
   * ★ `reuseExistingServer: false` 是刻意的，不能图省事改回 `!CI`。
   *
   *   本机开着 vite dev server 时，复用它会让整个 E2E 失去确定性：
   *   那个 server 是用**默认**代理起的（指向 :8080 的真后端），而
   *   `OPENHERON_API_TARGET` 根本没注入 —— 于是 `resetBackend()` 打的是
   *   假后端的会话表，浏览器却在跟真后端说话。测试照样「全绿」，
   *   但它证明不了任何关于假后端的事。
   *
   *   端口被占时 vite（strictPort）会起不来，Playwright 直接报错 ——
   *   这正是要的行为：响亮地失败，而不是安静地测错东西。
   *
   *   用独立的 :15173 / :18180 之后，上面的「碰巧复用」不再容易发生，
   *   但这个开关仍然是唯一保证 —— 端口错开是概率，strictPort 是必然。
   */
  webServer: [
    {
      // 假后端
      command: `bun run test/serve.ts ${FAKE_PORT}`,
      url: `${FAKE_URL}/v2/health`,
      reuseExistingServer: false,
      stdout: "ignore",
      stderr: "pipe",
    },
    {
      // 前端，/v2 反代到假后端
      command: "bun run dev -- --mode test",
      url: FRONTEND_URL,
      reuseExistingServer: false,
      env: { OPENHERON_API_TARGET: FAKE_URL },
      stdout: "ignore",
      stderr: "pipe",
    },
  ],
})

export { FAKE_URL, FRONTEND_URL }