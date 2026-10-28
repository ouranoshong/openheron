package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

var errCommandNotRunnable = errors.New("command is not runnable from a message")

type commandKind string

const (
	commandKindAction commandKind = "action"
	commandKindPrompt commandKind = "prompt"
)

type commandAction string

const (
	commandActionNewThread    commandAction = "new_thread"
	commandActionStopRun      commandAction = "stop_run"
	commandActionAttachFiles  commandAction = "attach_files"
	commandActionOpenSettings commandAction = "open_settings"
	commandActionOpenAgents   commandAction = "open_agents"
)

// commandSpec is the server-owned command table. The template stays here:
// clients only need names and descriptions, and shipping prompts to the
// browser would make them editable surface area for no benefit.
type commandSpec struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Kind         commandKind   `json:"kind"`
	Action       commandAction `json:"action,omitempty"`
	ArgumentHint string        `json:"argumentHint,omitempty"`
	Template     string        `json:"-"`
}

var commandRegistry = []commandSpec{
	{Name: "new", Description: "Start a new thread", Kind: commandKindAction, Action: commandActionNewThread},
	{Name: "stop", Description: "Stop the current run", Kind: commandKindAction, Action: commandActionStopRun},
	{Name: "attach", Description: "Attach files to this thread", Kind: commandKindAction, Action: commandActionAttachFiles},
	{Name: "settings", Description: "Open configuration", Kind: commandKindAction, Action: commandActionOpenSettings},
	{
		Name: "agents", Description: "Switch the active agent",
		Kind: commandKindAction, Action: commandActionOpenAgents, ArgumentHint: "[agent]",
	},
	{
		// ★ /skill <name> [task] —— 第一个词是 **skill 名**，其余是任务。
		//
		//	为什么要单独的 {{name}}：{{args}} 是「命令名之后的全部」，
		//	而「skill 名」与「任务」是**两件事**，粘在一起就没法把名字
		//	单独放进模板（会出现「skill `refactor 拆一下`」）。
		//
		//	★ 执行走的是模型自己的 `skill` 工具 —— 那是 skill 唯一的执行
		//	  路径，inline / fork / fork_with_context 三种模式都在里面。
		//	  后端另开一条「直接执行 skill」的路，等于把中间件里的实现
		//	  复制一份。
		Name: "skill", Description: "Use a named skill for this task",
		Kind: commandKindPrompt, ArgumentHint: "<skill> [task]",
		Template: "Use the `{{name}}` skill for this task. Read its instructions with the skill tool first, then follow them.\n{{args}}",
	},
	{
		Name: "review", Description: "Review the current changes or given paths",
		Kind: commandKindPrompt, ArgumentHint: "[path]",
		Template: "Review the current working tree changes. Focus on correctness, error handling, and regressions. Read the files you need before judging.\n{{args}}",
	},
	{
		Name: "test", Description: "Run the relevant tests and summarize failures",
		Kind: commandKindPrompt, ArgumentHint: "[path]",
		Template: "Run the tests relevant to the current change. Do not fix anything unless asked.\n{{args}}",
	},
	{
		Name: "explain", Description: "Explain how the given code works",
		Kind: commandKindPrompt, ArgumentHint: "<path or symbol>",
		Template: "Explain how the following code works, including the invariants it relies on and the parts that are easy to break.\n{{args}}",
	},
	{
		Name: "fix", Description: "Find the root cause and fix the given problem",
		Kind: commandKindPrompt, ArgumentHint: "<problem>",
		Template: "Find the root cause of the following problem, fix it with the smallest change, and verify with the relevant test.\n{{args}}",
	},
}

var commandByName = func() map[string]commandSpec {
	out := make(map[string]commandSpec, len(commandRegistry))
	for _, spec := range commandRegistry {
		out[spec.Name] = spec
	}
	return out
}()

var commandPattern = regexp.MustCompile(`^\/([A-Za-z0-9_-]+)(?:\s+([\s\S]*))?$`)

// commandView is the wire shape; Template is intentionally absent.
type commandView struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Kind         commandKind   `json:"kind"`
	Action       commandAction `json:"action,omitempty"`
	ArgumentHint string        `json:"argumentHint,omitempty"`
}

type commandList struct {
	Commands []commandView `json:"commands"`
}

func (s *Server) v2Commands(_ context.Context, c *app.RequestContext) {
	out := commandList{Commands: make([]commandView, 0, len(commandRegistry))}
	for _, spec := range commandRegistry {
		out.Commands = append(out.Commands, commandView{
			Name:         spec.Name,
			Description:  spec.Description,
			Kind:         spec.Kind,
			Action:       spec.Action,
			ArgumentHint: spec.ArgumentHint,
		})
	}
	c.JSON(consts.StatusOK, out)
}

// expandCommand turns a leading /command into the text the agent receives.
//
// It deliberately leaves anything it does not recognize untouched. A message
// that starts with an absolute path ("/home/me/file.go") must be sent as
// written, not rejected as an unknown command.
func expandCommand(query string) (string, error) {
	m := commandPattern.FindStringSubmatch(query)
	if m == nil {
		return query, nil
	}

	name := strings.ToLower(m[1])
	spec, ok := commandByName[name]
	if !ok {
		return query, nil
	}

	switch spec.Kind {
	case commandKindAction:
		return "", fmt.Errorf("%w: /%s", errCommandNotRunnable, spec.Name)
	case commandKindPrompt:
		args := strings.TrimSpace(m[2])
		// ★ 只有**带 {{name}} 的命令**（/skill）才拆「名字 + 其余」——
		//   「skill 名」与「任务」是两件事，粘在一起就没法分开。
		//
		//   别的命令（/review …）的 {{args}} 是「命令名之后的全部」，
		//   拆了就把参数弄丢了 —— `/review src/a.go` 的路径不是名字。
		name, rest := "", args
		if strings.Contains(spec.Template, "{{name}}") {
			name, rest = splitCommandName(args)
		}
		out := expandCommandTemplate(spec.Template, rest, name)
		if out == "" {
			return "", errEmptyMessage
		}
		return out, nil
	default:
		return query, nil
	}
}

// splitCommandName 把「命令名之后的全部」拆成「名字 + 其余」。
//
// ★ 只有**第一个词**是名字：skill 名不带空格 ✅ —— 拆更多就说不清
//
//	「`refactor 拆一下`」里哪个是名字了。
func splitCommandName(args string) (name, rest string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}
	fields := strings.Fields(args)
	name = fields[0]
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), name))
	return name, rest
}

// expandCommandTemplate drops the placeholder line when the caller supplied no
// arguments, so "/review" does not leave a blank line at the end of the prompt.
//
// ★ name 非空时替换 {{name}} —— 它是「命令的第一个词」（见 [splitCommandName]）。
func expandCommandTemplate(template, args, name string) string {
	if args != "" {
		return strings.TrimSpace(strings.ReplaceAll(
			strings.ReplaceAll(template, "{{name}}", name), "{{args}}", args))
	}

	lines := strings.Split(template, "\n")
	out := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == "{{args}}" {
			continue
		}
		out = append(out, strings.ReplaceAll(
			strings.ReplaceAll(line, "{{name}}", name), "{{args}}", ""))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
