package build

import (
	"sort"
	"strings"
	"unicode"

	"openheron/agentkit/spec/decl"
)

// 本文件是「某个 agent 名下某个工具到底能不能用」的**唯一产地**。
//
// ★ 它存在的理由是一个具体的坑：这份判定横跨两张表 —— [Table] 决定工具在
//
//	不在表里（deny 优先于 disable），[Policy] 决定表里的工具要不要人批。
//	两处语义互相咬合，而只有装配方知道正确的合成方式。任何人为了回答
//	「这个 agent 能干什么」在别处重算一遍，漂移只是时间问题 —— 而且两边
//	都能各自测通，所以漂移不会以「测试红了」的形式出现。
//
//	所以接口只读、结果来自真实的 [Builder] 状态，且刻意不提供「传一份
//	Policy 进来自己算」的变体：那个变体正是它要消灭的东西。

// ToolVerdict 是某个 agent 名下某个工具的**生效**结论。
//
// ★ Tier 不是「策略写了什么」，而是「模型这一轮碰到了会怎样」。被 [Layer]
//
//	摘掉的工具对模型根本不存在，生效档位因此恒为 deny —— 即便 policy
//	把它列在 allow 里，模型也永远走不到审批那一步。
type ToolVerdict struct {
	// Name 是工具名。
	Name string
	// Tier 是生效档位：allow / ask / deny。
	Tier Tier
	// Blocked 记下是哪一层摘的；LayerNone 表示工具表里留着它。
	Blocked Layer
	// Present 报告这个名字是否**真的**在运行期出现过。
	//
	// ★ 见 [Observed]：它把「挡住了一个真实存在的工具」与「策略点名了一个
	//   不存在的工具」分开。名字是拼错时两者此前长得一模一样。
	Present bool
	// ObservedKnown 表示 Present 可信。false = 还没跑过任何一轮。
	ObservedKnown bool
	// Reason 是给人看的**原因**，不是给程序判的。
	//
	// ★ 它是从 Blocked 派生的（见 [verdictOf]），不单独存：单独存就会出现
	//   「Blocked 说 deny、Reason 说 disabled_tools」这种自相矛盾，
	//   而接口照抄矛盾的那一份。api 层要它是因为
	//   「为什么这个 agent 调不到」是用户唯一真正需要的信息。
	Reason string
}

// Reached 报告模型是否够得着这个工具。
func (v ToolVerdict) Reached() bool { return v.Blocked == LayerNone }

// Observed 报告这个名字是否**真的**在运行期出现过。
//
// ★ 它把「策略挡住了 grep」与「策略点名了一个不存在的工具」分开 —— 后者
//
//	通常是 `deny: [grep_search]` 这种拼错，而此前两者在接口上长得一模一样：
//	都是一行 deny。照着它去排查，用户会去找一个从没存在过的东西。
type Observed struct {
	// Names 是**上一轮**框架实际交出来的工具名（已排序）。
	Names []string
	// Known 表示至少跑过一轮，因而 Names 是可信的。
	//
	// ★ false 时必须如实说出来。把「还不知道」当成「没有」，正是这个端点
	//   此前一直在犯的错：`task`、`skill` 由框架中间件注册，名字不出现在
	//   任何配置文件里，于是从来没人报过它们。
	Known bool
}

// Has 报告某个名字是否出现在观测里。
func (o Observed) Has(name string) bool {
	for _, n := range o.Names {
		if n == name {
			return true
		}
	}
	return false
}

// reasonOf 把「哪一层摘的」翻成对用户有意义的措辞。
//
// ★ Layer 是**内部**概念（deny 是外层、disable 是内层），直接报出去等于把
//
//	实现细节当接口。措辞要指向**该改哪个文件**：
//	policy.deny ⇒ 改 policy/permissions.yaml；disabled_tools ⇒ 改 agents/<id>.md。
func reasonOf(l Layer) string {
	switch l {
	case LayerDeny:
		return "policy.deny"
	case LayerDisable:
		return "disabled_tools"
	default:
		return ""
	}
}

