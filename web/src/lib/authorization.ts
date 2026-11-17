// AuthorizationMode is one of the three authorization levels a session can be
// in.
//
// ★ The names are the **wire's** names: the server parses them, so a client
//   that spells one differently sends a level the server refuses.
export type AuthorizationMode = "default" | "trusted" | "full"

export interface AuthorizationOption {
  mode: AuthorizationMode
  // label is what the interface says.
  label: string
  // title is the one-line explanation of what the level means.
  title: string
}

// AUTHORIZATION_OPTIONS are the three levels, in the order they are offered.
//
// ★ The order is fixed: it runs from "asks the most" to "asks the least", and
//   a list that reorders itself between two renders is a list the user has to
//   read again every time.
export const AUTHORIZATION_OPTIONS: readonly AuthorizationOption[] = [
  {
    mode: "default",
    label: "Default approval",
    title: "Tools ask before they run, as the configuration says",
  },
  {
    mode: "trusted",
    label: "Trusted paths",
    title:
      "Writes inside the project, the session workspace and the configuration do not ask",
  },
  {
    mode: "full",
    label: "Full authorization",
    title: "Every tool runs without asking",
  },
]

// authorizationMode reads a mode off the wire.
//
// ★ A server from before the modes knows nothing about them, and a level it
//   does not know must not be treated as one it does — an unknown name is
//   the default, and the default is what an old session behaved like anyway.
export function authorizationMode(value: unknown): AuthorizationMode {
  return value === "trusted" || value === "full" ? value : "default"
}

// authorizationOption is the option a mode is drawn from.
export function authorizationOption(mode: AuthorizationMode): AuthorizationOption {
  return AUTHORIZATION_OPTIONS.find((o) => o.mode === mode) ?? AUTHORIZATION_OPTIONS[0]
}
