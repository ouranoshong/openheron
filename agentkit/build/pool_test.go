package build

import (
	"context"
	"fmt"
	"github.com/ouranoshong/openheron/contract"
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

	e1, err := p.GetOrBuild(context.Background(), "main", "fp1", c.build("main"))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := p.GetOrBuild(context.Background(), "main", "fp1", c.build("main"))
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

// TestANewFingerprintBuildsANewInstance is the other side of the same rule.
//
// ★ The key is the fingerprint, so a fingerprint that moved is a different
//
//	agent — and the old entry stays where it is.
func TestANewFingerprintBuildsANewInstance(t *testing.T) {
	p, c := newTestPool(t)

	e1, _ := p.GetOrBuild(context.Background(), "main", "fp1", c.build("main"))
	e2, _ := p.GetOrBuild(context.Background(), "main", "fp2", c.build("main"))

	if e1 == e2 {
		t.Fatal("★ a new fingerprint must build a new instance")
	}
	if c.count() != 2 {
		t.Fatalf("it must rebuild once, cumulative %d", c.count())
	}
}

// TestTheOldEntrySurvivesANewFingerprint is what a pinned run depends on.
//
// ★ A run that recorded the previous tree asks for it by fingerprint, and
//
//	the answer has to still be there.
func TestTheOldEntrySurvivesANewFingerprint(t *testing.T) {
	p, c := newTestPool(t)

	e1, _ := p.GetOrBuild(context.Background(), "main", "fp1", c.build("main"))
	p.GetOrBuild(context.Background(), "main", "fp2", c.build("main"))

	got, ok := p.Lookup("main", "fp1")
	if !ok {
		t.Fatal("★ the old entry must survive: a run that pinned it still needs it")
	}
	if got != e1 {
		t.Fatal("the pinned lookup must return the entry that tree built")
	}
}

// TestThePoolKeepsOneEntryPerFingerprint replaces the old rule.
//
// ★ The pool used to hold one entry per agent and drop it when the
//
//	generation moved. ★ Now it holds one entry per (agent, fingerprint),
//	and the old one stays — the decision was to keep old entries until the
//	process exits rather than to invent a rule for reclaiming them.
func TestThePoolKeepsOneEntryPerFingerprint(t *testing.T) {
	p, c := newTestPool(t)
	for _, fp := range []string{"fp1", "fp2", "fp3"} {
		p.GetOrBuild(context.Background(), "main", fp, c.build("main"))
	}

	if _, _, size := p.Stats(); size != 3 {
		t.Fatalf("★ the pool must hold one entry per fingerprint, got %d entries", size)
	}
}

func TestUnregisteredAgentIsAnError(t *testing.T) {
	p := newPool()
	_, err := p.GetOrBuild(context.Background(), "ghost", "fp1", func(context.Context) (
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
			e, err := p.GetOrBuild(context.Background(), "main", "fp1", c.build("main"))
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

func TestPoolIsSafeAcrossFingerprints(t *testing.T) {
	p, c := newTestPool(t)
	p.GetOrBuild(context.Background(), "main", "fp1", c.build("main"))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			p.GetOrBuild(context.Background(), "main", fmt.Sprintf("fp%d", i+2), c.build("main"))
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
				if _, err := p.GetOrBuild(context.Background(), "main", "fp1", c.build("main")); err != nil {
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
	p.GetOrBuild(context.Background(), "main", "fp1", c.build("main")) // miss
	p.GetOrBuild(context.Background(), "main", "fp1", c.build("main")) // hit
	p.GetOrBuild(context.Background(), "main", "fp1", c.build("main")) // hit

	hits, misses, _ := p.Stats()
	if misses != 1 || hits != 2 {
		t.Fatalf("hit counters are wrong: hits=%d misses=%d (want 2/1)", hits, misses)
	}
}

func TestBuilderBuildGoesThroughPool(t *testing.T) {
	b := newMinimalBuilder(t)

	a1, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	a2, _, err := b.Build(context.Background(), "s2", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Fatal("★ two Build calls on the same tree must return the same instance")
	}
	hits, misses, _ := b.Stats()
	if hits == 0 {
		t.Fatalf("the second Build must hit the pool; stats hits=%d misses=%d", hits, misses)
	}
}

// TestTheSameConfigurationDoesNotRebuildTheAgent is the new rule.
//
// ★ The pool's key is the fingerprint, and the tree's identity is the
//
//	configuration **object** plus the MCP tool surface: a configuration
//	that is republished unchanged moves neither, so the agents stand
//	still. ★ What a bump used to buy — a rebuild when a server's tool
//	list changes — is bought by the fingerprint instead, because the
//	server's tool list is part of what an agent depends on.
func TestTheSameConfigurationDoesNotRebuildTheAgent(t *testing.T) {
	b := newMinimalBuilder(t)

	a1, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := managerOf(t, b).Current()
	managerOf(t, b).set(cfg)
	a2, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Fatal("★ the same configuration must not swap the instance: the fingerprint is what says whether the agent moved")
	}
}

func TestBuildSignatureUnchanged(t *testing.T) {
	b := newMinimalBuilder(t)
	var _ func(context.Context, string, string, contract.TurnKind, string) (
		adk.TypedAgent[*schema.Message], string, error) = b.Build
}
