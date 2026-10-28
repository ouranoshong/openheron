package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"openheron/persist"
)

// writeTmpSuffix 是配置文件的临时文件后缀。
//
// ★ 必须让加载逻辑忽略它：[Service.loadDir] 只收 `Categories[].Ext` 结尾的
//
//	文件，所以 `.tmp` 天然被挡住。而 `.yaml.tmp` 这类选择能让人在目录里
//	一眼看出「上次写崩了」。
const writeTmpSuffix = ".tmp"

// revisionLen 是 [Revision] 返回的哈希长度。
//
// ★ 取 8 位十六进制：够短能塞进日志与 UI，而要在一次编辑会话里撞上
//
//	（用户连点几次保存、或两个窗口同时编辑）几乎不可能。碰撞的后果是
//	「本该报 409 的并发写被放行」，极低概率且后果轻微。
const revisionLen = 8

// WriteError 是写入失败的分类。API 层据此映射 HTTP 状态码与错误码，
// 不必去匹配错误文本 —— 匹配文本在文案一改就失效。
type WriteErrorKind string

const (
	// ErrPathInvalid 是路径不合法：`..`、越级、类别不存在或不可编辑。
	ErrPathInvalid WriteErrorKind = "config_path_invalid"
	// ErrSecretLiteral 是 `api_key` 给了明文。
	ErrSecretLiteral WriteErrorKind = "config_secret_literal"
	// ErrMergeRejected 是单文件合法但合并后不合法。
	ErrMergeRejected WriteErrorKind = "config_would_break_merge"
	// ErrConflict 是 `baseRevision` 与磁盘不符。
	ErrConflict WriteErrorKind = "config_write_conflict"
)

// WriteError 是配置写入的失败。
//
// ★ 刻意把「路径」「密钥」「合并」「冲突」分成四类而不是笼统的
//
//	「写失败」：它们在界面上的**处置完全不同** —— 路径要改地址栏、
//	密钥要改成 `${ENV}` 引用、合并要指出改哪个文件、冲突要给出磁盘版本。
//	合成一个错误码，界面就只能给出一句「保存失败」。
type WriteError struct {
	Kind    WriteErrorKind
	Rel     string // 相对路径，用于界面定位
	Message string
	// Err 是原始错误，供日志。Message 已经足够展示。
	Err error
}

