package message

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

// 本文件是**消息出处**（provenance）的唯一家：一条消息是谁产出的、什么时候
// 产出的，都记在 `schema.Message.Extra` 里。
//
// ★ 为什么两个键放在一起：它们回答的是同一个问题的两半 —— 「这条消息
//
//	是谁、什么时候说的」。分到两个包里，键名就会各自漂移，而键名一漂移，
//	按 agent 过滤与按时间排序就再也对不上了。
//
// ★ 为什么落在 `Extra` 而不是新字段：`schema.Message` 是 eino 的类型，
//
//	加字段要动上游；而 `Extra` 本来就是「定制信息」的位置。且它**不会**
//	漏进模型请求 —— openheron 的协议层在消息转换时完全不读 `schema.Message.Extra`
//	（llm/common/request.go:177）。
//
// ★ 键名带 `openheron_` 前缀：与模型自己可能用到的 `Extra` 键区分开。
const (
	// ExtraAgent 是产出这条消息的 agent 名。
	ExtraAgent = "openheron_agent"
	// ExtraTS 是这条消息的**产生时刻**。
	ExtraTS = "openheron_ts"
	// ExtraSubagent 标记这条消息产生在**子 agent 的运行内部**。
	//
	// ★ 它与 [ExtraAgent] **正交**，不能互相替代：
	//
	//	ExtraAgent 答的是「哪个 agent 产出的」；
	//	ExtraSubagent 答的是「它产生在一次委派里面没有」。
	//
	//	为什么不能只看 agent 名：会话可以换根 agent
	//	（`PATCH /v2/sessions/:id/agent`）。换过之后，**旧根**的消息
	//	（`openheron_agent: "main"`）看起来与根 agent 的消息毫无区别 ——
	//	而按 agent 名过滤会把它们当成子 agent 的剔掉，或者反过来
	//	把子 agent 的当成根的留下。
	//
	//	而「产生在委派内部」是**这一轮的结构事实**，与谁在当根无关。
	ExtraSubagent = "openheron_subagent"
	// ExtraReasoningMs 是这条消息的**思维链用时**（毫秒）。
	//
	// ★ 0 = **不知道**（这条消息没有思维链，或者没记上）——
	//   不是「想了 0 毫秒」。
	//
	// ★ 为什么由**后端**算并存下来：思维链的起止只存在于事件流里，
	//   而事件流不落盘。历史回放时前端拿不到事件，只能从消息本身读 ——
	//   否则刷新一次，「想了多久」就没了。
	//
	//	用时按**服务端**的时刻算：客户端只知道事件到达的时间，
	//	那里面混着网络延迟。
	ExtraReasoningMs = "openheron_reasoning_ms"
	// ExtraModel 是产出这条消息的**模型**（`<provider>/<model>`）。
	//
	// ★ 记的是**实际服务那次请求的**那个模型，不是配置文件里写的那个 ——
	//   skill 可以覆盖 model（agentkit/build/skill.go），记配置值会与实际不符。
	ExtraModel = "openheron_model"
	// ExtraThinking 是产出这条消息时**实际注入请求**的思考档位名。
	//
	// ★ 空 = 没注入思考字段（模型没有该档位、或配置里压根没写）。
	//   它同时也是「装配那一刻配置里的档位」—— 模型实例带着它，
	//   注入的就是它。
	ExtraThinking = "openheron_thinking"
)

// tsLayout 是时间戳的存储格式。
//
// ★ **定宽**，不是 `time.RFC3339Nano` —— 后者会**丢掉末尾的零**，于是
//
//	"…28.071847747Z" 与 "…28.07Z" 长度不同，字典序比较出来的先后是反的
//	（'0' < 'Z'，纳秒多的那个反而排在前面）。而「按时间排序」正是这个
//	时间戳存在的理由之一，排序错了它就没有价值。
//
// ★ 一律 UTC：时区偏移会让同一批消息的字符串长度都不一样，跨机器比较
//
//	也就不可靠了。
const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

