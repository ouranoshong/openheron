import fs from "node:fs"
import path from "node:path"

const REPO_ROOT = path.resolve(import.meta.dirname, "../..")

export function testEnv(): Record<string, string> {
  const file = path.join(REPO_ROOT, ".env.test")
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

export function testBackendBase(): string {
  return process.env.OPENHERON_BASE ?? testEnv().OPENHERON_TEST_BACKEND_BASE ?? "http://127.0.0.1:18099"
}

export async function requireTestBackend(): Promise<void> {
  const base = testBackendBase()
  const health = `${base}/v2/health`
  try {
    const res = await fetch(health, { signal: AbortSignal.timeout(3000) })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
  } catch (err) {
    console.error(
      [
        `✗ the test instance is unreachable: ${health}`,
        `  reason: ${err instanceof Error ? err.message : String(err)}`,
        "",
        "  start one (isolated config + isolated session directory, fully separate from the dev environment):",
        "      task test-env-backend",
        "",
        "  to connect somewhere else, set it explicitly:",
        "      OPENHERON_BASE=http://127.0.0.1:8080 bun run smoke:interop",
      ].join("\n"),
    )
    process.exit(2)
  }
}
