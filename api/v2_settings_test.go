package api

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

func settingsEnv(t *testing.T) (*Server, *spec.Service, string) {
	t.Helper()
	return settingsEnvWithWatch(t, true)
}

func settingsEnvNoWatch(t *testing.T) (*Server, *spec.Service, string) {
	t.Helper()
	return settingsEnvWithWatch(t, false)
}

func settingsEnvWithWatch(t *testing.T, watch bool) (*Server, *spec.Service, string) {
	t.Helper()
	root := t.TempDir()
	cfgRoot := filepath.Join(root, spec.DefaultBaseDirName)

	for _, sub := range []string{spec.DirLLM, spec.DirAgent, spec.DirTools, spec.DirServer} {
		if err := os.MkdirAll(filepath.Join(cfgRoot, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_SETTINGS_KEY", "sk-test")

	write := func(rel, body string) {
		p := filepath.Join(cfgRoot, rel)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("llm/openai.yaml", "type: openai\nbase_url: https://a.example\napi_key: ${TEST_SETTINGS_KEY}\nmodels:\n"+
		"  - id: fast\n    reasoning_default: off\n    reasoning:\n      off: { reasoning_effort: none }\n      high: { reasoning_effort: high }\n"+
		"  - id: plain\n    context_window: 8192\n")
	write("agents/main.md", "---\nmodel: openai/fast\ndescription: root\nreasoning: high  # level\n---\nbody\n")
	write("tools/http.yaml", "methods: [get]\nallows: [read_file]\n")
	write("server/spec.yaml", "listen_addr: \":9099\"\n"+
		"watch:\n  enabled: "+map[bool]string{true: "true", false: "false"}[watch]+"\n")

	svc, err := spec.NewService(cfgRoot, root)
	if err != nil {
		t.Fatal(err)
	}
	srv := newV2Server(t)
	srv.SetConfigService(svc)
	var live atomic.Pointer[spec.Config]
	cfg, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	live.Store(cfg)
	srv.SetConfigSource(live.Load)
	return srv, svc, cfgRoot
}

func decodeSettings[T any](t *testing.T, got result) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode %T: %v (%s)", out, err, got.body)
	}
	return out
}

func TestSettingsManifestListsEveryCategory(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	m := decodeSettings[spec.Manifest](t, got)

	if len(m.Categories) != len(spec.Categories) {
		t.Fatalf("categories = %d, want %d", len(m.Categories), len(spec.Categories))
	}
	byID := map[string]spec.ManifestCategory{}
	for _, c := range m.Categories {
		byID[c.ID] = c
	}
	if !byID[spec.DirServer].Editable == false {
		t.Fatalf("server must be listed as read-only, got %+v", byID[spec.DirServer])
	}
	if byID[spec.DirServer].Note == "" {
		t.Fatal("a read-only category must say why")
	}
	if !byID[spec.DirTools].Editable || !byID[spec.DirTools].Hot {
		t.Fatalf("tools must be editable and hot: %+v", byID[spec.DirTools])
	}
	for _, rel := range []string{
		"tools/http.yaml", "tools/web_search.yaml", "tools/wikipedia.yaml", "tools/kb.yaml",
	} {
		if m.Templates[rel] == "" {
			t.Fatalf("the manifest must carry a per-path template for %s: %v", rel, m.Templates)
		}
	}
	if len(m.ModelRefs) == 0 {
		t.Fatal("modelRefs must feed the agent template and the model picker")
	}
}

// ★ The manifest's wire shape used to be pinned by decoding into a struct
// the API declared itself. It now decodes into spec.Manifest — the struct
// that produced it — so a renamed json tag would decode into itself and no
// assertion would notice. The keys are therefore checked against the raw
// body: the browser reads these names, not Go field names.
func TestSettingsManifestKeepsItsWireNames(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)

	for _, key := range []string{
		`"categories"`, `"items"`, `"templates"`,
		`"modelRefs"`, `"reasoningLevels"`, `"configVersion"`,
		`"id"`, `"editable"`, `"hot"`, `"note"`,
		`"path"`, `"exists"`, `"revision"`,
	} {
		if !strings.Contains(got.body, key) {
			t.Fatalf("the manifest no longer carries %s: %s", key, got.body)
		}
	}
	if strings.Contains(got.body, `"Label"`) || strings.Contains(got.body, `"ModelRefs"`) {
		t.Fatalf("a Go field name leaked into the wire shape: %s", got.body)
	}
}

func TestSettingsManifestCarriesTheReasoningVocabulary(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	m := decodeSettings[spec.Manifest](t, got)

	if len(m.ModelRefs) == 0 {
		t.Fatal("precondition failed: no models in this env")
	}
	found := 0
	for _, ref := range m.ModelRefs {
		levels, ok := m.ReasoningLevels[ref]
		if !ok {
			continue
		}
		found++
		if len(levels) == 0 {
			t.Fatalf("%s: present but empty — absent and empty are different states", ref)
		}
		for i := 1; i < len(levels); i++ {
			if levels[i-1] >= levels[i] {
				t.Fatalf("%s: levels must be sorted and unique, got %v", ref, levels)
			}
		}
	}
	if found == 0 {
		t.Fatalf("no model reported a reasoning vocabulary: %+v", m.ReasoningLevels)
	}

	if _, present := m.ReasoningLevels["openai/plain"]; present {
		t.Fatalf("a model without a declared vocabulary must be absent, not empty: %+v", m.ReasoningLevels)
	}
	var hasPlain bool
	for _, ref := range m.ModelRefs {
		if ref == "openai/plain" {
			hasPlain = true
		}
	}
	if !hasPlain {
		t.Fatal("precondition failed: the plain model is missing from modelRefs")
	}
}

func TestSettingsManifestOmitsSkills(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	m := decodeSettings[spec.Manifest](t, got)

	// ★ The check is about the **structure**, not about the word: the
	// manifest carries the templates it hands out, and the agent template
	// mentions the `skills` key on purpose. What must not exist is a skills
	// **section** — skills are a directory tree, not flat entity files.
	for _, c := range m.Categories {
		if c.ID == spec.DirSkills {
			t.Fatal("skills must not be a category")
		}
	}
	for _, it := range m.Items {
		if strings.HasPrefix(it.Path, spec.DirSkills+"/") {
			t.Fatalf("skills must not be an editable item: %s", it.Path)
		}
	}
}

func TestSettingsManifestListsEachFileOnce(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	m := decodeSettings[spec.Manifest](t, got)

	seen := map[string]int{}
	var httpFile *spec.ManifestItem
	for i := range m.Items {
		it := m.Items[i]
		seen[it.Path]++
		if it.Path == "tools/http.yaml" {
			httpFile = &m.Items[i]
		}
	}
	for p, n := range seen {
		if n != 1 {
			t.Fatalf("%s is listed %d times, want exactly 1", p, n)
		}
	}
	if httpFile == nil {
		t.Fatal("tools/http.yaml is missing from the manifest")
	}
	if httpFile.Revision == "" {
		t.Fatal("an existing file must carry a revision")
	}
}

func TestSettingsManifestOmitsRevisionForMissingFiles(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)

	if strings.Contains(got.body, `"path":"tools/kb.yaml"`) {
		t.Fatalf("a file that does not exist must not be listed: %s", got.body)
	}
}

