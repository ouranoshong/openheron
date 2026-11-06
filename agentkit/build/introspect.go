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
	case LayerDeny:
		return "policy.deny"
	case LayerDisable:
		return "disabled_tools"
	case LayerMCPScope:
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

	flat := newBoundary(policy, specTable(Policy{}, decl.SubAgentSpec{}, nil))
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
		v.Tier, v.Blocked = TierDeny, l
		v.Reason = reasonOf(l)
	}
	return v
}

func toolNameUniverse(b boundary, enabled []string, subs []decl.SubAgentSpec, obs Observed) []string {
	policy := b.Policy()
	seen := make(map[string]bool, len(policy.Ask)+len(policy.Deny)+len(enabled))
	add := func(list []string) {
		for _, n := range list {
			if n != "" {
				seen[n] = true
			}
		}
	}
	add(policy.Ask)
	add(policy.Deny)
	add(enabled)
	// ★ The names the servers' files allow are known from the
	//   configuration alone, so they are part of the universe before the
	//   server is ever up. The rest of a server's tools join it once they
	//   have been observed.
	for name := range policy.mcpAllow {
		add([]string{name})
	}
	for name := range reservedToolNames {
		add([]string{name})
	}
	if table := b.Table(); table != nil {
		add(table.Disable)
	}
	for _, s := range subs {
		addSpecDisabled(add, s)
	}
	add(obs.Names)

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func addSpecDisabled(add func([]string), agentSpec decl.SubAgentSpec) {
	add(agentSpec.DisabledTools)
	for _, s := range agentSpec.Sub {
		addSpecDisabled(add, s)
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
