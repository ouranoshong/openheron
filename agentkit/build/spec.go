package build

import (
	"fmt"
	"strings"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
)

// agentSpec 是「一代配置」在 agent 包内的形状 —— **派生**出来的，不是外部给的。
//
// ★ 它是**内部**类型：对外只有 [ConfigManager] 这一张嘴。
type agentSpec struct {
	// agents 是每个 agent 的完整规格：它自己 + 它的子树。
	//
	// ★ 键是 agent 的 id；值里的 Sub 是**它自己**的那棵树。
	//
	// ★ 「从某个入口看过去是这样一棵树」而不是「属于某一棵树」：同一个
	//   agent 可以出现在多棵树里（task 既是 main 的下属，又是自己的
	//   入口）—— 那不冲突。
	agents map[string]decl.SubAgentSpec
	// defaultAgent 是新会话默认用的那个 agent。
	defaultAgent string
	// policy 是审批三档（全局，所有 agent 共享）。
	policy Policy
	// maxDepth 是嵌套深度上限。
	maxDepth int
}

// specOf 从配置派生出这一代的规格。
//
// ★ 为什么派生放在 agent 包：规格是「装配的输入」，而装配归这里 ——
//
//	配置层只给**声明**，不给「装配要什么」。
func specOf(cfg *spec.Config, projectRoot string) (agentSpec, error) {
	if cfg == nil {
		return agentSpec{}, fmt.Errorf("agent: nil config")
	}
	// ★ 每个 agent 一棵树 —— 会话选哪个就按哪个装配。
	agentSpecs, err := cfg.AgentSpecs()
	if err != nil {
		return agentSpec{}, err
	}
	agents := make(map[string]decl.SubAgentSpec, len(agentSpecs))
	for id, sp := range agentSpecs {
		// 路径规则是**代码**的部分：每个 agent 都得带上（它是那棵树的
		// 入口，模型先要知道项目根在哪）。
		sp.Instruction = instruction(projectRoot, sp.Instruction)
		// ★ 0 在 eino 里意味着「不设上限」—— 行为会悄悄变成无界循环，
		//   所以入口必须有具体值。
		if sp.MaxIter <= 0 {
			sp.MaxIter = defaultMaxIterations
		}
		agents[id] = sp
	}
	defaultAgent := cfg.DefaultAgent()
	if _, ok := agents[defaultAgent]; !ok {
		return agentSpec{}, fmt.Errorf("agent: the default agent %q is not declared", defaultAgent)
	}
	return agentSpec{
		agents:       agents,
		defaultAgent: defaultAgent,
		policy:       policyOf(cfg),
		maxDepth:     cfg.MaxDepth(),
	}, nil
}

// policyOf 造这一代的审批三档。
//
// ★ 只有 ask / deny 两档 —— 未点名的工具一律放行（见 [Policy.TierOf] ✅）。
//
//	★ 策略的 ask / deny 仍然优先（见 [Policy.TierOf]）—— 策略是安全边界，
//	配置只是「这个能力开不开」✅ ✅。
func policyOf(cfg *spec.Config) Policy {
	return NewPolicy(cfg.Policy.Ask, cfg.Policy.Deny)
}

// agentSpec 返回某个 agent 的规格（它自己 + 它的子树）。
func (s *agentSpec) agentSpec(id string) (decl.SubAgentSpec, bool) {
	sp, ok := s.agents[id]
	return sp, ok
}

// subsOf 返回某个 agent 的**子树**（它下面的那些）。
//
// ★ 装配时只给**当前入口**的那棵：skill fork 要在自己的树里找下属，
//
//	跨树找会「找到一个不在我团队里的人」。
func (s *agentSpec) subsOf(id string) []decl.SubAgentSpec {
	sp, ok := s.agents[id]
	if !ok {
		return nil
	}
	return sp.Sub
}

// defaultMaxIterations 是 agent 没写 max_iterations 时的上限。
//
// ★ 它是**规格派生**的一部分，不是 main.go 的一次性装配参数：留在外面会让
//
//	根拿到 0，而 0 在 eino 里意味着「不设上限」—— 行为会悄悄变成无界循环。
const defaultMaxIterations = 50

// instruction 拼出 agent 的系统提示。
//
// 前置的路径规则是**代码**的部分：每个子 agent 都继承它。
// 正文（来自 agents/main.md）是**配置**的部分，追加在后面 —— 配置为空时
// 仍有路径规则兜底，不会退化成 eino 的内置提示。
func instruction(projectRoot, body string) string {
	rule := fmt.Sprintf(`You are a helpful assistant exploring a Go codebase.

The project root directory is: %s

- Always pass ABSOLUTE paths to filesystem tools (ls, read_file, glob, grep).
- When the user names a relative path, prepend the project root.
- When writing or editing a file and then claiming it compiles, run the build
  first with the execute tool; never assume a build succeeded.
- Anything that mutates the machine (write_file, edit_file, execute) requires
  explicit human approval. Explain what you intend to do before asking.`,
		projectRoot)
	if body = strings.TrimSpace(body); body == "" {
		return rule
	}
	return rule + "\n\n" + body
}

// subs 返回这一代的全部 agent 规格。
//
// ★ 用访问器而不是让每个调用点写 snap.agentSpec.agents[x].Sub：树的所有者只有
//
//	一个，而「从哪个字段拿」不该成为调用点各自记住的事实。
//
// ★ 自省用：它要回答「这个 agent 的工具表是什么」，而每个 agent 都在
//
//	这里 —— 一个 agent 可能出现在多棵树里（既是别人的下属，又是自己的
//	入口），但它的**声明**只有一份。
//
// ★ 装配**不用**它 —— 那里要的是「当前入口的那一棵」（见 [spec.subsOf]）。
func (s *snapshot) subs() []decl.SubAgentSpec {
	out := make([]decl.SubAgentSpec, 0, len(s.agentSpec.agents))
	for _, sp := range s.agentSpec.agents {
		// ★ 含**入口自己** —— 它的 disabled_tools 也要进名字全集
		//   （策略视图要列出所有 agent 声明过的工具）。
		out = append(out, sp)
	}
	return out
}

// tableFor 返回某个 agent 的工具边界表。
//
// ★ 每个 agent 的表**不同**（各自的 disabled_tools），所以不能只有一张。
// ★ 取的是**缓存的那张**（snapshot.tables），不是现造的：运行期观测到的
//
//	工具名记在表实例上，装配与自省必须是同一批实例。
func (s *snapshot) tableFor(agentID string) *Table {
	return s.tables[agentID]
}

// tablesOf 按 agent 造出工具边界表。
//
// ★ 一次造好、之后只用同一批实例 —— 见 [snapshot.tables] 的注释。
func tablesOf(sp agentSpec) map[string]*Table {
	out := make(map[string]*Table, len(sp.agents))
	for id, as := range sp.agents {
		out[id] = specTable(sp.policy, as)
	}
	return out
}
