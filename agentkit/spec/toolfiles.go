package spec

import (
	"sort"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// toolFile binds a `tools/` file to the tools it configures.
//
// ★ One table, two questions asked backwards: "which file configures this
//
//	tool" and "which tools does this file configure". ★ Two tables would be
//	two answers to it, and the second one to drift is the one nobody reads.
//
// ★ The names live in decl, because the builder refuses a provider that
//
//	claims a built-in name and the loader checks the names an agent's
//	`tools:` list mentions: the table has to agree with both.
type toolFile struct {
	file  string
	tools []string
}

// toolFiles is the `tools/` partition and what each of its files carries.
//
// ★ The order is the order the files are read in; the answer is sorted
//
//	wherever an order matters.
var toolFiles = []toolFile{
	{"http.yaml", []string{
		decl.ToolNameHTTPGet, decl.ToolNameHTTPPost,
		decl.ToolNameHTTPPut, decl.ToolNameHTTPDelete,
	}},
	{"web_search.yaml", []string{decl.ToolNameWebSearch}},
	{"wikipedia.yaml", []string{decl.ToolNameWikipedia}},
	{"kb.yaml", []string{decl.ToolNameKBSearch, decl.ToolNameKBIndex}},
	{"filesystem.yaml", []string{
		decl.ToolNameLs, decl.ToolNameReadFile, decl.ToolNameWriteFile,
		decl.ToolNameEditFile, decl.ToolNameGlob, decl.ToolNameGrep,
		decl.ToolNameExecute,
	}},
	// ★ The configuration tools have a file of their own, and it is what
	//	says that the read-only ones run without a nod — so an agent that
	//	uses one of them depends on that file.
	{"config.yaml", append([]string(nil), decl.ConfigToolNames...)},
}

// ToolConfigFile returns the `tools/` file that configures the named tool,
// and "" when no file does.
//
// ★ A tool with no file of its own — the delegation tools and the skill
//
//	reader — is provided by the code, so nothing on disk can change it.
//
// ★ The answer is what "this agent depends on that file" is built on: the
//
//	agent that uses the HTTP tool depends on `tools/http.yaml`, because the
//	timeout and the headers live there, and a change to them has to reach
//	the agent.
func ToolConfigFile(name string) string {
	for _, tf := range toolFiles {
		for _, t := range tf.tools {
			if t == name {
				return DirTools + "/" + tf.file
			}
		}
	}
	return ""
}

// ToolConfigFiles returns the `tools/` partition's files, sorted.
//
// ★ It is the list the dependency walk hashes, so it has to be the same
//
//	list the loader reads: a file the loader parses and the dependency walk
//	does not know about would be a change that never reaches an agent.
func ToolConfigFiles() []string {
	out := make([]string, 0, len(toolFiles))
	for _, tf := range toolFiles {
		out = append(out, DirTools+"/"+tf.file)
	}
	sort.Strings(out)
	return out
}

// ToolAllows returns the exceptions each `tools/` file names.
//
// ★ It is the loader's own answer: the switch in loadTool knows which
//
//	section a file is, and this reads the same sections back. ★ One
//	answer, two readers — the tier table asks "what does this file
//	exempt", and the dependency walk asks the same question about the same
//	agent.
func (c *Config) ToolAllows() map[string][]string {
	out := map[string][]string{}
	if c == nil {
		return out
	}
	if t := c.Tools.HTTP; t != nil {
		out[DirTools+"/http.yaml"] = t.Allows
	}
	if t := c.Tools.WebSearch; t != nil {
		out[DirTools+"/web_search.yaml"] = t.Allows
	}
	if t := c.Tools.Wikipedia; t != nil {
		out[DirTools+"/wikipedia.yaml"] = t.Allows
	}
	if t := c.Tools.Filesystem; t != nil {
		out[DirTools+"/filesystem.yaml"] = t.Allows
	}
	if t := c.Tools.Config; t != nil {
		out[DirTools+"/config.yaml"] = t.Allows
	}
	out[DirTools+"/kb.yaml"] = c.KB.Allows
	return out
}
