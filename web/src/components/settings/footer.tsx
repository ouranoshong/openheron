/**
 * 底栏：校验状态 + 保存/删除。
 *
 * ★ 三类消息用**不同颜色**，因为它们可信度不同：
 *
 *   客户端预校验 —— 语法层，瞬时，但看不到跨文件
 *   服务端试合并 —— 语义层，慢，是唯一权威
 *   保存结果 —— 是否真的生效
 *
 *   合成一句话「保存失败」的话，用户既不知道改哪一行，也不知道是不是
 *   已经写进去了。
 */

import { useSettings, selectCanSave, selectServerIssues } from "@/lib/settings-store"
import type { Issue } from "@/lib/settings-validate"
import { Button } from "@/components/ui/button"

export function SettingsFooter() {
  const saveState = useSettings((s) => s.saveState)
  const applied = useSettings((s) => s.applied)
  const saveError = useSettings((s) => s.saveError)
  const issues = useSettings((s) => s.issues)
  const serverIssues = useSettings(useServerIssues())
  const canSave = useSettings(canSaveSel)
  const save = useSettings((s) => s.save)
  const remove = useSettings((s) => s.remove)
  const dirty = useSettings((s) => s.draft !== s.saved)
  const selected = useSettings((s) => s.selected)
  const exists = useSettings((s) => s.exists)

  const busy = saveState === "saving" || saveState === "validating"

  return (
    <div className="flex shrink-0 items-center gap-3 border-t pt-3">
      <div className="min-w-0 flex-1 text-xs" data-testid="settings-status">
        <Status
          saveState={saveState}
          applied={applied}
          saveError={saveError}
          issues={issues}
          serverIssues={serverIssues}
          dirty={dirty}
        />
      </div>

      <Button
        type="button"
        variant="ghost"
        className="text-xs"
        disabled={!selected || !exists || busy}
        onClick={() => void remove()}
        data-testid="settings-delete"
      >
        Delete
      </Button>
      <Button
        type="button"
        size="sm"
        disabled={!canSave}
        onClick={() => void save()}
        data-testid="settings-save"
      >
        {saveState === "saving" ? "Saving…" : saveState === "validating" ? "Validating…" : "Save"}
      </Button>
    </div>
  )
}

function Status({
  saveState,
  applied,
  saveError,
  issues,
  serverIssues,
  dirty,
}: {
  saveState: string
  applied: boolean
  saveError?: string
  issues: Issue[]
  serverIssues: string[]
  dirty: boolean
}) {
  // 优先级：进行中 > 服务端权威 > 客户端 > 保存结果。
  // ★ 服务端在前：它是唯一权威，客户端那条可能已经过时。
  if (saveState === "validating") return <span className="text-muted-foreground">Validating…</span>
  if (saveState === "saving") return <span className="text-muted-foreground">Saving…</span>

  if (serverIssues.length > 0) {
    return (
      <ul className="text-destructive flex flex-col gap-0.5">
        {serverIssues.map((m, i) => (
          <li key={i}>{m}</li>
        ))}
      </ul>
    )
  }
  if (issues.length > 0) {
    // ★ 按严重级别分开渲染：hint 用 muted，error 才用 destructive。
    //   混在一起渲染的话，一条提示看上去和「存不了」一样重 ——
    //   而这正是之前那条「服务端会校验」提示被当成错误的原因。
    const errors = issues.filter((i) => (i.severity ?? "error") === "error")
    const hints = issues.filter((i) => i.severity === "hint")
    return (
      <ul className="flex flex-col gap-0.5">
        {errors.map((i, idx) => (
          <li key={`e${idx}`} className="text-destructive">
            line {i.line}: {i.message}
          </li>
        ))}
        {hints.map((i, idx) => (
          <li key={`h${idx}`} className="text-muted-foreground">
            line {i.line}: {i.message}
          </li>
        ))}
      </ul>
    )
  }
  if (saveState === "error" && saveError) {
    return <span className="text-destructive whitespace-pre-line">{saveError}</span>
  }
  if (saveState === "conflict") {
    return <span className="text-warning">This file changed on disk — reload or overwrite.</span>
  }
  if (saveState === "saved") {
    // ★ 「保存成功」与「已生效」必须分开说：applied 为 false 时写下去是
    //   成功的，但 watcher 没开或该分区不热更新，运行时还没变。
    return (
      <span className={applied ? "text-muted-foreground" : "text-warning"}>
        {applied ? "Saved · applied" : "Saved · restart openheron to apply"}
      </span>
    )
  }
  if (dirty) return <span className="text-muted-foreground">Unsaved changes</span>
  return <span className="text-muted-foreground">Valid</span>
}

// 两个小包装：让主组件读起来整齐，也避免在 JSX 里写 useShallow 的类型体操。
import { useShallow } from "zustand/react/shallow"
function useServerIssues() {
  return useShallow(selectServerIssues)
}
function canSaveSel(s: Parameters<typeof selectCanSave>[0]) {
  return selectCanSave(s)
}