/**
 * 配置编辑器 · 对话框外壳。
 *
 * ★ 入口在 sidebar 底部而不是 header：设置是低频操作，而 header 只有 48px
 *   高，已经装着折叠钮、标题与状态点。塞进去会挤，且把它变成了一个
 *   与主流程同等显眼的入口。
 *
 * ★ 大尺寸：`agents/*.md` 的正文就是**系统提示词**，可能要长看 ——
 *   窄对话框让编辑器只剩几百像素，读一段提示词要来回滚动。
 */

import { XIcon } from "lucide-react"
import { useShallow } from "zustand/react/shallow"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfigEditor } from "@/components/settings/config-editor"
import { SettingsFileList } from "@/components/settings/file-list"
import { SettingsFooter } from "@/components/settings/footer"
import { ConflictDialog } from "@/components/settings/conflict-dialog"
import { useSettings } from "@/lib/settings-store"
import { selectCategories, selectIsEditable } from "@/lib/settings-store"

export function SettingsDialog() {
  const open = useSettings((s) => s.open)
  const close = useSettings((s) => s.closeSettings)
  const selected = useSettings((s) => s.selected)
  const draft = useSettings((s) => s.draft)
  const loading = useSettings((s) => s.loading)
  const loadError = useSettings((s) => s.loadError)
  const edit = useSettings((s) => s.edit)

  // useShallow：返回新数组的 selector 必须配它，否则每次渲染都判定为「变了」。
  const categories = useSettings(useShallow(selectCategories))
  const isEditable = useSettings(selectIsEditable)

  // ★ ⌘, 的监听在 settings-entry.tsx —— 那个文件在首屏 chunk 里，
  //   而这里是对话框（懒加载）。挂在懒加载的 chunk 上意味着「页面刚打开
  //   到 chunk 到位之前按快捷键没反应」，而那正是用户第一次试它的时刻。
  const dark = document.documentElement.classList.contains("dark")

  return (
    <>
      <Dialog open={open} onOpenChange={(v) => (!v ? close() : undefined)}>
        <DialogContent
          // 覆盖 ui/dialog.tsx 默认的 sm:max-w-sm。
          className="sm:max-w-6xl h-[85vh] flex flex-col gap-3 p-4"
          data-testid="settings-dialog"
          // 默认关闭钮是 absolute 定位，会压住右侧的级别切换。这里把它
          // 放进 header 的 flex 流里，宽度不够时级别切换与关闭钮一起
          // 保持完整，标题负责截断。
          showCloseButton={false}
        >
          <DialogHeader className="flex-row items-center justify-between gap-4 space-y-0">
            <div className="min-w-0">
              <DialogTitle>Settings</DialogTitle>
              <DialogDescription className="truncate">{selected ?? "no file selected"}</DialogDescription>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <DialogClose asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Close settings">
                  <XIcon className="size-4" />
                </Button>
              </DialogClose>
            </div>
          </DialogHeader>

          <div className="flex min-h-0 flex-1 gap-4">
            <SettingsFileList />

            <div className="flex min-w-0 flex-1 flex-col gap-2">
              {loadError ? (
                <p className="text-destructive text-sm" role="alert">
                  {loadError}
                </p>
              ) : selected ? (
                <>
                  {categories.find((c) => !c.editable && selected.startsWith(c.id)) && (
                    <p className="text-muted-foreground bg-muted rounded-md px-3 py-2 text-xs" role="status">
                      This section is not hot-reloaded; changes apply after a restart.
                    </p>
                  )}
                  <ConfigEditor
                    path={selected}
                    value={draft}
                    onChange={edit}
                    readOnly={!isEditable}
                    dark={dark}
                  />
                </>
              ) : (
                <div className="text-muted-foreground grid flex-1 place-items-center text-sm">
                  {loading ? "Loading…" : "Select a file"}
                </div>
              )}
            </div>
          </div>

          <SettingsFooter />
        </DialogContent>
      </Dialog>
      <ConflictDialog />
    </>
  )
}