package build

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/authz"
	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// scope answers the question the gate asks in the trusted mode: **does this
// call write inside one of the trusted paths?**
//
// ★ The trusted paths are the code's own facts, not a list the user writes:
// the round's project root, the session's own workspace, and the
// configuration root. The mode says "trust these"; these are what "these"
// are.
//
// ★ The scope never decides **whether a tool may be used** — that is the
// tier table and the agent's own `tools:` list. It only decides whether a
// call that would have asked has to ask.
type scope struct {
	// local is the filesystem backend: it owns the path resolution and the
	// containment walk, so the answer here is the backend's own.
	local *fsx.Local
	// configRoot is the configuration root, absolute. Empty when the process
	// runs without a configuration service.
	configRoot string
	// trustedWriters are the MCP tools a server declared as writers, in
	// their exposed names (`mcp__<server>__<tool>`).
	trustedWriters map[string]bool
}

// trusted reports whether this call is a write the trusted mode waives.
//
// ★ Three families, and they do not overlap:
//
//   - **MCP tools**: the server's own file declared the tool a writer
//     (`mcps/<server>.yaml`'s `writes:`). The gate cannot read an MCP
//     tool's arguments — they are the server's own — so the declaration is
//     the answer.
//   - **The filesystem tools**: the target path comes from the call's
//     arguments and is resolved against the round's root.
//   - **The configuration tools**: the path is relative to the
//     configuration root, so it is joined onto that root.
//
// ★ Everything else asks. A call whose path cannot be read, a tool nobody
// declared, a path that lands outside every trusted path — all of them ask.
// ⇒ **fail-closed**: a write the scope cannot vouch for is a write that
// asks.
func (s scope) trusted(ctx context.Context, name, args string) bool {
	if s.local == nil {
		return false
	}
	// ★ The MCP family is a declaration, so it is asked about the tool and
	//   not about its arguments.
	if s.trustedWriters[name] {
		return true
	}
	switch name {
	case decl.ToolNameWriteFile, decl.ToolNameEditFile:
		p := pathArgument(args, "file_path")
		if p == "" {
			return false
		}
		abs, err := s.local.AbsolutePath(ctx, p)
		if err != nil {
			return false
		}
		for _, root := range s.roots(ctx) {
			if s.local.InsideRoot(ctx, root, abs) {
				return true
			}
		}
		return false
	case decl.ToolNameConfigWrite:
		rel := pathArgument(args, "path")
		if rel == "" || s.configRoot == "" {
			return false
		}
		// ★ The configuration tools speak in paths **relative to the
		//   configuration root** — not to the project root. Joining them
		//   onto the wrong base would answer about a different file.
		abs := filepath.Join(s.configRoot, filepath.FromSlash(rel))
		return s.local.InsideRoot(ctx, s.configRoot, abs)
	}
	return false
}

// roots are the trusted paths of this round.
func (s scope) roots(ctx context.Context) []string {
	out := make([]string, 0, 3)
	if root := fsx.ProjectRoot(ctx); root != "" {
		out = append(out, root)
	}
	if ws := authz.SessionWorkspace(ctx); ws != "" {
		out = append(out, ws)
	}
	if s.configRoot != "" {
		out = append(out, s.configRoot)
	}
	return out
}

// pathArgument reads one argument out of the tool call's arguments.
//
// ★ An empty result means "the call did not carry that argument" — and the
//
//	caller treats it as "ask", never as "no target".
func pathArgument(args, key string) string {
	if strings.TrimSpace(args) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}
