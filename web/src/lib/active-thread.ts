"use client"

const KEY = "openheron.activeThread"

let writeWarned = false

export function readActiveThreadId(): string | undefined {
  try {
    return localStorage.getItem(KEY) ?? undefined
  } catch {
    return undefined
  }
}

export function writeActiveThreadId(id: string): void {
  try {
    if (id) localStorage.setItem(KEY, id)
    else localStorage.removeItem(KEY)
  } catch {
    if (!writeWarned) {
      writeWarned = true
      console.warn(
        "[openheron] cannot persist the active thread id: the current conversation will not survive a page reload",
      )
    }
  }
}
