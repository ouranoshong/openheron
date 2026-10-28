package api

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"openheron/agentkit/spec"
)

// `/v2/settings` 是配置的**增删改查**，与只读的 `/v2/config` 分开：
// 前者回答「改掉它」，后者回答「现在是什么」。合成一处会让「看」也带上
// 写入口的副作用，而两者该有的严格程度完全不同。
//
// ★ 整层的编辑单元是「某一级的一个文件」，不是合并后的配置。理由见
//
//	config-editor-design.md §3 —— 合并视图写回会同时踩密钥泄漏、
//	提权入库、默认值物化、用户级偏好被搬进仓库四个坑。

// 写入类错误码。与 [spec.WriteErrorKind] 一一对应。
const (
	codeConfigPathInvalid   = "config_path_invalid"
	codeConfigSecretLiteral = "config_secret_literal"
	codeConfigWouldBreak    = "config_would_break_merge"
	codeConfigWriteConflict = "config_write_conflict"
)

// ── 视图 ──────────────────────────────────────────────────────────────────

type settingsCategory struct {
	ID       string `json:"id"`
	Editable bool   `json:"editable"`
	// Hot 报告保存后是否热生效。为 false 时界面必须显式提示需重启。
	Hot bool `json:"hot"`
	// Note 面向用户的一句话说明，只在 !Editable 时有意义。
	Note string `json:"note,omitempty"`
}

// settingsItem 是「某一个文件」。
type settingsItem struct {
	Path string `json:"path"`
	// Exists 报告这个文件是否真的存在。为 false 时它是「可以新建」而不是
	// 「读不出来」—— 界面要能区分这两件事。
	Exists bool `json:"exists"`
	// Hot 逐文件给出（同分类下取值相同，但逐项返回省得界面自己推）。
	Hot bool `json:"hot"`
	// Revision 用于写入的乐观并发；文件不存在时为空串。
	Revision string `json:"revision,omitempty"`
}

type settingsManifest struct {
	Categories []settingsCategory `json:"categories"`
	Items      []settingsItem     `json:"items"`
	// Templates 供「新建」用；键是分区 id。
	Templates map[string]string `json:"templates"`
	// ModelRefs 供 agents 模板与模型下拉用。
	ModelRefs []string `json:"modelRefs"`
	// ReasoningLevels 是每个 modelRef 声明的思考档位词汇表。
	//
	// ★ 编辑器据此在**本地**就能判「这个档位不存在」，而不是给一句
	//   「服务端会校验」——后者对着一份完全合法的配置也会报，
	//   而 Issue 曾无严重级别、save() 见 issue 就 return。
	//
	// 没有声明词汇表的模型不出现在这里（不是空数组，见 [spec.Config.ReasoningLevels]）。
	ReasoningLevels map[string][]string `json:"reasoningLevels"`
	// ConfigVersion 是当前配置的指纹，界面据此判断是否需要刷新。
	ConfigVersion string `json:"configVersion"`
}

type settingsItemBody struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Exists   bool   `json:"exists"`
	Revision string `json:"revision,omitempty"`
	Hot      bool   `json:"hot"`
}

type settingsWriteBody struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// BaseRevision 是乐观并发键。空串表示「文件当前不存在」，即新建。
	//
	// ★ 它**必填**（新建时显式传空串），因为「忘了检查」与「真的没有
	//	冲突」在界面上长得一模一样，而后者会让用户手改的版本被静默覆盖。
	BaseRevision string `json:"baseRevision"`
}

type settingsWriteResult struct {
	Path          string `json:"path"`
	Revision      string `json:"revision"`
	ConfigVersion string `json:"configVersion"`
	// Applied 报告保存后是否热生效。
	//
	// ★ 本端点**只做校验与落盘，不应用** —— 应用一律由 watcher 负责。
	//	若这里也应用一次，watcher 随后再应用，池会换代两次、
	//	无谓驱逐在飞 turn 的条目。
	Applied bool `json:"applied"`
	// RequiresRestart 在 watch 关闭时为真：写成功了，但要重启才生效。
	RequiresRestart bool `json:"requiresRestart,omitempty"`
	// Note 是给界面的一句话，配合 Applied=false 使用。
	Note string `json:"note,omitempty"`
}

type settingsValidateBody struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type settingsValidateResult struct {
	OK bool `json:"ok"`
	// Errors 每条都带文件与行号（单文件错误）或文件路径（跨文件错误）。
	Errors        []string `json:"errors,omitempty"`
	ConfigVersion string   `json:"configVersion"`
}

// ── 接线 ──────────────────────────────────────────────────────────────────

// SetConfigService wires /v2/settings.
//
// Optional: without it the endpoints answer 503. Config is still fully
// readable through /v2/config, so a caller that only wants to inspect does
// not have to wire this.
func (s *Server) SetConfigService(svc *spec.Service) { s.cfgSvc = svc }

