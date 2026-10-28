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

// ── 未知工具兜底 ────────────────────────────────────────────────────────
//
// ★ 这个文件存在的原因是一处**自家 prompt 与自家工具表脱节**：
//
//	编排层的 context prompt 无条件教模型用 grep / glob / read_file
//	（agentkit/run/input.go），而那份清单不知道 disabled_tools、不知道全局
//	deny、也不知道当前跑的是入口 agent 还是子 agent。于是配置一旦让 grep
//	不在工具表里，模型就是被 openheron 自己的 prompt 教去调一个不存在的工具，
//	而 ToolsNode 对认不出的名字是**直接返回 error** 的 —— 整轮失败，
//	一句输出都没有。
//
//	那行 prompt 该改，但它只能治「已知的那几个名字」：模型仍会幻觉出
//	grep_search、Grep、search 之类。兜底要在框架这一层，因为**只有这里
//	知道本轮真实的工具表**。
//
// ★ 兜底的方式是回一条结果、而不是回 error。理由：模型的下一个动作是
//
//	「看到工具结果」——回文本它能自纠（换个工具或直接回答），
//	回 error 整轮就结束了，而「换个工具」本来是它能做的正��。
//
// ★ 不告诉模型「这是被策略禁掉的」。措辞一律是「本轮没有这个工具」：
//
//	说「你被禁止了 grep」只会诱导它去找绕过办法（改用 execute、写脚本），
//	而 deny 的意义是**别绕**，不是「换个姿势再来」。

// toolNamesKey 携带本轮真实可用的工具名。
type toolNamesKey struct{}

// withToolNames 把本轮可用的工具名写进 ctx。
func withToolNames(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, toolNamesKey{}, names)
}

// toolNamesFrom 读回本轮可用的工具名；没标注过返回 nil。
func toolNamesFrom(ctx context.Context) []string {
	names, _ := ctx.Value(toolNamesKey{}).([]string)
	return names
}

// UnknownToolScope 在 BeforeAgent 里把「本轮真实可用的工具名」标注进 ctx。
//
// ★ 它与 [filterMiddleware] 分开，而不是并进去：过滤只关心「摘掉什么」，
//
//	而这里关心的是「剩下什么」——后者要在**任何**过滤都发生之后取。
//	合成一个中间件的话，就得在 Filter 的早退分支里也补一遍标注，
//	而那个早退分支正是最容易漏的地方。
func newUnknownToolScope() adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &unknownToolScope{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
	}
}

type unknownToolScope struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
}

var _ adk.TypedChatModelAgentMiddleware[*schema.Message] = (*unknownToolScope)(nil)

// BeforeAgent 标注可用工具名。
func (m *unknownToolScope) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (
	context.Context, *adk.ChatModelAgentContext, error) {

	if runCtx == nil {
		return ctx, runCtx, nil
	}
	// ★ 排在过滤**之后**读 runCtx.Tools：`applyBeforeAgent` 按注册顺序依次
	//   调 handler，后一个看到的是前一个改过的 runCtx。所以这个中间件必须
	//   注册在 filterMiddleware 之后，否则标注到的是**未过滤**的那份 ——
	//   而那正是本文件要消灭的那个 bug。
	return withToolNames(ctx, toolNamesOf(runCtx.Tools)), runCtx, nil
}

// toolNamesOf 取工具名；报不出名字的跳过。
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

// UnknownToolsReply 是 [compose.ToolsNodeConfig.UnknownToolsHandler]。
//
// ★ 返回值会**原样作为工具结果给模型看**，所以它属于运行时输出：
//
//	按 AGENTS.md 的语言约定用英文，不写成中文。
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

// withUnknownToolsReply 挂上未知工具兜底。
//
// ★ 不覆盖已存在的 handler：那是装配层的选择权，本函数只负责「没配时有兜底」。
//
// 「没配」是真会发生的 —— compose.ToolsNodeConfig 零值时 UnknownToolsHandler
// 是 nil，而 ToolsNode 对认不出的工具名**直接返回 error**，整轮失败且无输出。
//
// ★ 兜底本身也是 tool 侧的事：它是「模型调了一个本轮不存在的工具」时的应答。
func withUnknownToolsReply(tc adk.ToolsConfig) adk.ToolsConfig {
	if tc.ToolsNodeConfig.UnknownToolsHandler == nil {
		tc.ToolsNodeConfig.UnknownToolsHandler = UnknownToolsReply
	}
	return tc
}
