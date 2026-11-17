package build

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type entry struct {
	agent adk.TypedAgent[*schema.Message]
	model model.BaseModel[*schema.Message]
	// fingerprint is the entry's identity: ★ the pool keeps the entry
	// whose fingerprint matches what the caller asks for, so a change that
	// does not reach this agent leaves it in place.
	fingerprint string
}

type buildFn func(ctx context.Context) (adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error)

type pool struct {
	mu   sync.Mutex
	ents map[string]map[string]*entry

	hits, misses uint64
}

func newPool() *pool {
	return &pool{ents: map[string]map[string]*entry{}}
}

// GetOrBuild returns the entry for this agent at this fingerprint, building
// it when there is none.
//
// ★ The key is **(name, fingerprint)**, not (name, generation): a generation
//
//	says "everything changed", and a fingerprint says "this one changed".
//	★ That is what lets a change to one skill leave the agents that do not
//	use it in place — the whole point of the dependency walk.
//
// ★ The old entry is **kept**: a run that pinned the previous tree still
//
//	needs it (see the run lifecycle), and the decision was to keep old
//	entries until the process exits rather than to invent a rule for
//	reclaiming them.
func (p *pool) GetOrBuild(ctx context.Context, name, fingerprint string, fn buildFn) (*entry, error) {
	p.mu.Lock()
	if m, ok := p.ents[name]; ok {
		if e, ok := m[fingerprint]; ok {
			p.hits++
			p.mu.Unlock()
			return e, nil
		}
	}
	p.misses++
	p.mu.Unlock()

	agent, mdl, err := fn(ctx)
	if err != nil {
		return nil, err
	}
	e := &entry{agent: agent, model: mdl, fingerprint: fingerprint}

	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.ents[name]
	if !ok {
		m = map[string]*entry{}
		p.ents[name] = m
	}
	// ★ Another builder may have won the race; its entry is as good as
	//   ours, and one entry per fingerprint is what makes the cache a
	//   cache.
	if cur, ok := m[fingerprint]; ok {
		p.hits++
		return cur, nil
	}
	m[fingerprint] = e
	return e, nil
}

// Lookup returns the entry for this agent at this fingerprint, and whether
// it was there.
//
// ★ It is the **pinned** lookup: a run that recorded a tree asks for it by
//
//	fingerprint, and the answer is the entry that tree built — or nothing.
func (p *pool) Lookup(name, fingerprint string) (*entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.ents[name]
	if !ok {
		return nil, false
	}
	e, ok := m[fingerprint]
	return e, ok
}

// Stats reports the cache's counters and how many entries it holds.
//
// ★ The size counts **entries**, not agents: an agent that was rebuilt at a
//
//	new fingerprint is two entries now, and the old one is still there.
func (p *pool) Stats() (hits, misses uint64, size int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	size = 0
	for _, m := range p.ents {
		size += len(m)
	}
	return p.hits, p.misses, size
}
