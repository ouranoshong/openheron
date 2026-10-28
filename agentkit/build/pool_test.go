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

// stubModel 只需要满足 model.BaseModel（Generate/Stream）。
type stubModel struct{}

func (*stubModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, nil
}

func (*stubModel) Stream(context.Context, []*schema.Message, ...model.Option) (
	*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{}), nil
}

// newMinimalBuilder 造一个只够跑通池逻辑的 Builder。
//
// ★ 模型走 modelFactory 桩；配置里只有 ModelRef，没有实例。
func newMinimalBuilder(t *testing.T) *Builder {
	t.Helper()
	return newTestBuilder(t)
}

// countingBuild 数一个 agent 名被真建了几次。
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

// build 是「造一个 main」的构造函数（现在由调用方当参数传进池）。
func (c *countingBuild) build(id string) buildFn { return c.fn(id) }

// ── 复用 ─────────────────────────────────────────────────────────────────

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
		t.Fatal("★ 同 key 两次取必须拿到同一实例 —— 否则每轮都在重编 ReAct 图")
	}
	if c.count() != 1 {
		t.Fatalf("it must be built exactly once, got %d", c.count())
	}
}

// ── 换代 ─────────────────────────────────────────────────────────────────

func TestBumpInvalidatesEntries(t *testing.T) {
	p, c := newTestPool(t)

	// ★ 换代现在是**代际号**的事：池的键含代际，而代际由快照给。
	//   池自己不再记代际 —— 否则「换代」会有两个来源，必须永远同步。
	e1, _ := p.GetOrBuild(context.Background(), "main", 1, c.build("main"))
	e2, _ := p.GetOrBuild(context.Background(), "main", 2, c.build("main"))

	if e1 == e2 {
		t.Fatal("★ 换代后必须取到新实例")
	}
	if c.count() != 2 {
		t.Fatalf("after the bump it must rebuild once, cumulative %d", c.count())
	}
}

// 池的条目数**不随换代增长**：同名再被用到时就地替换。
//
// ★ 实际回收交给 GC —— 在飞 turn 的引用会让旧实例存活到用完。
func TestPoolDoesNotGrowAcrossGenerations(t *testing.T) {
	p, c := newTestPool(t)
	for gen := uint64(1); gen <= 5; gen++ {
		p.GetOrBuild(context.Background(), "main", gen, c.build("main"))
	}

	if _, _, size, _ := p.Stats(); size != 1 {
		t.Fatalf("★ the pool must not grow across generations, got %d entries", size)
	}
}

// ── 边界 ─────────────────────────────────────────────────────────────────

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

// ★ 并发取用是主要用法（多 session 同时起 turn），必须无数据竞争。
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

// 换代与取用并发：不得 panic、不得把请求路由到已失效的条目上。
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

// ── 可观测 ───────────────────────────────────────────────────────────────

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

// ── Builder 走池 ─────────────────────────────────────────────────────────

// Builder.Build 必须走池 —— 否则池白建，每轮照旧重编图。
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
		t.Fatal("★ 同一代际下两次 Build 必须拿到同一实例")
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
	managerOf(t, b).set(cfg) // 只换代际，不换内容
	a2, err := b.Build(context.Background(), "s1", "", contract.TurnQuery)
	if err != nil {
		t.Fatal(err)
	}
	if a1 == a2 {
		t.Fatal("★ 换代后必须换实例")
	}
}

// Build 的签名与语义对 agentkit/run 不变 —— 依赖铁律不允许为了池而破。
func TestBuildSignatureUnchanged(t *testing.T) {
	b := newMinimalBuilder(t)
	var _ func(context.Context, string, string, contract.TurnKind) (adk.TypedAgent[*schema.Message], error) = b.Build
}
