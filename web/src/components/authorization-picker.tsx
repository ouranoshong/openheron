import { CheckIcon, ShieldIcon } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ApiError } from "@/lib/api"
import {
  AUTHORIZATION_OPTIONS,
  authorizationOption,
  type AuthorizationMode,
} from "@/lib/authorization"
import {
  selectAuthorizationConfirmed,
  selectAuthorizationMode,
  useStore,
} from "@/lib/store"
import { cn } from "@/lib/utils"

// describe turns a refused call into the **server's** sentence.
//
// ★ The server owns the rules, so the reason on the screen is the one the
// server wrote — not a second copy of them on this side that could drift.
function describe(e: unknown): string {
  if (e instanceof ApiError) return e.message
  return (e as Error).message ?? String(e)
}

/**
 * AuthorizationPicker is the session's authorization mode, on the line under
 * the input box.
 *
 * ★ It lives **before the project button** and in the same action node, and
 * it never changes its place: React reconciles children by index, so an
 * entry that shifts is an entry that is unmounted and mounted again — and the
 * dialog that lives in it closes when it is.
 *
 * ★ The mode is the **server's** answer. The picker never writes it on its
 * own: a refused change leaves the old mode standing, and the label on the
 * button is the one the server holds.
 *
 * ★ Without a conversation there is nothing to change yet, and the mode is
 * **staged** — it travels with the conversation that is about to start, the
 * same way the agent choice does. ★ The button is not disabled there: a
 * choice nobody can make is a choice the welcome page cannot express.
 *
 * ★ Full authorization is the one level that takes the human out of the
 * loop, and the server asks for an explicit confirmation before it accepts
 * it (409 `confirmation_required`) — so the box is shown when the server
 * says so, and never on the client's own account.
 *
 * ★ On the welcome page there is no server to say so, so the client asks and
 *   the server checks the answer when the conversation is born.
 */
