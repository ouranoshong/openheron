package api

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

const (
	codeConfigPathInvalid   = "config_path_invalid"
	codeConfigSecretLiteral = "config_secret_literal"
	codeConfigWouldBreak    = "config_would_break_merge"
	codeConfigWriteConflict = "config_write_conflict"
)

type settingsCategory struct {
	ID       string `json:"id"`
	Editable bool   `json:"editable"`
	Hot      bool   `json:"hot"`
	Note     string `json:"note,omitempty"`
}

type settingsItem struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Hot      bool   `json:"hot"`
	Revision string `json:"revision,omitempty"`
}

type settingsManifest struct {
	Categories      []settingsCategory  `json:"categories"`
	Items           []settingsItem      `json:"items"`
	Templates       map[string]string   `json:"templates"`
	ModelRefs       []string            `json:"modelRefs"`
	ReasoningLevels map[string][]string `json:"reasoningLevels"`
	ConfigVersion   string              `json:"configVersion"`
}

type settingsItemBody struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Exists   bool   `json:"exists"`
	Revision string `json:"revision,omitempty"`
	Hot      bool   `json:"hot"`
}

type settingsWriteBody struct {
	Path         string `json:"path"`
	Content      string `json:"content"`
	BaseRevision string `json:"baseRevision"`
}

type settingsWriteResult struct {
	Path            string `json:"path"`
	Revision        string `json:"revision"`
	ConfigVersion   string `json:"configVersion"`
	Applied         bool   `json:"applied"`
	RequiresRestart bool   `json:"requiresRestart,omitempty"`
	Note            string `json:"note,omitempty"`
}

type settingsValidateBody struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type settingsValidateResult struct {
	OK            bool     `json:"ok"`
	Errors        []string `json:"errors,omitempty"`
	ConfigVersion string   `json:"configVersion"`
}

// SetConfigService wires /v2/settings.
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
			v2Fail(c, consts.StatusUnprocessableEntity, codeConfigWouldBreak, msg, false)
		case spec.ErrConflict:
			v2Fail(c, consts.StatusConflict, codeConfigWriteConflict, msg, false)
		default:
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
		manifest.Templates[cat.Dir] = spec.Skeleton(cat.Dir, refs)
		// ★ The per-file templates: the file's own skeleton wins over the
		//   section's, because a section skeleton may be nothing but
		//   comments and a file built from it has no YAML document at all.
		for rel, tpl := range spec.Templates(cat.Dir) {
			manifest.Templates[rel] = tpl
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

	res := settingsValidateResult{OK: true}
	if err := svc.Validate(rel, []byte(body.Content)); err != nil {
		res.OK = false
		res.Errors = []string{err.Error()}
	}
	res.ConfigVersion = svc.Fingerprint()
	c.JSON(consts.StatusOK, res)
}

func (s *Server) pathArg(c *app.RequestContext, rel string) (string, bool) {
	if _, err := spec.ValidatePath(rel); err != nil {
		settingsFail(c, err)
		return "", false
	}
	return rel, true
}

func (s *Server) settingsTarget(c *app.RequestContext) (string, bool) {
	return s.pathArg(c, c.Query("path"))
}

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

func (s *Server) settingsApplied(c *app.RequestContext, svc *spec.Service,
	rel, rev string) {

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

func (s *Server) watchEnabled() bool {
	cfg := s.liveConfig()
	if cfg == nil {
		return false
	}
	return cfg.WatchEnabled()
}

func (s *Server) liveConfig() *spec.Config {
	if s.cfgFn == nil {
		return nil
	}
	return s.cfgFn()
}
