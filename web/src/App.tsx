import { lazy, Suspense } from "react"

import { Shell } from "@/app/shell"

const SettingsDialog = lazy(() =>
  import("@/components/settings/settings-dialog").then((m) => ({ default: m.SettingsDialog })),
)

export default function App() {
  return (
    <>
      <Shell />
      <Suspense fallback={null}>
        <SettingsDialog />
      </Suspense>
    </>
  )
}
