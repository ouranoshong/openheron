package spec

import (
	"fmt"
	"openheron/agentkit/spec/decl"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlNode 是 yaml.Node 的别名，让 decl.Duration 的方法签名可读。
type yamlNode = yaml.Node

// loadProvider 读一个 llm/<id>.yaml。
func (c *Config) loadProvider(file string, data []byte) error {
	id := strings.TrimSuffix(file, ".yaml")
	if id == "" {
		return fmt.Errorf("provider file name is empty")
	}

	var p decl.Provider
	if err := decodeStrict(data, &p); err != nil {
		return err
	}
	p.ID = id

	if p.Type == "" {
		return fmt.Errorf("missing type")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("missing base_url")
	}
	rawKey := p.APIKey
	key, resolved, err := expandValue(rawKey)
	if err != nil {
		return fmt.Errorf("api_key: %w", err)
	}
	if !resolved {
		// ★ 密钥此刻取不到 ⇒ **不阻塞启动**，只记一条提醒。真正失败点移到
		//   调用该端点时：空 key 会拿到 401，协议层归成 auth 并提示检查 api_key。
		//   这样未用到的 provider（如模板里留着待用的一家）不会拖垮整个进程。
		p.APIKey = ""
		c.deferKey("llm/%s.yaml: api_key env %s is not set; calls to this endpoint will fail with 401 Unauthorized",
			id, envVarName(rawKey))
	} else {
		p.APIKey = key
	}

	// 同名文件**整体替换**：先撤掉它之前声明的模型，再登记新的。
	for ref := range c.Models {
		if ProviderIDOf(ref) == id {
			delete(c.Models, ref)
		}
	}
	for i, m := range p.Models {
		if m == nil {
			continue
		}
		if m.ID == "" {
			return fmt.Errorf("models[%d]: missing id", i)
		}
		c.Models[id+"/"+m.ID] = m
	}

	// ★ 同级出现同名文件在文件系统层面不可能，所以「已存在」就是覆盖，
	//   不是错误 —— 同名文件整体替换是预期行为。
	c.Providers[id] = &p
	return nil
}

// loadAgent 读一个 agents/<id>.md。
func (c *Config) loadAgent(file string, data []byte) error {
	id := strings.TrimSuffix(file, ".md")
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}

	var a decl.Agent
	if err := decodeStrict(fm, &a); err != nil {
		return fmt.Errorf("%s: frontmatter: %w", id, err)
	}
	a.ID = id
	a.File = DirAgent + "/" + file
	a.Instruction = body

	// ★ 缺 model 是错误，**不继承、不回退**。子 agent 静默继承主模型是成本事故。
	if strings.TrimSpace(a.ModelRef) == "" {
		return fmt.Errorf("%s: must declare model (no inheritance, no fallback)", id)
	}
	if _, err := ParseRef(a.ModelRef); err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}

	c.Agents[id] = &a
	return nil
}

// loadServer 读 server/config.yaml。
func (c *Config) loadServer(file string, data []byte) error {
	// 预填默认值 ⇒ 文档里没出现的键保留默认（这就是分层合并的机制）。
	s := c.Server
	if err := decodeStrict(data, &s); err != nil {
		return err
	}
	c.Server = s
	return nil
}

// loadTool 读 tools/ 下的一个文件。
//
// ★ **按文件名分派** —— tools/ 里每个文件是一种工具，形状各不相同：
//
//	http.yaml        → decl.HTTPToolsCfg
//	web_search.yaml  → decl.WebSearchCfg
//	wikipedia.yaml   → decl.WikipediaCfg
//	kb.yaml          → decl.KB
//
//	所以每个文件的内容就是**那个工具自己的配置**（没有外层包装键）——
//	「文件在不在」就是开关，见 [decl.ToolsCfg]。
//
// ★ 未知文件名**报错**：写进去一个加载时不认的文件，等于配置里有个
//
//	永远不生效的段落，而症状是「我明明写了却不生效」—— 极难查。
func (c *Config) loadTool(file string, data []byte) error {
	switch strings.TrimSuffix(file, ".yaml") {
	case "http":
		var t decl.HTTPToolsCfg
		if err := decodeStrict(data, &t); err != nil {
			return err
		}
		c.Tools.HTTP = &t
		return nil
	case "web_search":
		var t decl.WebSearchCfg
		if err := decodeStrict(data, &t); err != nil {
			return err
		}
		c.Tools.WebSearch = &t
		return nil
	case "wikipedia":
		var t decl.WikipediaCfg
		if err := decodeStrict(data, &t); err != nil {
			return err
		}
		c.Tools.Wikipedia = &t
		return nil
	case "kb":
		return c.loadKB(data)
	default:
		return fmt.Errorf("tools/ only accepts http.yaml, web_search.yaml, wikipedia.yaml and kb.yaml; unknown file %q", file)
	}
}

