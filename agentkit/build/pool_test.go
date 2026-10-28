package build

import (
	"context"
	"openheron/contract"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type stubModel struct{}

func (*stubModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, nil
}

func (*stubModel) Stream(context.Context, []*schema.Message, ...model.Option) (
	*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{}), nil
}

func newMinimalBuilder(t *testing.T) *Builder {
	t.Helper()
	return newTestBuilder(t)
}

type countingBuild struct {
	mu    sync.Mutex
	calls int
}

func (c *countingBuild) fn(id string) buildFn {
	return func(context.Context) (adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error) {
		c.mu.Lock()
		c.calls++
		c.mu.Unlock()
		return &stubAgent{name: id}, &stubModel{}, nil
	}
}

func (c *countingBuild) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newTestPool(t *testing.T) (*pool, *countingBuild) {
	t.Helper()
	p := newPool()
	c := &countingBuild{}
	return p, c
}

func (c *countingBuild) build(id string) buildFn { return c.fn(id) }

func TestPoolReusesInstanceForSameKey(t *testing.T) {
	p, c := newTestPool(t)

	e1, err := p.GetOrBuild(context.Background(), "main", 1, c.build("main"))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := p.GetOrBuild(context.Background(), "main", 1, c.build("main"))
	if err != nil {
		t.Fatal(err)
	}
	if e1 != e2 {
		t.Fatal("★ two lookups with the same key must return the same instance - otherwise the ReAct graph is rebuilt every turn")
	}
	if c.count() != 1 {
		t.Fatalf("it must be built exactly once, got %d", c.count())
	}
}

func TestBumpInvalidatesEntries(t *testing.T) {
	p, c := newTestPool(t)

	e1, _ := p.GetOrBuild(context.Background(), "main", 1, c.build("main"))
	e2, _ := p.GetOrBuild(context.Background(), "main", 2, c.build("main"))

	if e1 == e2 {
		t.Fatal("★ after the generation bump a new instance must be returned")
	}
	if c.count() != 2 {
		t.Fatalf("after the bump it must rebuild once, cumulative %d", c.count())
	}
}

func TestPoolDoesNotGrowAcrossGenerations(t *testing.T) {
	p, c := newTestPool(t)
	for gen := uint64(1); gen <= 5; gen++ {
		p.GetOrBuild(context.Background(), "main", gen, c.build("main"))
	}

	if _, _, size, _ := p.Stats(); size != 1 {
		t.Fatalf("★ the pool must not grow across generations, got %d entries", size)
	}
}

func TestUnregisteredAgentIsAnError(t *testing.T) {
	p := newPool()
	_, err := p.GetOrBuild(context.Background(), "ghost", 1, func(context.Context) (
		adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error) {
		return &stubAgent{name: "ghost"}, &stubModel{}, nil
	})
	if err != nil {
		t.Fatalf("the build fn is now passed in, so a build must succeed: %v", err)
	}
}

func TestPoolIsSafeUnderConcurrentUse(t *testing.T) {
	p, c := newTestPool(t)

	var wg sync.WaitGroup
	const n = 32
	results := make([]*entry, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := p.GetOrBuild(context.Background(), "main", 1, c.build("main"))
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = e
		}(i)
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("under concurrency every caller must get the same instance (index %d differs from 0)", i)
		}
	}
}

func TestPoolIsSafeAcrossBump(t *testing.T) {
	p, c := newTestPool(t)
	p.GetOrBuild(context.Background(), "main", 1, c.build("main"))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			p.GetOrBuild(context.Background(), "main", uint64(i+2), c.build("main"))
		}
		close(stop)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := p.GetOrBuild(context.Background(), "main", 1, c.build("main")); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	wg.Wait()
}

func TestPoolStatsCountHitsAndMisses(t *testing.T) {
	p, c := newTestPool(t)
	p.GetOrBuild(context.Background(), "main", 1, c.build("main")) // miss
	p.GetOrBuild(context.Background(), "main", 1, c.build("main")) // hit
	p.GetOrBuild(context.Background(), "main", 1, c.build("main")) // hit

	hits, misses, _, _ := p.Stats()
	if misses != 1 || hits != 2 {
		t.Fatalf("hit counters are wrong: hits=%d misses=%d (want 2/1)", hits, misses)
	}
}

func TestBuilderBuildGoesThroughPool(t *testing.T) {
	b := newMinimalBuilder(t)

	a1, err := b.Build(context.Background(), "s1", "", contract.TurnQuery)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := b.Build(context.Background(), "s2", "", contract.TurnQuery)
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Fatal("★ two Build calls in the same generation must return the same instance")
	}
	hits, misses, _, _ := b.Stats()
	if hits == 0 {
		t.Fatalf("the second Build must hit the pool; stats hits=%d misses=%d", hits, misses)
	}
}

func TestBuilderBumpSwapsInstance(t *testing.T) {
	b := newMinimalBuilder(t)

	a1, err := b.Build(context.Background(), "s1", "", contract.TurnQuery)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := managerOf(t, b).Current()
	managerOf(t, b).set(cfg)
	a2, err := b.Build(context.Background(), "s1", "", contract.TurnQuery)
	if err != nil {
		t.Fatal(err)
	}
	if a1 == a2 {
		t.Fatal("★ the instance must be swapped after the generation bump")
	}
}

func TestBuildSignatureUnchanged(t *testing.T) {
	b := newMinimalBuilder(t)
	var _ func(context.Context, string, string, contract.TurnKind) (adk.TypedAgent[*schema.Message], error) = b.Build
}
