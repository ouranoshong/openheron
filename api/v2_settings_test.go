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

	"openheron/agentkit/spec"
)

// settingsEnv 造一套两级配置目录与一个接线好的 Server。
//
// ★ 与 testConfig（v2_test.go）分开：那边测的是只读视图的形状，
//
//	而 /v2/settings 的每条断言都要求磁盘处于某个**已知状态**
//	（某级缺文件、某级被遮蔽），共享一个 fixture 反而要互相迁就。
//

func settingsEnv(t *testing.T) (*Server, *spec.Service, string) {
	t.Helper()
	return settingsEnvWithWatch(t, true)
}

// settingsEnvNoWatch 建一套热更新**关闭**的环境。
//
// ★ 为什么要单独一个：watch.enabled 是在**启动期**从 server/spec.yaml
//
//	读一次的，之后只由热更新改。测试里事后往磁盘上写它不会生效
//	—— live 快照已经是启动那一刻的了。
func settingsEnvNoWatch(t *testing.T) (*Server, *spec.Service, string) {
	t.Helper()
	return settingsEnvWithWatch(t, false)
}

func settingsEnvWithWatch(t *testing.T, watch bool) (*Server, *spec.Service, string) {
	t.Helper()
	root := t.TempDir()
	cfgRoot := filepath.Join(root, spec.DefaultBaseDirName)

	for _, sub := range []string{spec.DirLLM, spec.DirAgent, spec.DirPolicy, spec.DirServer} {
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
	// ★ 夹具里的模型要**声明 reasoning 词汇表**，与出厂 deepseek.yaml 同形。
	//   少了它，「编辑器拿不到档位表」这件事在测试里就看不见 ——
	//   而那正是「合法的 reasoning 存不进去」那条 bug 的上游。
	write("llm/openai.yaml", "type: openai\nbase_url: https://a.example\napi_key: ${TEST_SETTINGS_KEY}\nmodels:\n"+
		"  - id: fast\n    reasoning_default: off\n    reasoning:\n      off: { reasoning_effort: none }\n      high: { reasoning_effort: high }\n"+
		"  - id: plain\n    context_window: 8192\n")
	write("agents/main.md", "---\nmodel: openai/fast\ndescription: root\nreasoning: high  # level\n---\nbody\n")
	write("policy/permissions.yaml", "ask: [read_file]\n")
	write("server/spec.yaml", "listen_addr: \":9099\"\n"+
		"watch:\n  enabled: "+map[bool]string{true: "true", false: "false"}[watch]+"\n")

	svc, err := spec.NewService(cfgRoot, root)
	if err != nil {
		t.Fatal(err)
	}
	srv := newV2Server(t)
	srv.SetConfigService(svc)
	// main.go 持有的是原子指针，读的是**实时**配置 —— 这里同理：
	// 热更新会让写死的快照停在启动那一刻。
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

// ── 清单 ──────────────────────────────────────────────────────────────────

func TestSettingsManifestListsEveryCategory(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	m := decodeSettings[settingsManifest](t, got)

	// 四个分区都要在 —— server 也要在，它是「只读」而不是「不存在」。
	if len(m.Categories) != len(spec.Categories) {
		t.Fatalf("categories = %d, want %d", len(m.Categories), len(spec.Categories))
	}
	byID := map[string]settingsCategory{}
	for _, c := range m.Categories {
		byID[c.ID] = c
	}
	if !byID[spec.DirServer].Editable == false {
		t.Fatalf("server must be listed as read-only, got %+v", byID[spec.DirServer])
	}
	if byID[spec.DirServer].Note == "" {
		t.Fatal("a read-only category must say why")
	}
	if !byID[spec.DirPolicy].Editable || !byID[spec.DirPolicy].Hot {
		t.Fatalf("policy must be editable and hot: %+v", byID[spec.DirPolicy])
	}
	// ★ tools/ 必须**可编辑且热更新** —— 它是「改一个工具的参数下一轮就生效」
	//   的那个分区。不可编辑或不快照，用户就只能去改文件碰运气。
	if !byID[spec.DirTools].Editable || !byID[spec.DirTools].Hot {
		t.Fatalf("tools must be editable and hot: %+v", byID[spec.DirTools])
	}
	// ★ tools/ 下有四种文件形态，而分区模板放不下四份内容 ——
	//   所以模板表里必须有**按路径**的键（前端优先用它）。
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

// TestSettingsManifestCarriesTheReasoningVocabulary 守住编辑器的本地档位校验。
//
// ★ 少了这张表，编辑器只能说「服务端会校验」—— 而那句话对着一份**合法**
//
//	配置也会报，Issue 当时又没有严重级别、save() 见 issue 就 return，
//	于是「agents/main.md 写了 reasoning: high」根本存不进去。
//
// ★ 要的是**每个**模型的词汇表，不只是已被 agent 引用的那些：
//
//	「新建 agent」时要先能对候选模型说清档位合不合法。
func TestSettingsManifestCarriesTheReasoningVocabulary(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	m := decodeSettings[settingsManifest](t, got)

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
		// ★ 必须排序：前端会把它显示成下拉，顺序逐次不同会让刷新都在跳。
		for i := 1; i < len(levels); i++ {
			if levels[i-1] >= levels[i] {
				t.Fatalf("%s: levels must be sorted and unique, got %v", ref, levels)
			}
		}
	}
	if found == 0 {
		t.Fatalf("no model reported a reasoning vocabulary: %+v", m.ReasoningLevels)
	}

	// ★ 夹具里还有一个**不声明**词汇表的模型（openai/plain）。
	//   它必须**不在表里**，而不是「在表里但值是空数组」——
	//   前端对这两种是不同处置：空数组会被读成「这个模型一个档位都没有」，
	//   而「不在表里」才表示「它不支持思考档位」。
	//   这条断言守的正是 [spec.Config.ReasoningLevels] 里那个 len==0 的守卫。
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
	// ★ 用户拍板「skills 先不处理」。列出来必然招来「为什么这个不能改」，
	//   而答案（它是目录结构）是实现细节，不是产品决策。
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	m := decodeSettings[settingsManifest](t, got)

	if strings.Contains(got.body, spec.DirSkills) {
		t.Fatalf("skills must not appear anywhere in the manifest: %s", got.body)
	}
	for _, c := range m.Categories {
		if c.ID == spec.DirSkills {
			t.Fatal("skills must not be a category")
		}
	}
}

// ★ 清单里列的是**生效根**里的文件，且每个文件只有一项 ——
//
//	取消继承链之后没有「另一层的那份」了，所以「同一路径出现两次」是 bug。
func TestSettingsManifestListsEachFileOnce(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	got := callJSON(t, srv, "GET", "/v2/settings", nil)
	m := decodeSettings[settingsManifest](t, got)

	seen := map[string]int{}
	var policy *settingsItem
	for i := range m.Items {
		it := m.Items[i]
		seen[it.Path]++
		if it.Path == "policy/permissions.yaml" {
			policy = &m.Items[i]
		}
	}
	for p, n := range seen {
		if n != 1 {
			t.Fatalf("%s is listed %d times, want exactly 1", p, n)
		}
	}
	if policy == nil {
		t.Fatal("policy/permissions.yaml is missing from the manifest")
	}
	if policy.Revision == "" {
		t.Fatal("an existing file must carry a revision")
	}
}

func TestSettingsManifestOmitsRevisionForMissingFiles(t *testing.T) {
	// 界面靠空 revision 区分「可新建」与「读不出来」。
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings", nil)

	// ★ 清单只列**磁盘上真的有**的文件 —— 「可以新建」的那些不在清单里，
	//   而是由界面按分区模板现生成的。
	if strings.Contains(got.body, `"path":"policy/limits.yaml"`) {
		t.Fatalf("a file that does not exist must not be listed: %s", got.body)
	}
}

// ── 单项读取 ──────────────────────────────────────────────────────────────

func TestSettingsItemReturnsContent(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	got := callJSON(t, srv, "GET", "/v2/settings/item?path=policy/permissions.yaml", nil)
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
		t.Fatal("policy/ is hot")
	}
}

func TestSettingsItemMissingFileIs200WithEmptyContent(t *testing.T) {
	// ★ 「还没有这个文件」不是错误 —— 界面要能把它当成新建起点。
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings/item?path=policy/limits.yaml", nil)

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
		{"non-editable", "/v2/settings/item?path=server/spec.yaml", codeConfigPathInvalid},
		// ★ policy/ 下只有两个白名单名字。limits.yaml 现在是**真的**限制规则，
		//   所以它合法（见 config-design.md §12.2）—— 被拒的是第三个名字。
		{"policy whitelist", "/v2/settings/item?path=policy/whatever.yaml", codeConfigPathInvalid},
	}
	for _, c := range cases {
		got := callJSON(t, srv, "GET", c.path, nil)
		if code := decodeCode(t, got.body); code != c.want {
			t.Errorf("%s: code = %q, want %q (%d %s)", c.name, code, c.want, got.code, got.body)
		}
	}
}

