
import { useEffect, useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"
import { envState, removeGroupMessage, sourceLabel, statusOf } from "@/lib/secrets"
import {
  selectSecretError,
  selectSecretGroups,
  selectSelectedGroup,
  selectSelectedSecret,
  useSecrets,
} from "@/lib/secrets-store"
import type { SecretGroup, SecretItem } from "@/lib/secrets-api"

/**
 * The Secrets tab of the Settings dialog.
 *
 * ★ It is **not** a third branch of the file tree. That tree is built from
 *   the paths the server sends, and a secret has no path: it lives in the
 *   keyring under a name, and its name is derived from the entity's name.
 *   Putting it in the tree would break the model the tree already has.
 *
 * ★ It is also not a second entry in the sidebar: a secret is **configuration**,
 *   and the configuration is in one place.
 *
 * ★ The value is **write-only**. The panel shows where a key is and whether
 *   it is in force; it never shows the key, not even its length. The input is
 *   a password field, it is emptied after every write, and the DOM never
 *   carries the value.
 */
export function SecretsPanel() {
  const groups = useSecrets(selectSecretGroups)
  const loading = useSecrets((s) => s.loading)
  const error = useSecrets(selectSecretError)
  const selected = useSecrets(selectSelectedSecret)
  const selectedGroup = useSecrets(selectSelectedGroup)
  const load = useSecrets((s) => s.load)
  const select = useSecrets((s) => s.select)
  const removeGroup = useSecrets((s) => s.removeGroup)
  const busy = useSecrets((s) => s.busy)

  const [confirming, setConfirming] = useState<SecretGroup | undefined>(undefined)

  // ★ It reads the state when the tab **opens**, every time: the state is
  //   live (a key can be stored from the command line while the dialog is
  //   open), and the tree's caches are not the place for it.
  useEffect(() => {
    void load()
  }, [load])

  return (
    <div className="flex min-h-0 flex-1 gap-4" data-testid="secrets-panel">
      <div className="flex w-56 shrink-0 flex-col gap-3 overflow-y-auto">
        {groups.map((group) => (
          <section key={group.id} className="flex flex-col gap-1">
            <h3 className="text-muted-foreground px-2 text-[11px] font-medium tracking-wide uppercase">
              {group.label}
            </h3>
            <ul className="flex flex-col gap-0.5">
              {group.items.map((item) => (
                <li key={`${group.id}:${item.name}`}>
                  <button
                    type="button"
                    data-testid={`secrets-item-${group.id}-${item.name}`}
                    onClick={() => select(`${group.id}:${item.name}`)}
                    aria-current={selected?.name === item.name && selectedGroup?.id === group.id}
                    className={cn(
                      "hover:bg-accent flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs",
                      selected?.name === item.name && selectedGroup?.id === group.id && "bg-accent",
                    )}
                  >
                    <StatusDot item={item} />
                    <span className="font-mono">{item.name}</span>
                    {/* ★ The state is a **word** as well as a dot: a colored
                        dot is a shape, and a reader who cannot see the color
                        is left with nothing. */}
                    <span className="text-muted-foreground ml-auto text-[10px]">
                      {statusOf(item).label}
                    </span>
                  </button>
                </li>
              ))}
              {group.items.length === 0 ? (
                <li className="text-muted-foreground px-2 py-1.5 text-xs">Nothing here yet</li>
              ) : null}
            </ul>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="self-start text-xs"
              data-testid={`secrets-remove-group-${group.id}`}
              onClick={() => setConfirming(group)}
            >
              Remove all {group.label.toLowerCase()}
            </Button>
          </section>
        ))}
        {groups.length === 0 && !loading ? (
          <p className="text-muted-foreground px-2 py-1.5 text-xs">No providers or MCP servers yet</p>
        ) : null}
      </div>

      <div className="flex min-w-0 flex-1 flex-col gap-3">
        {error ? (
          <p className="text-destructive text-sm" role="alert" data-testid="secrets-error">
            {error}
          </p>
        ) : null}

        {selected && selectedGroup ? (
          <SecretDetail group={selectedGroup} item={selected} busy={busy === `${selectedGroup.id}:${selected.name}`} />
        ) : (
          <div className="text-muted-foreground grid flex-1 place-items-center text-sm">
            {loading ? "Loading…" : "Select a secret"}
          </div>
        )}
      </div>

      <RemoveGroupDialog
        group={confirming}
        onCancel={() => setConfirming(undefined)}
        onConfirm={(group) => {
          setConfirming(undefined)
          void removeGroup(group.id)
        }}
      />
    </div>
  )
}

/**
 * One secret's detail: where it is, which one is in force, and the two
 * actions.
 *
 * ★ "Stored" and "in effect" are two rows and not one, because they are two
 *   questions. They disagree exactly when the environment overrides the
 *   keyring, and that disagreement is the whole trap of the design.
 */
function SecretDetail({
  group,
  item,
  busy,
}: {
  group: SecretGroup
  item: SecretItem
  busy: boolean
}) {
  const set = useSecrets((s) => s.set)
  const remove = useSecrets((s) => s.remove)
  const [value, setValue] = useState("")

  const status = statusOf(item)

  const submit = () => {
    if (value.trim() === "") return
    void set(group.id, item.name, value).then(() => {
      // ★ The value leaves the component the moment it is stored: it is in
      //   the keyring now, and the only copy the browser should hold is none.
      setValue("")
    })
  }

  return (
    <div className="flex flex-col gap-3" data-testid="secrets-detail" data-name={item.name}>
      <header className="flex items-baseline gap-2">
        <h2 className="text-base font-medium">{item.name}</h2>
        <StatusDot item={item} />
        <span className="text-muted-foreground text-xs">{status.label}</span>
      </header>

      <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt>Stored in</dt>
        <dd className="text-foreground font-mono" data-testid="secrets-source">
          {item.stored ? item.source : "—"}
        </dd>
        <dt>In effect</dt>
        <dd className="text-foreground" data-testid="secrets-effective">
          {sourceLabel(item.effective)}
        </dd>
        <dt>Environment</dt>
        <dd className="text-foreground font-mono">
          {item.envVar} ({envState(item)})
        </dd>
      </dl>

      {status.warning ? (
        <p
          className="rounded-md border px-3 py-2 text-xs"
          data-testid="secrets-warning"
          style={{ borderColor: "var(--warning)", color: "var(--warning-foreground)" }}
        >
          {status.warning}
        </p>
      ) : null}

      <div className="flex items-center gap-2">
        <Input
          type="password"
          autoComplete="off"
          spellCheck={false}
          data-testid="secrets-value"
          placeholder="Paste the key"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit()
          }}
          className="max-w-md"
        />
        <Button
          type="button"
          size="sm"
          data-testid="secrets-set"
          disabled={busy || value.trim() === ""}
          onClick={submit}
        >
          Set value
        </Button>
        <Button
          type="button"
          variant="destructive"
          size="sm"
          data-testid="secrets-remove"
          disabled={busy || !item.stored}
          onClick={() => void remove(group.id, item.name)}
        >
          Remove
        </Button>
      </div>

      <p className="text-muted-foreground text-xs">
        The value goes to the keyring only — it is never written into the configuration, and it is
        never shown here again.
      </p>
    </div>
  )
}

