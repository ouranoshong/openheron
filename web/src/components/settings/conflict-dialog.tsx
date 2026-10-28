/**
 * 并发冲突对话框。
 *
 * ★ **绝不自动合并**：两份都是对方在编辑，合出来的第三份谁都没写过。
 *   「重载」丢草稿、「覆盖」丢磁盘 —— 两者都是明确的取舍，
 *   而自动合并只是把取舍藏起来，然后在半年后以「这段是谁写的」形式出现。
 *
 * ★ 「覆盖」不设默认焦点：那是一次不可撤销的丢弃，不该是回车直接落到的
 *   那个按钮。
 */

import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { useSettings } from "@/lib/settings-store"

export function ConflictDialog() {
  const conflict = useSettings((s) => s.conflict)
  const reload = useSettings((s) => s.reloadFromDisk)

  return (
    <Dialog open={conflict !== undefined} onOpenChange={() => (conflict ? reload() : undefined)}>
      <DialogContent className="sm:max-w-md" data-testid="conflict-dialog">
        <DialogHeader>
          <DialogTitle>This file changed on disk</DialogTitle>
          <DialogDescription>You edited an older version. Overwriting discards the on-disk version permanently.</DialogDescription>
        </DialogHeader>

        <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <dt>Your version</dt>
          <dd className="text-foreground font-mono">{conflict?.revision ?? ""}</dd>
          <dt>On disk</dt>
          <dd className="text-foreground font-mono">changed by someone else</dd>
        </dl>

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => void reload()} data-testid="conflict-reload">
            Reload from disk
          </Button>
          <Button type="button" variant="destructive" onClick={() => useSettings.setState({ conflict: undefined })} data-testid="conflict-dismiss">
            Keep editing
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}