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
	gen   uint64
}

type buildFn func(ctx context.Context) (adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error)

type pool struct {
	mu   sync.Mutex
	ents map[string]*entry

	hits, misses uint64
}

func newPool() *pool {
	return &pool{ents: map[string]*entry{}}
}

func (p *pool) GetOrBuild(ctx context.Context, name string, gen uint64, fn buildFn) (*entry, error) {
	p.mu.Lock()
	if e, ok := p.ents[name]; ok && e.gen == gen {
		p.hits++
		p.mu.Unlock()
		return e, nil
	}
	p.misses++
	p.mu.Unlock()

	agent, mdl, err := fn(ctx)
	if err != nil {
		return nil, err
	}
	e := &entry{agent: agent, model: mdl, gen: gen}

	p.mu.Lock()
	defer p.mu.Unlock()
	if cur, ok := p.ents[name]; ok {
		switch {
		case cur.gen == gen:
			p.hits++
			return cur, nil
		case cur.gen > gen:
			return cur, nil
		}
	}
	p.ents[name] = e
	return e, nil
}

func (p *pool) Stats() (hits, misses uint64, size int, gen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	newest := uint64(0)
	for _, e := range p.ents {
		if e.gen > newest {
			newest = e.gen
		}
	}
	return p.hits, p.misses, len(p.ents), newest
}