func (e *WriteError) Error() string {
	if e.Rel != "" {
		return fmt.Sprintf("%s: %s: %s", e.Kind, e.Rel, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *WriteError) Unwrap() error { return e.Err }

func writeErrf(kind WriteErrorKind, rel string, format string, args ...any) *WriteError {
	return &WriteError{Kind: kind, Rel: rel, Message: fmt.Sprintf(format, args...)}
}

// PathErrorKind 让调用方能用 errors.Is 判断某类失败。
func (e *WriteError) Is(target error) bool {
	other, ok := target.(*WriteError)
	return ok && other.Kind == e.Kind
}

// ── 路径校验 ──────────────────────────────────────────────────────────────

// ValidatePath 校验一个「相对路径」是否可以写入。
//
// ★ 它是纯函数，所以非法路径这一整类不必起进程、不必碰磁盘就能测干净。
//
// 三道检查，缺一不可：
//
//  1. **形状** —— `..`、绝对路径、空段。前两者会让路径逃出该级配置目录，
//     而 sessionID 直落文件路径这件事本仓库已经吃过一次亏（见
//     architecture.md §5.6「身份即安全边界」）。
//  2. **分区** —— 必须是 [Categories] 里某一项，且该项 [Category.Editable]。
//  3. **文件名** —— `policy/` 下只认白名单里的名字。文件名白名单比键级校验强：
//     拼错就是「文件不存在」，在写下的那一刻就失败。
func ValidatePath(rel string) (Category, error) {
	if strings.TrimSpace(rel) == "" {
		return Category{}, writeErrf(ErrPathInvalid, rel, "path is empty")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return Category{}, writeErrf(ErrPathInvalid, rel, "path must be relative to the config directory")
	}
	// ★ 在 **Clean 之前**判 `..`：Clean 会把 `policy/../llm/x.yaml` 规范化成
	//   `llm/x.yaml`，规范化后自然没有 `..`，检查就白做了。
	//   那条路径落盘位置确实没错（仍在本级目录内），但它是从 HTTP 参数
	//   来的：静默规范化等于「同一个目标有无穷多种写法」，而界面的
	//   shadowed 判定是按路径字符串比对的，写法一多就判不准。
	slash := filepath.ToSlash(rel)
	if containsDotDot(slash) {
		return Category{}, writeErrf(ErrPathInvalid, rel, "path must not contain %q", "..")
	}
	slash = path.Clean(slash)

	cat, ok := CategoryByDir(path.Dir(slash))
	if !ok {
		return Category{}, writeErrf(ErrPathInvalid, rel,
			"unknown config section %q; known sections are %s", path.Dir(slash), knownDirs())
	}
	if !cat.Editable {
		return Category{}, writeErrf(ErrPathInvalid, rel,
			"section %q is not editable: it is not hot-reloaded, so changes would need a restart", cat.Dir)
	}
	if name := path.Base(slash); name == "" || name == "." || strings.HasPrefix(name, ".") {
		return Category{}, writeErrf(ErrPathInvalid, rel, "file name must not start with a dot")
	}
	if err := validateFileName(cat, rel); err != nil {
		return Category{}, err
	}
	return cat, nil
}

// validateFileName 施加分区的文件名白名单。
//
// ★ policy/ 与 tools/ 有白名单，因为它们的键名与文件名高度重合
//
//	（permissions / limits；http / web_search / wikipedia / kb），
//	而 [Service.loadPolicy] 与 [Service.loadTool] 对未知文件名**报错**。
//	写入门若不守同一条规则，用户会写进去一个「保存成功但加载即报错」的文件。
func validateFileName(cat Category, rel string) error {
	base := path.Base(filepath.ToSlash(rel))
	switch cat.Dir {
	case DirPolicy:
		switch base {
		case "permissions.yaml", "limits.yaml":
			return nil
		default:
			return writeErrf(ErrPathInvalid, rel,
				"policy/ only accepts permissions.yaml and limits.yaml; other files here are rejected at load time")
		}
	case DirTools:
		switch base {
		case "http.yaml", "web_search.yaml", "wikipedia.yaml", "kb.yaml":
			return nil
		default:
			return writeErrf(ErrPathInvalid, rel,
				"tools/ only accepts http.yaml, web_search.yaml, wikipedia.yaml and kb.yaml; "+
					"other files here are rejected at load time")
		}
	}
	return nil
}

func containsDotDot(cleaned string) bool {
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func knownDirs() string {
	out := make([]string, 0, len(Categories))
	for _, c := range Categories {
		out = append(out, c.Dir)
	}
	return strings.Join(out, ", ")
}

// ── 清单 ──────────────────────────────────────────────────────────────────

// ListFiles 列出某个分区下的实体文件名（不含路径），已排序。
//
// ★ 它**不施加写入门的限制** —— `server/` 的文件也要能被界面列出来
//
//	（只是不可编辑）。若复用 [ValidatePath]，只读那一类就整个消失了，
//	用户会以为这个分区不存在。
//
// 目录不存在返回空切片而不是错误：新项目就是空的。
func (s *Service) ListFiles(cat Category) ([]string, error) {
	if s.root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(s.root, cat.Dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), cat.Ext) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// ── 模板 ──────────────────────────────────────────────────────────────────

// Template 返回某个分区新建文件时的骨架。
//
// ★ 骨架是**故意不完整**的：`agents` 的 `model:` 留空，于是直接保存会被
//
//	试合并拒掉（[Service.loadAgent] 视空 model 为错误）。强制用户从
//	当前候选里挑一个 —— 候选会变，而模板生成那一刻的候选可能已经过期。
//
// 模板的字段必须跟得住加载器：少一个必填字段，用户建出来的文件就一定
// 保存失败，那是最差的「新建」体验。
func Template(catDir string, modelRefs []string) string {
	switch catDir {
	case DirPolicy:
		return "ask: []\ndeny: []\n"
	case DirLLM:
		return "type: openai\nbase_url: \napi_key: ${}\nmodels: []\n"
	case DirAgent:
		s := "---\nmodel: \ndescription: \n---\n"
		if len(modelRefs) > 0 {
			s += "# available models: " + strings.Join(modelRefs, ", ") + "\n"
		}
		return s + "\n"
	case DirTools:
		// ★ tools/ 下有**四种**文件形态，而分区模板只有一个键 —— 放不下
		// 四份内容。所以这里给的是**提示**（有哪些文件、每个文件装什么），
		// 真正的新建内容由按路径的模板给出（见 [ToolTemplates]）。
		//
		//	它只有注释、没有键 —— 所以存到任何名字下都会被加载器拒
		//	（文件名是白名单）。这是有意的：先挑一个工具，再填它的配置。
		return "# tools/ 下一个文件 = 一种工具：\n" +
			"#   http.yaml         request_get / request_post / request_put / request_delete\n" +
			"#   web_search.yaml   duckduckgo_search\n" +
			"#   wikipedia.yaml    wikipedia_search\n" +
			"#   kb.yaml           知识库\n" +
			"#\n" +
			"# ★ 文件的内容就是**那个工具自己的配置**（没有外层包装键）。\n" +
			"#   「文件在不在」就是开关 —— 关一个工具 = 删掉那个文件。\n" +
			"#\n" +
			"# ★ tools/ 进热更新：改完下一轮就生效，不用重启进程。\n"
	default:
		return ""
	}
}

// ToolTemplates 是 tools/ 下各文件的「新建」模板，键是**相对路径**。
//
// ★ 单独一个函数而不是 [Template] 的一个分支：tools/ 下有**四种**文件形态
//
//	（http / web_search / wikipedia / kb），而 [Template] 是**按分区**给的 ——
//	一个键放不下四份内容。形状与 [LimitsTemplate] 同源。
//
// ★ 模板里写**空值**而不是一个具体数字：HTTP 的方法、搜索的 provider、
//
//	维基的语言、知识库的语料目录 —— 那些该由用户自己填。
//	填一个具体值进去，工具一建出来就连到了用户没打算连的地方。
func ToolTemplates() map[string]string {
	return map[string]string{
		DirTools + "/http.yaml": "# HTTP 工具。写了这个文件就等于启用。\n" +
			"#\n" +
			"# ★ methods 默认只有 get —— 写方法（post/put/delete）必须显式点名，\n" +
			"#   否则「顺手加个 POST」就成了一条外发通道。\n" +
			"methods: [get]\n" +
			"headers: {}\n" +
			"timeout: 30s\n",
		DirTools + "/web_search.yaml": "# 网页搜索。写了这个文件就等于启用。\n" +
			"#\n" +
			"# ★ provider 只有两个：duckduckgo（零配置、零 key）与 searxng（自建）。\n" +
			"provider: duckduckgo\n" +
			"max_results: 10\n" +
			"timeout: 60s\n" +
			"region: \"\"\n",
		DirTools + "/wikipedia.yaml": "# 维基百科检索。写了这个文件就等于启用。\n" +
			"top_k: 3\n" +
			"language: zh\n" +
			"timeout: 15s\n" +
			"base_url: \"\"\n",
		DirTools + "/kb.yaml": "# 知识库。写了这个文件就等于启用。\n" +
			"#\n" +
			"# ★ corpus 是**语料目录**，相对项目根。空串 = 知识库关闭。\n" +
			"#   它与索引同生共死：换了目录不重建索引，就会自信地返回无关的答案。\n" +
			"corpus: \"\"\n" +
			"index: default\n" +
			"# model 必须指向 llm/*.yaml 里 kind: embedding 的模型；空 = 纯词法检索。\n" +
			"model: \"\"\n" +
			"query_instruction: \"\"\n" +
			"batch_size: 10\n" +
			"max_batch_chars: 1200\n" +
			"top_k: 8\n",
	}
}

// ★ 单独一个函数而不是 [Template] 的一个分支：policy/ 下有**两种**文件形态
//
//	（permissions.yaml 与 limits.yaml），而 [Template] 是**按分区**给的 ——
//	一个键放不下两份内容。所以编辑器的模板表里多一个**按路径**的键
//	（"policy/limits.yaml"），前端优先用它，回落到分区模板。
//
// ★ 模板里写 0 而不是写一个具体数字：0 = 不设限、沿用全局值 ——
//
//	用户填进去一个数就立刻收紧了，而那个数该由他自己定。
func LimitsTemplate() string {
	return "# 限制规则：数量上的上限。\n" +
		"#\n" +
		"# ★ 只能**收紧** —— 放宽上限请改 server/config.yaml（需重启）。\n" +
		"#   收紧立即生效：policy/ 进热更新。\n" +
		"# ★ 0 = 不设限，沿用 server/config.yaml 的全局值。\n" +
		"#\n" +
		"# agent.max_depth   嵌套深度上限（入口 agent 是第 0 层）\n" +
		"# retry.max_retries 模型调用重试次数上限\n" +
		"agent:\n" +
		"  max_depth: 0\n" +
		"retry:\n" +
		"  max_retries: 0\n"
}

// ModelRefs 返回全部已声明的 `<provider>/<model>` 引用，已排序。
func (c *Config) ModelRefs() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Models))
	for ref := range c.Models {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

// ReasoningLevels 给出每个 `<provider>/<model>` 声明的思考档位词汇表。
//
// ★ 存在的理由是**编辑器的本地校验**：写 `agents/<id>.md` 时它要当场说清
//
//	「`ultra` 不是这个模型的档位」，而不是等保存后由服务端 deferred 出来。
//	而它只需要 `llm/*.yaml` 里 models[].reasoning 的**键** ——
//	端点字段形状是各家的事，前端不该也不需要知道。
//
// ★ 没有声明词汇表的模型**不出现**在这个表里（不是出现空数组）。
//
//	「没声明」与「声明了但为空」在前端是两种处置：前者意味着这个模型
//	不支持思考，写 `reasoning:` 毫无意义。混成一个空数组就分不出来了。
//
// 返回的切片都不共享底层数组，调用方可以随意改。
func (c *Config) ReasoningLevels() map[string][]string {
	out := make(map[string][]string, len(c.Models))
	if c == nil {
		return out
	}
	for ref, m := range c.Models {
		if m == nil || len(m.Reasoning) == 0 {
			continue
		}
		levels := make([]string, 0, len(m.Reasoning))
		for level := range m.Reasoning {
			levels = append(levels, level)
		}
		sort.Strings(levels)
		out[ref] = levels
	}
	return out
}

// ── revision ──────────────────────────────────────────────────────────────

// Revision 返回一段内容的短哈希，用于写入的乐观并发。
//
// ★ 与 [Service.Fingerprint] 是两回事，不要混：
//
//	Fingerprint 是「名+大小+mtime」，用来察觉**变化**（热更新），每秒轮询，
//	  所以刻意避开读文件；Revision 是**内容**哈希，用来察觉「我手上这份
//	  是不是还是磁盘上那份」（写入路径，一次一次），所以必须读内容。
//
// 不存在的文件返回空串 —— 调用方把空串视为「新建」，不必特判文件是否存在。
func Revision(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:revisionLen]
}

// ── 读取 ──────────────────────────────────────────────────────────────────

// ReadFile 读某一个配置文件。
//
// 返回的 rev 是该内容的 [Revision]；文件不存在时 content 为 nil、rev 为空串。
// **不存在不是错误** —— 界面要能区分「还没有这个文件」与「读不出来」。
func (s *Service) ReadFile(rel string) (content []byte, rev string, err error) {
	if _, err := ValidatePath(rel); err != nil {
		return nil, "", err
	}
	if s.root == "" {
		return nil, "", writeErrf(ErrPathInvalid, rel, "no config root")
	}

	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return b, Revision(b), nil
}

// ── 写入 ──────────────────────────────────────────────────────────────────

// Write 落盘一次改动，且**只在试合并通过之后**。
//
// ★ 三步的顺序是这份 API 的全部意义：
//
//	① 试合并（[Service.loadWith]）—— 合并后不合法就不写，磁盘保持原样
//	② 落盘（[persist.AtomicWrite]）—— 读者永远看不到写了一半的内容
//	③ 重新 Fingerprint —— 返回落盘**之后**的指纹
//
// ★ ③ 不是多余的：试合并得到的 [Config.Version] 是写之前的指纹（见
//
//	[Fingerprint] 的说明），候选内容的大小知道、mtime 不知道。
//
// ★ baseRev 为空表示「不检查并发」。调用方（写接口）应当总是传它，
//
//	空值只留给内部与测试。
func (s *Service) Write(rel string, content []byte, baseRev string) (rev string, cfg *Config, err error) {
	if _, err := ValidatePath(rel); err != nil {
		return "", nil, err
	}
	if err := s.checkSecretFor(rel, content); err != nil {
		return "", nil, err
	}

	m := &mutation{Rel: rel, Content: content}

	if err := s.checkRevision(rel, baseRev); err != nil {
		return "", nil, err
	}
	trial, err := s.loadWith(m)
	if err != nil {
		return "", nil, &WriteError{Kind: ErrMergeRejected, Rel: rel, Message: err.Error(), Err: err}
	}

	if s.root == "" {
		return "", nil, writeErrf(ErrPathInvalid, rel, "no config root")
	}
	dst := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := persist.AtomicWrite(dst, content, writeTmpSuffix); err != nil {
		return "", nil, err
	}

	// ★ 返回落盘之后的指纹，不是 trial.Version —— 见函数注释第 ③ 步。
	return Revision(content), trial, nil
}

// File 是待写的一个文件：相对路径 + 内容。
//
// ★ rel 是**相对该级配置目录**的路径，形如 `policy/permissions.yaml`
//
//	（不是绝对路径，也不是带 `.openheron/` 前缀的）。
type File struct {
	Rel     string
	Content []byte
}

// WriteMany 落盘一批改动，且**只在整批试合并通过之后**。
//
// ★ 与 [Service.Write] 的差别只有一个：试合并带的是**整批**改动，
//
//	不是一次一个。互相依赖的文件（agent 的 model 引用 provider）
//	逐个写会永远过不了校验：先写 agent 则 provider 还没声明，
//	先写 provider 则 agent 还不存在 —— **没有顺序能过**。
//	见 docs/init-design.md §6。
//
// ★ 返回的 rev 是**落盘之后的配置指纹**（不是 [Service.Write] 那种单文件
//
//	内容哈希）—— 批量写没有「那一个文件」可哈希。
//
// ★ 落盘中途失败会**回滚**：新建的文件删掉，被覆盖的文件写回旧内容。
//
//	多文件写没有原子性，而「写了一半的配置」比「没写」更糟 ——
//	它看起来是完整的，却缺了一半。
func (s *Service) WriteMany(files []File, baseRev string) (rev string, cfg *Config, err error) {
	if len(files) == 0 {
		return "", nil, fmt.Errorf("config: no files to write")
	}
	ms := map[string]*mutation{}
	for _, f := range files {
		if _, err := ValidatePath(f.Rel); err != nil {
			return "", nil, err
		}
		if err := s.checkSecretFor(f.Rel, f.Content); err != nil {
			return "", nil, err
		}
		if err := s.checkRevision(f.Rel, baseRev); err != nil {
			return "", nil, err
		}
		ms[f.Rel] = &mutation{Rel: f.Rel, Content: f.Content}
	}

	trial, err := s.loadWithMany(ms)
	if err != nil {
		return "", nil, &WriteError{Kind: ErrMergeRejected, Rel: relsOf(files), Message: err.Error(), Err: err}
	}

	if s.root == "" {
		return "", nil, writeErrf(ErrPathInvalid, relsOf(files), "no config root")
	}
	root := s.root

	// ★ 先把旧内容读出来备着：回滚要靠它。
	backups := make([]savedFile, 0, len(files))
	for _, f := range files {
		dst := filepath.Join(root, filepath.FromSlash(f.Rel))
		b := savedFile{dst: dst}
		if old, err := os.ReadFile(dst); err == nil {
			b.existed = true
			b.content = old
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", nil, err
		}
		backups = append(backups, b)
	}

	for i, f := range files {
		dst := filepath.Join(root, filepath.FromSlash(f.Rel))
		if err := persist.AtomicWrite(dst, f.Content, writeTmpSuffix); err != nil {
			// ★ 回滚：**逆序**还原已落盘的那几个。
			for j := i - 1; j >= 0; j-- {
				restore(backups[j])
			}
			return "", nil, err
		}
	}
	return s.Fingerprint(), trial, nil
}

// savedFile 是写之前那个文件的样子（回滚用）。
type savedFile struct {
	dst     string
	existed bool
	content []byte
}

// restore 把一个文件还原成写之前的样子。
func restore(b savedFile) {
	if b.existed {
		_ = persist.AtomicWrite(b.dst, b.content, writeTmpSuffix)
		return
	}
	_ = os.Remove(b.dst)
}

// relsOf 把一批相对路径拼成一个可读的串（给错误信息用）。
func relsOf(files []File) string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Rel)
	}
	return strings.Join(out, ", ")
}

