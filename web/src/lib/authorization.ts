import type { Translate } from "./i18n"

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

// authorizationOptions are the three levels, in the order they are offered.
//
// ★ The order is fixed: it runs from "asks the most" to "asks the least", and
//   a list that reorders itself between two renders is a list the user has to
//   read again every time.
//
// ★ The translator comes **in**: this module is pure, and a pure function
//   cannot read the language on its own.
export function authorizationOptions(t: Translate): AuthorizationOption[] {
  return [
    {
      mode: "default",
      label: t("authorization.label.default"),
      title: t("authorization.title.default"),
    },
    {
      mode: "trusted",
      label: t("authorization.label.trusted"),
      title: t("authorization.title.trusted"),
    },
    {
      mode: "full",
      label: t("authorization.label.full"),
      title: t("authorization.title.full"),
    },
  ]
}

// authorizationMode reads a mode off the wire.
//
// ★ A server from before the modes knows nothing about them, and a level it
//   does not know must not be treated as one it does — an unknown name is
//   the default, and the default is what an old session behaved like anyway.
export function authorizationMode(value: unknown): AuthorizationMode {
  return value === "trusted" || value === "full" ? value : "default"
}

// authorizationOption is the option a mode is drawn from.
export function authorizationOption(
  mode: AuthorizationMode,
  t: Translate,
): AuthorizationOption {
  const options = authorizationOptions(t)
  return options.find((o) => o.mode === mode) ?? options[0]
}
