import fs from "node:fs"
import path from "node:path"
import { defineConfig, devices } from "@playwright/test"

const repoRoot = path.resolve(import.meta.dirname, "..")

function envFile(): Record<string, string> {
  const file = path.join(repoRoot, ".env.test")
  if (!fs.existsSync(file)) {
    throw new Error(`missing ${file} — the test ports are all defined here, see the header comment`)
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

  webServer: [
    {
      command: `bun run test/serve.ts ${FAKE_PORT}`,
      url: `${FAKE_URL}/v2/health`,
      reuseExistingServer: false,
      stdout: "ignore",
      stderr: "pipe",
    },
    {
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