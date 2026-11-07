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

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/agentkit/templates"
	"github.com/ouranoshong/openheron/persist"
)

const writeTmpSuffix = ".tmp"

const revisionLen = 8

type WriteErrorKind string

const (
	ErrPathInvalid   WriteErrorKind = "config_path_invalid"
	ErrSecretLiteral WriteErrorKind = "config_secret_literal"
	ErrMergeRejected WriteErrorKind = "config_would_break_merge"
	ErrConflict      WriteErrorKind = "config_write_conflict"
)

type WriteError struct {
	Kind    WriteErrorKind
	Rel     string
	Message string
	Err     error
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

func (e *WriteError) Is(target error) bool {
	other, ok := target.(*WriteError)
	return ok && other.Kind == e.Kind
}

func ValidatePath(rel string) (Category, error) {
	if strings.TrimSpace(rel) == "" {
		return Category{}, writeErrf(ErrPathInvalid, rel, "path is empty")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return Category{}, writeErrf(ErrPathInvalid, rel, "path must be relative to the config directory")
	}
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

func validateFileName(cat Category, rel string) error {
	base := path.Base(filepath.ToSlash(rel))
	switch cat.Dir {
	case DirTools:
		switch base {
		case "http.yaml", "web_search.yaml", "wikipedia.yaml", "kb.yaml":
			return nil
		default:
			return writeErrf(ErrPathInvalid, rel,
				"tools/ only accepts http.yaml, web_search.yaml, wikipedia.yaml and kb.yaml; "+
					"other files here are rejected at load time")
		}
	case DirMCP:
		// ★ The file's name is the server's name, and it is the namespace
		//   segment of that server's tools: a name that does not match the
		//   pattern could not be read back.
		name := strings.TrimSuffix(base, ".yaml")
		if name == base || !decl.ValidMCPServerName(name) {
			return writeErrf(ErrPathInvalid, rel,
				"mcps/ accepts <server>.yaml, and the server name must match %s", decl.MCPServerNamePattern)
		}
		return nil
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

// Skeleton returns the starting point for a **new** file of one partition.
//
// ★ It is not the partition's files: it is what a user starts from when
//
//	they create a file the configuration does not have yet. The files
//	themselves are in Templates.
func Skeleton(catDir string, modelRefs []string) string {
	switch catDir {
	case DirLLM:
		return "type: openai\nbase_url: \napi_key: ${}\nmodels: []\n"
	case DirAgent:
		s := "---\nmodel: \ndescription: \n"
		// ★ The skills hint belongs in the frontmatter: the key is a
		//   frontmatter key, and a comment in the body would reach the model
		//   as part of the prompt.
		s += "# skills: [name]  the skills this agent may use; the names are the folders under skills/\n"
		s += "---\n"
		if len(modelRefs) > 0 {
			s += "# available models: " + strings.Join(modelRefs, ", ") + "\n"
		}
		return s + "\n"
	case DirTools:
		return "# tools/ one file = one tool:\n" +
			"#   http.yaml         request_get / request_post / request_put / request_delete\n" +
			"#   web_search.yaml   duckduckgo_search\n" +
			"#   wikipedia.yaml    wikipedia_search\n" +
			"#   kb.yaml           knowledge base\n" +
			"#\n" +
			"# ★ The content of the file IS **that tool's own configuration** (no outer wrapper key).\n" +
			"#   A provider whose configuration is absent mounts nothing, so for these tools\n" +
			"#   the file's presence IS the switch — turning a tool off = deleting that file.\n" +
			"#\n" +
			"# ★ tools/ is hot-reloaded: the change takes effect on the next turn, no process restart.\n"
	case DirMCP:
		// ★ The MCP example is one file, in the templates package: the
		//   configuration editor and `openheron init` both read it from
		//   there, so the two cannot drift.
		tpl, err := templates.File("mcps/example.yaml")
		if err != nil {
			return ""
		}
		return tpl
	default:
		return ""
	}
}

// ToolTemplates are the per-file skeletons of the tools/ partition.
//
// ★ They come from the templates package, so the configuration editor and
//
//	`openheron init --tools` hand out the very same files.
func ToolTemplates() map[string]string {
	return Templates(DirTools)
}

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

func Revision(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:revisionLen]
}

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

	return Revision(content), trial, nil
}

type File struct {
	Rel     string
	Content []byte
}

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
			for j := i - 1; j >= 0; j-- {
				restore(backups[j])
			}
			return "", nil, err
		}
	}
	return s.Fingerprint(), trial, nil
}

type savedFile struct {
	dst     string
	existed bool
	content []byte
}

func restore(b savedFile) {
	if b.existed {
		_ = persist.AtomicWrite(b.dst, b.content, writeTmpSuffix)
		return
	}
	_ = os.Remove(b.dst)
}

func relsOf(files []File) string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Rel)
	}
	return strings.Join(out, ", ")
}

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

func (s *Service) checkSecretFor(rel string, content []byte) error {
	cat, ok := CategoryByDir(path.Dir(path.Clean(filepath.ToSlash(rel))))
	if !ok {
		return nil
	}
	return s.checkSecret(cat, rel, content)
}

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
			continue
		}
		if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
			continue
		}
		if strings.HasPrefix(v, "@file:") {
			continue
		}
		return writeErrf(ErrSecretLiteral, rel,
			"api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path so the secret stays out of the repository")
	}
	return nil
}

func splitYAMLLine(line string) (key, val string, ok bool) {
	if line != strings.TrimLeft(line, " \t") {
		return "", "", false
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

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
