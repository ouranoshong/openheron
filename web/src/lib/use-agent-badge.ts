import { useCallback, useEffect, useRef, useState } from "react"

import { badgeForAgent, checkConfigChanged, loadAgents, type AgentBadge } from "@/lib/agent-info"
import type { AgentsView } from "@/lib/api"

const CONFIG_POLL_MS = 3000

export interface AgentBadgeState {
  badge: AgentBadge | null
  view: AgentsView | null
  loading: boolean
  error?: string
}

export function useAgentBadge(settingsOpen: boolean, agentId?: string): AgentBadgeState {
  const [state, setState] = useState<{ view: AgentsView | null; loading: boolean; error?: string }>({
    view: null,
    loading: true,
  })

  const reload = useCallback((force: boolean) => {
    let alive = true
    void loadAgents(force)
      .then((view) => {
        if (alive) setState({ view, loading: false })
      })
      .catch((err: unknown) => {
        if (!alive) return
        setState((prev) => ({
          ...prev,
          loading: false,
          error: err instanceof Error ? err.message : "Failed to load agent info",
        }))
      })
    return () => {
      alive = false
    }
  }, [])

  const sawSettingsOpen = useRef(false)
  useEffect(() => {
    if (settingsOpen) {
      sawSettingsOpen.current = true
      return
    }
    const force = sawSettingsOpen.current
    sawSettingsOpen.current = false
    return reload(force)
  }, [settingsOpen, reload])

  // ★ The badge shows the server's **current** configuration, and the
  //   configuration moves under the browser: an editor, a `git pull`, another
  //   tab, another machine sharing the config root. ★ Nothing pushes that
  //   news here — the run stream is the only long-lived channel to the
  //   server, and it exists only while a turn runs, and it carries the
  //   turn's events, not the configuration.
  //
  //   ⇒ The badge has to ask. It asks for the **fingerprint** and nothing
  //   else, and it fetches the table only once the fingerprint moved.
  //
  //   ★ A hidden tab does not ask — there is nobody to show it to. The
  //   moment it comes back it asks once, so the answer it acts on is never
  //   stale.
  useEffect(() => {
    const tick = () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return
      void checkConfigChanged().then((changed) => {
        // ★ Force: the cheap check only says "it moved". The view that says
        //   **how** it moved is fetched here — and only here.
        if (changed) void reload(true)
      })
    }
    const id = setInterval(tick, CONFIG_POLL_MS)
    const onVisible = () => {
      if (document.visibilityState === "visible") tick()
    }
    document.addEventListener("visibilitychange", onVisible)
    return () => {
      clearInterval(id)
      document.removeEventListener("visibilitychange", onVisible)
    }
  }, [reload])

  return {
    badge: state.view ? badgeForAgent(state.view, agentId) : null,
    view: state.view,
    loading: state.loading,
    error: state.error,
  }
}
