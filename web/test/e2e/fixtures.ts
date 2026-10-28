import { expect, type Locator, type Page } from "@playwright/test"

import { FAKE_URL } from "../../playwright.config"

export { FAKE_URL }

export const MARKER_A = "MARKER-ALPHA"
export const MARKER_B = "MARKER-BRAVO"

export async function setScenario(name: string): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/scenario?name=${name}`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to set scenario ${name}: ${r.status}`)
}

export async function setAgentsMode(mode: "default" | "multi" | "many" | "grew"): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/agents-mode?mode=${mode}`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to set agents mode ${mode}: ${r.status}`)
}

export async function setDefaultRoot(agent: string): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/default-root?agent=${agent}`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to set the default root ${agent}: ${r.status}`)
}

export async function clearFakeThreads(): Promise<void> {
  const r = await fetch(`${FAKE_URL}/__fake/threads/clear`, { method: "POST" })
  if (!r.ok) throw new Error(`failed to clear the fake threads: ${r.status}`)
}

export async function resetBackend(): Promise<void> {
  await fetch(`${FAKE_URL}/__fake/reset`, { method: "POST" })
}

export const NEW_THREAD = '[data-testid="new-thread"]'
export const THREAD_ITEM = '[data-testid="thread-item"]'
export const THREAD_ROW = '[data-testid="thread-row"]'
export const COMPOSER = '[aria-label="Message input"]'
export const SEND_BUTTON = '[aria-label="Send message"]'
export const COMPOSER_POPUP = '[data-testid="composer-popup"]'
export const COMPOSER_OPTION = '[data-testid="composer-option"]'

export const APPROVE_BUTTON = "button, [role=button]"
export const STOP_BUTTON = '[aria-label="Stop generating"]'

export async function clickByText(page: Page, ...texts: string[]): Promise<void> {
  const el = await findByText(page, ["button"], texts)
  if (!el) throw new Error(`no button matching any of: ${texts.join(" / ")}`)
  await el.click()
}

export async function clickMenuItem(page: Page, ...texts: string[]): Promise<void> {
  const el = await findByText(page, ["menuitem", "button", "link"], texts)
  if (!el) throw new Error(`no menu item matching any of: ${texts.join(" / ")}`)
  await el.click()
}

async function findByText(
  page: Page,
  roles: readonly string[],
  texts: readonly string[],
): Promise<Locator | null> {
  for (const role of roles) {
    for (const t of texts) {
      const loc = page.getByRole(role as "button", { name: t }).first()
      if (await loc.count()) return loc
    }
  }
  return null
}

export async function openApp(page: Page): Promise<void> {
  await page.goto("/")
  await expect(page.locator(THREAD_ITEM).first()).toBeVisible()
}

export async function sendMessage(page: Page, text: string): Promise<void> {
  const box = page.locator(COMPOSER)
  await box.click()
  await box.fill(text)
  await page.locator(SEND_BUTTON).click()
}

export async function typeAndEnter(page: Page, text: string): Promise<void> {
  const box = page.locator(COMPOSER)
  await box.click()
  await box.fill(text)
  await box.press("Enter")
}

export async function threadTitles(page: Page): Promise<string[]> {
  const items = page.locator(THREAD_ITEM)
  const n = await items.count()
  const out: string[] = []
  for (let i = 0; i < n; i++) out.push((await items.nth(i).innerText()).trim())
  return out
}

export async function openThread(page: Page, title: string): Promise<void> {
  const row = page.locator(THREAD_ITEM).filter({ hasText: title }).first()
  await row.waitFor({ state: "visible", timeout: 5000 })
  await row.click()
}

export function trackRequests(page: Page): string[] {
  const seen: string[] = []
  page.on("request", (r) => {
    const p = new URL(r.url()).pathname
    if (p.startsWith("/v2/")) seen.push(p)
  })
  return seen
}
