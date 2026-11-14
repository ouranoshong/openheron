package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/imaging"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
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

	// ★ The session's announcement channel: the facts that outlive a run —
	//   an approval was answered, a new round began. ★ It is what keeps a
	//   second surface from sitting on a question the server has already
	//   answered.
	h.GET("/v2/sessions/:id/events", s.v2SessionEvents)

	h.POST("/v2/sessions/:id/files", s.v2UploadFiles)
	h.GET("/v2/sessions/:id/files", s.v2ListFiles)
	// ★ The bytes of one file, for the preview. It never interprets the
	//   content — see [v2RawFile].
	h.GET("/v2/sessions/:id/files/raw", s.v2RawFile)
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
		// ★ The bytes are read **once**, into memory: the file is about to
		//   be compressed, and the size on disk afterwards is not the size
		//   the client sent — so the client's number cannot be the truth.
		data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
		_ = f.Close()
		if err != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeFileRequired, "cannot read upload", false)
			return
		}
		if int64(len(data)) > maxUploadBytes {
			s.v2Fail(c, consts.StatusRequestEntityTooLarge, codeFileTooLarge,
				"file exceeds the 32 MiB limit", false)
			return
		}

		// ★ A picture is compressed here, on the server, and the format is
		//   kept: a PNG stays a PNG, transparent pixels and all. Everything
		//   else comes through untouched.
		payload, _, err := imaging.Compress(fh.Filename, data)
		if err != nil {
			s.v2FailInternal(c, err)
			return
		}

		// ★ The stored name carries a hash of the name, the size and the
		//   time, so two uploads of the same name do not overwrite each
		//   other — an overwrite would make the transcript's attachment
		//   list lie about what the first message carried.
		//
		//   The time is **pinned** onto the file afterwards, so the hash
		//   can be recomputed from the file on disk: read it, stat it,
		//   hash the three, and the name checks out.
		storedAt := time.Now()
		size := int64(len(payload))
		stored := imaging.HashedName(fh.Filename, size, storedAt)

		path, putErr := sess.PutFile(stored, bytes.NewReader(payload))
		if putErr != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidFileName, putErr.Error(), false)
			return
		}
		if err := os.Chtimes(path, storedAt, storedAt); err != nil {
			// ★ A file whose timestamp could not be pinned still works —
			//   it just cannot be checked against its name. That is worth
			//   a log line, not a failed upload.
			s.logger().Warn("could not pin the modification time of an uploaded file",
				"path", path, "err", err)
		}

		rel, relErr := workspaceRel(sess.Workspace(), path)
		if relErr != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidFileName, relErr.Error(), false)
			return
		}

		// ★ The name the user recognises is kept beside the session, not
		//   in the workspace: the workspace is the directory the model
		//   reads, and a map from hashed names means nothing to it.
		display := imaging.DisplayName(fh.Filename)
		if err := sess.RecordUpload(rel, display); err != nil {
			s.logger().Warn("could not remember the original name of an uploaded file",
				"path", rel, "err", err)
		}

		out.Files = append(out.Files, uploadedFile{
			Name: display,
			Path: rel,
			Size: size,
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

// ── GET /v2/sessions/{id}/files/raw ───────────────────────────────────────

// v2RawFile serves the bytes of one workspace file.
//
// ★ The endpoint's promise is narrow, and it is what makes the endpoint
//
//	safe to open up: **it never interprets the content.** Every response is
//	one of four shapes:
//
//	  - a picture: its own image type, inline;
//	  - a PDF: inline, for the browser's own reader;
//	  - text: **text/plain**, inline — never text/html;
//	  - anything else: octet-stream, as a download.
//
// ★ The text rule is the one that matters. A file named `.html` or `.svg`
//
//	can carry a script, and a browser that renders it as a document or as
//	an image runs that script **on this origin**. Serving text as text
//	closes that door — and nothing is lost by it, because the interface
//	renders Markdown and code itself.
//
// ★ The only boundary is the workspace itself: the path is resolved by
//
//	the session, and a name that escapes it is refused.
func (s *Server) v2RawFile(ctx context.Context, c *app.RequestContext) {
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
	full, err := sess.ResolveWorkspaceFile(name)
	if err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidFileName, err.Error(), false)
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			s.v2Fail(c, consts.StatusNotFound, codeInvalidFileName,
				"no such file in the session workspace", false)
			return
		}
		s.v2FailInternal(c, err)
		return
	}

	mime := message.MIMEOfExt(name)
	if isPreviewImage(mime) {
		// ★ A thumbnail is scaled **on the way out**: the picture is
		//   decoded, scaled and encoded again, and nothing is stored.
		//   There is no second file to keep track of, and no second file
		//   to clean up.
		if maxEdge, ok := previewMaxEdge(c.Query("max")); ok {
			scaled, _, err := imaging.Resize(data, maxEdge)
			if err != nil {
				s.v2FailInternal(c, err)
				return
			}
			data = scaled
		}
		c.Data(consts.StatusOK, mime, data)
		return
	}

	switch {
	case mime == "application/pdf":
		c.Data(consts.StatusOK, mime, data)
	case isTextualMime(mime):
		// ★ text/plain, whatever the file calls itself. See the doc
		//   comment: this is the line that keeps an uploaded script from
		//   running on this origin.
		c.Data(consts.StatusOK, "text/plain; charset=utf-8", data)
	default:
		// ★ Anything else is a download. The browser must not be given
		//   the chance to interpret it.
		c.Response.Header.Set("Content-Disposition",
			"attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(name)))
		c.Data(consts.StatusOK, "application/octet-stream", data)
	}
}

// isPreviewImage reports whether a content type may be shown as an image.
//
// ★ SVG is not one of them, even though it is an image type: an SVG can
//
//	carry a script, and a browser renders it as a document. The attachment
//	design treats SVG as text for the same reason.
func isPreviewImage(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// isTextualMime reports whether a content type may be shown as text.
//
// ★ text/html is textual by its name and **not** by its use: the raw
//
//	endpoint serves it as text/plain, so a page that tries to run cannot.
func isTextualMime(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "application/json", "application/xml", "application/yaml",
		"application/x-yaml", "application/javascript", "application/x-ndjson":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mime)), "text/")
	}
}

// previewMaxEdge reads the pixel limit of a thumbnail request.
//
// ★ A limit that is not a positive number is **ignored**, not an error: the
//
//	caller asked for a picture and the server gives it one.
func previewMaxEdge(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ── GET /v2/sessions/{id}/files ──────────────────────────────────────────

func (s *Server) v2ListFiles(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}
	entries, err := listWorkspace(sess)
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
//
// ★ The **name** is the one the user typed, not the one on disk. A file is
//
//	stored under a hashed name, and a listing that showed the hash would
//	be a listing of hashes — the interface shows names.
func listWorkspace(sess storage.Session) ([]workspaceEntry, error) {
	root := sess.Workspace()
	if root == "" {
		return []workspaceEntry{}, nil
	}
	originals := sess.OriginalNames()
	out := []workspaceEntry{}
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
		name := originals[rel]
		if name == "" {
			name = d.Name()
		}
		out = append(out, workspaceEntry{
			Name:       name,
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
