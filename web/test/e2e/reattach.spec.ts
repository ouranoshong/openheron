import { expect, test } from "@playwright/test"

import { openApp, resetBackend, setScenario, typeAndEnter } from "./fixtures"

const MARKER = "MARKER-ALPHA"

test.beforeEach(async () => {
  await resetBackend()
})

// A dropped connection does not lose the turn.
//
// ★ The run outlives the connection: the server archives the turn, not the
//   stream. So when the stream dies, the client comes back with the run's
//   id, and the server hands it the rest of the turn.
//
// ★ Before the split, the turn's output was the renderer's to deliver —
//   and a dead connection meant an answer nobody ever saw.
test("连接断了以后重新挂上，这一轮不丢", async ({ page }) => {
  await setScenario("dropped")
  await openApp(page)

  await typeAndEnter(page, "hello")

  // The first half arrives, and then the connection dies.
  await expect(page.getByText(`${MARKER} 前半`)).toBeVisible()

  // The client comes back to the run it started, and the rest of the turn
  // arrives with it.
  await expect(page.getByText(`${MARKER} 后半`)).toBeVisible({ timeout: 10000 })
})

// The re-attach is a request of its own: it names the run and carries no
// question. A request with a question would start a new run — and run the
// user's message a second time.
test("重新挂上时只带 run id，不带问题", async ({ page }) => {
  await setScenario("dropped")
  await openApp(page)

  const bodies: string[] = []
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === "/v2/run") {
      bodies.push(r.postData() ?? "")
    }
  })

  await typeAndEnter(page, "hello")
  await expect(page.getByText(`${MARKER} 后半`)).toBeVisible({ timeout: 10000 })

  expect(bodies.length).toBeGreaterThanOrEqual(2)
  const first = JSON.parse(bodies[0]) as Record<string, unknown>
  expect(first.query).toBe("hello")

  const attach = JSON.parse(bodies[bodies.length - 1]) as Record<string, unknown>
  expect(attach.runId).toBeTruthy()
  expect(attach.query).toBeUndefined()
})