// loadKB 读 tools/kb.yaml。
//
// ★ 切分器的 Headers 是 **map，整体替换**（不合并）—— 与 decl.Provider.Header
//
//	/ Extra 同一套语义。合并 map 会让「只想改一个标题层级」变成默认
//	外加一层，而症状是「配的 ### 不见了 / 还在」，两种都很难查。
//
// ★ yaml.v3 把 map 解进**已存在的**那个 map（往里塞键），所以「预填默认值
//
//   - 整体替换」这两件事直接放一起是矛盾的：预填的默认永远留在结果里。
//     这里显式清一次。探针刻意只有一个字段且**不是**严格解 —— 它只回答
//     「这个键在不在」，紧接着的 decodeStrict 仍然负责全部严格性，
//     所以 `headr:` 这种拼错照样会报错。
func (c *Config) loadKB(data []byte) error {
	k := c.KB
	var probe struct {
		Splitter *struct {
			Headers *map[string]string `yaml:"headers"`
		} `yaml:"splitter"`
	}
	if err := yaml.Unmarshal(data, &probe); err == nil &&
		probe.Splitter != nil && probe.Splitter.Headers != nil {
		k.Splitter.Headers = nil
	}
	// 预填默认值 ⇒ 文档里没出现的键保留默认（这就是分层合并的机制）。
	if err := decodeStrict(data, &k); err != nil {
		return err
	}
	c.KB = k
	return nil
}

// loadPolicy 读 policy/permissions.yaml。
//
// ★ **deny 与出厂默认取并集**：整体替换会让「解除一条禁令」变成一次可提交
// 的提权（cairn 有同款风险，其 Grant 因此只允许在最高层）。
//
// ★ **ask 则是整体替换** —— 它是偏好，不是安全边界。
//
//	★ 于是这份文件若没写 ask，出厂那几条审批就都没了：
//	「openheron init」把 ask 列表写全，正是为了让这件事**显式**而不是靠默认。
func (c *Config) loadPolicy(file string, data []byte) error {
	switch strings.TrimSuffix(file, ".yaml") {
	case "permissions":
		var p decl.Policy
		if err := decodeStrict(data, &p); err != nil {
			return err
		}
		c.Policy.Deny = union(c.Policy.Deny, p.Deny)
		c.Policy.Ask = p.Ask
		return nil
	case "limits":
		// ★ 限制规则：逐字段**取更严的**（min），不是「后加载的覆盖先加载的」。
		//
		//	限制是安全边界 —— 只能收紧，不能放松。若允许覆盖，
		//	「放一份 limits.yaml 写个大数」就成了一条提权路径。
		//	这与上面 deny 取并集是同一个思路：安全边界只往严的方向合并。
		var l decl.Limits
		if err := decodeStrict(data, &l); err != nil {
			return err
		}
		c.Limits = mergeLimits(c.Limits, l)
		return nil
	default:
		return fmt.Errorf("policy/ only accepts permissions.yaml and limits.yaml; unknown file %q", file)
	}
}

// mergeLimits 合并两份限制规则，逐字段**取更严的**。
//
// ★ 0 = 不设限，不参与比较 —— 所以「0 与 5 取严」得 5，不是 0。
//
//	两边的 0 与 0 取 0（都不设限），由上层的默认值兜底。
func mergeLimits(a, b decl.Limits) decl.Limits {
	return decl.Limits{
		Agent: decl.AgentLimit{MaxDepth: tighter(a.Agent.MaxDepth, b.Agent.MaxDepth)},
		Retry: decl.RetryLimit{MaxRetries: tighter(a.Retry.MaxRetries, b.Retry.MaxRetries)},
	}
}

