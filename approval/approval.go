// Package approval carries the human decision a gated tool reads back when it
// resumes.
//
// ★ 它是一个**叶子包**：agentkit/run（送决定）与 agent（读决定）都要用它，
//
//	而两者之间不能互相 import（依赖铁律：agentkit/run 不 import agent，
//	agent 也不 import agentkit/run）。
//
// ★ 为什么载荷类型必须是**同一个**：eino 的
//
//	`tool.GetResumeContext[T]` 是**具体类型**断言
//	（eino internal/core/resume.go:102：`data, hasData = rCtx.resumeData.(T)`），
//	断言失败时 hasData 是 false，而闸门此前把「拿不到决定」当成「照常执行」——
//	于是「拒绝」被忽略，ask 档的工具在没有人工点头的情况下照样跑起来。
//
//	各自定义一份同名结构（`agentkit/run.ApprovalResult` 与 `agent.ApprovalResult`）
//	编译期完全看不出来，只有运行期才炸 —— 而炸的方式是**放行**。
//	所以决定载荷归一个叶子包，类型identity 由编译器守住。
package approval

import "encoding/gob"

// Decision 是「人点了什么」。
//
// ★ Approved=false **必须**被当成一个有效决定（拒绝），而不是「没有决定」。
//
//	两者混为一谈的话，「拒绝」与「答复丢了」会走同一条路 —— 而那条路
//	曾经是放行。
type Decision struct {
	// Approved 报告人是否同意这次调用。
	Approved bool `json:"approved"`
	// Reason 是人给的理由，模型会看到它。拒绝时可为空。
	Reason string `json:"reason,omitempty"`
}

// ApprovalInfo 是审批中断的**用户可见的那一半**：工具名与参数。
//
// ★ 它也归这个叶子包：闸门（agent）产出它，渲染层（transport）读它，
//
//	而渲染层不该为了读一个中断载荷就 import 整个装配层 ——
//	那会让「渲染」依赖「装配」，方向是反的。
//
// ★ 它同样要 gob 注册（见下面的 init）—— 它跟着中断进 checkpoint。
type ApprovalInfo struct {
	ToolName        string
	ArgumentsInJSON string
}

// ★ gob 注册是审批链路能走通的前提，不是可选项。
//
//	eino 的 TurnLoop 把未决的 interrupt 连同它的 Info 一起写进 checkpoint，
//	而 agentkit/run.FileStore 用 gob 序列化。gob 对接口字段的具体类型必须在
//	包初始化时 Register，否则编码直接失败 —— 而且失败发生在**停机提交
//	checkpoint 的那一刻**，表现为
//	「模型说要调工具 → 直接 RUN_ERROR」，看起来像模型出错，实际是编码器。
//
//	这类注册必须留在类型定义旁边：类型挪了文件而 init 留下，就成了
//	一个没人知道它为什么存在、删掉又立刻复发的东西。
func init() {
	gob.Register(&Decision{})
	gob.Register(&ApprovalInfo{})
}
