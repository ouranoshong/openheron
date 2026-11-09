package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/message"
)

// ConfigSource supplies the effective configuration to the introspection
// endpoints.
//
//	live value: openheron hot-reloads llm/, agents/, tools/ and mcps/, and a
//	captured pointer would serve stale agent topology after every reload.
type ConfigSource func() *spec.Config

// SetConfigSource wires /v2/config, /v2/agents, /v2/health and /v2/runtime.
// Without it those endpoints answer 503 — they are introspective, and openheron
// still serves the runtime endpoints fine without a config source.
func (s *Server) SetConfigSource(fn ConfigSource) { s.cfgFn = fn }

// registerV2Routes mounts the /v2 family.
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
	h.POST("/v2/sessions/:id/compact", s.v2Compact)

	// ★ The run ledger's scan. It is a read of durable facts, and it is
	//   the only way a client learns what a session is doing — the stream
	//   is a hint, this is the truth.
	h.GET("/v2/sessions/:id/runs", s.v2SessionRuns)
	h.GET("/v2/sessions/:id/runs/:runId", s.v2SessionRun)

	h.POST("/v2/sessions/:id/files", s.v2UploadFiles)
	h.GET("/v2/sessions/:id/files", s.v2ListFiles)
	h.DELETE("/v2/sessions/:id/files", s.v2DeleteFile)

	//   /v2 route. project/files feeds the composer picker; it is read-only.
	h.GET("/v2/project/files", s.v2ProjectFiles)
	h.GET("/v2/commands", s.v2Commands)

	h.POST("/v2/run", s.v2Run)

	h.GET("/v2/skills", s.v2Skills)
	h.GET("/v2/agents", s.v2Agents)
	h.GET("/v2/agents/:agentId", s.v2AgentDetail)
	h.GET("/v2/mcps", s.v2MCPServers)
	h.GET("/v2/config", s.v2Config)
	h.GET("/v2/health", s.v2Health)
	h.GET("/v2/runtime", s.v2Runtime)

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
	// Path is **relative to the session's workspace**.
	//
	// ★ An absolute path is a fact about one machine, and the client has to
	//   send this path back with the next question — so it has to be the
	//   name the server itself understands, not the name of a directory on
	//   somebody's disk.
	Path string `json:"path"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
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
		s.v2FailErr(c, err)
		return
	}

	// Multipart read: v1 only ever looked at files[0], silently discarding
	// every other file the client attached.
	form, err := c.MultipartForm()
	if err != nil || form == nil {
		s.v2Fail(c, consts.StatusBadRequest, codeFileRequired, "file is required", false)
		return
	}
	headers := form.File["files"]
	if len(headers) == 0 {
		headers = form.File["file"] // tolerate the singular name
	}
	if len(headers) == 0 {
		s.v2Fail(c, consts.StatusBadRequest, codeFileRequired, "file is required", false)
		return
	}

	out := uploadResult{
		Files:         make([]uploadedFile, 0, len(headers)),
		WorkspacePath: sess.Workspace(),
	}
	for _, fh := range headers {
		if fh.Size > maxUploadBytes {
			s.v2Fail(c, consts.StatusRequestEntityTooLarge, codeFileTooLarge,
				"file exceeds the 32 MiB limit", false)
			return
		}
		f, err := fh.Open()
		if err != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeFileRequired, "cannot read upload", false)
			return
		}
		path, putErr := sess.PutFile(fh.Filename, f)
		_ = f.Close()
		if putErr != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidFileName, putErr.Error(), false)
			return
		}
		rel, relErr := workspaceRel(sess.Workspace(), path)
		if relErr != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidFileName, relErr.Error(), false)
			return
		}
		out.Files = append(out.Files, uploadedFile{
			Name: fh.Filename,
			Path: rel,
			Size: fh.Size,
			Mime: message.MIMEOfExt(rel),
		})
	}
	c.JSON(consts.StatusCreated, out)
}

// workspaceRel turns a path inside the workspace into a name relative to it.
//
// ★ The workspace root is the session's own directory, so the relative name
//
//	is what the client may send back — and what the server resolves when
//	the question arrives.
func workspaceRel(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve workspace file %q: %w", path, err)
	}
	if rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("workspace file %q is outside the workspace", path)
	}
	return filepath.ToSlash(rel), nil
}

// ── GET /v2/sessions/{id}/files ──────────────────────────────────────────

func (s *Server) v2ListFiles(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}
	entries, err := listWorkspace(sess.Workspace())
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{
		"workspacePath": sess.Workspace(),
		"files":         entries,
	})
}

// workspaceEntry is one uploaded file.
type workspaceEntry struct {
	Name string `json:"name"`
	// Path is relative to the session's workspace — the same name the
	// client sends back when the file becomes an attachment.
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
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
		rel, relErr := workspaceRel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, workspaceEntry{
			Name:       d.Name(),
			Path:       rel,
			Size:       info.Size(),
			Mime:       message.MIMEOfExt(rel),
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

// ── DELETE /v2/sessions/{id}/files ───────────────────────────────────────

// v2DeleteFile removes one file from the session's workspace.
//
// ★ The granularity is the **file**. The attachment list of a message is
//
//	history — it says "this question came with that file" — and history is
//	not rewritten when a file is deleted. A file may be referenced by
//	several messages, and there is no reference count to keep in step.
//
// ★ The name is relative to the workspace, and a name that escapes it is
//
//	refused: the same rule that keeps a read inside the session keeps a
//	delete inside it.
func (s *Server) v2DeleteFile(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}

	name := c.Query("path")
	if strings.TrimSpace(name) == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidFileName,
			"path is required; it is the file's name relative to the session workspace", false)
		return
	}
	if err := sess.DeleteWorkspaceFile(name); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidFileName, err.Error(), false)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{
		"deleted": true,
		"path":    name,
	})
}