// tighter 取两个上限里更严的那个；0 表示不设限，不参与比较。
func tighter(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// union 合并并去重，**保持稳定顺序**（便于日志与测试断言）。
func union(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, v := range append(append([]string{}, a...), b...) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// ── 引用解析 ─────────────────────────────────────────────────────────────

// Ref 是一个 `<provider-id>/<model-id>` 引用。
type Ref struct {
	ProviderID string
	ModelID    string
}

func (r Ref) String() string { return r.ProviderID + "/" + r.ModelID }

// ParseRef 拆引用：取第一个 `/`，两侧都不得为空。
func ParseRef(s string) (Ref, error) {
	i := strings.Index(s, "/")
	if i < 0 {
		return Ref{}, fmt.Errorf("model ref %q is missing the provider prefix; expected <provider>/<model>", s)
	}
	if i == 0 || i == len(s)-1 {
		return Ref{}, fmt.Errorf("model ref %q has an empty provider or model", s)
	}
	return Ref{ProviderID: s[:i], ModelID: s[i+1:]}, nil
}

// ProviderIDOf 从 `<provider>/<model>` 里取 provider 部分。
func ProviderIDOf(ref string) string {
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i]
	}
	return ref
}

// ── 校验 ─────────────────────────────────────────────────────────────────

// validate 是启动期的硬校验。
//
// 原则：**配错了就在启动时炸出来**，而不是等第一次模型调用以难懂的形态爆掉。
func (c *Config) validate() error {
	// 默认 agent 缺失 ⇒ 显式报错，不静默造一个。
	// 「没有默认 agent」与「默认 agent 配错了」是不同的两种问题。
	//
	// ★ main.md 是**必需**的：新会话没指定 agent 时用它，所以它必须存在。
	//   别的 agent 同样能当会话入口 —— main 只是那个默认值。
	if _, ok := c.Agents[DefaultAgentID]; !ok {
		return fmt.Errorf("agents/%s.md does not exist or could not be parsed", DefaultAgentID)
	}

	// ★ 每个 agent 的模型引用都要能解析到已声明的 ModelSpec。
	//   解析不到直接报错，**不静默退化到默认模型** ——
	//   那会让「为什么这么贵」变成查不出的问题。
	ids := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids) // 稳定顺序 ⇒ 多处错误时报告可复现

	for _, id := range ids {
		a := c.Agents[id]
		ref, _ := ParseRef(a.ModelRef)
		if _, ok := c.Models[ref.String()]; !ok {
			// ★ 带文件路径：这是**跨文件**错误，错的不是引用它的那个 agent，
			//   而是被删/没建的那个模型档案。不说路径的话用户只知道
			//   「agent broken」，得自己去 llm/ 里逐个翻才知道该动哪一个。
			where := a.File
			if where == "" {
				where = DirAgent + "/" + id + ".md"
			}
			return fmt.Errorf("agent %s (%s) references model %s which is not declared under llm/",
				id, where, a.ModelRef)
		}
	}

	// subagents 的引用要能解析，且不能有环。
	if err := c.validateAgentGraph(ids); err != nil {
		return err
	}
	if err := c.validateKB(); err != nil {
		return err
	}
	return c.validateDepth(ids)
}

// validateKB 校验知识库配置。
//
// ★ 语料**拒绝绝对路径**是这一节最要紧的一条。它不是一个「路径格式」的讲究，
//
//	而是「索引与语料同生共死」的结构保证：索引跟着 projectDir 走，语料一旦
//	能指向项目外，两者就会分家，而那种错配**不报错** —— 只是自信地返回
//	另一个项目的文档。
func (c *Config) validateKB() error {
	if c.KB.Corpus == "" {
		// 没配语料 = 知识库关闭。这不是错误：没配就不该有一个召回不到东西
		// 的 kb_search 挂在工具表里。
		return nil
	}
	if filepath.IsAbs(c.KB.Corpus) {
		return fmt.Errorf("kb.corpus must be relative to the project root, got an absolute path %q; "+
			"the index and its corpus must have the same lifetime", c.KB.Corpus)
	}
	if c.KB.Model == "" {
		return fmt.Errorf("kb.model is required when kb.corpus is set; "+
			"it must reference a model declared with kind: embedding under %s/", DirLLM)
	}
	ref, _ := ParseRef(c.KB.Model)
	m, ok := c.Models[ref.String()]
	if !ok {
		return fmt.Errorf("kb.model %s is not declared under %s/", c.KB.Model, DirLLM)
	}
	if !m.IsEmbedding() {
		return fmt.Errorf("kb.model %s is not an embedding model; add `kind: embedding` to it "+
			"(a chat endpoint has no %s endpoint)", c.KB.Model, "/embeddings")
	}
	if m.Dimensions < 0 {
		return fmt.Errorf("kb.model %s declares dimensions: %d; it must be a positive count or "+
			"omitted (0 = unknown, learned at index time)", c.KB.Model, m.Dimensions)
	}
	// ★ **不校验 `splitter.headers`**（v2 起）。
	//
	//	它已经不决定任何产物 —— 章节与行号由行扫描产出（§6.2）。一个被忽略的
	//	字段再去校验内容，只会让「按文档说的把它清空」变成一次启动失败，而
	//	报出来的理由（「每个文件会变成一个块」）在 v2 里根本不成立。
	//
	//	保留字段本身（而不是删掉）的理由是 `decodeStrict`：存量配置里写着它，
	//	当未知字段处理会让那些配置起不来。§9.1 记了这两条的分工。
	return nil
}

