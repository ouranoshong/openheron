package transport

import (
	"encoding/json"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/approval"
	"openheron/contract"
)

// interruptDetail turns the framework's interrupt payload into the shape the
// UI and the persisted session need.
//
// ★ 中断载荷的类型断言降级而不是失败：认不出来的载荷也得让人批。
//
// interruptDetailOf 是**单个** root cause 的待批详情。
//
// ★ 逐个算，不是算一份通用的：一次可以抛出多个独立的待批（模型一次请求
//
//	四个都要审批的工具），而工具名与参数**只在中断事件里出现过一次** ——
//	流关闭之后它们就不存在了，所以每个待批都得各存一份。
//
// agentkit/run cannot import agent (agent imports agentkit/run). The assertion degrades to
// the message alone rather than failing — an unrecognised payload must still
// admit the approval.
func interruptDetailOf(ic *adk.InterruptCtx, sub string) *contract.InterruptDetail {
	detail := &contract.InterruptDetail{
		SubagentRunID: sub,
		// ★ RequestedByAgent 是**语义**字段：「这个待批是谁要的」。
		//   它此前**从没被写过** —— 归属只挂在 SubagentRunID 上，而那个是
		//   协议层的名字（「子 agent 的运行 id」），语义上偏了。
		//
		//   两个都写：前端读哪个都对，且历史回放（只有持久化的 detail）
		//   也能拿到请求方 —— 回放路径没有实时事件可参照。
		RequestedByAgent: sub,
		CreatedAt:        time.Now().UTC(),
		Message:          describe(ic.Info),
	}
	if info, ok := ic.Info.(*approval.ApprovalInfo); ok && info != nil {
		detail.ToolName = info.ToolName
		// Keep the args as raw JSON so the client renders them verbatim;
		// invalid JSON degrades to no arguments instead of failing.
		if json.Valid([]byte(info.ArgumentsInJSON)) && info.ArgumentsInJSON != "" {
			detail.Arguments = json.RawMessage(info.ArgumentsInJSON)
		}
	}
	return detail
}

// approvalMetadata 把审批所需的**结构化**内容放进 AG-UI 的 metadata。
//
// ★ 别让前端去解析 Message：它是给人看的兜底文案，工具名一改格式就错，
// 参数更是不在里面。metadata 是协议预留的扩展位，前端据此渲染审批卡。
func approvalMetadata(info any) types.Metadata {
	ai, ok := info.(*approval.ApprovalInfo)
	if !ok || ai == nil {
		return nil
	}
	md := types.Metadata{"toolName": ai.ToolName}
	if ai.ArgumentsInJSON != "" && json.Valid([]byte(ai.ArgumentsInJSON)) {
		// RawMessage 让参数以对象而不是 JSON 字符串出现在事件里。
		md["arguments"] = json.RawMessage(ai.ArgumentsInJSON)
	}
	return md
}

func describe(info any) string {
	if info == nil {
		return ""
	}
	if s, ok := info.(string); ok {
		return s
	}
	if info, ok := info.(*approval.ApprovalInfo); ok && info != nil {
		return "tool " + info.ToolName + " requires approval"
	}
	return ""
}

// aggregateUsage sums the token accounting the endpoint already reported.
//
// ★ Pure read: eino writes ResponseMeta.Usage onto every assistant message and
//
//	openheron persists it in session.jsonl; nothing here invents numbers, so a
//	null endpoint simply yields a zero Usage.
func aggregateUsage(msgs []*schema.Message) contract.Usage {
	var u contract.Usage
	for _, m := range msgs {
		if m == nil || m.ResponseMeta == nil || m.ResponseMeta.Usage == nil {
			continue
		}
		t := m.ResponseMeta.Usage
		u.PromptTokens += t.PromptTokens
		u.CompletionTokens += t.CompletionTokens
		u.TotalTokens += t.TotalTokens
		u.CachedTokens += t.PromptTokenDetails.CachedTokens
		u.ReasoningTokens += t.CompletionTokensDetails.ReasoningTokens
	}
	return u
}
