import { useEffect } from "react"

import { Button } from "@/components/ui/button"
import { useSettings } from "@/lib/settings-store"

/**
 * 侧栏底部的设置入口。
 *
 * ★ **刻意与对话框分文件。** 它只有几十字节，却 import 了 settings-store；
 *
 *   而对话框 import 了 CodeMirror。若两者同文件，sidebar 的静态引入会把
 *   整个对话框拖进首屏 —— vite 会直接报 INEFFECTIVE_DYNAMIC_IMPORT。
 *   聊天界面一个字节都用不到 CodeMirror。
 *
 *   状态在 zustand 里，所以入口（首屏）与对话框（懒加载）可以是两个 chunk。
 *
 * ★ **⌘, 快捷键也在这里**（原本在对话框里）。它必须**不依赖**对话框那个
 *   chunk：对话框是懒加载的，监听器挂在里面的话，页面刚打开到 chunk 到位
 *   之前按 ⌘, 毫无反应 —— 而那正好是用户第一次试它的时候。
 */
export function SettingsEntry({ className }: { className?: string }) {
  const openSettings = useSettings((s) => s.openSettings)
  const open = useSettings((s) => s.open)
  const close = useSettings((s) => s.closeSettings)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === ",") {
        e.preventDefault()
        if (useSettings.getState().open) close()
        else void openSettings()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [open, close, openSettings])

  return (
    <Button
      type="button"
      variant="ghost"
      className={className}
      onClick={() => void openSettings()}
      data-testid="settings-entry"
    >
      Settings
      <kbd className="text-muted-foreground ml-auto text-[10px]">⌘,</kbd>
    </Button>
  )
}