package build

import (
	"sort"
	"strings"
	"unicode"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

type ToolVerdict struct {
	Name          string
	Tier          Tier
	Blocked       Layer
	Present       bool
	ObservedKnown bool
	Reason        string
}

func (v ToolVerdict) Reached() bool { return v.Blocked == LayerNone }

type Observed struct {
	Names []string
	Known bool
}

func (o Observed) Has(name string) bool {
	for _, n := range o.Names {
		if n == name {
			return true
		}
	}
	return false
}

func reasonOf(l Layer) string {
	switch l {
	case LayerNotLoaded:
		return "tools"
	case LayerNotMounted:
		// ★ The key that decides it: an MCP tool is not in the agent's
		//   tools: list, and never was — it is in the servers it mounts.
		return "mcps"
	default:
		return ""
	}
}

func (b *Builder) EffectiveTools(agentID string) []ToolVerdict {
	snap := b.currentSnapshot()
	if snap == nil {
		return nil
	}
	policy, table := snap.agentSpec.policy, tableOfAgent(snap, agentID)
	if table == nil {
		return nil
	}

	obs := observedOf(table)
	boundary := newBoundary(policy, table)
	names := toolNameUniverse(boundary, snap.cfg.EnabledToolNames(), nil, obs)
	out := make([]ToolVerdict, 0, len(names))
	for _, n := range names {
		out = append(out, verdictOf(boundary, n, obs))
	}
	return out
}

func (b *Builder) RuntimeTools(agentID string) Observed {
	snap := b.currentSnapshot()
	if snap == nil {
		return Observed{}
	}
	table := tableOfAgent(snap, agentID)
	if table == nil {
		return Observed{}
	}
	return observedOf(table)
}

func tableOfAgent(snap *snapshot, agentID string) *Table {
	if agentID == "" {
		return snap.tableFor(snap.agentSpec.defaultAgent)
	}
	if t := snap.tableFor(agentID); t != nil {
		return t
	}
	return snap.tableFor(snap.agentSpec.defaultAgent)
}

func observedOf(t *Table) Observed {
	names, ok := t.Observed()
	return Observed{Names: names, Known: ok}
}

func (b *Builder) PolicyTools() []ToolVerdict {
	snap := b.currentSnapshot()
	if snap == nil {
		return nil
	}
	policy, rootTable, subs := snap.agentSpec.policy, snap.tableFor(snap.agentSpec.defaultAgent), snap.subs()
	obs := observedOf(rootTable)
	names := toolNameUniverse(newBoundary(policy, rootTable), snap.cfg.EnabledToolNames(), subs, obs)

	// ★ The policy view carries **no agent's narrowing**: it is the tier
	//   table on its own. ★ A nil table is what says "nothing is narrowed
	//   here" — with an allow-list, an empty one would say the opposite.
	flat := newBoundary(policy, nil)
	out := make([]ToolVerdict, 0, len(names))
	for _, n := range names {
		out = append(out, verdictOf(flat, n, obs))
	}
	return out
}

func verdictOf(b boundary, name string, obs Observed) ToolVerdict {
	v := ToolVerdict{
		Name:          name,
		Tier:          b.Policy().TierOf(name),
		Present:       obs.Has(name),
		ObservedKnown: obs.Known,
	}
	if l := b.Table().Blocked(name); l != LayerNone {
		// ★ The tier stays what it is: a tool the agent does not get still
		//   asks — it is this agent that cannot have it.
		v.Blocked = l
		v.Reason = reasonOf(l)
	}
	return v
}

func toolNameUniverse(b boundary, enabled []string, subs []decl.SubAgentSpec, obs Observed) []string {
	policy := b.Policy()
	seen := make(map[string]bool, len(policy.Allow)+len(enabled))
	add := func(list []string) {
		for _, n := range list {
			if n != "" {
				seen[n] = true
			}
		}
	}
	// ★ The names that run without a nod are known from the configuration
	//   alone, so they are part of the universe before the server is ever
	//   up. The rest of a server's tools join it once they have been
	//   observed.
	add(policy.Allow)
	add(enabled)
	for name := range reservedToolNames {
		add([]string{name})
	}
	if table := b.Table(); table != nil {
		add(table.Load)
	}
	for _, s := range subs {
		addSpecTools(add, s)
	}
	add(obs.Names)

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func addSpecTools(add func([]string), agentSpec decl.SubAgentSpec) {
	add(agentSpec.Tools)
	for _, s := range agentSpec.Sub {
		addSpecTools(add, s)
	}
}

type DescriptionVerdict struct {
	AgentID    string
	Empty      bool
	Duplicates []string
	Distinct   bool
}

func (b *Builder) DescriptionVerdicts() []DescriptionVerdict {
	snap := b.currentSnapshot()
	if snap == nil {
		return nil
	}
	ids := make([]string, 0, len(snap.agentSpec.agents))
	for id := range snap.agentSpec.agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]DescriptionVerdict, 0, len(ids))
	for _, id := range ids {
		v := DescriptionVerdict{AgentID: id}
		desc := strings.TrimSpace(snap.agentSpec.agents[id].Description)
		if desc == "" {
			v.Empty = true
		} else {
			for _, other := range ids {
				if other == id {
					continue
				}
				od := strings.TrimSpace(snap.agentSpec.agents[other].Description)
				if od == "" {
					continue
				}
				if similarDescriptions(desc, od) {
					v.Duplicates = append(v.Duplicates, other)
				}
			}
		}
		v.Distinct = !v.Empty && len(v.Duplicates) == 0
		out = append(out, v)
	}
	return out
}

const descriptionSimilarityThreshold = 0.8

func similarDescriptions(a, b string) bool {
	ta, tb := descriptionTokens(a), descriptionTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	inter := 0
	for w := range ta {
		if tb[w] {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return false
	}
	return float64(inter)/float64(union) >= descriptionSimilarityThreshold
}

func descriptionTokens(s string) map[string]bool {
	var clean []rune
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			clean = append(clean, r)
		}
	}
	out := make(map[string]bool, len(clean))
	for i := 0; i+1 < len(clean); i++ {
		out[string(clean[i:i+2])] = true
	}
	return out
}
