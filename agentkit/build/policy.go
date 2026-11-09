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
)

// Policy is the assembled tier table: a name → a tier.
//
// ★ The default is **ask**, and the exceptions are named. ★ There is no
//
//	global deny any more: "not exposed" is said in exactly two places —
//	a file's own `allows:` list, and an agent's own `tools` list.
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

	// Load is the agent's allow-list: the tools it may use.
	//
	// ★ The default is **none**. A tool the list does not name is not
	//   this agent's tool — the tool set is one set for the whole
	//   snapshot, and the model never sees a tool the table removes.
	Load []string
	Name string

	loaded   map[string]bool
	observed map[string]bool

	// loadPrefix holds the trailing-* patterns of the agent's tools list.
	//
	// ★ They are prefixes and never parsed names: the composed name is
	//   never split back into its two halves, and a server name may not
	//   contain a run of underscores — so "mcp__github__" cannot reach
	//   "mcp__github_x__".
	loadPrefix []string
}

func newTable(name string, load []string) *Table {
	t := &Table{
		Name:   name,
		Load:   append([]string(nil), load...),
		loaded: make(map[string]bool, len(load)),
	}
	for _, n := range load {
		prefix, isPattern, err := decl.ParseToolNamePattern(n)
		if err != nil {
			// ★ The loader refuses these shapes, so a pattern that does
			//   not parse can only come from a caller that did not go
			//   through the loader. Treat it as an exact name rather than
			//   dropping the rule silently.
			t.loaded[n] = true
			continue
		}
		if isPattern {
			t.loadPrefix = append(t.loadPrefix, prefix)
			continue
		}
		t.loaded[prefix] = true
	}
	return t
}

// Blocked reports whether the agent may not have this tool.
//
// ★ The list is an **allow-list**, so the answer is the other way round
// from the blacklist it replaced: a tool the list does not name is
// blocked, and one it names is not.
func (t *Table) Blocked(toolName string) Layer {
	if t == nil {
		return LayerNone
	}
	if t.loaded[toolName] {
		return LayerNone
	}
	for _, p := range t.loadPrefix {
		if strings.HasPrefix(toolName, p) {
			return LayerNone
		}
	}
	return LayerNotLoaded
}

func (t *Table) Filter(tools []tool.BaseTool) ([]tool.BaseTool, map[Layer]int) {
	if t == nil {
		return tools, nil
	}
	t.observe(tools)

	// ★ There is no "nothing to do" shortcut any more: the list is an
	//   allow-list, so an **empty** list means every tool is removed. The
	//   shortcut the blacklist could afford ("no rules, keep everything")
	//   is exactly the case the allow-list must not have.
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
	LayerNone Layer = ""
	// LayerNotLoaded is the agent's own tools: list: the tool is not in
	// it, so the agent does not have the tool at all.
	//
	// ★ There is **one** reason now. The global deny list is gone with the
	//   partition, and the MCP scope layer went with it: a server the
	//   agent does not mount has no tools to block, and one it does mount
	//   but does not name is blocked by this very list.
	LayerNotLoaded Layer = "not_loaded"
)
