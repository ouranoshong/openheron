package build

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

type Tier string

const (
	TierAllow Tier = "allow"
	TierAsk   Tier = "ask"
	TierDeny  Tier = "deny"
)

// Policy is the assembled tier table: a name → a tier.
//
// ★ The default is **ask**, and the exceptions are named. ★ There is no
//
//	global deny any more: "not exposed" is said in exactly two places —
//	a file's own `allows:` list, and an agent's own `disabled_tools`.
//
// ★ The table is assembled from the **configuration**, so the tier of a
//
//	tool is answerable before its server is even up.
type Policy struct {
	// Allow lists the tools that run without a nod.
	Allow []string

	allow map[string]bool
}

// NewPolicy builds the table from the names that run without a nod.
func NewPolicy(allow []string) Policy {
	p := Policy{
		Allow: append([]string(nil), allow...),
		allow: make(map[string]bool, len(allow)),
	}
	for _, n := range allow {
		p.allow[n] = true
	}
	return p
}

// TierOf answers the tier of one tool name.
//
// ★ The default is ask, because the cost of guessing wrong is asymmetric:
//
//	one extra click against a destructive call nobody saw.
func (p Policy) TierOf(name string) Tier {
	if p.allow[name] {
		return TierAllow
	}
	return TierAsk
}

type Layer string

type boundary struct {
	policy Policy
	table  *Table
}

func newBoundary(policy Policy, table *Table) boundary {
	return boundary{policy: policy, table: table}
}

func (b boundary) Policy() Policy { return b.policy }

func (b boundary) Table() *Table { return b.table }

type Table struct {
	mu sync.RWMutex

	Disable []string
	Name    string

	disabled map[string]bool
	observed map[string]bool

	// denyMCP holds the namespace prefixes of the servers this agent does
	// not use, and disablePrefix the trailing-* patterns of the agent's
	// disabled_tools.
	//
	// ★ Both are prefixes and never parsed names: the composed name is
	//   never split back into its two halves, and a server name may not
	//   contain a run of underscores — so "mcp__github__" cannot reach
	//   "mcp__github_x__".
	denyMCP       []string
	disablePrefix []string
}

func newTable(name string, disable []string) *Table {
	t := &Table{
		Name:     name,
		Disable:  append([]string(nil), disable...),
		disabled: make(map[string]bool, len(disable)),
	}
	for _, n := range disable {
		prefix, isPattern, err := decl.ParseToolNamePattern(n)
		if err != nil {
			// ★ The loader refuses these shapes, so a pattern that does
			//   not parse can only come from a caller that did not go
			//   through the loader. Treat it as an exact name rather than
			//   dropping the rule silently.
			t.disabled[n] = true
			continue
		}
		if isPattern {
			t.disablePrefix = append(t.disablePrefix, prefix)
			continue
		}
		t.disabled[prefix] = true
	}
	return t
}

// denyMCPNamespaces denies the servers this agent does not use.
//
// ★ The narrowing lands here, in the agent's table, exactly where
//
//	disabled_tools already narrows the tools: the tool set is one set for
//	the whole snapshot, and the model never sees a tool the table removes.
func (t *Table) denyMCPNamespaces(list *[]string, servers []string) {
	if t == nil || list == nil {
		// ★ An absent list means every configured server.
		return
	}
	allowed := make(map[string]bool, len(*list))
	for _, n := range *list {
		allowed[n] = true
	}
	for _, s := range servers {
		if allowed[s] {
			continue
		}
		t.denyMCP = append(t.denyMCP, decl.MCPToolPrefix+s+decl.MCPSeparator)
	}
}

func (t *Table) Blocked(toolName string) Layer {
	if t == nil {
		return LayerNone
	}
	for _, p := range t.denyMCP {
		if strings.HasPrefix(toolName, p) {
			return LayerMCPScope
		}
	}
	if t.disabled[toolName] {
		return LayerDisable
	}
	for _, p := range t.disablePrefix {
		if strings.HasPrefix(toolName, p) {
			return LayerDisable
		}
	}
	return LayerNone
}

func (t *Table) Filter(tools []tool.BaseTool) ([]tool.BaseTool, map[Layer]int) {
	if t == nil {
		return tools, nil
	}
	t.observe(tools)

	if len(t.disabled) == 0 && len(t.denyMCP) == 0 {
		return tools, nil
	}
	kept := make([]tool.BaseTool, 0, len(tools))
	removed := map[Layer]int{}
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			kept = append(kept, tl)
			continue
		}
		if l := t.Blocked(info.Name); l != LayerNone {
			removed[l]++
			continue
		}
		kept = append(kept, tl)
	}
	return kept, removed
}

func (t *Table) observe(tools []tool.BaseTool) {
	seen := make(map[string]bool, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		seen[info.Name] = true
	}
	t.mu.Lock()
	t.observed = seen
	t.mu.Unlock()
}

func (t *Table) Observed() (names []string, ok bool) {
	if t == nil {
		return nil, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.observed == nil {
		return nil, false
	}
	out := make([]string, 0, len(t.observed))
	for n := range t.observed {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, true
}

const (
	LayerNone    Layer = ""
	LayerDisable Layer = "disable"
	// LayerMCPScope is the agent's own mcps: list: the server is not
	// there at all, so the finer rules would have said nothing about it.
	LayerMCPScope Layer = "mcps"
)