// Provenance 是「这条消息的出处」。
//
// ★ 它把六个标记（agent / ts / subagent / reasoningMs / model / thinking）
//
//	收进**一个类型**。此前它们是六个 map 键、六个写函数、六个读函数，
//	散在三个文件里 —— 于是「读一条消息的出处」要记住六个函数名，
//	而「这六个键各是什么含义」没有一个地方能一次读完。
//
// ★ 写只有一处（[Provenance.Stamp]），读只有一处（[Read]）。
//
//	下面的 Stamp* / *Of 函数是它们的**薄封装**，保留是为了让调用点
//	不必一次改完；新代码直接用这个类型。
type Provenance struct {
	// Agent 是产出这条消息的 agent 名。
	Agent string
	// TS 是这条消息的**产生时刻**（不是归档那一刻，见 [StampTS]）。
	TS time.Time
	// Subagent 标记这条消息产生在**子 agent 的运行内部**（见 [StampSubagent]）。
	Subagent bool
	// ReasoningMs 是这条消息的思维链用时（毫秒）；0 = 不知道。
	ReasoningMs int64
	// Model 是产出这条消息的模型（`<provider>/<model>`）。
	Model string
	// Thinking 是产出这条消息时**实际注入请求**的思考档位名。
	Thinking string
}

// TSString 返回这条消息的时间戳**原样字符串**。
//
// ★ 原样而不是重新格式化：存储格式是定宽的（见 tsLayout），而
//
//	time.RFC3339Nano 会丢掉末尾的零 —— 重新格式化一次就把「可按字典序
//	排序」这个性质弄丢了，而那正是这个时间戳存在的理由之一。
//
//	（读回来的时刻按同一个布局格式化，得到的就是存储的那一串字节 ——
//	定宽布局保证了这一点。）
func (p Provenance) TSString() string {
	if p.TS.IsZero() {
		return ""
	}
	return p.TS.UTC().Format(tsLayout)
}

// Stamp 把出处写进消息。
//
// ★ 幂等，且**先写者胜**：每个键只在「还没有」时写。
//
//	归属、时刻、模型、档位都是**已经发生的事实** —— 后写的那个不能推翻
//	先写的那个（同一条消息被标两次：产出时标一次，归档前又标一次）。
//
// ★ 零值不写：空 agent、零时刻、零用时、空档位都不落键 ——
//
//	「没记上」与「记了个零值」是两件事：读出来一个是「不知道」，
//	另一个是「确实为零」。
func (p Provenance) Stamp(msg *schema.Message) {
	if msg == nil {
		return
	}
	if p.Agent != "" {
		put(msg, ExtraAgent, p.Agent)
	}
	if !p.TS.IsZero() {
		put(msg, ExtraTS, p.TS.UTC().Format(tsLayout))
	}
	if p.Subagent {
		put(msg, ExtraSubagent, true)
	}
	if p.ReasoningMs > 0 {
		put(msg, ExtraReasoningMs, p.ReasoningMs)
	}
	if p.Model != "" {
		put(msg, ExtraModel, p.Model)
	}
	if p.Thinking != "" {
		put(msg, ExtraThinking, p.Thinking)
	}
}

// put 写一个键，**先写者胜**。
//
// ★ 它顺手造 Extra —— 但只在真的要写的时候：造一个空 map 再什么都不写，
//
//	会让「这条消息没有出处」这件事变得看不出来（序列化时被 omitempty
//	抹掉，可读代码时却多了一层噪音）。
func put(msg *schema.Message, key string, value any) {
	if msg.Extra == nil {
		msg.Extra = map[string]any{}
	}
	if _, ok := msg.Extra[key]; ok {
		return
	}
	msg.Extra[key] = value
}

// Read 读出一条消息的出处。
//
// ★ 第二返回值是「有没有出处」：老消息（加这些标记之前写的）一个键都没有，
//
//	那与「有出处但某个字段是零值」是两回事 —— 前者是「不知道」。
func Read(msg *schema.Message) (Provenance, bool) {
	if msg == nil || msg.Extra == nil {
		return Provenance{}, false
	}
	p := Provenance{
		Agent:       MessageAgent(msg),
		Subagent:    IsSubagent(msg),
		ReasoningMs: ReasoningMs(msg),
		Model:       ModelRef(msg),
		Thinking:    ThinkingTier(msg),
	}
	if ts, ok := MessageTS(msg); ok {
		p.TS = ts
	}
	_, hasTS := msg.Extra[ExtraTS]
	ok := hasTS || p.Agent != "" || p.Subagent || p.ReasoningMs != 0 ||
		p.Model != "" || p.Thinking != ""
	return p, ok
}

// StampAgent 在消息上标记它属于哪个 agent。
//
// ★ 幂等：**先写者胜**。归属是「谁产出的」这个事实，同一条消息被标两次
//
//	（归档前又标了一次）时，后写的那个不能推翻先写的。
func StampAgent(msg *schema.Message, agent string) {
	Provenance{Agent: agent}.Stamp(msg)
}