// ★ policy/limits.yaml 是**合法**路径 —— 它是 2026-10-21 起真的限制规则。
//
// 曾经的断言是「limits.yaml 被拒」（那时它只是个 deferred 的空壳）。
func TestSettingsItemAcceptsLimitsYaml(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	got := callJSON(t, srv, "GET", "/v2/settings/item?path=policy/limits.yaml", nil)

	if got.code != consts.StatusOK {
		t.Fatalf("policy/limits.yaml must be a valid path: = %d (%s)", got.code, got.body)
	}
}

// ── 写入 ──────────────────────────────────────────────────────────────────

func TestSettingsSaveLands(t *testing.T) {
	srv, svc, cfgRoot := settingsEnv(t)
	_ = svc
	_, orig, _ := svc.ReadFile("policy/permissions.yaml")

	body := settingsWriteBody{
		Path:    "policy/permissions.yaml",
		Content: "ask: [execute]\n", BaseRevision: orig,
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
		t.Fatalf("policy/ is hot and the watcher is on, so it must report applied: %+v", res)
	}
	onDisk, err := os.ReadFile(filepath.Join(cfgRoot, "policy", "permissions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "ask: [execute]\n" {
		t.Fatalf("on disk = %q", onDisk)
	}
}

func TestSettingsSaveReportsNotAppliedWhenWatchIsOff(t *testing.T) {
	// ★ 写成功但不生效，必须说出来。
	//   「保存成功」与「已生效」在界面上长得一样，而那个区别是全部要害。
	srv, _, _ := settingsEnvNoWatch(t)

	got := callJSON(t, srv, "PUT", "/v2/settings/item", settingsWriteBody{
		Path: "policy/permissions.yaml", Content: "ask: [execute]\n",
	})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	res := decodeSettings[settingsWriteResult](t, got)
	if res.Applied || !res.RequiresRestart || res.Note == "" {
		t.Fatalf("watch is off, so this must say so: %+v", res)
	}
	// 写仍然成功落盘了 —— 「不生效」说的是运行时，不是「没写成」。
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
	// ★ 单文件 yaml 合法，合并后不合法 —— 那是 422 而不是 400：
	//   前者改这个文件就能好，后者是「这个改动会破坏别的东西」。
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
		Path:    "policy/permissions.yaml",
		Content: "ask: [execute]\n", BaseRevision: "deadbeef",
	})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeConfigWriteConflict {
		t.Fatalf("code = %q", code)
	}
}