/**
 * The state dot.
 *
 * ★ A **token**, never a literal: the light and the dark palettes are the
 *   same token set with two values, so a color that exists in one exists in
 *   the other.
 */
function StatusDot({ item }: { item: SecretItem }) {
  const color = item.shadowed
    ? "var(--warning)"
    : item.stored
      ? "var(--success)"
      : "var(--muted-foreground)"
  return (
    <span
      aria-hidden
      data-testid="secrets-dot"
      className="inline-block size-2 shrink-0 rounded-full"
      style={{ backgroundColor: color }}
    />
  )
}

/**
 * The confirmation for removing a whole group.
 *
 * ★ It names the count **and** the scope. The two groups are separate
 *   namespaces, so "remove everything" is a scoped operation and the user
 *   has to be told which scope they picked.
 */
function RemoveGroupDialog({
  group,
  onCancel,
  onConfirm,
}: {
  group?: SecretGroup
  onCancel(): void
  onConfirm(group: SecretGroup): void
}) {
  return (
    <Dialog open={group !== undefined} onOpenChange={(v) => (!v ? onCancel() : undefined)}>
      <DialogContent className="sm:max-w-md" data-testid="secrets-remove-dialog">
        <DialogHeader>
          <DialogTitle>Remove the stored secrets?</DialogTitle>
          <DialogDescription>{group ? removeGroupMessage(group) : ""}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onCancel} data-testid="secrets-remove-cancel">
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => group && onConfirm(group)}
            data-testid="secrets-remove-confirm"
          >
            Remove
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
