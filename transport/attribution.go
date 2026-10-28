package transport

import (
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

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

func ownerOf(ev *adk.TypedAgentEvent[*schema.Message], mainAgent string) string {
	if ev == nil {
		return mainAgent
	}
	if ev.AgentName != "" {
		return ev.AgentName
	}
	return mainAgent
}

func stampMessage(msg *schema.Message, owner string) {
	if msg == nil {
		return
	}
	message.StampAgent(msg, owner)
	message.StampNow(msg)
}

func stampSubagentMessage(msg *schema.Message, owner string) {
	stampMessage(msg, owner)
	message.StampSubagent(msg)
}