// validateDepth 校验每个 agent 的**队伍**深度不超过上限。
//
// ★ **启动期就报错，而不是运行时静默截断**：配置里写了三层而上限是 1，
//
//	用户以为第三层会跑，实际它根本不存在 —— 这种「配了没生效」最难查。
//
// ★ 逐个 agent 校验：每个 agent 都能当会话入口，所以它**自己那支队伍**
//
//	的深度就得在上限内 —— 否则以它为入口跑的时候，深的那一层会被静默
//	截掉。
func (c *Config) validateDepth(sorted []string) error {
	max := c.MaxDepth()
	for _, id := range sorted {
		d, ok := c.teamDepth(id)
		if !ok {
			continue
		}
		if d > max {
			return fmt.Errorf(
				"agent %s has a team nested to depth %d, hitting the cap of %d; "+
					"raise server/config.yaml agent.max_depth (or policy/limits.yaml agent.max_depth) "+
					"or reduce the subagents nesting",
				id, d, max)
		}
	}
	return nil
}

// teamDepth 算出以 id 为入口时，它那支队伍的最深层数。
//
// ★ 第二个返回值是「到不到」—— id 自己在树里（深度 0），所以正常为 true；
//
//	找不到声明时为 false（那由别处的校验负责报错）。
func (c *Config) teamDepth(id string) (int, bool) {
	if _, ok := c.Agents[id]; !ok {
		return 0, false
	}
	visited := map[string]bool{}
	queue := []struct {
		name  string
		depth int
	}{{id, 0}}
	deepest := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth > deepest {
			deepest = cur.depth
		}
		if visited[cur.name] {
			continue
		}
		visited[cur.name] = true
		if sub, ok := c.Agents[cur.name]; ok {
			for _, s := range sub.SubAgents {
				queue = append(queue, struct {
					name  string
					depth int
				}{s, cur.depth + 1})
			}
		}
	}
	return deepest, true
}

// MaxDepth 返回嵌套深度上限。
//
// ★ 它取三处声明里**最严的**那个：
//
//	server/config.yaml 的 agent.max_depth —— 全局基线
//	policy/limits.yaml 的 agent.max_depth —— 可热更新的收紧
//
// ★ **只能收紧，不能放宽**。放宽上限是进程级的决定：改
//
//	server/config.yaml（要重启），而不是改一个能热改的文件。
//	理由与 [decl.Policy.Deny] 取并集同源：限制是安全边界，
//	只往严的方向合并 —— 否则「在配置根里放一份 limits.yaml 写个大数」
//	就成了提权路径。
//
//	★ 放宽的声明**不是静默忽略**：它会被记进 deferred，启动时打印
//	（见 [Config.collectNoopLimits]）。
//
// 0（两处都没声明）时用 [decl.DefaultMaxDepth]。
func (c *Config) MaxDepth() int {
	if c == nil {
		return decl.DefaultMaxDepth
	}
	return tighter(c.globalMaxDepth(), c.Limits.Agent.MaxDepth)
}

// MaxRetries 返回模型调用的重试次数上限。
//
// ★ 与 [Config.MaxDepth] 同一套语义：server/config.yaml 的
//
//	retry.max_retries 是全局基线，policy/limits.yaml 的是可热更新的收紧，
//	取最严的；放宽的声明会被记进 deferred。
//
// 0（两处都没声明）时用 [decl.DefaultMaxRetries]。
func (c *Config) MaxRetries() int {
	if c == nil {
		return decl.DefaultMaxRetries
	}
	return tighter(c.globalMaxRetries(), c.Limits.Retry.MaxRetries)
}