// ── 删除 ──────────────────────────────────────────────────────────────────

func TestSettingsDeleteRemovesTheFile(t *testing.T) {
	srv, svc, cfgRoot := settingsEnv(t)
	got := call(t, srv, "DELETE", "/v2/settings/item?path="+url.QueryEscape("policy/permissions.yaml"), "")
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	if _, err := os.Stat(filepath.Join(cfgRoot, "policy", "permissions.yaml")); err == nil {
		t.Fatal("the file is still there")
	}
	// 删除后的 revision 归零 —— 界面据此把它标成「可新建」。
	res := decodeSettings[settingsWriteResult](t, got)
	if res.Revision != "" {
		t.Fatalf("revision = %q, want empty after a delete", res.Revision)
	}
	_ = svc
}

func TestSettingsDeleteDanglingProviderIs422(t *testing.T) {
	// ★ DELETE 的试合并不是可选的：一删就再也回不来了。
	srv, svc, _ := settingsEnv(t)
	got := call(t, srv, "DELETE", "/v2/settings/item?path="+url.QueryEscape("llm/openai.yaml"), "")

	if got.code != consts.StatusUnprocessableEntity {
		t.Fatalf("= %d, want 422 (%s)", got.code, got.body)
	}
	if _, _, err := svc.ReadFile("llm/openai.yaml"); err != nil {
		t.Fatalf("the file must survive a rejected delete: %v", err)
	}
}

