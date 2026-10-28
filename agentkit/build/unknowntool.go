package build

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type toolNamesKey struct{}

func withToolNames(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, toolNamesKey{}, names)
}

func toolNamesFrom(ctx context.Context) []string {
	names, _ := ctx.Value(toolNamesKey{}).([]string)
	return names
}

func newUnknownToolScope() adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &unknownToolScope{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
	}
}

type unknownToolScope struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
}

var _ adk.TypedChatModelAgentMiddleware[*schema.Message] = (*unknownToolScope)(nil)

func (m *unknownToolScope) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (
	context.Context, *adk.ChatModelAgentContext, error) {

	if runCtx == nil {
		return ctx, runCtx, nil
	}
	return withToolNames(ctx, toolNamesOf(runCtx.Tools)), runCtx, nil
}

func toolNamesOf(tools []tool.BaseTool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if info, err := t.Info(context.Background()); err == nil {
			names = append(names, info.Name)
		}
	}
	sort.Strings(names)
	return names
}

func UnknownToolsReply(ctx context.Context, name, _ string) (string, error) {
	avail := toolNamesFrom(ctx)
	if len(avail) == 0 {
		return fmt.Sprintf(
			"[tool unavailable] %q is not available in this turn, and this turn exposes no tools. "+
				"Answer from what you already have; do not call it again.", name), nil
	}
	return fmt.Sprintf(
		"[tool unavailable] %q is not available in this turn. Available tools: %s. "+
			"Use one of those, or answer without it; do not call %q again.",
		name, strings.Join(avail, ", "), name), nil
}

func withUnknownToolsReply(tc adk.ToolsConfig) adk.ToolsConfig {
	if tc.ToolsNodeConfig.UnknownToolsHandler == nil {
		tc.ToolsNodeConfig.UnknownToolsHandler = UnknownToolsReply
	}
	return tc
}
