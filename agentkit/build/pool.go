package build

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ── 为什么需要池 ─────────────────────────────────────────────────────────
//
// agent 实例**可以安全地跨并发 run 复用**：`TypedChatModelAgent` 在
// `once.Do` 里编好 ReAct 图与工具 schema（`chatmodel.go:1368`），随后冻结
// （`frozen` 标志）；每次 run 在 `getRunFunc`（`:1412`）造一份**运行期副本**
// `runtimeBC`，per-run 状态不落在实例上。
//
// 而 `deep.New` **不编译图** —— 编译在首次 Run 的 `once.Do` 里触发。所以
// 每轮都造新实例 ⇒ 每轮都重编一次图、重算一次全部工具的 JSON schema。
//
// ★ 于是「复用实例」不是优化洁癖，是一个每轮都在付的固定成本。

// entry 是一个 agent 实例及其构建它所需的依赖。
//
// ★ 依赖（ChatModel / Backend）**按代际持有**，不按进程 ——
//
//	否则热更新换配置时模型换不掉。
type entry struct {
	agent adk.TypedAgent[*schema.Message]
	model model.BaseModel[*schema.Message]
	// gen 是这个实例属于哪一代。
	gen uint64
}

// buildFn 造一个新实例。Pool 不关心它内部怎么建。
type buildFn func(ctx context.Context) (adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error)

// Pool 按 (agent 名, 代际) 复用 agent 实例。
//
// ★ 它是 [Builder] 的**内部实现细节**：agentkit/run 照旧调
// `AgentBuilder.Build(ctx, sessionID, turnKind)`，对池的存在毫无感知 ——
// 这条依赖铁律不允许为了池而破。
//
// ★ 键只有 (名, 代际) 两个字段。
//
//	曾经还有「角色」与「深度」—— 那是为一个**生产路径从不发生**的场景
//	付的复杂度：子 agent 根本不进池（[buildSubAgents] 直接调 deep.New），
//	所以 role 恒为 root、depth 恒为 0。留着的唯一效果是让人以为
//	「同名不同角色会分开」—— 而实际上那条路径没人走。
//
// ★ 代际由**调用方**给（[Builder] 从当前快照拿），池自己不记代际 ——
//
//	否则同一个「换代」概念会在两个地方各记一遍，而它们必须永远同步。
type pool struct {
	mu   sync.Mutex
	ents map[string]*entry

	hits, misses uint64
}

// NewPool 造一个空池。
func newPool() *pool {
	return &pool{ents: map[string]*entry{}}
}

// GetOrBuild 取实例，未命中则构建并登记。
//
// gen 是调用方要的**那一代** —— 池只负责「这一代的这个 agent 建过没有」。
func (p *pool) GetOrBuild(ctx context.Context, name string, gen uint64, fn buildFn) (*entry, error) {
	p.mu.Lock()
	if e, ok := p.ents[name]; ok && e.gen == gen {
		p.hits++
		p.mu.Unlock()
		return e, nil
	}
	p.misses++
	p.mu.Unlock()

	// 构建在锁外：它是重活（图编译 + 工具 schema），持锁会阻塞所有其他 agent。
	// 代价是同一代际下并发首次请求可能各建一份 —— 它们互不共享状态，
	// 后写的赢，且 `Once` 保证只有一份被留下。这是**刻意接受的重复**：
	// 宁可多建一次，也不要把重活塞进全局锁里。
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
			// 期间别人已经建好同一代的 —— 用他的，扔掉自己这份。
			p.hits++
			return cur, nil
		case cur.gen > gen:
			// ★ 期间已经**换代**了，而池里那份是更新的。
			//
			//	扔掉自己这份旧的，返回更新的那份：让这一轮跑在**新配置**上，
			//	而不是跑在它自己发起时的那份旧配置上。热更新的语义就是
			//	「下一次就用新的」，而这一轮恰好跨过了换代点。
			return cur, nil
		}
	}
	p.ents[name] = e
	return e, nil
}

// Stats 返回可观测计数。
//
// ★ Pool 属于优化，**没有数据就无法证明它有收益**：命中率长期为 0 说明
//
//	键设计错了，或者调用路径根本没走池。
//
// gen 是当前池里**最新**那一代的号（0 = 还没有任何实例）。
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