// globalMaxDepth 返回 server/config.yaml 里声明的深度基线（0 = 用默认）。
func (c *Config) globalMaxDepth() int {
	if c == nil || c.Server.Agent.MaxDepth <= 0 {
		return decl.DefaultMaxDepth
	}
	return c.Server.Agent.MaxDepth
}

// globalMaxRetries 返回 server/config.yaml 里声明的重试基线（0 = 用默认）。
func (c *Config) globalMaxRetries() int {
	if c == nil || c.Server.Retry.MaxRetries <= 0 {
		return decl.DefaultMaxRetries
	}
	return c.Server.Retry.MaxRetries
}

// collectNoopLimits 报告「声明了但没生效」的限制。
//
// ★ 限制只能收紧（见 [Config.MaxDepth]），所以声明一个比全局基线**更宽松**
//
//	的值是**空操作** —— 而「配了不生效」必须可见，所以进 deferred，
//	启动时打印。不能让它悄悄躺在那儿：用户会以为上限被放宽了。
//
// 只报**严格更宽松**的。等于基线的声明是无害的（本来就是那个值），
// 报它只会制造噪音。
func (c *Config) collectNoopLimits() {
	if c == nil {
		return
	}
	if v := c.Limits.Agent.MaxDepth; v > c.globalMaxDepth() {
		c.deferKey("policy/limits.yaml agent.max_depth: %d is looser than the global baseline (%d); "+
			"it has no effect — raise server/config.yaml agent.max_depth to widen the limit",
			v, c.globalMaxDepth())
	}
	if v := c.Limits.Retry.MaxRetries; v > c.globalMaxRetries() {
		c.deferKey("policy/limits.yaml retry.max_retries: %d is looser than the global baseline (%d); "+
			"it has no effect — raise server/config.yaml retry.max_retries to widen the limit",
			v, c.globalMaxRetries())
	}
}

// validateAgentGraph 校验 subagents 这张图：每个引用都能解析，且无环。
func (c *Config) validateAgentGraph(sorted []string) error {
	for _, id := range sorted {
		for _, sub := range c.Agents[id].SubAgents {
			if _, ok := c.Agents[sub]; !ok {
				known := make([]string, 0, len(c.Agents))
				for k := range c.Agents {
					known = append(known, k)
				}
				sort.Strings(known)
				return fmt.Errorf("agent %s subagents references unknown %q (declared: %s)",
					id, sub, strings.Join(known, ", "))
			}
		}
	}
	return c.detectCycle(sorted)
}

// detectCycle 深度优先找环，返回可读的环路径。
func (c *Config) detectCycle(sorted []string) error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string

	var visit func(string) error
	visit = func(id string) error {
		color[id] = grey
		path = append(path, id)
		for _, sub := range c.Agents[id].SubAgents {
			switch color[sub] {
			case grey:
				// 找到环：从它开始截断路径。
				at := 0
				for i, p := range path {
					if p == sub {
						at = i
						break
					}
				}
				return fmt.Errorf("subagents contains a cycle: %s", strings.Join(append(append([]string{}, path[at:]...), sub), " → "))
			case white:
				if err := visit(sub); err != nil {
					return err
				}
			}
		}
		path = path[:len(path)-1]
		color[id] = black
		return nil
	}

	for _, id := range sorted {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// ── 试合并用的改动 ───────────────────────────────────────────────────────

// mutation 是「本轮想改的那一个文件」，只活在内存里。
//
// ★ 它**不是**一次写入请求，而是一次**试算**：写入路径先造一个它、
//
//	跑一遍完整的加载与校验，合并后的配置不成立就放弃。
//	所以这个结构里没有任何能落盘的字段，也不该加 ——
//	加了就会出现「试算顺手把文件写了」，而那正好毁掉它存在的理由。
type mutation struct {

	// Rel 是相对配置根的路径，形如 `policy/permissions.yaml`。
	Rel string
	// Delete 为 true 时这次是删除，Content 无意义。
	Delete bool
	// Content 是候选内容（UTF-8 原文，不做规范化）。
	Content []byte
}