// MessageAgent 读一条消息的 agent 归属。
//
// ★ 空 = 不知道是谁（user 消息、加这个标记之前的老会话）。不猜。
func MessageAgent(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	v, _ := msg.Extra[ExtraAgent].(string)
	return v
}

// StampSubagent 标记这条消息产生在子 agent 的运行内部。
//
// ★ 幂等：先写者胜（同 [StampAgent] 的理由）。
func StampSubagent(msg *schema.Message) {
	Provenance{Subagent: true}.Stamp(msg)
}

// IsSubagent 报告这条消息是否产生在子 agent 的运行内部。
//
// ★ 这是**模型上下文**的过滤判据：子 agent 的中间过程留在记录里（可观测），
//
//	但**不进父 agent 的下一轮上下文** —— 隔离靠的是它，不是靠「不记录」。
func IsSubagent(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	v, _ := msg.Extra[ExtraSubagent].(bool)
	return v
}

// StampReasoningMs 记下这条消息的思维链用时（毫秒）。
//
// ★ ms <= 0 时**不写**：那意味着「没记上」，写个 0 会被读成「想了 0 毫秒」。
//
// ★ 先写者胜（与其余标记同一套语义）：用时是「这条消息思考了多久」这个
//
//	事实，后来者不能改写它。
func StampReasoningMs(msg *schema.Message, ms int64) {
	Provenance{ReasoningMs: ms}.Stamp(msg)
}

// ReasoningMs 读出这条消息的思维链用时（毫秒）；0 = 不知道。
func ReasoningMs(msg *schema.Message) int64 {
	if msg == nil || msg.Extra == nil {
		return 0
	}
	switch v := msg.Extra[ExtraReasoningMs].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		// ★ JSON 读回来的是 float64 —— 归档走 JSONL，回放时就是这个类型。
		return int64(v)
	default:
		return 0
	}
}

// StampTS 给消息打上产生时刻。
//
// ★ 时刻的语义是「**这条消息被产出的那一刻**」，不是「它被归档的那一刻」。
//
//	并行委派下这两个时刻差得很远：归档是一轮结束时一次性做的，而产出是
//	每个 agent 各做各的。若在归档时打戳，一轮内所有消息的时间戳会几乎
//	相同 —— 交错顺序的信息就整个丢了。
//
// ★ 幂等：先写者胜（同 [StampAgent] 的理由）。
func StampTS(msg *schema.Message, t time.Time) {
	Provenance{TS: t}.Stamp(msg)
}

// StampNow 用当前时刻给消息打戳。
func StampNow(msg *schema.Message) {
	StampTS(msg, time.Now())
}

// TSString 原样取出存储的时间戳字符串。
//
// ★ **原样**而不是重新格式化：存储的格式是定宽的（见 tsLayout），而
//
//	time.RFC3339Nano 会丢掉末尾的零 —— 重新格式化一次就把「可按字典序
//	排序」这个性质弄丢了，而那正是这个时间戳存在的理由之一。
func TSString(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	s, _ := msg.Extra[ExtraTS].(string)
	return s
}

// MessageTS 读一条消息的产生时刻。
//
// ★ 第二返回值是「有没有」：老会话（加这个标记之前写的）没有时间戳，
//
//	那与「时刻是零值」是两回事 —— 前者是「不知道」，后者是「1970 年」。
func MessageTS(msg *schema.Message) (time.Time, bool) {
	if msg == nil || msg.Extra == nil {
		return time.Time{}, false
	}
	s, ok := msg.Extra[ExtraTS].(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(tsLayout, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// StampModel 记下产出这条消息的模型与思考档位。
//
// ★ ref 是**实际服务那次请求**的 `<provider>/<model>`；tier 是**实际注入
//
//	请求**的档位名（空 = 没注入思考字段）。
func StampModel(msg *schema.Message, ref, tier string) {
	Provenance{Model: ref, Thinking: tier}.Stamp(msg)
}

// ModelRef 读出产出这条消息的模型引用；空 = 不知道。
func ModelRef(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	s, _ := msg.Extra[ExtraModel].(string)
	return s
}

// ThinkingTier 读出产出这条消息时的思考档位；空 = 没注入 / 不知道。
func ThinkingTier(msg *schema.Message) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	s, _ := msg.Extra[ExtraThinking].(string)
	return s
}
