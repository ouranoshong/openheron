package api

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"openheron/agentkit/spec"
)

// ConfigSource supplies the effective configuration to the introspection
// endpoints.
//
// ★ A function rather than a *spec.Config so the endpoint always reads the
//
//	live value: openheron hot-reloads llm/, agents/ and policy/, and a captured
//	pointer would serve stale agent topology after every reload.
type ConfigSource func() *spec.Config

// SetConfigSource wires /v2/config, /v2/agents, /v2/health and /v2/runtime.
// Without it those endpoints answer 503 — they are introspective, and openheron
// still serves the runtime endpoints fine without a config source.
func (s *Server) SetConfigSource(fn ConfigSource) { s.cfgFn = fn }

// registerV2Routes mounts the /v2 family.
//
// ★ Order matters: every /v2 path is registered before StaticFS, which is
//
//	mounted last as a catch-all. A single misplaced route silently becomes a
//	404 that looks exactly like "endpoint not implemented".
func (s *Server) registerV2Routes() {
	h := s.h

	h.POST("/v2/sessions", s.v2CreateSession)
	h.GET("/v2/sessions", s.v2ListSessions)
	h.GET("/v2/sessions/:id", s.v2GetSession)
	h.PATCH("/v2/sessions/:id", s.v2RenameSession)
	h.PATCH("/v2/sessions/:id/agent", s.v2SwitchAgent)
	h.DELETE("/v2/sessions/:id", s.v2DeleteSession)

	h.GET("/v2/sessions/:id/messages", s.v2Messages)
	h.POST("/v2/sessions/:id/abort", s.v2Abort)

	h.POST("/v2/sessions/:id/files", s.v2UploadFiles)
	h.GET("/v2/sessions/:id/files", s.v2ListFiles)

	// ★ Must be registered before the static catch-all, like every other
	//   /v2 route. project/files feeds the composer picker; it is read-only.
	h.GET("/v2/project/files", s.v2ProjectFiles)
	h.GET("/v2/commands", s.v2Commands)

	h.POST("/v2/run", s.v2Run)

	h.GET("/v2/skills", s.v2Skills)
	h.GET("/v2/agents", s.v2Agents)
	h.GET("/v2/agents/:agentId", s.v2AgentDetail)
	h.GET("/v2/config", s.v2Config)
	h.GET("/v2/health", s.v2Health)
	h.GET("/v2/runtime", s.v2Runtime)

	// ★ /v2/settings 必须在静态回落**之前**注册：SPA 兜底会吃掉任何没
	//   匹配到路由的 GET，而那条 404 长得和「没实现」一模一样。
	h.GET("/v2/settings", s.v2SettingsManifest)
	h.GET("/v2/settings/item", s.v2SettingsItem)
	h.POST("/v2/settings/item", s.v2SettingsSave)
	h.PUT("/v2/settings/item", s.v2SettingsSave)
	h.DELETE("/v2/settings/item", s.v2SettingsDelete)
	h.POST("/v2/settings/validate", s.v2SettingsValidate)
}

// ── POST /v2/sessions/{id}/files ─────────────────────────────────────────

type uploadedFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type uploadResult struct {
	Files         []uploadedFile `json:"files"`
	WorkspacePath string         `json:"workspacePath"`
}

// maxUploadBytes bounds a single file. v1 took whatever the client sent.
const maxUploadBytes = 32 << 20 // 32 MiB

func (s *Server) v2UploadFiles(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}

	// Multipart read: v1 only ever looked at files[0], silently discarding
	// every other file the client attached.
	form, err := c.MultipartForm()
	if err != nil || form == nil {
		v2Fail(c, consts.StatusBadRequest, codeFileRequired, "file is required", false)
		return
	}
	headers := form.File["files"]
	if len(headers) == 0 {
		headers = form.File["file"] // tolerate the singular name
	}
	if len(headers) == 0 {
		v2Fail(c, consts.StatusBadRequest, codeFileRequired, "file is required", false)
		return
	}

	out := uploadResult{
		Files:         make([]uploadedFile, 0, len(headers)),
		WorkspacePath: sess.Workspace(),
	}
	for _, fh := range headers {
		if fh.Size > maxUploadBytes {
			v2Fail(c, consts.StatusRequestEntityTooLarge, codeFileTooLarge,
				"file exceeds the 32 MiB limit", false)
			return
		}
		f, err := fh.Open()
		if err != nil {
			v2Fail(c, consts.StatusBadRequest, codeFileRequired, "cannot read upload", false)
			return
		}
		path, putErr := sess.PutFile(fh.Filename, f)
		_ = f.Close()
		if putErr != nil {
			v2Fail(c, consts.StatusBadRequest, codeInvalidFileName, putErr.Error(), false)
			return
		}
		out.Files = append(out.Files, uploadedFile{Name: fh.Filename, Path: path, Size: fh.Size})
	}
	c.JSON(consts.StatusCreated, out)
}

// ── GET /v2/sessions/{id}/files ──────────────────────────────────────────

func (s *Server) v2ListFiles(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	entries, err := listWorkspace(sess.Workspace())
	if err != nil {
		v2FailInternal(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{
		"workspacePath": sess.Workspace(),
		"files":         entries,
	})
}

// workspaceEntry is one uploaded file.
type workspaceEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

// listWorkspace walks the session workspace. The path comes from the session,
// which already confines uploads, so this only reports what is there.
func listWorkspace(root string) ([]workspaceEntry, error) {
	if root == "" {
		return []workspaceEntry{}, nil
	}
	var out []workspaceEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		out = append(out, workspaceEntry{
			Name:       d.Name(),
			Path:       path,
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(rfc3339Nano),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []workspaceEntry{}
	}
	_ = time.Now
	return out, nil
}
