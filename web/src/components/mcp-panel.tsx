import { RefreshCwIcon } from "lucide-react"
import { useEffect, useState } from "react"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { api, type MCPServerView } from "@/lib/api"
import { cn } from "@/lib/utils"

export interface MCPPanelProps {
  open: boolean
  onClose(): void
  /** The agent the next turn runs as. Its servers are the ones shown. */
  agentId: string
}

/**
 * The MCP servers of one agent.
 *
 * ★ It fetches when it opens, every time: the status is live state, and the
 *   tab's caches (the command table, the agents list) are not the place for
 *   it. The Refresh button re-reads it, because the user fixes a server and
 *   wants to see the fix without closing the dialog.
 */
export function MCPPanel({ open, onClose, agentId }: MCPPanelProps) {
  const [servers, setServers] = useState<MCPServerView[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  useEffect(() => {
    if (!open) return
    let alive = true
    void api
      .listMCPServers(agentId)
      .then((res) => {
        if (!alive) return
        setServers(res.servers)
        setError(null)
      })
      .catch((e: unknown) => {
        if (!alive) return
        setServers([])
        setError((e as Error).message)
      })
    return () => {
      alive = false
    }
  }, [open, agentId])

  const refresh = () => {
    setRefreshing(true)
    void api
      .listMCPServers(agentId)
      .then((res) => {
        setServers(res.servers)
        setError(null)
      })
      .catch((e: unknown) => {
        setServers([])
        setError((e as Error).message)
      })
      .finally(() => setRefreshing(false))
  }

  return (
    <Dialog open={open} onOpenChange={(v) => (!v ? onClose() : undefined)}>
      <DialogContent data-testid="mcp-panel" className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>MCP servers</DialogTitle>
          <DialogDescription>
            The servers of agent <code>{agentId}</code> — what it would get from them.
          </DialogDescription>
        </DialogHeader>

        <div className="flex items-center justify-between">
          <span className="text-muted-foreground text-xs">
            {servers === null ? "Asking the servers…" : "Live state, read on open."}
          </span>
          <Button
            variant="outline"
            size="sm"
            data-testid="mcp-refresh"
            onClick={refresh}
          >
            <RefreshCwIcon className={cn("size-3.5", refreshing && "animate-spin")} />
            Refresh
          </Button>
        </div>

        {error ? (
          <p data-testid="mcp-panel-error" className="text-destructive text-xs">
            {error}
          </p>
        ) : null}

        {servers !== null && servers.length === 0 ? (
          <p data-testid="mcp-panel-empty" className="text-muted-foreground text-sm">
            No MCP servers are configured
          </p>
        ) : null}

        <div className="max-h-80 space-y-3 overflow-y-auto">
          {(servers ?? []).map((srv) => (
            <section
              key={srv.name}
              data-testid="mcp-server"
              data-status={srv.status}
              className="rounded-md border p-3"
            >
              <header className="flex items-baseline gap-2">
                <span className="font-medium">{srv.name}</span>
                <span className="text-muted-foreground text-xs">{srv.transport}</span>
                <span className="text-muted-foreground text-xs">— {srv.status}</span>
              </header>

              {srv.error ? (
                <p data-testid="mcp-server-error" className="text-destructive mt-1 text-xs">
                  {srv.error}
                </p>
              ) : null}

              {srv.tools.length === 0 ? null : (
                <table className="mt-2 w-full text-xs">
                  <thead>
                    <tr className="text-muted-foreground text-left">
                      <th className="font-normal">Tool</th>
                      <th className="font-normal">Exposed name</th>
                      <th className="font-normal">Tier</th>
                      <th className="font-normal">State</th>
                    </tr>
                  </thead>
                  <tbody>
                    {srv.tools.map((t) => (
                      <tr key={`${srv.name}/${t.exposedName}/${t.name}`} data-testid="mcp-tool">
                        <td className="py-0.5 pr-2">{t.name}</td>
                        <td className="py-0.5 pr-2 font-mono">{t.exposedName}</td>
                        <td className="py-0.5 pr-2">{t.tier}</td>
                        <td className="py-0.5">
                          {t.status}
                          {t.reason ? <span className="text-muted-foreground"> ({t.reason})</span> : null}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </section>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
