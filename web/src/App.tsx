import { lazy, Suspense } from "react"

import { Shell } from "@/app/shell"

/**
 * ★ 设置对话框**懒加载**，且刻意与 [SettingsEntry] 分文件（见那里的说明）。
 *
 *   它 import 了 CodeMirror —— 聊天界面一个字节都用不到，
 *   而静态引入会把 230KB gzip 拖进首屏。
 *   状态在 zustand 里，所以入口（首屏 chunk）与对话框（懒加载 chunk）
 *   可以是两个包；挂载点必须在这里，因为入口自己不能引它。
 */
const SettingsDialog = lazy(() =>
  import("@/components/settings/settings-dialog").then((m) => ({ default: m.SettingsDialog })),
)

/**
 * ★ 不再需要 RuntimeProvider。
 *
 *   上一版这里包了一层 assistant-ui 的 runtime provider，那是整个「改不动」
 *   的根源：受控 threadId、乐观新线程、三套 adapter 契约、legacy runtime
 *   的生命周期，每一样都要先读源码才敢改。现在状态在 src/lib/store.ts，
 *   组件直接订阅，没有中间层。
 */
export default function App() {
  return (
    <>
      <Shell />
      {/* 始终挂载：开关状态在 store 里，条件挂载会让 ⌘, 在「关」时
          找不到组件（chunk 都不必下载 —— 那正是要的效果）。 */}
      <Suspense fallback={null}>
        <SettingsDialog />
      </Suspense>
    </>
  )
}
