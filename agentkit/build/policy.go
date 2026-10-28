package build

import (
	"context"
	"slices"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components/tool"
)

type Tier string

const (
	TierAllow Tier = "allow"
	TierAsk   Tier = "ask"
	TierDeny  Tier = "deny"
)

type Policy struct {
	Ask  []string
	Deny []string

	ask map[string]bool
}

func NewPolicy(ask, deny []string) Policy {
	p := Policy{
		Ask:  append([]string(nil), ask...),
		Deny: append([]string(nil), deny...),
		ask:  make(map[string]bool, len(ask)),
	}
	for _, n := range ask {
		p.ask[n] = true
	}
	return p
}

func (p Policy) TierOf(name string) Tier {
	switch {
	case slices.Contains(p.Deny, name):
		return TierDeny
	case p.ask[name]:
		return TierAsk
	default:
		return TierAllow
	}
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

	Deny    []string
	Disable []string
	Name    string

	denied   map[string]bool
	disabled map[string]bool
	observed map[string]bool
}

func newTable(name string, deny, disable []string) *Table {
	t := &Table{
		Name:     name,
		Deny:     append([]string(nil), deny...),
		Disable:  append([]string(nil), disable...),
		denied:   make(map[string]bool, len(deny)),
		disabled: make(map[string]bool, len(disable)),
	}
	for _, n := range deny {
		t.denied[n] = true
	}
	for _, n := range disable {
		t.disabled[n] = true
	}
	return t
}

func (t *Table) Blocked(toolName string) Layer {
	if t == nil {
		return LayerNone
	}
	if t.denied[toolName] {
		return LayerDeny
	}
	if t.disabled[toolName] {
		return LayerDisable
	}
	return LayerNone
}

func (t *Table) Filter(tools []tool.BaseTool) ([]tool.BaseTool, map[Layer]int) {
	if t == nil {
		return tools, nil
	}
	t.observe(tools)

	if len(t.denied) == 0 && len(t.disabled) == 0 {
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
	LayerDeny    Layer = "deny"
	LayerDisable Layer = "disable"
)