// Delete 删掉某一级的某一个配置文件，同样先试合并。
//
// ★ 试合并在这里**不是可选的**：删掉唯一声明某个 provider 的文件，
// 而有 agent 引用它的模型 —— 磁盘上一删就再也回不来了。
// 拦它的唯一机会就是落盘前那一次合并。
func (s *Service) Delete(rel string) (cfg *Config, err error) {
	if _, err := ValidatePath(rel); err != nil {
		return nil, err
	}
	m := &mutation{Rel: rel, Delete: true}

	trial, err := s.loadWith(m)
	if err != nil {
		return nil, &WriteError{Kind: ErrMergeRejected, Rel: rel, Message: err.Error(), Err: err}
	}

	if s.root == "" {
		return nil, writeErrf(ErrPathInvalid, rel, "no config root")
	}
	if err := os.Remove(filepath.Join(s.root, filepath.FromSlash(rel))); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &WriteError{
				Kind:    ErrPathInvalid,
				Rel:     rel,
				Message: "the file does not exist",
			}
		}
		return nil, err
	}
	return trial, nil
}

// Validate 试合并一段候选内容，**不落盘**。
//
// ★ 它与 [Service.Write] 的校验段是同一段代码（都走 [Service.loadWith]），
//
//	不是另写一份「预校验」。两份校验必然漂，而漂移的表现是
//	「界面说能存，保存时失败」—— 而那类不一致最难复现，因为
//	它在同一个界面里前后矛盾。
func (s *Service) Validate(rel string, content []byte) error {
	if _, err := ValidatePath(rel); err != nil {
		return err
	}
	if err := s.checkSecretFor(rel, content); err != nil {
		return err
	}
	m := &mutation{Rel: rel, Content: content}
	if _, err := s.loadWith(m); err != nil {
		return &WriteError{Kind: ErrMergeRejected, Rel: rel, Message: err.Error(), Err: err}
	}
	return nil
}

