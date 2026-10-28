package transport

import (
	"encoding/json"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/approval"
	"github.com/ouranoshong/openheron/contract"
)

// interruptDetail turns the framework's interrupt payload into the shape the
// UI and the persisted session need.
// agentkit/run cannot import agent (agent imports agentkit/run). The assertion degrades to
// the message alone rather than failing — an unrecognised payload must still
// admit the approval.
func interruptDetailOf(ic *adk.InterruptCtx, sub string) *contract.InterruptDetail {
	detail := &contract.InterruptDetail{
		SubagentRunID:    sub,
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

func approvalMetadata(info any) types.Metadata {
	ai, ok := info.(*approval.ApprovalInfo)
	if !ok || ai == nil {
		return nil
	}
	md := types.Metadata{"toolName": ai.ToolName}
	if ai.ArgumentsInJSON != "" && json.Valid([]byte(ai.ArgumentsInJSON)) {
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
