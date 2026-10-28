import { useCallback, useEffect, useRef, useState } from "react"

import { badgeForAgent, checkConfigChanged, loadAgents, type AgentBadge } from "@/lib/agent-info"
import type { AgentsView } from "@/lib/api"

/**
 * 配置指纹的轮询间隔。
 *
 * ★ 服务端自己的轮询是 100ms + 200ms 防抖（config/reload.go），
 *   所以 3 秒足够「配置变了 → 界面跟上」，代价只有每 3 秒一个小请求
 *   （/v2/agents 约 1.4KB）。
 */
const CONFIG_POLL_MS = 3000

export interface AgentBadgeState {
  /** 当前活动 agent 的摘要；拓扑还没到时为 null。 */
  badge: AgentBadge | null
  /** 完整拓扑，选择器与底栏共用同一份数据。 */
  view: AgentsView | null
  loading: boolean
  error?: string
}

/**
 * useAgentBadge 负责把 agent 拓扑接到 Composer。
 *
 * ★ 首次挂载取一次；设置弹窗关闭后再取一次（force）。用户可能刚改过
 *   `agents/*.md` 或 `llm/*.yaml`，不刷新会一直显示旧模型。
 *
 * ★ 另外**轮询配置指纹**：用户可以在**编辑器里**改文件 —— 那时设置弹窗
 *   没开过，「关闭时 force」那条路根本不会触发，界面会一直拿着旧缓存
 *   显示过时的 agent 名单。指纹变了就把缓存换成新的并重渲染。
 *
 * ★ agentId 变化**不重新取数**：拓扑没变，变的只是从哪一条读。
 *   切换后底栏立刻显示新 agent，不必等一次往返。
 */
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

  // settingsOpen 从 true 变回 false 时才强制刷新；首次挂载走普通缓存。
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

  // ★ 轮询配置指纹 —— 「在编辑器里改了文件」那条路的信号。
  //
  //   变了才 setState：没变就**不重渲染** —— 否则每 3 秒重建一次
  //   Composer（连带 agent 过滤条与底栏），那是白烧。
  useEffect(() => {
    const tick = () => {
      // 页面不可见时没人看得见 —— 不拉。
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return
      void checkConfigChanged().then((changed) => {
        if (changed) void reload(false)
      })
    }
    const id = setInterval(tick, CONFIG_POLL_MS)
    // ★ 切回这个标签页时立刻查一次：用户多半是在**别的窗口**改完文件
    //   才切回来的，等一个轮询周期会让他以为「改了没反应」。
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