// requireSettings returns the wired Service or writes 503.
func (s *Server) requireSettings(c *app.RequestContext) *spec.Service {
	if s.cfgSvc == nil {
		v2Fail(c, consts.StatusServiceUnavailable, "config_unavailable",
			"config writing is not wired on this server", false)
		return nil
	}
	return s.cfgSvc
}

// settingsFail maps a config write failure onto HTTP.
//
// ★ 用 errors.As + Kind 判定，**不匹配错误文本** —— 文案一改就失效，
//
//	而这类映射错了的表现是「错误码对不上，前端走错分支」。
func settingsFail(c *app.RequestContext, err error) {
	var we *spec.WriteError
	if errors.As(err, &we) {
		msg := we.Message
		if we.Rel != "" {
			msg = we.Rel + ": " + msg
		}
		switch we.Kind {
		case spec.ErrPathInvalid:
			v2Fail(c, consts.StatusBadRequest, codeConfigPathInvalid, msg, false)
		case spec.ErrSecretLiteral:
			v2Fail(c, consts.StatusBadRequest, codeConfigSecretLiteral, msg, false)
		case spec.ErrMergeRejected:
			// ★ 422 而不是 400：单文件语法没错，是**合并之后**不成立。
			//   前者的处置是「改这个文件」，后者是「这个改动会破坏别的」。
			v2Fail(c, consts.StatusUnprocessableEntity, codeConfigWouldBreak, msg, false)
		case spec.ErrConflict:
			v2Fail(c, consts.StatusConflict, codeConfigWriteConflict, msg, false)
		default:
			// ★ 未归类的 WriteError 仍带 we.Message —— 它是写失败的原因，
			//   只是还不知道该归到哪个 4xx/422。先说清楚，再谈分类。
			v2FailInternal(c, errors.New(msg))
		}
		return
	}
	v2FailInternal(c, err)
}

// ── GET /v2/settings ──────────────────────────────────────────────────────

func (s *Server) v2SettingsManifest(_ context.Context, c *app.RequestContext) {
	svc := s.requireSettings(c)
	if svc == nil {
		return
	}
	live := s.liveConfig()
	refs := live.ModelRefs()

	manifest := settingsManifest{
		Categories:      make([]settingsCategory, 0, len(spec.Categories)),
		Items:           []settingsItem{},
		Templates:       map[string]string{},
		ModelRefs:       refs,
		ReasoningLevels: live.ReasoningLevels(),
	}
	for _, cat := range spec.Categories {
		view := settingsCategory{ID: cat.Dir, Editable: cat.Editable, Hot: cat.Hot()}
		if !cat.Editable {
			view.Note = "not hot-reloaded; changes apply on restart"
		}
		manifest.Categories = append(manifest.Categories, view)
		manifest.Templates[cat.Dir] = spec.Template(cat.Dir, refs)
		// ★ policy/ 与 tools/ 下都有**多种**文件形态，而 [spec.Template] 是
		//	**按分区**给的 ——
		//
		//	一个键放不下多份内容。所以模板表里多几个**按路径**的键，
		//	前端优先用它（见 web/src/components/settings/new-file-dialog.tsx），
		//	回落到分区模板。服务端仍然是模板的唯一真相源。
		if cat.Dir == spec.DirPolicy {
			manifest.Templates[spec.DirPolicy+"/limits.yaml"] = spec.LimitsTemplate()
		}
		if cat.Dir == spec.DirTools {
			for rel, tpl := range spec.ToolTemplates() {
				manifest.Templates[rel] = tpl
			}
		}

		names, err := svc.ListFiles(cat)
		if err != nil {
			settingsFail(c, err)
			return
		}
		for _, name := range names {
			rel := cat.Dir + "/" + name
			item := settingsItem{Path: rel, Exists: true, Hot: cat.Hot()}
			if _, rev, err := svc.ReadFile(rel); err == nil {
				item.Revision = rev
			}
			manifest.Items = append(manifest.Items, item)
		}
	}
	manifest.ConfigVersion = svc.Fingerprint()
	c.JSON(consts.StatusOK, manifest)
}

// ── GET /v2/settings/item ─────────────────────────────────────────────────

func (s *Server) v2SettingsItem(_ context.Context, c *app.RequestContext) {
	svc := s.requireSettings(c)
	if svc == nil {
		return
	}
	rel, ok := s.settingsTarget(c)
	if !ok {
		return
	}

	cat, err := spec.ValidatePath(rel)
	if err != nil {
		settingsFail(c, err)
		return
	}
	// ★ 一次读就够：ReadFile 用「空 revision」表示文件不存在，
	//   而界面要的正是 exists 布尔，不需要再探一次盘。
	body, rev, err := svc.ReadFile(rel)
	if err != nil {
		settingsFail(c, err)
		return
	}

	out := settingsItemBody{
		Path: rel, Content: string(body),
		Exists: rev != "", Revision: rev, Hot: cat.Hot(),
	}
	c.JSON(consts.StatusOK, out)
}