func TestSettingsItemReturnsContent(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	got := callJSON(t, srv, "GET", "/v2/settings/item?path=tools/http.yaml", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	item := decodeSettings[settingsItemBody](t, got)

	if !item.Exists || item.Revision == "" {
		t.Fatalf("exists=%v rev=%q", item.Exists, item.Revision)
	}
	if !strings.Contains(item.Content, "read_file") {
		t.Fatalf("content = %q", item.Content)
	}
	if !item.Hot {
		t.Fatal("tools/ is hot")
	}
}

func TestSettingsItemMissingFileIs200WithEmptyContent(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings/item?path=tools/kb.yaml", nil)

	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	item := decodeSettings[settingsItemBody](t, got)
	if item.Exists || item.Content != "" || item.Revision != "" {
		t.Fatalf("a missing file must read as empty-but-absent: %+v", item)
	}
}

func TestSettingsItemRejectsBadInput(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	cases := []struct {
		name, path string
		want       string
	}{
		{"missing path", "/v2/settings/item", codeConfigPathInvalid},
		{"escape", "/v2/settings/item?path=" + url.QueryEscape("../x.yaml"), codeConfigPathInvalid},
		{"a retired partition", "/v2/settings/item?path=policy/whatever.yaml", codeConfigPathInvalid},
	}
	for _, c := range cases {
		got := callJSON(t, srv, "GET", c.path, nil)
		if code := decodeCode(t, got.body); code != c.want {
			t.Errorf("%s: code = %q, want %q (%d %s)", c.name, code, c.want, got.code, got.body)
		}
	}
}

// ★ A read-only partition is **readable**. It is the file that configures the
// process, so it is the one a reader most wants to look at; refusing the read
// because the write needs a restart was answering the wrong question. What
// stays refused is the write — and that is what the next case pins.
func TestSettingsItemReadsAReadOnlyPartition(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	got := callJSON(t, srv, "GET", "/v2/settings/item?path=server/spec.yaml", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("server/ must be readable: = %d (%s)", got.code, got.body)
	}
	item := decodeSettings[settingsItemBody](t, got)
	if !item.Exists || !strings.Contains(item.Content, "listen_addr") {
		t.Fatalf("the content must be the file's own: %+v", item)
	}
	if item.Revision == "" {
		t.Fatal("a file that exists has a revision; an empty one says it does not exist")
	}
	if item.Hot {
		t.Fatal("server/ is not hot-reloaded, and the answer must say so")
	}

	// The read path is open; the write path is not.
	for _, method := range []string{"PUT", "POST", "DELETE"} {
		var wrote result
		switch method {
		case "DELETE":
			wrote = call(t, srv, method, "/v2/settings/item?path=server/spec.yaml", "")
		default:
			wrote = callJSON(t, srv, method, "/v2/settings/item", settingsWriteBody{
				Path: "server/spec.yaml", Content: "listen_addr: \":1\"\n", BaseRevision: item.Revision,
			})
		}
		if wrote.code == consts.StatusOK {
			t.Fatalf("%s server/spec.yaml must be refused, got %d (%s)", method, wrote.code, wrote.body)
		}
		if code := decodeCode(t, wrote.body); code != codeConfigPathInvalid {
			t.Fatalf("%s: code = %q, want %q", method, code, codeConfigPathInvalid)
		}
	}
}

func TestSettingsItemAcceptsLimitsYaml(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings/item?path=tools/kb.yaml", nil)

	if got.code != consts.StatusOK {
		t.Fatalf("tools/kb.yaml must be a valid path: = %d (%s)", got.code, got.body)
	}
}

// ★ The mcps/ partition is editable, and it does not have to exist yet:
//
//	the write creates the directory, so a user who never had an MCP server
//	can still add one from the editor.
func TestSettingsCreatesTheMCPPartition(t *testing.T) {
	srv, _, cfgRoot := settingsEnvNoWatch(t)

	if _, err := os.Stat(filepath.Join(cfgRoot, spec.DirMCP)); err == nil {
		t.Fatal("the partition must not exist yet")
	}

	body := settingsWriteBody{
		Path:    "mcps/github.yaml",
		Content: "transport: stdio\ncommand: npx\n",
	}
	got := callJSON(t, srv, "PUT", "/v2/settings/item", body)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	onDisk, err := os.ReadFile(filepath.Join(cfgRoot, "mcps", "github.yaml"))
	if err != nil {
		t.Fatalf("the file must be there: %v", err)
	}
	if !strings.Contains(string(onDisk), "command: npx") {
		t.Fatalf("the file = %q", onDisk)
	}
}

// ★ The file's name IS the server's name, so a name that could not be
//
//	read back is refused.
func TestSettingsRefusesAnUnreadableServerName(t *testing.T) {
	srv, _, _ := settingsEnvNoWatch(t)

	body := settingsWriteBody{
		Path:    "mcps/github__x.yaml",
		Content: "transport: stdio\ncommand: npx\n",
	}
	got := callJSON(t, srv, "PUT", "/v2/settings/item", body)
	if got.code == consts.StatusOK {
		t.Fatalf("a server name with a run of underscores must be refused: %s", got.body)
	}
}

func TestSettingsSaveLands(t *testing.T) {
	srv, svc, cfgRoot := settingsEnv(t)
	_ = svc
	_, orig, _ := svc.ReadFile("tools/http.yaml")

	body := settingsWriteBody{
		Path:    "tools/http.yaml",
		Content: "methods: [get]\nallows: [execute]\n", BaseRevision: orig,
	}
	got := callJSON(t, srv, "PUT", "/v2/settings/item", body)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	res := decodeSettings[settingsWriteResult](t, got)

	if res.Revision == "" {
		t.Fatal("the response must carry the new revision")
	}
	if !res.Applied {
		t.Fatalf("tools/ is hot and the watcher is on, so it must report applied: %+v", res)
	}
	onDisk, err := os.ReadFile(filepath.Join(cfgRoot, "tools", "http.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "methods: [get]\nallows: [execute]\n" {
		t.Fatalf("on disk = %q", onDisk)
	}
}

func TestSettingsSaveReportsNotAppliedWhenWatchIsOff(t *testing.T) {
	srv, _, _ := settingsEnvNoWatch(t)

	got := callJSON(t, srv, "PUT", "/v2/settings/item", settingsWriteBody{
		Path: "tools/http.yaml", Content: "methods: [get]\nallows: [execute]\n",
	})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	res := decodeSettings[settingsWriteResult](t, got)
	if res.Applied || !res.RequiresRestart || res.Note == "" {
		t.Fatalf("watch is off, so this must say so: %+v", res)
	}
	if res.Revision == "" {
		t.Fatal("the write must still have landed and reported a revision")
	}
}

func TestSettingsSaveRejectsLiteralSecret(t *testing.T) {
	srv, svc, _ := settingsEnv(t)
	_, before, _ := svc.ReadFile("llm/openai.yaml")

	got := callJSON(t, srv, "PUT", "/v2/settings/item", settingsWriteBody{
		Path: "llm/openai.yaml", BaseRevision: before,
		Content: "type: openai\nbase_url: https://a.example\napi_key: sk-leaked\nmodels:\n  - id: fast\n",
	})
	if code := decodeCode(t, got.body); code != codeConfigSecretLiteral {
		t.Fatalf("code = %q, want %q (%d %s)", code, codeConfigSecretLiteral, got.code, got.body)
	}
	_, after, _ := svc.ReadFile("llm/openai.yaml")
	if before != after {
		t.Fatal("a rejected write must not touch the file")
	}
}

func TestSettingsSaveBrokenMergeIs422(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "PUT", "/v2/settings/item", settingsWriteBody{
		Path: "agents/main.md", Content: "---\nmodel: openai/nope\n---\nx\n",
	})
	if got.code != consts.StatusUnprocessableEntity {
		t.Fatalf("= %d, want 422 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeConfigWouldBreak {
		t.Fatalf("code = %q", code)
	}
}

func TestSettingsSaveStaleRevisionIs409(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "PUT", "/v2/settings/item", settingsWriteBody{
		Path:    "tools/http.yaml",
		Content: "methods: [get]\nallows: [execute]\n", BaseRevision: "deadbeef",
	})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeConfigWriteConflict {
		t.Fatalf("code = %q", code)
	}
}

func TestSettingsDeleteRemovesTheFile(t *testing.T) {
	srv, svc, cfgRoot := settingsEnv(t)
	got := call(t, srv, "DELETE", "/v2/settings/item?path="+url.QueryEscape("tools/http.yaml"), "")
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	if _, err := os.Stat(filepath.Join(cfgRoot, "tools", "http.yaml")); err == nil {
		t.Fatal("the file is still there")
	}
	res := decodeSettings[settingsWriteResult](t, got)
	if res.Revision != "" {
		t.Fatalf("revision = %q, want empty after a delete", res.Revision)
	}
	_ = svc
}

func TestSettingsDeleteDanglingProviderIs422(t *testing.T) {
	srv, svc, _ := settingsEnv(t)
	got := call(t, srv, "DELETE", "/v2/settings/item?path="+url.QueryEscape("llm/openai.yaml"), "")

	if got.code != consts.StatusUnprocessableEntity {
		t.Fatalf("= %d, want 422 (%s)", got.code, got.body)
	}
	if _, _, err := svc.ReadFile("llm/openai.yaml"); err != nil {
		t.Fatalf("the file must survive a rejected delete: %v", err)
	}
}

func TestSettingsValidateAgreesWithSave(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	cases := []struct {
		name, path, content string
		wantOK              bool
	}{
		{"valid tools file", "tools/http.yaml", "methods: [get]\nallows: [execute]\n", true},
		{"literal secret", "llm/openai.yaml", "type: openai\nbase_url: https://x\napi_key: sk-x\nmodels: []\n", false},
		{"dangling ref", "agents/main.md", "---\nmodel: openai/nope\n---\nx\n", false},
		{"bad yaml", "tools/http.yaml", "methods: [\n", false},
	}
	for _, c := range cases {
		got := callJSON(t, srv, "POST", "/v2/settings/validate", settingsValidateBody{
			Path: c.path, Content: c.content,
		})
		if got.code != consts.StatusOK {
			t.Fatalf("%s: validate = %d (%s)", c.name, got.code, got.body)
		}
		res := decodeSettings[spec.Diagnostics](t, got)
		if res.OK != c.wantOK {
			t.Errorf("%s: validate ok = %v, want %v (%v)", c.name, res.OK, c.wantOK, res.Issues)
			continue
		}
		if !c.wantOK && len(res.Issues) == 0 {
			t.Errorf("%s: a failure must carry at least one issue", c.name)
		}
	}
}

func TestSettingsWithoutServiceIs503(t *testing.T) {
	srv := newV2Server(t)
	for _, p := range []string{"/v2/settings", "/v2/settings/item?path=tools/http.yaml"} {
		got := callJSON(t, srv, "GET", p, nil)
		if got.code != consts.StatusServiceUnavailable {
			t.Errorf("%s: = %d, want 503 (%s)", p, got.code, got.body)
		}
	}
}

func TestSettingsRoutesBeatTheStaticCatchAll(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	for _, p := range []string{"/v2/settings", "/v2/settings/item?path=tools/http.yaml"} {
		got := callJSON(t, srv, "GET", p, nil)
		if got.code == consts.StatusNotFound || got.code == consts.StatusOK {
			continue
		}
		if strings.Contains(got.body, "<!doctype html>") || strings.Contains(got.body, "<html") {
			t.Fatalf("%s fell through to the SPA catch-all", p)
		}
	}
	if got := callJSON(t, srv, "GET", "/v2/settings", nil); got.code != consts.StatusOK {
		t.Fatalf("GET /v2/settings = %d, want 200 (%s)", got.code, got.body)
	}
}

// ★ The reason this endpoint answers with issues instead of one string: the
// parser already knows the line, and the editor puts a marker beside it. A
// response that flattens that back into prose throws away the only thing the
// round trip bought.
func TestSettingsValidateReportsIssuesWithLines(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	got := callJSON(t, srv, "POST", "/v2/settings/validate", settingsValidateBody{
		Path:    "agents/main.md",
		Content: "---\nmodel: openai/fast\nbogus_key: 1\n---\nbody\n",
	})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	for _, key := range []string{`"ok"`, `"issues"`, `"configVersion"`, `"line"`, `"severity"`, `"message"`, `"path"`} {
		if !strings.Contains(got.body, key) {
			t.Fatalf("the answer no longer carries %s: %s", key, got.body)
		}
	}
	if strings.Contains(got.body, `"errors"`) {
		t.Fatalf("the string list is gone, replaced by issues: %s", got.body)
	}

	res := decodeSettings[spec.Diagnostics](t, got)
	if res.OK {
		t.Fatalf("an unknown key must not validate: %s", got.body)
	}
	if len(res.Issues) != 1 {
		t.Fatalf("issues = %+v, want exactly the unknown key", res.Issues)
	}
	iss := res.Issues[0]
	if iss.Line != 3 || iss.Severity != spec.SeverityError || iss.Path != "agents/main.md" {
		t.Fatalf("issue = %+v, want line 3 of agents/main.md as an error", iss)
	}
	if !strings.Contains(iss.Message, "bogus_key") {
		t.Fatalf("message = %q", iss.Message)
	}
}
