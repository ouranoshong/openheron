package api

import (
	"strconv"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// toAgUiMessage maps one persisted openheron message onto the AG-UI wire shape.
//
// index is the message's position in the transcript; it is what makes the
// derived id unique (see stableID).
//
// The same shape streams back during a run and is served by
// GET /v2/sessions/{id}/messages, so the client feeds both through one
// reducer instead of keeping two view models in sync.
//
// Returns false for a message that carries nothing renderable.
func toAgUiMessage(m *schema.Message, index int) (types.Message, bool) {
	if m == nil {
		return types.Message{}, false
	}
	out := types.Message{ID: stableID(m, index)}

	// ★ 出处一次读完：归属、时刻、用时、模型、档位。
	//
	//	它们是**同一个概念** —— 「这条消息是谁、在什么时候、用什么产出的」。
	//	散在六个读函数里，读者要记住六个键名才知道一条消息带了什么。
	prov, _ := message.Read(m)

	// ★ Name 是 AG-UI 的「发送者名字」，这里用它承载**产出这条消息的
	//   agent**。
	//
	//   为什么是 Name 而不是别的：前端要能**按 agent 过滤消息列表**，
	//   而实时流与历史回放是两条不同的路 —— 实时流走 TEXT_MESSAGE_START
	//   事件，历史走这个转换。两边必须落在**同一个字段**上，否则过滤
	//   在「刷新后」就失效了。
	//
	//   入口 agent 的消息也带（就是根自己的名字）；没有归属标记的旧消息
	//   留空 —— 空 = 不知道是谁，不是「是根」。
	if prov.Agent != "" {
		out.Name = prov.Agent
	}

	// ★ Metadata 带出**产生时刻**。
	//
	//   归档的消息上记着它被产出的那一刻（message.ExtraTS），而 AG-UI 的
	//   Message 有个通用的 Metadata 字段 —— 走它，前端拿到的历史就带着
	//   时间，不必再要一个专门的接口。
	//
	//   ★ 并行委派下这个时刻是有用的：几个 agent 的输出是交错的，只有
	//     带着各自的时间戳，前端才能把「谁先谁后」摆对。
	//
	//   没有时间戳的老消息不带这个键 —— 空 = 不知道什么时候，不是「零时刻」。
	if ts := prov.TSString(); ts != "" {
		// ★ 对外的名字是 createdAt，**不是**内部键 openheron_ts。
		//
		//	内部键名进了契约，前端从此依赖它 —— 而它是实现细节：
		//	哪天内部键改名，协议就跟着断。契约里的名字要站在
		//	**使用方**的立场上（这条消息什么时候产生的）。
		out.Metadata = types.Metadata{"createdAt": ts}
	}
	// ★ 思维链也带上：**正文 + 用时**。
	//
	//	历史里原来**只有正文** —— 推理内容从来没进过历史，于是刷新一次，
	//	思维链整个没了（连正文都没了，只剩答案）。
	//
	//	用时由后端算好存在归档消息的 Extra 里（message.ExtraReasoningMs），
	//	这里只是带出去：事件流不落盘，不存下来就没有「刷新后想了多久」。
	if reasoning := message.AssistantDeltaReasoning(m); reasoning != "" {
		if out.Metadata == nil {
			out.Metadata = types.Metadata{}
		}
		out.Metadata["reasoning"] = reasoning
		if ms := prov.ReasoningMs; ms > 0 {
			out.Metadata["reasoningMs"] = ms
		}
	}
	// ★ 模型与思考档位也带上 —— 事后要能回答「这条是哪个模型、哪个档位
	//	产的」（对账与复现都要它）。没有就是「不知道」，不是「没配」。
	if prov.Model != "" {
		if out.Metadata == nil {
			out.Metadata = types.Metadata{}
		}
		out.Metadata["model"] = prov.Model
	}
	if prov.Thinking != "" {
		if out.Metadata == nil {
			out.Metadata = types.Metadata{}
		}
		out.Metadata["thinking"] = prov.Thinking
	}

	switch m.Role {
	case schema.User:
		out.Role = types.RoleUser
		out.Content = message.UserText(m)
	case schema.System:
		out.Role = types.RoleDeveloper
		out.Content = m.Content
	case schema.Tool:
		out.Role = types.RoleTool
		out.Content = message.Text(m)
		out.ToolCallID = m.ToolCallID
	case schema.Assistant, "":
		out.Role = types.RoleAssistant
		out.Content = message.AssistantText(m)
		out.ToolCalls = convertToolCalls(m)
	default:
		out.Role = types.RoleAssistant
		out.Content = message.Text(m)
	}

	// ★ 只有思维链、没有正文的消息**也要留着** —— 丢掉它就等于
	//   「这一轮它只想了、没说话」被抹掉了。
	if out.Content == "" && len(out.ToolCalls) == 0 && out.ToolCallID == "" &&
		out.Metadata["reasoning"] == nil {
		return types.Message{}, false
	}
	return out, true
}

func convertToolCalls(m *schema.Message) []types.ToolCall {
	calls := message.ToolCalls(m)
	if len(calls) == 0 {
		return nil
	}
	out := make([]types.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if tc.Name == "" {
			continue
		}
		id := tc.ID
		if id == "" {
			// The transcript is append-only and a tool call without an id can
			// never be matched to its result; give it a stable derived one so
			// the card still renders instead of vanishing.
			id = "call-" + firstToken(message.Text(m))
		}
		out = append(out, types.ToolCall{
			ID:   id,
			Type: types.ToolCallTypeFunction,
			Function: types.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.Args,
			},
		})
	}
	return out
}

// firstToken returns a short identifier fragment used only for synthesised
// tool-call ids.
func firstToken(v string) string {
	const maxRunes = 8
	r := []rune(v)
	if len(r) > maxRunes {
		r = r[:maxRunes]
	}
	out := make([]rune, 0, len(r))
	for _, c := range r {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

// stableID derives a message id from content **and position**, so repeated
// fetches of the same transcript produce the same ids and the client's reducer
// keys stay stable across a reload.
//
// ★ index 必须参与运算，否则 id 会撞车：
//
//	一轮里连续发两次工具调用，agent 会产出**两条** assistant 消息 ——
//	都是空内容、都没有 ToolCallID（工具调用挂在 ToolCalls 上）。仅按
//	role + text + ToolCallID 取摘要时，它们得到完全相同的 id。
//
//	实测一个 13 条消息的会话里出现了三条 id 相同的 assistant 消息
//	（工具分别是 ls / read_file×2 / ls），前端直接报
//	"Encountered two children with the same key"。
//
//	加进工具调用**也不够** —— 上面第一条和第三条都是 ["ls"]，照样撞。
//	位置是唯一可靠的区别，而日志是 append-only 的，位置在多次读取之间
//	不会变，正好满足「重载后 key 稳定」这个原始诉求。
func stableID(m *schema.Message, index int) string {
	var b []byte
	b = append(b, []byte(string(m.Role))...)
	b = append(b, 0)
	b = append(b, []byte(message.Text(m))...)
	b = append(b, 0)
	if m.ToolCallID != "" {
		b = append(b, []byte(m.ToolCallID)...)
	}
	b = append(b, 0)
	b = strconv.AppendInt(b, int64(index), 10)
	return "msg-" + hexOf(b)
}

// hexOf is a tiny FNV-1a hex digest; the ids only need to be stable and
// collision-resistant within one transcript, not globally unique.
func hexOf(b []byte) string {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	var h uint64 = offset64
	for _, c := range b {
		h ^= uint64(c)
		h *= prime64
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = digits[h&0xf]
		h >>= 4
	}
	return string(out)
}