// ── 校验 ──────────────────────────────────────────────────────────────────

func TestSettingsValidateAgreesWithSave(t *testing.T) {
	// ★ 两条路径必须给同一个答案。预校验与写入各写一份的话，
	//   漂移的表现是「界面说能存，保存时失败」。
	srv, _, _ := settingsEnv(t)

	cases := []struct {
		name, path, content string
		wantOK              bool
	}{
		{"valid policy", "policy/permissions.yaml", "ask: [execute]\n", true},
		{"literal secret", "llm/openai.yaml", "type: openai\nbase_url: https://x\napi_key: sk-x\nmodels: []\n", false},
		{"dangling ref", "agents/main.md", "---\nmodel: openai/nope\n---\nx\n", false},
		{"bad yaml", "policy/permissions.yaml", "allow: [\n", false},
	}
	for _, c := range cases {
		got := callJSON(t, srv, "POST", "/v2/settings/validate", settingsValidateBody{
			Path: c.path, Content: c.content,
		})
		if got.code != consts.StatusOK {
			t.Fatalf("%s: validate = %d (%s)", c.name, got.code, got.body)
		}
		res := decodeSettings[settingsValidateResult](t, got)
		if res.OK != c.wantOK {
			t.Errorf("%s: validate ok = %v, want %v (%v)", c.name, res.OK, c.wantOK, res.Errors)
			continue
		}
		if !c.wantOK && len(res.Errors) == 0 {
			t.Errorf("%s: a failure must carry an error message", c.name)
		}
	}
}

// ── 未接线 ────────────────────────────────────────────────────────────────

func TestSettingsWithoutServiceIs503(t *testing.T) {
	// 只读视图仍然完全可用 —— 不想让人从界面上改配置时，断开它即可。
	srv := newV2Server(t)
	for _, p := range []string{"/v2/settings", "/v2/settings/item?path=policy/permissions.yaml"} {
		got := callJSON(t, srv, "GET", p, nil)
		if got.code != consts.StatusServiceUnavailable {
			t.Errorf("%s: = %d, want 503 (%s)", p, got.code, got.body)
		}
	}
}

// TestSettingsRoutesBeatTheStaticCatchAll 复用既有那条路由优先级测试的形状。
//
// StaticFS 挂在最后当兜底；任何晚于它注册的 /v2 路由都会变成一个
// 「看起来像未实现」的 404。
func TestSettingsRoutesBeatTheStaticCatchAll(t *testing.T) {
	srv, _, _ := settingsEnv(t)
	for _, p := range []string{"/v2/settings", "/v2/settings/item?path=policy/permissions.yaml"} {
		got := callJSON(t, srv, "GET", p, nil)
		if got.code == consts.StatusNotFound || got.code == consts.StatusOK {
			continue // 通过路由表匹配到了
		}
		if strings.Contains(got.body, "<!doctype html>") || strings.Contains(got.body, "<html") {
			t.Fatalf("%s fell through to the SPA catch-all", p)
		}
	}
	if got := callJSON(t, srv, "GET", "/v2/settings", nil); got.code != consts.StatusOK {
		t.Fatalf("GET /v2/settings = %d, want 200 (%s)", got.code, got.body)
	}
}
