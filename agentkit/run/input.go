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

// attachmentRules explain the @ header. They are always present, not just on
// the turn that carries a file: a history message can carry the header too,
// and a rule that only appears with the current turn would leave "that file
// you just sent" unexplained.
//
// ★ An attachment's bytes are **not** in the message any more. The rule has
//
//	to say so explicitly, and it has to be an obligation: a model that treats
//	@ as ordinary prose answers about a file it never opened.
var attachmentRules = []string{
	"  6. A line at the start of a user message that begins with @ is a file path injected by the system, not words the user typed.",
	"     Pass it to read_file exactly as written; when the path is wrapped in double quotes, drop the quotes.",
	"     An absolute path is used as-is; a relative one is resolved from the project root.",
	"  7. Attachment contents are NOT included in the message. Before answering about an attached file, you MUST call read_file on its @ path.",
	"     For a large file, read it in chunks with read_file's offset and limit (the tool returns at most 2000 lines per call).",
	"  8. A file named in the prompt as \"cannot be processed yet\" is not available to you. Say so; do not try to read it as text.",
}

// buildInput returns the agent input for one turn: a transient context prompt
// followed by history. The context prompt is never persisted to the session.
//
// ★ query is the turn's **user message object**, not its text.
//
//		The caller builds the message, hands it here, and persists it only after
//		this succeeds. Two things fall out of that order:
//
//	  - The history does not carry the question yet, so the model sees it
//	    exactly once. Appending the text again — as this function used to —
//	    handed the model the same question twice.
//
//	  - A message without attachments is the **same object** the transcript
//	    holds, so its stamps (a provenance stamp, a message id) reach the
//	    model too. A copy rebuilt from the text would carry none of it.
//
// ★ A message with attachments is handed to the model as a **copy** (see
//
//	[presentMessage]). The copy carries the @ header and, for a picture, the
//	base64 that must stay out of the transcript. The header is a view: writing
//	it into the persisted message would put machine paths in the user's own
//	bubble, and an absolute path is not a fact the transcript should keep.
//	The copy shares the Extra map, so every stamp still rides along.
//
// A nil query means "no question this turn": the input is the context and
// the history alone.
func (c InputConfig) buildInput(ctx context.Context, p inputParams, query *schema.Message) (*adk.TypedAgentInput[*schema.Message], error) {
	messages := make([]*schema.Message, 0, len(p.History)+2)

	// ★ The attachments are assembled **before** the prompt is written: the
	//   prompt names the files that cannot be processed, and a file the model
	//   is not told about is a file it may claim to have read.
	note := ""
	modelQuery := query
	if query != nil {
		note, modelQuery = assembleAttachments(query, attachmentContext{workspace: p.Workspace})
	}

	messages = append(messages, newSystem(c.contextPrompt(ctx, p.Workspace, note)))

	history := withoutSubagentMessages(p.History)
	for _, msg := range normalizeHistory(history) {
		if msg != nil {
			messages = append(messages, withHistoryAttachmentPaths(msg, p.Workspace))
		}
	}
	if modelQuery != nil {
		messages = append(messages, modelQuery)
	}

	return &adk.TypedAgentInput[*schema.Message]{
		Messages:        messages,
		EnableStreaming: true,
	}, nil
}

// withHistoryAttachmentPaths rebuilds the @ header of an earlier user message.
//
// ★ The transcript stores only the **relative** path in Extra; it does not
//
//	store the header, and Extra is not part of what the model sees. Without
//	this pass, "what did that file say?" one turn later would show the model
//	a question about a file with no way to name it.
//
// ★ Pictures are not re-inlined here. The old behavior inlined a picture
//
//	only for the turn that carried it, and doing it for every earlier turn
//	would put megabytes of base64 back into every later request.
func withHistoryAttachmentPaths(msg *schema.Message, workspace string) *schema.Message {
	if msg.Role != schema.User || !message.HasAttachments(msg) {
		return msg
	}
	header, _ := attachmentPaths(msg, attachmentContext{workspace: workspace})
	return presentMessage(msg, header, nil)
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

// contextPrompt builds the transient context the model sees.
//
// ★ attachments is the note about the files that came with this turn. It is
//
//	part of the prompt because a file the model is not told about is a file
//	the model cannot use: the workspace note says "files live there", which
//	answers "where" but not "which ones".
func (c InputConfig) contextPrompt(ctx context.Context, workspace, attachments string) string {
	var lines []string
	lines = append(lines, "[Context]")
	lines = append(lines, "IMPORTANT RULES:")
	lines = append(lines, defaultRules...)
	lines = append(lines, attachmentRules...)

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

	// ★ The attachments come last: they are what this turn is about, and
	//   they are the one thing the workspace note cannot say on its own.
	if attachments != "" {
		lines = append(lines, strings.TrimSpace(attachments))
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
