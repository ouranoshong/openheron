package transport

import (
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// 本文件回答「这条输出/这个待批是谁的」。
//
// ★ 为什么单独成文件：并行委派下「谁」有两个**不同**的问题，而它们的答案
//
//	**不在同一个地方**：
//
//	① 谁在产生输出（实时事件）—— 事件自带 `AgentName`。
//	② 谁发起了被拦下的调用（待批）—— 事件是**根** agent 发的，
//	   `AgentName` 是根的名字；真正干活的是子 agent。
//
//	② 的答案在**地址链**里：`InterruptCtx.Address` 是一串分层地址，
//	agent 段填的是 agent 名（eino adk/flow.go:357）。
//	典型值：`agent:main;tool:task;agent:coder;tool:write_file`。
//
//	★ 这也是为什么不能靠 `ev.AgentName`：串行时它常常恰好等于干活的那个
//	  agent，所以看不出问题；并行时必然不同 —— 而「必然不同」正是并行
//	  引入的第一个 bug。
//
// ★ 归属标记与时间戳的**写入口**在 [message]（消息出处）—— 两个键
//
//	必须待在一起，理由见那里的注释。

// requestedBy 从中断的地址链推导发起这次工具调用的 agent。
//
// ★ 取**最后一个** agent 段：地址是从根往下长的，越靠后越深，而最深
//
//	的那个 agent 才是真正握着工具的那个。
//
// ★ 入口 agent 自己发起的调用返回空串 —— 与「入口 agent 的事件不标
//
//	subagentRunId」是同一条规则（见 transport.go 的 sub 计算）：
//	归属标记的语义是「这不是根」，不是「这是谁」。
//
// ★ 不用 `Parent` 链：`Parent` 描述的是**中断信号**的父子（谁把中断
//
//	冒泡上来的），而 `Address` 描述的是**执行位置**（谁在干活）。
//	并行时这两者会分叉：一个入口 agent 可以同时是另一个 agent 的父。
func requestedBy(ic *adk.InterruptCtx, mainAgent string) string {
	if ic == nil {
		return ""
	}
	agent := ""
	for _, seg := range ic.Address {
		if seg.Type == adk.AddressSegmentAgent && seg.ID != "" {
			agent = seg.ID
		}
	}
	if agent == "" || agent == mainAgent {
		return ""
	}
	return agent
}

// ownerOf 返回这条事件里的输出属于哪个 agent。
//
// ★ 与协议层的 `SubagentRunID` 不是一回事：那个字段是「**这不是根**」的
//
//	标记（根不标），而归属标记要回答「这是谁」—— 根的消息也要有归属，
//	否则「谁花的 token」永远缺一个桶。
//
// ★ 事件没带 AgentName 时回落到入口 agent：那意味着框架没告诉我们是谁，
//
//	而把它归给某个子 agent是**编造**。
func ownerOf(ev *adk.TypedAgentEvent[*schema.Message], mainAgent string) string {
	if ev == nil {
		return mainAgent
	}
	if ev.AgentName != "" {
		return ev.AgentName
	}
	return mainAgent
}

// stampMessage 给一条产出的消息打上出处：归属 + 产生时刻。
//
// ★ 两个标记**同时**打，因为它们回答的是同一个问题的两半：这条消息是谁、
// 什么时候说的。分开打就会出现「有归属没时刻」或反之的半截状态。
//
// ★ 时刻取**此刻**（消息被产出的那一刻），不是归档那一刻 —— 归档是
//
//	一轮结束时一次性做的，那时打戳会让一轮内所有消息的时间几乎相同，
//	并行交错的顺序信息就整个丢了。
func stampMessage(msg *schema.Message, owner string) {
	if msg == nil {
		return
	}
	message.StampAgent(msg, owner)
	message.StampNow(msg)
}

// stampSubagentMessage 给一条**产生在子 agent 运行内部**的消息打标记。
//
// ★ 与 [stampMessage] 分开：归属（哪个 agent）与作用域（产生在委派内部）
//
//	是两个正交的事实。只有作用域决定「进不进父 agent 的下一轮上下文」。
func stampSubagentMessage(msg *schema.Message, owner string) {
	stampMessage(msg, owner)
	message.StampSubagent(msg)
}