export function AuthorizationPicker() {
  const mode = useStore(selectAuthorizationMode)
  const confirmed = useStore(selectAuthorizationConfirmed)
  const setMode = useStore((s) => s.setAuthorizationMode)
  const activeId = useStore((s) => s.activeId)

  const [open, setOpen] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | undefined>()
  const [confirmError, setConfirmError] = useState<string | undefined>()

  const option = authorizationOption(mode)

  const choose = async (next: AuthorizationMode) => {
    if (busy) return
    // ★ Without a conversation there is nobody to ask: the server's 409 is
    //   the handshake for a conversation that exists, and the welcome page
    //   has none. ⇒ The confirmation is asked here, and the server still
    //   demands the flag when the conversation is born.
    //
    //   ★ A person who already confirmed does not confirm twice — the same
    //   rule the session follows, where the confirmation is a fact the
    //   server holds.
    if (!activeId && next === "full" && !confirmed) {
      setOpen(false)
      setConfirmOpen(true)
      return
    }
    setBusy(true)
    setError(undefined)
    try {
      await setMode(next)
      setOpen(false)
    } catch (e) {
      // ★ The server refuses `full` until this session confirmed it, and it
      //   says so with 409 `confirmation_required`. ⇒ The confirmation box
      //   is the answer to that, not another retry.
      if (e instanceof ApiError && e.code === "confirmation_required") {
        setOpen(false)
        setConfirmOpen(true)
        return
      }
      // ★ The mode on the button is still the server's old one — nothing was
      //   written on the client — and the reason is the server's sentence.
      setError(describe(e))
    } finally {
      setBusy(false)
    }
  }

  const confirmFull = async () => {
    if (busy) return
    setBusy(true)
    setConfirmError(undefined)
    try {
      // ★ With a conversation the server does the handshake and holds the
      //   fact; without one the client asks and the flag travels with the
      //   creation request — and the server checks it there too.
      await setMode("full", true)
      setConfirmOpen(false)
    } catch (e) {
      setConfirmError(describe(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    // ★ One wrapper around the button and the two dialogs. ★ React aligns
    //   children by index, and a node that shifts is a node that is unmounted
    //   and mounted again — the open state of a dialog lives in exactly that
    //   kind of node. ⇒ The three are one entry in the action slot, and
    //   nothing ever joins them beside.
    <span data-testid="authorization-picker" className="inline-flex items-center gap-1">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="hover:text-foreground h-6 gap-1 px-1.5 text-[11px]"
        onClick={() => setOpen(true)}
        data-testid="authorization-button"
        aria-label={`Authorization: ${option.label}`}
        title={
          activeId
            ? option.title
            : "The mode belongs to the conversation that is about to start."
        }
      >
        <ShieldIcon aria-hidden="true" className="size-3" />
        {/* ★ The color is a token, and the full level wears the warning one:
            the user has to see at a glance that nobody is watching anymore. */}
        <span
          data-testid="authorization-label"
          className={mode === "full" ? "text-warning" : "text-muted-foreground"}
        >
          {option.label}
        </span>
      </Button>

      <Dialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v)
          if (!v) setError(undefined)
        }}
      >
        <DialogContent data-testid="authorization-picker-dialog" className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Authorization mode</DialogTitle>
            {/* ★ The next-turn rule lives **here**, and not on the button: it
                is always true, so it needs no badge that appears only while a
                turn is running — and a badge that comes and goes would make
                the button's line of text jump. ★ Without a conversation
                there is no running turn: the mode is simply carried into the
                conversation that is about to start. */}
            <DialogDescription data-testid="authorization-picker-description">
              {activeId
                ? "The mode belongs to this conversation. A running turn keeps the mode it started with, so a change you make now takes effect on the next turn."
                : "The mode belongs to the conversation that is about to start. It is sent when that conversation is created, so the first turn already runs in it."}
            </DialogDescription>
          </DialogHeader>

          <ul
            role="listbox"
            aria-label="Authorization modes"
            data-testid="authorization-options"
            className="max-h-72 overflow-y-auto"
          >
            {AUTHORIZATION_OPTIONS.map((opt) => {
              const current = opt.mode === mode
              return (
                <li key={opt.mode} role="presentation">
                  <button
                    type="button"
                    role="option"
                    aria-selected={current}
                    data-testid="authorization-option"
                    data-mode={opt.mode}
                    disabled={busy}
                    onClick={() => void choose(opt.mode)}
                    className={cn(
                      "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm disabled:opacity-40",
                      current && "bg-accent",
                    )}
                  >
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1 font-medium">
                        <span className={opt.mode === "full" ? "text-warning" : undefined}>
                          {opt.label}
                        </span>
                        {current ? <CheckIcon className="size-3.5" aria-label="Current mode" /> : null}
                      </span>
                      <span className="text-muted-foreground block truncate text-xs">{opt.title}</span>
                      {opt.mode === "full" && confirmed ? (
                        <span className="text-muted-foreground block text-xs">
                          Already confirmed for this conversation — it will not ask again.
                        </span>
                      ) : null}
                    </span>
                  </button>
                </li>
              )
            })}
          </ul>

          {error ? (
            <p data-testid="authorization-error" className="text-destructive text-xs">
              {error}
            </p>
          ) : null}
        </DialogContent>
      </Dialog>

      <Dialog
        open={confirmOpen}
        onOpenChange={(v) => {
          setConfirmOpen(v)
          if (!v) setConfirmError(undefined)
        }}
      >
        <DialogContent
          data-testid="authorization-confirm"
          onEscapeKeyDown={(e) => {
            // ★ Escape has to close this box, and Radix listens for it on the
            //   document in the **capture** phase — so a handler in the tree
            //   is already too late to say anything. ⇒ `onEscapeKeyDown` is
            //   the one place that can still answer, and the answer is yes.
            e.preventDefault()
            setConfirmOpen(false)
          }}
        >
          <DialogHeader>
            <DialogTitle>Full authorization</DialogTitle>
            <DialogDescription>
              Full authorization — every tool runs without asking, including shell commands and
              configuration changes.
            </DialogDescription>
          </DialogHeader>

          {confirmError ? (
            <p data-testid="authorization-confirm-error" className="text-destructive text-xs">
              {confirmError}
            </p>
          ) : null}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button type="button" onClick={() => void confirmFull()}>
              Enable full authorization
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </span>
  )
}