// ── POST|PUT /v2/settings/item ────────────────────────────────────────────

func (s *Server) v2SettingsSave(_ context.Context, c *app.RequestContext) {
	svc := s.requireSettings(c)
	if svc == nil {
		return
	}
	rel, baseRev, content, ok := s.settingsWriteArgs(c)
	if !ok {
		return
	}

	rev, _, err := svc.Write(rel, content, baseRev)
	if err != nil {
		settingsFail(c, err)
		return
	}
	s.settingsApplied(c, svc, rel, rev)
}

// ── DELETE /v2/settings/item ──────────────────────────────────────────────

func (s *Server) v2SettingsDelete(_ context.Context, c *app.RequestContext) {
	svc := s.requireSettings(c)
	if svc == nil {
		return
	}
	rel, ok := s.settingsTarget(c)
	if !ok {
		return
	}

	if _, err := svc.Delete(rel); err != nil {
		settingsFail(c, err)
		return
	}
	// 删除之后不再有该文件 → 它的 revision 归零（=「不存在」）。
	s.settingsApplied(c, svc, rel, "")
}

// ── POST /v2/settings/validate ────────────────────────────────────────────

func (s *Server) v2SettingsValidate(_ context.Context, c *app.RequestContext) {
	svc := s.requireSettings(c)
	if svc == nil {
		return
	}
	var body settingsValidateBody
	if err := c.BindAndValidate(&body); err != nil {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	rel, ok := s.pathArg(c, body.Path)
	if !ok {
		return
	}

	// ★ 复用与写入**完全相同**的校验路径（[spec.Service.Validate]），
	//   自己另写一份「预校验」就是接口开始说谎的第一天。
	res := settingsValidateResult{OK: true}
	if err := svc.Validate(rel, []byte(body.Content)); err != nil {
		res.OK = false
		res.Errors = []string{err.Error()}
	}
	res.ConfigVersion = svc.Fingerprint()
	c.JSON(consts.StatusOK, res)
}

// ── 辅助 ──────────────────────────────────────────────────────────────────

// pathArg 解析并校验 path 参数。
func (s *Server) pathArg(c *app.RequestContext, rel string) (string, bool) {
	if _, err := spec.ValidatePath(rel); err != nil {
		settingsFail(c, err)
		return "", false
	}
	return rel, true
}

// settingsTarget 从 query 读 path。
func (s *Server) settingsTarget(c *app.RequestContext) (string, bool) {
	return s.pathArg(c, c.Query("path"))
}

// settingsWriteArgs 解析写请求体。
func (s *Server) settingsWriteArgs(c *app.RequestContext) (string, string, []byte, bool) {
	var body settingsWriteBody
	if err := c.BindAndValidate(&body); err != nil {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return "", "", nil, false
	}
	rel, ok := s.pathArg(c, body.Path)
	if !ok {
		return "", "", nil, false
	}
	return rel, body.BaseRevision, []byte(body.Content), true
}

// settingsApplied 组装写成功的响应。
func (s *Server) settingsApplied(c *app.RequestContext, svc *spec.Service,
	rel, rev string) {

	// ★ ConfigVersion 是**落盘之后**重新算的，不是试合并带回来的那个 ——
	//   后者是写之前的指纹（候选内容的大小知道、mtime 不知道）。
	version := svc.Fingerprint()
	hot := false
	if cat, err := spec.ValidatePath(rel); err == nil {
		hot = cat.Hot()
	}
	applied := hot && s.watchEnabled()

	out := settingsWriteResult{
		Path: rel, Revision: rev,
		ConfigVersion: version, Applied: applied,
	}
	if !applied {
		out.RequiresRestart = true
		if !hot {
			out.Note = "this section is not hot-reloaded; restart openheron to apply"
		} else {
			out.Note = "hot reload is disabled; restart openheron to apply"
		}
	}
	c.JSON(consts.StatusOK, out)
}

// watchEnabled 读当前配置的 watch.enabled。
//
// ★ 不从启动时的配置读 —— 热更新会改它，读旧值会让界面说「已生效」
//
//	而实际没生效。
func (s *Server) watchEnabled() bool {
	cfg := s.liveConfig()
	if cfg == nil {
		return false
	}
	return cfg.WatchEnabled()
}

// liveConfig 返回当前配置，没有接线时为 nil。
func (s *Server) liveConfig() *spec.Config {
	if s.cfgFn == nil {
		return nil
	}
	return s.cfgFn()
}
