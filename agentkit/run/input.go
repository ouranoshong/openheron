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

	"github.com/ouranoshong/openheron/message"
)

// InputConfig controls the context prompt. All fields are optional; an empty
// InputConfig still yields a usable rules block.
type InputConfig struct {
	ProjectRoot string
	ExamplesDir string
	Workspace   string
}

// inputParams is everything buildInput needs from the session.
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
func (c InputConfig) buildInput(ctx context.Context, p inputParams, query string) (*adk.TypedAgentInput[*schema.Message], error) {
	messages := make([]*schema.Message, 0, len(p.History)+2)
	messages = append(messages, newSystem(c.contextPrompt(ctx, p.Workspace)))

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
