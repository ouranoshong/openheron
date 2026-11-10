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

// SetConfigService wires /v2/settings.
// Optional: without it the endpoints answer 503. Config is still fully
// readable through /v2/config, so a caller that only wants to inspect does
// not have to wire this.
func (s *Server) SetConfigService(svc *spec.Service) { s.cfgSvc = svc }

// requireSettings returns the wired Service or writes 503.
func (s *Server) requireSettings(c *app.RequestContext) *spec.Service {
	if s.cfgSvc == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, "config_unavailable",
			"config writing is not wired on this server", false)
		return nil
	}
	return s.cfgSvc
}

// settingsFail maps a config write failure onto HTTP.
func (s *Server) settingsFail(c *app.RequestContext, err error) {
	var we *spec.WriteError
	if errors.As(err, &we) {
		msg := we.Message
		if we.Rel != "" {
			msg = we.Rel + ": " + msg
		}
		switch we.Kind {
		case spec.ErrPathInvalid:
			s.v2Fail(c, consts.StatusBadRequest, codeConfigPathInvalid, msg, false)
		case spec.ErrSecretLiteral:
			s.v2Fail(c, consts.StatusBadRequest, codeConfigSecretLiteral, msg, false)
		case spec.ErrMergeRejected:
			s.v2Fail(c, consts.StatusUnprocessableEntity, codeConfigWouldBreak, msg, false)
		case spec.ErrConflict:
			s.v2Fail(c, consts.StatusConflict, codeConfigWriteConflict, msg, false)
		default:
			s.v2FailInternal(c, errors.New(msg))
		}
		return
	}
	s.v2FailInternal(c, err)
}

// ── GET /v2/settings ──────────────────────────────────────────────────────

func (s *Server) v2SettingsManifest(_ context.Context, c *app.RequestContext) {
	svc := s.requireSettings(c)
	if svc == nil {
		return
	}
	// ★ The manifest is assembled in spec, not here: `openheron init` and
	//   the config_* tools ask the same question, and three assemblies of
	//   "which files are there" is how the CLI and the editor drift.
	m, err := svc.Manifest(s.liveConfig())
	if err != nil {
		s.settingsFail(c, err)
		return
	}
	c.JSON(consts.StatusOK, m)
}

// ── GET /v2/settings/item ─────────────────────────────────────────────────

func (s *Server) v2SettingsItem(_ context.Context, c *app.RequestContext) {
	svc := s.requireSettings(c)
	if svc == nil {
		return
	}
	rel := c.Query("path")
	cat, ok := s.readPath(c, rel)
	if !ok {
		return
	}
	body, rev, err := svc.ReadFile(rel)
	if err != nil {
		s.settingsFail(c, err)
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
		s.settingsFail(c, err)
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
	rel := c.Query("path")
	if _, ok := s.writePath(c, rel); !ok {
		return
	}

	if _, err := svc.Delete(rel); err != nil {
		s.settingsFail(c, err)
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
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	rel := body.Path
	if _, ok := s.readPath(c, rel); !ok {
		return
	}

	d, err := svc.Diagnostics(rel, []byte(body.Content))
	if err != nil {
		s.settingsFail(c, err)
		return
	}
	c.JSON(consts.StatusOK, d)
}

// readPath is the gate for a request that only reads.
//
// ★ Two gates, not one: reading a file the editor may not write is exactly
// what a reader needs most (see `spec.ValidateReadPath`), and conflating them
// made `server/config.yaml` unreadable while the manifest listed it.
func (s *Server) readPath(c *app.RequestContext, rel string) (spec.Category, bool) {
	cat, err := spec.ValidateReadPath(rel)
	if err != nil {
		s.settingsFail(c, err)
		return spec.Category{}, false
	}
	return cat, true
}

// writePath is the gate for a request that writes.
func (s *Server) writePath(c *app.RequestContext, rel string) (spec.Category, bool) {
	cat, err := spec.ValidatePath(rel)
	if err != nil {
		s.settingsFail(c, err)
		return spec.Category{}, false
	}
	return cat, true
}

func (s *Server) settingsWriteArgs(c *app.RequestContext) (string, string, []byte, bool) {
	var body settingsWriteBody
	if err := c.BindAndValidate(&body); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return "", "", nil, false
	}
	if _, ok := s.writePath(c, body.Path); !ok {
		return "", "", nil, false
	}
	return body.Path, body.BaseRevision, []byte(body.Content), true
}

func (s *Server) settingsApplied(c *app.RequestContext, svc *spec.Service,
	rel, rev string) {

	version := svc.Fingerprint()
	hot := false
	if cat, err := spec.ValidateReadPath(rel); err == nil {
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