// EffectiveTools 报告 agentID 名下每个工具的生效结论。
//
// agentID 为空或等于入口 agent 名时按入口 agent 算。未声明的名字退回根表 ——
// 少报一项比直接报错更难查，而调用方（自省端点）本来就会先校验它存在。
//
// ★ 它读的是管理器当前一代派生出的快照 —— 配置换内容后，这里与装配同源。
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

// RuntimeTools 报告 agentID 名下**运行期真实出现过**的工具。
//
// ★ 这份清单只能观测，不能计算：eino 的 fs/skill 中间件在每一轮的
//
//	`runCtx.Tools` 里才把工具交出来（`adk/handler.go:98`），而
//	`adk.ChatModelAgent` 没有任何办法在装配期问出它有哪些工具。所以
//	[Observed.Known] 为 false 是**正常状态**（还没跑过任何一轮），
//	而不是错误 —— 调用方必须把这两件事分开报。
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

// tableOfAgent 返回某个 agent 的工具边界表。
//
// ★ 每个 agent 都有一张**缓存的**表（见 [snapshot.tables]）：
//
//	运行期观测到的工具名记在表实例上，而自省要读的正是那些观测 ——
//	装配与自省必须是同一批实例。
//
// ★ 以前只有「能当会话入口」的那几个 agent 有缓存表，更深的那些要现造
//
//	一张（拿不到运行期观测）。现在每个 agent 都是入口，表也就按 agent
//	建齐了 —— 现造那张的分支随之消失。
func tableOfAgent(snap *snapshot, agentID string) *Table {
	if agentID == "" {
		return snap.tableFor(snap.agentSpec.defaultAgent)
	}
	if t := snap.tableFor(agentID); t != nil {
		return t
	}
	return snap.tableFor(snap.agentSpec.defaultAgent)
}

// observedOf 读一张表的观测结果。
func observedOf(t *Table) Observed {
	names, ok := t.Observed()
	return Observed{Names: names, Known: ok}
}

// PolicyTools 报告策略本身的三档，**不含**任何 agent 的 disabled_tools 的影响。
//
// ★ 与 [Builder.EffectiveTools] 的差别就是后者多套了一层 [Table]：`/v2/config`
//
//	要的是策略视图，把某个 agent 的禁用混进去会让「为什么这个工具是 deny」
//	指向错的文件。
//
//	但名字的并集仍然覆盖**全部** agent 的 disabled_tools：那些工具确实存在于
//	系统里，清单上必须有它们，只是报的是策略档位而非被谁摘掉了。
func (b *Builder) PolicyTools() []ToolVerdict {
	snap := b.currentSnapshot()
	if snap == nil {
		return nil
	}
	policy, rootTable, subs := snap.agentSpec.policy, snap.tableFor(snap.agentSpec.defaultAgent), snap.subs()
	// 名字的并集要收到所有 agent 声明过的工具（根表 + 子 agent 树），
	// 外加运行期观测到的那些。
	obs := observedOf(rootTable)
	names := toolNameUniverse(newBoundary(policy, rootTable), snap.cfg.EnabledToolNames(), subs, obs)

	// ……但档位一律按**空表**判：策略视图里不许出现任何 agent 分层。
	flat := newBoundary(policy, specTable(Policy{}, decl.SubAgentSpec{}))
	out := make([]ToolVerdict, 0, len(names))
	for _, n := range names {
		out = append(out, verdictOf(flat, n, obs))
	}
	return out
}

// verdictOf 判定单个工具的生效结论。
//
// ★ 两级的顺序不可换：**先看工具表，再看审批档位**。
//
//	反过来的话，一个被本 agent 禁用、但策略列在 allow 的工具会报成 allow ——
//	而模型够不着它。接口于是说「这个 agent 能直接读这个工具」，用户照着
//	配的模型却每次都因为工具不存在而绕路。
//
// ★ 参数是 [boundary] 而不是 Policy + Table 两个：审批档位与工具表必须
//
//	来自**同一个 agent**，拆开传就又回到了「传错配对编译不报错」的老路。
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

