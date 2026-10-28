package build

import (
	"context"
	"slices"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components/tool"
)

// 本文件是「谁能调什么」的数据部分：审批三档、分层工具表，以及把两者绑定
// 的 [boundary]。执行机制在 [gate.go]。
//
// ★ 绑定成一个类型是为了让「审批按 main、过滤按 task」这种错配在编译期
// 写不出来 —— 它们必须来自同一个 agent。
//
// Tier 是审批档位。
type Tier string

const (
	// TierAllow 直接放行。
	TierAllow Tier = "allow"
	// TierAsk 可见，但调用需人批准。
	TierAsk Tier = "ask"
	// TierDeny 直接拒绝 —— 且**它会被 filterMiddleware 从工具表里摘掉**，
	// 模型根本看不到它。
	TierDeny Tier = "deny"
)

// Policy 是审批策略，由 .openheron/policy/permissions.yaml 给出。
//
// ★ **只有 ask 与 deny 拦得住东西** —— 没被点名的工具一律放行。
type Policy struct {
	// Ask 是需要人批准的工具名。
	Ask []string
	// Deny 是全局禁止的。★ 它在装配期就被摘掉工具，闸门不参与。
	Deny []string

	ask map[string]bool
}

// NewPolicy 预编译查找索引。
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

// TierOf 判定档位。
//
// ★ **兜底是 allow**：只有明确写进 ask / deny 的工具才被拦。
//
//	为什么不是「兜底 ask」：那等于给**每个**工具装一道门 —— 用户点到手酸，
//	而「读文件」「列目录」这类只读工具本来就不必点头。真正有后果的
//	（写文件、执行命令、建索引）由 permissions.yaml **点名**进 ask / deny ✅。
//
//	★ 兜底 allow 不等于「什么都不管」：deny 仍是第一顺位，ask 紧随其后 ✅。
//	  未被点名的工具照常走**内层**的边界（每个 agent 自己的 disabled_tools
//	  —— 那是内层，管得住 ✅ ✅）。
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

// Layer 标识一条封锁来自哪一层。
type Layer string

// boundary 是「这个 agent 的工具边界」的唯一表示：审批三档 + 工具表。
type boundary struct {
	// policy 是审批三档：这个工具要不要人点头。
	policy Policy
	// table 是工具表：这个 agent 能不能调它。
	table *Table
}

// newBoundary 造一个工具边界。
func newBoundary(policy Policy, table *Table) boundary {
	return boundary{policy: policy, table: table}
}

// Policy 露出审批三档。
func (b boundary) Policy() Policy { return b.policy }

// Table 露出工具表。
func (b boundary) Table() *Table { return b.table }

// Table 是一个 agent 的工具表边界。
//
// ★ Deny 与 Disable 分开存、绝不合并：外层全局禁止必须对**所有**子 agent
// 生效，合并后就看不出某个工具是被外层封杀，还是只是本 agent 没声明。
type Table struct {
	// mu 只保护 observed —— 唯一会在运行期写入的字段。其余字段装配期定型。
	mu sync.RWMutex

	// Deny 来自 decl.Policy，与出厂默认取并集（安全边界只往严的方向合）。
	Deny []string
	// Disable 来自该 agent 的 disabled_tools，只影响它自己。
	Disable []string
	// Name 是这个 agent 的名字，出现在观测里。
	Name string

	denied   map[string]bool
	disabled map[string]bool
	// observed 是**实际出现过**的工具名（观测用）。
	//
	// ★ 它存在的理由：一张表能回答「这个 agent 能不能用 grep」，但答不了
	//   「除了配置里点过名的，还有哪些工具真的存在」—— 而后者恰恰是
	//   `/v2/agents` 一直漏报的那半（`task`、`skill` 由框架中间件注册，
	//   名字从来不出现在任何配置文件里）。
	//
	// ★ 只有**运行期**才知道这份清单：eino 的 fs/skill 中间件在每一轮的
	//   `runCtx.Tools` 里才把工具交出来，`adk.ChatModelAgent` 没有任何办法
	//   在装配期问出它有哪些工具。所以这里是观测，不是计算 ——
	//   **第一轮跑起来之前它是空的**，而那件事必须由接口如实说出来，
	//   而不是拿「配置里点过名的那些」冒充完整清单。
	observed map[string]bool
}

// NewTable 按名建表并预编译查找索引（工具名匹配在每次调用前都要跑，不该每次建 map）。
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

// Blocked 返回某个工具被哪一层封锁。
//
// ★ **Deny 永远优先于 Disable**：同一名字同时出现在两层时，
//
//	按内层处理会让「全局禁止」被一个 agent 的偏好悄悄解除。
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

// Filter 从工具列表里摘掉被封锁的，返回摘掉的数量与分层计数。
//
// ★ 它在 **BeforeAgent** 里跑 —— eino 每轮 run 用 `runCtx.Tools` 重建工具表
//
//	（`chatmodel.go:815` 克隆、`chat_model.go:828` 写回），所以摘除对
//	后续每一次 run 都生效，而不是只对第一轮。
//
// ★ 观测发生在**摘除之前**，且与有没有 deny/disable 无关 —— 报的是「这一轮
//
//	框架交来了哪些工具」。放在摘除之后的话，被 deny 的工具会从「存在」里
//	消失，而它恰恰是最需要被看见的那一类（「策略挡住了一个真实存在的工具」
//	与「策略点名了一个不存在的工具」是两件事）。
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
		// ★ 摘不摘得下取决于工具报不报得出自己的名字；报不出就保留 ——
		//   宁可漏摘（可见、可报）也不要错摘（工具凭空消失，模型无从解释）。
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

// observe 记下这一轮真实出现过的工具名。
func (t *Table) observe(tools []tool.BaseTool) {
	seen := make(map[string]bool, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil || info == nil || info.Name == "" {
			// 报不出名字的工具就记不下来。**不猜** —— 猜一个名字会让接口
			// 报出一个不存在的工具，而那正是本文件要消灭的东西。
			continue
		}
		seen[info.Name] = true
	}
	t.mu.Lock()
	t.observed = seen
	t.mu.Unlock()
}

// Observed 返回**上一轮**真实出现过的工具名。
//
// ★ 返回 ok=false 表示**还没跑过任何一轮**。调用方必须把这两种情况分开报：
//
//	「没有工具」与「还不知道有哪些工具」是完全不同的两件事，而后者被当成
//	前者正是这个端点此前一直在犯的错。
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
	// LayerNone = 未被封锁。
	LayerNone Layer = ""
	// LayerDeny 是**外层**：全局 policy，子 agent 不能加回。
	LayerDeny Layer = "deny"
	// LayerDisable 是**内层**：本 agent 自己声明的 disabled_tools。
	LayerDisable Layer = "disable"
)
