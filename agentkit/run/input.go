// 这一轮给模型的输入：一段临时的上下文提示 + 会话历史。
//
// ★ 它住在编排层里，不是一个独立的包 —— 「这一轮给模型看什么」是**编排规则**，
//
//	而编排规则只有一处说得清。独立成包（原来的 inputctx）反而制造了一条
//	「编排层 → 输入层」的边，多一跳、少一层归属。
//
// ★ 跨层共享的类型（[contract.TurnKind]）不在这里 —— 那是叶子包的事。
package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// InputConfig controls the context prompt. All fields are optional; an empty
// InputConfig still yields a usable rules block.
type InputConfig struct {
	ProjectRoot string
	ExamplesDir string
	Workspace   string
}

// inputParams is everything buildInput needs from the session.
//
// ★ 它只带**纯数据**：历史与工作区。带一个会话对象过来就会在
//
//	「读历史」与「改状态」之间分不清边界。
type inputParams struct {
	History   []*schema.Message
	Workspace string
}

var defaultRules = []string{
	"  1. Always use filesystem tools to look up real code before answering. Do not guess or make up information.",
	"  2. After using tools (even if they return no results), you MUST write a text response to the user summarizing what you found.",
	"  3. Never end your turn without a text response — tool calls alone are not sufficient.",
	"  4. When asked to build or test code, use the execute tool to run the command.",
	"     Each Go example has its own go.mod. To build an example, run:",
	"       cd <example-dir> && go build ./...",
	"     NEVER assume a build succeeded without actually running it.",
	"  5. When writing or editing a file and then claiming it compiles, you MUST run the build tool to verify.",
}

// buildInput returns the agent input for one turn: a transient context prompt
// followed by history. The context prompt is never persisted to the session.
//
// ★ 历史先过一遍 [normalizeHistory]：端点对「tool_calls 没有对应 tool 消息」
//
//	是硬校验（直接 400），而历史是持久化的 —— 一条坏 turn 会把会话永久毒化。
//	详见 [history.go]。
func (c InputConfig) buildInput(ctx context.Context, p inputParams, query string) (*adk.TypedAgentInput[*schema.Message], error) {
	messages := make([]*schema.Message, 0, len(p.History)+2)
	messages = append(messages, newSystem(c.contextPrompt(ctx, p.Workspace)))

	// ★ 先剔掉**子 agent 的中间过程**，再做配对修复。
	//
	//	子 agent 的消息留在历史里（那是可观测性的来源），但**不进**父 agent
	//	的上下文 —— 「Agent 即工具」的隔离靠的是这一步，不是靠「不记录」。
	//
	//	★ 顺序不能反：专家的 assistant 消息带 tool_calls，若先归一化，
	//	  它会为**马上要被剔掉**的那些调用补合成 tool 消息 —— 白做，
	//	  而且合成出来的消息可能被父的配对逻辑认错。
	history := withoutSubagentMessages(p.History)
	for _, msg := range normalizeHistory(history) {
		if msg != nil {
			messages = append(messages, msg)
		}
	}
	if query != "" {
		messages = append(messages, newUser(query))
	}

	return &adk.TypedAgentInput[*schema.Message]{
		Messages:        messages,
		EnableStreaming: true,
	}, nil
}

// withoutSubagentMessages 剔掉产生在子 agent 运行内部的消息。
//
// ★ 剔的是**整组**（专家的 assistant 消息与它的工具结果一起），所以剩下
//
//	的序列仍然合法：父的 assistant(tool_calls=[task]) 紧跟着它自己的
//	tool(task 结果)，配对不乱。
//
// ★ 跨轮也剔：专家的过程在任何一轮都不该进父的上下文。
func withoutSubagentMessages(hist []*schema.Message) []*schema.Message {
	kept := make([]*schema.Message, 0, len(hist))
	for _, m := range hist {
		if message.IsSubagent(m) {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

func newSystem(text string) *schema.Message {
	m := schema.SystemMessage(text)
	return m
}

func newUser(text string) *schema.Message {
	return schema.UserMessage(text)
}

func (c InputConfig) contextPrompt(ctx context.Context, workspace string) string {
	var lines []string
	lines = append(lines, "[Context]")
	lines = append(lines, "IMPORTANT RULES:")
	lines = append(lines, defaultRules...)

	if c.ProjectRoot != "" {
		lines = append(lines,
			fmt.Sprintf("Project root: %s", c.ProjectRoot),
			"  IMPORTANT: Always pass the project root as the path argument when using filesystem tools.",
			fmt.Sprintf("  - grep(pattern=\"...\", path=\"%s\")", c.ProjectRoot),
			fmt.Sprintf("  - glob(pattern=\"%s/**/*.go\")", c.ProjectRoot),
			fmt.Sprintf("  - read_file(file_path=\"%s/some/file.go\")", c.ProjectRoot),
			"  grep and glob recurse into ALL subdirectories under the given path.",
		)
		if dirs := subdirs(ctx, c.ProjectRoot); len(dirs) > 0 {
			lines = append(lines, "  Top-level subdirectories of the project root:")
			for _, d := range dirs {
				lines = append(lines, "    - "+d+"/")
			}
		}
		lines = append(lines, "  Use these tools to read actual source code before answering questions about the codebase.")
	}

	if c.ExamplesDir != "" && c.ExamplesDir != c.ProjectRoot {
		lines = append(lines,
			fmt.Sprintf("eino-examples directory: %s", c.ExamplesDir),
		)
	}

	ws := workspace
	if ws == "" {
		ws = c.Workspace
	}
	if ws != "" {
		lines = append(lines,
			fmt.Sprintf("storage.Session workspace: %s", ws),
			"  Files uploaded for this session live there. Use filesystem tools with this path.",
		)
	}

	return strings.Join(lines, "\n")
}

func subdirs(ctx context.Context, root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}