// toolNameUniverse 是要汇报的工具名全集：策略表 + 配置里启用的工具 +
// 所有 disabled_tools + 运行期观测。
//
// ★ 「配置里启用的工具」要单独加一份：`tools:` 段的工具（网页搜索、维基、
//
//	HTTP 请求）不在策略里 —— 策略只管「要不要点头」，不管「有没有这个
//	能力」✅ ✅。没有它们，用户在工具表里看不到自己启用的能力 ✅ ✅。
//
// 没被任何配置点过名的工具无从枚举 —— 那部分由 [Policy.TierOf] 的兜底
// （allow ✅）在运行期给出 ✅ ✅。
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
	// ★ middleware / built-in 拥有的那些名字（ls / read_file / task / skill …）
	//	也要在清单里 —— 它们**不由配置声明** ✅ ✅，但用户得看得见「系统有
	//	哪些工具、它们是什么档位」✅ ✅。
	for name := range reservedToolNames {
		add([]string{name})
	}
	if table := b.Table(); table != nil {
		add(table.Disable)
	}
	// 策略视图不套 agent 分层，但清单上仍要列出别的 agent 声明过的工具。
	for _, s := range subs {
		addSpecDisabled(add, s)
	}
	// ★ 运行期观测也必须进全集：task / skill 由 eino 中间件注册，名字不出现
	// 在任何配置文件里。
	add(obs.Names)

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	// 汇报的东西要有稳定顺序，否则同一份配置的两次响应看不出是同一份。
	sort.Strings(out)
	return out
}

// addSpecDisabled 把一棵声明树里的 disabled_tools 摊平。
func addSpecDisabled(add func([]string), agentSpec decl.SubAgentSpec) {
	add(agentSpec.DisabledTools)
	for _, s := range agentSpec.Sub {
		addSpecDisabled(add, s)
	}
}

// DescriptionVerdict 报告一个 agent 的描述**够不够模型分辨**。
//
// ★ 它关心的是「模型选不选它」：专家被建模成工具之后，模型选工具
//
//	**只依据描述**（见 [Builder.Build] 的装配点）。描述是空的、或者
//	两个专家的描述几乎一样，模型就分不出该叫谁 —— 而症状是
//	「模型老是不叫那个专家」，从外面看不出原因。
//
// ★ 它是**提示**不是闸门：模型选错工具的后果由模型承担，
//
//	而配置校验不该因为「两段描述像」就拦住启动。
type DescriptionVerdict struct {
	// AgentID 是被判定的 agent。
	AgentID string
	// Empty 报告描述为空。
	//
	// ★ 空描述是最严重的一种：模型连这个工具是干什么的都不知道。
	Empty bool
	// Duplicates 是与它**高度相似**的其它 agent id（按 id 排序）。
	Duplicates []string
	// Distinct 报告它是否可区分（非空，且不与别的重复）。
	Distinct bool
}

// DescriptionVerdicts 报告各 agent 描述的可区分性。
//
// ★ 它读的是管理器当前一代派生出的快照 —— 与装配同源，
//
//	所以「描述重复」的结论对**正在跑的那一代**成立。
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

// descriptionSimilarityThreshold 是「两段描述算重复」的阈值。
//
// ★ 0.8 是经验值：它要抓的是「几乎逐字相同」与「只是用词相近」之间
//
//	那一档 —— 前者模型分不出该叫谁，后者分得出。
const descriptionSimilarityThreshold = 0.8

// similarDescriptions 判两段描述是否**高度相似**。
//
// ★ 判据是**字符二元组**的重合度（Jaccard），不是编辑距离 ——
//
//	编辑距离问的是「这两句话差几个字」，而模型看到的是「这两段话
//	说的是不是一件事」。二元组更接近后一个问题。
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

// descriptionTokens 把一段描述切成**字符二元组**集合。
//
// ★ 用字符二元组而不是词：描述大多是中文，按空格切词会把整句变成
//
//	一个「词」，重合度恒为 0 —— 那样判据就废了。二元组对中英文都成立，
//	且对「同一件事换个说法」与「两件不同的事」区分得开。
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
