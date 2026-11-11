package build

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// configToolEnv writes a small but real configuration root and returns the
// service, the loaded config, and the six config tools.
func configToolEnv(t *testing.T) (*spec.Service, *spec.Config, map[string]tool.InvokableTool) {
	t.Helper()

	root := t.TempDir()
	cfgRoot := filepath.Join(root, spec.DefaultBaseDirName)
	for _, sub := range []string{spec.DirLLM, spec.DirAgent, spec.DirTools, spec.DirServer, spec.DirMCP} {
		if err := os.MkdirAll(filepath.Join(cfgRoot, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_CONFIG_TOOLS_KEY", "sk-test")

	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(cfgRoot, rel)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("llm/openai.yaml", "type: openai\nbase_url: https://a.example\napi_key: ${TEST_CONFIG_TOOLS_KEY}\n"+
		"models:\n  - id: fast\n    reasoning:\n      off: { reasoning_effort: none }\n      high: { reasoning_effort: high }\n")
	write("agents/main.md", "---\nmodel: openai/fast\ntools: [ls, config_get, config_write]\n---\nbody\n")
	write("tools/http.yaml", "methods: [get]\nallows: [config_write]\n")
	write("server/config.yaml", "listen_addr: \":18099\"\n")

	svc, err := spec.NewService(cfgRoot, root)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}

	batch, err := defaultToolRegistry(nil).Build(context.Background(), toolEnv{
		cfg: cfg, svc: svc, projectRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]tool.InvokableTool{}
	for _, b := range batch {
		it, ok := b.(tool.InvokableTool)
		if !ok {
			t.Fatalf("%T is not invokable", b)
		}
		info, err := it.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		tools[info.Name] = it
	}
	return svc, cfg, tools
}

func callTool(t *testing.T, tools map[string]tool.InvokableTool, name, args string) (string, error) {
	t.Helper()
	it, ok := tools[name]
	if !ok {
		t.Fatalf("no tool %q; got %v", name, configToolNames(tools))
	}
	return it.InvokableRun(context.Background(), args)
}

func callToolJSON[T any](t *testing.T, tools map[string]tool.InvokableTool, name, args string) T {
	t.Helper()
	raw, err := callTool(t, tools, name, args)
	if err != nil {
		t.Fatalf("%s(%s): %v", name, args, err)
	}
	var out T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%s(%s): decode %q: %v", name, args, raw, err)
	}
	return out
}

func configToolNames(m map[string]tool.InvokableTool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestTheConfigToolsAreAllThereAndSayWhatTheyDo(t *testing.T) {
	_, _, tools := configToolEnv(t)

	if len(tools) < len(decl.ConfigToolNames) {
		t.Fatalf("built %d tools, want at least the %d config ones: %v",
			len(tools), len(decl.ConfigToolNames), configToolNames(tools))
	}
	for _, name := range decl.ConfigToolNames {
		it, ok := tools[name]
		if !ok {
			t.Fatalf("no tool %q", name)
		}
		info, err := it.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(info.Desc) == "" {
			t.Fatalf("%s has no description; the model chooses by reading it", name)
		}
	}
}

// ★ No service, no tools. A registry assembled by a test without one must
// not offer a tool that panics on first use.
func TestTheConfigToolsNeedAService(t *testing.T) {
	batch, err := defaultToolRegistry(nil).Build(context.Background(),
		toolEnv{cfg: minimalTestConfig()})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range batch {
		info, err := b.(tool.InvokableTool).Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range decl.ConfigToolNames {
			if info.Name == want {
				t.Fatalf("%s was offered without a config service", want)
			}
		}
	}
}

func TestConfigGetReadsAFileAndTheOverview(t *testing.T) {
	_, _, tools := configToolEnv(t)

	file := callToolJSON[struct {
		Path     string `json:"path"`
		Exists   bool   `json:"exists"`
		Hot      bool   `json:"hot"`
		Revision string `json:"revision"`
		Content  string `json:"content"`
	}](t, tools, decl.ToolNameConfigGet, `{"path":"agents/main.md"}`)
	if !file.Exists || file.Revision == "" {
		t.Fatalf("a file on disk must exist and carry a revision: %+v", file)
	}
	if !strings.Contains(file.Content, "model: openai/fast") {
		t.Fatalf("content = %q", file.Content)
	}
	if !file.Hot {
		t.Fatal("agents/ is a hot partition")
	}

	overview := callToolJSON[struct {
		ConfigVersion string `json:"configVersion"`
		Items         []struct {
			Path string `json:"path"`
		} `json:"items"`
		Effective struct {
			DefaultAgent string   `json:"defaultAgent"`
			Agents       []string `json:"agents"`
			Models       []string `json:"models"`
			Watch        bool     `json:"watch"`
		} `json:"effective"`
	}](t, tools, decl.ToolNameConfigGet, `{}`)

	if overview.ConfigVersion == "" {
		t.Fatal("the overview must carry the fingerprint")
	}
	var sawAgent bool
	for _, it := range overview.Items {
		if it.Path == "agents/main.md" {
			sawAgent = true
		}
	}
	if !sawAgent {
		t.Fatalf("the overview must list the files: %+v", overview.Items)
	}
	if overview.Effective.DefaultAgent != spec.DefaultAgentID {
		t.Fatalf("defaultAgent = %q", overview.Effective.DefaultAgent)
	}
	if len(overview.Effective.Models) == 0 {
		t.Fatal("the overview must name the models the process holds")
	}
}

func TestConfigGetRefusesAPathOutsideTheTree(t *testing.T) {
	_, _, tools := configToolEnv(t)
	if _, err := callTool(t, tools, decl.ToolNameConfigGet, `{"path":"../secrets.yaml"}`); err == nil {
		t.Fatal("a path outside the config root must be refused")
	}
	// ★ A partition the agent may not write is still readable: the file that
	//   says what the process is running with is the one worth reading.
	read := callToolJSON[struct {
		Exists  bool   `json:"exists"`
		Hot     bool   `json:"hot"`
		Content string `json:"content"`
	}](t, tools, decl.ToolNameConfigGet, `{"path":"server/config.yaml"}`)
	if !read.Exists || !strings.Contains(read.Content, "listen_addr") {
		t.Fatalf("server/config.yaml must be readable: %+v", read)
	}
	if read.Hot {
		t.Fatal("server/ is not hot-reloaded, and the answer must say so")
	}

	// ★ And the write gate did not move.
	if _, err := callTool(t, tools, decl.ToolNameConfigWrite,
		`{"path":"server/config.yaml","content":"listen_addr: \":1\"\n"}`); err == nil {
		t.Fatal("server/ must still refuse to be written")
	}
}

// ★ The acceptance for the whole tool surface: an agent writes a file and
// asks whether it would load, and the answer names the line.
func TestConfigValidateAnswersWithLines(t *testing.T) {
	_, _, tools := configToolEnv(t)

	res := callToolJSON[spec.Diagnostics](t, tools, decl.ToolNameConfigValidate,
		`{"path":"agents/main.md","content":"---\nmodel: openai/fast\nbogus_key: 1\n---\nbody\n"}`)
	if res.OK {
		t.Fatalf("an unknown key must not validate: %+v", res)
	}
	if len(res.Issues) != 1 || res.Issues[0].Line != 3 {
		t.Fatalf("issues = %+v, want one at line 3", res.Issues)
	}

	good := callToolJSON[spec.Diagnostics](t, tools, decl.ToolNameConfigValidate,
		`{"path":"agents/main.md","content":"---\nmodel: openai/fast\n---\nbody\n"}`)
	if !good.OK {
		t.Fatalf("a legal file must validate: %+v", good.Issues)
	}

	// With no arguments the tree on disk is checked.
	tree := callToolJSON[spec.Diagnostics](t, tools, decl.ToolNameConfigValidate, `{}`)
	if !tree.OK {
		t.Fatalf("the tree on disk is legal: %+v", tree.Issues)
	}
}

func TestConfigTemplateReturnsTheSkeletonForAPath(t *testing.T) {
	_, _, tools := configToolEnv(t)

	single := callToolJSON[struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}](t, tools, decl.ToolNameConfigTemplate, `{"path":"tools/http.yaml"}`)
	if !strings.Contains(single.Content, "methods:") {
		t.Fatalf("the tools/http.yaml template must carry the fields the file needs: %q", single.Content)
	}

	section := callToolJSON[struct {
		Section   string            `json:"section"`
		Templates map[string]string `json:"templates"`
	}](t, tools, decl.ToolNameConfigTemplate, `{"section":"tools"}`)
	if section.Templates[spec.DirTools] == "" {
		t.Fatalf("the section skeleton must be in the answer: %v", section.Templates)
	}
	if section.Templates["tools/http.yaml"] == "" {
		t.Fatalf("the per-file templates must be in the answer: %v", section.Templates)
	}

	// ★ With neither, an index: the whole set is tens of kilobytes, and a
	//   model handed all of it still has to look for the one it wanted.
	index := callToolJSON[struct {
		Sections []struct {
			ID    string   `json:"id"`
			Paths []string `json:"paths"`
		} `json:"sections"`
	}](t, tools, decl.ToolNameConfigTemplate, `{}`)
	var sawTools bool
	for _, s := range index.Sections {
		if s.ID == spec.DirTools {
			sawTools = true
			if len(s.Paths) == 0 {
				t.Fatalf("tools/ ships templates, so its paths must be listed: %+v", s)
			}
		}
	}
	if !sawTools {
		t.Fatalf("the index must cover every partition: %+v", index.Sections)
	}
}

func TestConfigNoticesReportsWhatTheLoaderNoticed(t *testing.T) {
	svc, cfg, tools := configToolEnv(t)

	out := callToolJSON[struct {
		Deferred      []string `json:"deferred"`
		Version       string   `json:"version"`
		ConfigVersion string   `json:"configVersion"`
		Watch         bool     `json:"watch"`
	}](t, tools, decl.ToolNameConfigNotices, `{}`)

	if out.Version == "" || out.ConfigVersion == "" {
		t.Fatalf("both fingerprints must be present: %+v", out)
	}
	if out.Deferred == nil {
		t.Fatal("an empty list must be [], not null: a model reads the shape")
	}
	_ = svc
	_ = cfg
}

func TestConfigTierExplainsTheExceptionList(t *testing.T) {
	_, _, tools := configToolEnv(t)

	// A reader that is a built-in exception.
	got := callToolJSON[struct {
		Tool string `json:"tool"`
		Tier Tier   `json:"tier"`
	}](t, tools, decl.ToolNameConfigTier, `{"tool":"read_file"}`)
	if got.Tier != TierAllow {
		t.Fatalf("read_file tier = %q, want allow", got.Tier)
	}

	// A writer that is not.
	ask := callToolJSON[struct {
		Tool string `json:"tool"`
		Tier Tier   `json:"tier"`
	}](t, tools, decl.ToolNameConfigTier, `{"tool":"write_file"}`)
	if ask.Tier != TierAsk {
		t.Fatalf("write_file tier = %q, want ask", ask.Tier)
	}

	// ★ The one rule that cannot be widened: tools/http.yaml in this tree
	//   says `allows: [config_write]`, and the answer is still ask.
	self := callToolJSON[struct {
		Tool string `json:"tool"`
		Tier Tier   `json:"tier"`
	}](t, tools, decl.ToolNameConfigTier, `{"tool":"config_write"}`)
	if self.Tier != TierAsk {
		t.Fatalf("config_write tier = %q; a file that lists it must not grant its own editor", self.Tier)
	}

	perAgent := callToolJSON[struct {
		Agent string `json:"agent"`
		Tools []struct {
			Name string `json:"name"`
			Tier Tier   `json:"tier"`
		} `json:"tools"`
	}](t, tools, decl.ToolNameConfigTier, `{"agent":"main"}`)
	if len(perAgent.Tools) != 3 {
		t.Fatalf("tools = %+v, want the agent's own list", perAgent.Tools)
	}

	all := callToolJSON[struct {
		DefaultTier Tier     `json:"defaultTier"`
		Allow       []string `json:"allow"`
	}](t, tools, decl.ToolNameConfigTier, `{}`)
	if all.DefaultTier != TierAsk {
		t.Fatalf("defaultTier = %q, want ask", all.DefaultTier)
	}
	if len(all.Allow) == 0 {
		t.Fatal("the exception list must not be empty")
	}
	for _, n := range all.Allow {
		if n == decl.ToolNameConfigWrite {
			t.Fatal("config_write must never be in the exception list")
		}
	}
}

func TestConfigWriteSavesAndReportsWhenItApplies(t *testing.T) {
	svc, _, tools := configToolEnv(t)

	out := callToolJSON[struct {
		Path            string `json:"path"`
		Revision        string `json:"revision"`
		ConfigVersion   string `json:"configVersion"`
		Applied         bool   `json:"applied"`
		RequiresRestart bool   `json:"requiresRestart"`
	}](t, tools, decl.ToolNameConfigWrite,
		`{"path":"agents/main.md","content":"---\nmodel: openai/fast\ntools: [ls, config_get]\n---\nbody two\n"}`)

	if out.Revision == "" || out.ConfigVersion == "" {
		t.Fatalf("a write must report both revisions: %+v", out)
	}
	body, _, err := svc.ReadFile("agents/main.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "body two") {
		t.Fatalf("the file was not written: %q", body)
	}
	if out.RequiresRestart == out.Applied {
		t.Fatalf("applied and requiresRestart cannot agree: %+v", out)
	}
}

// ★ The refusal is the point of routing writes through the service: a file
// that would break the tree is rejected before it lands, and what was there
// stays there.
func TestConfigWriteRefusesAFileThatBreaksTheTree(t *testing.T) {
	svc, _, tools := configToolEnv(t)

	before, _, err := svc.ReadFile("agents/main.md")
	if err != nil {
		t.Fatal(err)
	}

	_, err = callTool(t, tools, decl.ToolNameConfigWrite,
		`{"path":"agents/main.md","content":"---\nmodel: openai/nope\n---\nbody\n"}`)
	if err == nil {
		t.Fatal("a dangling model reference must be refused")
	}
	if !strings.Contains(err.Error(), "openai/nope") {
		t.Fatalf("the refusal must name the model: %v", err)
	}

	after, _, err := svc.ReadFile("agents/main.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("a refused write must leave the file untouched")
	}
}

func TestConfigWriteRefusesAStaleBaseRevision(t *testing.T) {
	_, _, tools := configToolEnv(t)
	_, err := callTool(t, tools, decl.ToolNameConfigWrite,
		`{"path":"agents/main.md","content":"x","baseRevision":"deadbeef"}`)
	if err == nil {
		t.Fatal("a stale base revision must be refused")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("the refusal must say the file changed: %v", err)
	}
}

// ★ Visibility is the agent's own `tools:` list, and nothing else. An agent
// that does not name them does not see them, even though the tools exist.
func TestTheAgentSeesOnlyTheConfigToolsItNames(t *testing.T) {
	_, cfg, _ := configToolEnv(t)

	policy := policyOf(cfg)
	sp, err := specOf(cfg, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	table := specTable(policy, sp.agents[spec.DefaultAgentID])

	// The agent names config_get and config_write, so those are visible and
	// the other two are not.
	for _, name := range []string{decl.ToolNameConfigGet, decl.ToolNameConfigWrite} {
		if l := table.Blocked(name); l != LayerNone {
			t.Fatalf("%s is named by the agent, so it must not be blocked: %v", name, l)
		}
	}
	for _, name := range []string{decl.ToolNameConfigValidate, decl.ToolNameConfigNotices} {
		if l := table.Blocked(name); l == LayerNone {
			t.Fatalf("%s is not in the agent's tools: list and must be blocked", name)
		}
	}
}

// ★ A turn that has just created an agent with config_write used to be told
//
//	"no agent X under agents/". It read that as a failed write, wrote the file
//	again, polled config_get and even delegated a lookup to a subagent. All
//	config_* tools in a turn hold the snapshot that turn started with, so the
//	answer has to name the timing instead of claiming the agent is absent.
func TestConfigTierNamesAJustWrittenAgentAsPending(t *testing.T) {
	_, _, tools := configToolEnv(t)

	write := callToolJSON[struct {
		Path string `json:"path"`
		Note string `json:"note"`
	}](t, tools, decl.ToolNameConfigWrite,
		`{"path":"agents/brand-new.md","content":"---\nmodel: openai/fast\ntools: [ls]\n---\nbody\n"}`)
	if write.Path != "agents/brand-new.md" {
		t.Fatalf("the file was not written: %+v", write)
	}
	for _, want := range []string{"next turn", "snapshot"} {
		if !strings.Contains(write.Note, want) {
			t.Fatalf("a successful write must say when it takes effect (%q): %q", want, write.Note)
		}
	}

	_, err := callTool(t, tools, decl.ToolNameConfigTier, `{"agent":"brand-new"}`)
	if err == nil {
		t.Fatal("the agent is not in this turn's configuration; the lookup must say so")
	}
	for _, want := range []string{"brand-new", "next turn", "do not write it again"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the answer must say %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), `no agent "brand-new" under agents/`) {
		t.Fatalf("the answer must not claim the agent is absent: %v", err)
	}
}

// ★ A file can be on disk and still never run: an undeclared model leaves the
//
//	agent out of the settled tree. Promising "next turn" there would be a
//	second wrong answer, so missingAgentError checks LeftOut before the disk.
func TestMissingAgentErrorSeparatesLeftOutFromAbsent(t *testing.T) {
	svc, cfg, _ := configToolEnv(t)

	err := missingAgentError(svc, cfg, "ghost")
	if err == nil || !strings.Contains(err.Error(), `no agent "ghost" under agents/`) {
		t.Fatalf("a name with no file must be reported plainly: %v", err)
	}

	if err := os.WriteFile(filepath.Join(svc.Root(), "agents", "broken.md"),
		[]byte("---\nmodel: openai/missing\ntools: [ls]\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LeftOut()["broken"] == "" {
		t.Fatal("the loader must have left the agent with an undeclared model out")
	}
	err = missingAgentError(svc, reloaded, "broken")
	if err == nil {
		t.Fatal("a left-out agent must be reported")
	}
	for _, want := range []string{"left out", "will not appear next turn", "config_notices"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the answer must say %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "takes effect on the next turn") {
		t.Fatalf("a broken file never takes effect; do not promise the next turn: %v", err)
	}
}