// ── 内部检查 ──────────────────────────────────────────────────────────────

// checkRevision 比对调用方手上的版本与磁盘上的版本。
func (s *Service) checkRevision(rel, baseRev string) error {
	if baseRev == "" {
		return nil
	}
	_, cur, err := s.ReadFile(rel)
	if err != nil {
		return err
	}
	if cur != baseRev {
		return &WriteError{
			Kind: ErrConflict,
			Rel:  rel,
			Message: fmt.Sprintf("the file changed on disk (you have %s, disk has %s); "+
				"reload it before saving, or your edit will be based on a stale version", baseRev, orNone(cur)),
		}
	}
	return nil
}

func orNone(rev string) string {
	if rev == "" {
		return "none (the file does not exist)"
	}
	return rev
}

// checkSecretFor 是 [Service.checkSecret] 的无分区版本，供 [Service.Validate]
// 用 —— 那条路径自己已经跑过 ValidatePath，不必再解析一次分区。
func (s *Service) checkSecretFor(rel string, content []byte) error {
	cat, ok := CategoryByDir(path.Dir(path.Clean(filepath.ToSlash(rel))))
	if !ok {
		return nil
	}
	return s.checkSecret(cat, rel, content)
}

// checkSecret 拒绝明文密钥。
//
// ★ 规则来自 config-design.md §3「密钥不入库，只存 ${ENV} / @file:」。
//
//	本函数只保护 `llm/*.yaml` 的 `api_key` 一个字段。写得窄是有意的：
//	禁得越宽，越容易在某次「顺手」里把正当内容也拦下来，而那种误报
//	会让人把整个校验关掉。
//
// 空值合法 —— 那就是「这个端点不要鉴权」（自建网关 / 本地 ollama）。
func (s *Service) checkSecret(cat Category, rel string, content []byte) error {
	if cat.Dir != DirLLM {
		return nil
	}
	for _, line := range strings.Split(string(content), "\n") {
		key, val, ok := splitYAMLLine(line)
		if !ok || key != "api_key" {
			continue
		}
		v := unquote(strings.TrimSpace(val))
		if v == "" {
			continue // 空 = 不鉴权，是合法的
		}
		if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
			continue
		}
		if strings.HasPrefix(v, "@file:") {
			continue
		}
		// ★ 到这里就是一个会被提交进版本库的明文凭证。
		return writeErrf(ErrSecretLiteral, rel,
			"api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path so the secret stays out of the repository")
	}
	return nil
}

// splitYAMLLine 拆出**顶层**的 `key: value`。
//
// ★ 手写而不引 yaml：这里要在**解析前**把明文拦下来，而 yaml.v3 一旦解析
//
//	就把整份文档解进了结构体，那时密钥早已进了内存。
//
//	逐行看够用，因为 [decl.Provider] 的字段是扁平的 —— 嵌套结构里不会出现
//	顶层 `api_key`。判据是**缩进为零**，所以 `extra:` 下面缩进着的
//	`api_key:` 是别人的字段名，不是凭据。
//
//	值要去掉引号：yaml.v3 的展开（[expandValue]）作用在解出来的字符串上，
//	所以用户写 `api_key: "@file:/k"` 是合法且必要的写法
//	（`@` 是 YAML 保留指示符，不加引号连解析都过不去）。
func splitYAMLLine(line string) (key, val string, ok bool) {
	if line != strings.TrimLeft(line, " \t") {
		return "", "", false // 缩进 ⇒ 不是顶层键
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	i := strings.Index(trimmed, ":")
	if i <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(trimmed[:i]), trimmed[i+1:], true
}

// unquote 去掉一对匹配的外层引号。
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
