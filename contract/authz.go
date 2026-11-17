package contract

import (
	"fmt"
	"strings"
)

// AuthorizationMode is the session's approval mode.
//
// ★ The mode answers exactly one question: **does this call need a human
//
//	decision?** It never answers "may this tool be used at all" — that is
//	the tier table and the agent's own `tools:` list, and only those two.
//
// ★ The names are the wire names. They are part of the contract with the
//
//	client, so they are spelled out rather than derived from the constants.
type AuthorizationMode string

const (
	// AuthzDefault is the tier table's own answer: an ask-tier tool asks.
	//
	// ★ It is the default of a session that never chose one, and it is what
	//	a context that carries no mode reads as — the safe answer is the one
	//	that asks.
	AuthzDefault AuthorizationMode = "default"

	// AuthzTrusted waives the nod for the writes that land inside the
	// trusted paths, and for the MCP tools a server declared as writers.
	//
	// ★ It does not widen the tool surface: a tool the agent may not use
	// stays out of its hands.
	AuthzTrusted AuthorizationMode = "trusted"

	// AuthzFull waives the nod for every ask-tier tool.
	//
	// ★ Switching to it needs an explicit confirmation, once per session.
	AuthzFull AuthorizationMode = "full"
)

// String returns the wire name of the mode.
func (m AuthorizationMode) String() string { return string(m) }

// ParseAuthorizationMode reads a mode off the wire.
//
// ★ An unknown name is an **error**, not a default: a name nobody recognises
//
//	must not silently become the safest one — the user would think they had
//	widened the mode, and the session would still ask.
func ParseAuthorizationMode(s string) (AuthorizationMode, error) {
	switch m := AuthorizationMode(strings.TrimSpace(s)); m {
	case AuthzDefault:
		return AuthzDefault, nil
	case AuthzTrusted:
		return AuthzTrusted, nil
	case AuthzFull:
		return AuthzFull, nil
	default:
		return "", fmt.Errorf("unknown authorization mode %q", s)
	}
}
