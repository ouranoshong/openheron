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

// ValidatePath answers "may a caller WRITE this path?".
//
// ★ Reading and writing ask different questions, and this function used to
// answer both. The shape (a known partition, a name that partition accepts) is
// the same for either; the **editable** rule is not: `server/` configures the
// process itself, so a change there needs a restart and the editor must not
// save it. That says nothing about reading it — and refusing the read made the
// tool that reads the configuration unable to read a third of it, with a
// message about restarts for a call that changes nothing.
//
// ★ The write gate keeps this name because every existing caller of it is a
// writer, and a caller that got the stricter answer by mistake is safe; a
// writer that got the looser one is a file it should not have touched.
func ValidatePath(rel string) (Category, error) {
	cat, err := ValidateReadPath(rel)
	if err != nil {
		return Category{}, err
	}
	if !cat.Editable {
		return Category{}, writeErrf(ErrPathInvalid, rel,
			"section %q is not editable: it is not hot-reloaded, so changes would need a restart", cat.Dir)
	}
	return cat, nil
}

// ValidateReadPath answers "may a caller READ this path?".
//
// ★ Shape only, and it is the same shape the writer checks: a path outside the
// root, an unknown partition, or a file name the partition does not accept is
// not readable either. What it does not ask is whether the partition is
// editable — that is a question about writing, and a read-only partition is
// exactly the one a reader most wants to look at.
func ValidateReadPath(rel string) (Category, error) {
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

	// ★ The depth is the **partition's** property, not a free-for-all. Every
	//   partition but one holds its entity files directly; `skills/` holds a
	//   folder per skill, because the folder name is the skill's name. A path
	//   one level too deep elsewhere is refused rather than saved: the loader
	//   reads one level, so a file at `agents/sub/main.md` would be written
	//   and never loaded.
	segs := strings.Split(slash, "/")
	if len(segs) < 2 {
		return Category{}, writeErrf(ErrPathInvalid, rel,
			"path must name a file inside a config section, like agents/main.md")
	}
	cat, ok := CategoryByDir(segs[0])
	if !ok {
		return Category{}, writeErrf(ErrPathInvalid, rel,
			"unknown config section %q; known sections are %s", segs[0], knownDirs())
	}
	want := 2
	if cat.Nested {
		want = 3
	}
	if len(segs) != want {
		return Category{}, writeErrf(ErrPathInvalid, rel,
			"%s/ holds %s", cat.Dir, shapeHint(cat))
	}
	if name := segs[len(segs)-1]; name == "" || name == "." || strings.HasPrefix(name, ".") {
		return Category{}, writeErrf(ErrPathInvalid, rel, "file name must not start with a dot")
	}
	if err := validateFileName(cat, rel); err != nil {
		return Category{}, err
	}
	return cat, nil
}

// shapeHint says what the files of a partition look like.
func shapeHint(cat Category) string {
	if cat.Nested {
		return fmt.Sprintf("<name>/%s", SkillFileName)
	}
	return "<name>" + cat.Ext
}

// validateWritable is ValidatePath plus the one refusal that needs the service
// rather than the path: `skills/` is a partition the process may not read.
//
// ★ Silent success is the failure this guards against. The editor writes
//
//	`<config root>/skills/`, the runtime reads whatever its own probe found,
//	and when those differ the save would succeed and change nothing — the
//	user would be editing a tree nobody reads, with no error to tell them.
func (s *Service) validateWritable(rel string) (Category, error) {
	cat, err := ValidatePath(rel)
	if err != nil {
		return Category{}, err
	}
	if cat.Dir == DirSkills {
		if elsewhere := s.skillsElsewhere(); elsewhere != "" {
			return Category{}, writeErrf(ErrPathInvalid, rel,
				"this process reads skills from %s, not from <config root>/%s; "+
					"a file written here would never be loaded", elsewhere, DirSkills)
		}
	}
	return cat, nil
}

func validateFileName(cat Category, rel string) error {
	base := path.Base(filepath.ToSlash(rel))
	switch cat.Dir {
	case DirTools:
		// ★ One list, shared with the loader: a second copy here would let
		//   the editor save a file the loader then rejects.
		for _, name := range ToolFileNames() {
			if base == name {
				return nil
			}
		}
		return writeErrf(ErrPathInvalid, rel,
			"tools/ accepts one file per tool: %s; other files here are rejected at load time",
			toolFileList())
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
	case DirSkills:
		// ★ The last segment is always SKILL.md; the skill's name is its
		//   folder, and an agent writes that name in `skills:`. A name the
		//   runtime cannot read back is refused here, at the moment somebody
		//   types it, rather than discovered as a skill that never loads.
		if base != SkillFileName {
			return writeErrf(ErrPathInvalid, rel, "skills/ accepts <name>/%s", SkillFileName)
		}
		name := strings.Split(filepath.ToSlash(rel), "/")[1]
		if !decl.ValidSkillName(name) {
			return writeErrf(ErrPathInvalid, rel,
				"the skill name is its folder name and must match %s", decl.SkillNamePattern)
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
	dir := filepath.Join(s.root, cat.Dir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if cat.Nested {
		return listNested(dir, entries), nil
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

// listNested lists `skills/<name>/SKILL.md` for every folder that holds one.
//
// ★ A folder without the file is **not** a skill: the runtime decides that by
//
//	looking for exactly this file, so listing the folder would offer the user
//	something that will never load. The names come back as `<name>/SKILL.md`,
//	which is the path the rest of the surface already speaks.
func listNested(dir string, entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, e.Name(), SkillFileName))
		if err != nil || fi.IsDir() {
			continue
		}
		names = append(names, e.Name()+"/"+SkillFileName)
	}
	sort.Strings(names)
	return names
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
			"#   filesystem.yaml   ls / read_file / write_file / edit_file / glob / grep / execute\n" +
			"#   config.yaml       config_get / config_validate / config_template / config_notices / config_tier / config_write\n" +
			"#\n" +
			"# ★ The content of the file IS **that tool's own configuration** (no outer wrapper key).\n" +
			"#   A provider whose configuration is absent mounts nothing, so for those tools\n" +
			"#   the file's presence IS the switch — turning a tool off = deleting that file.\n" +
			"#   The filesystem and configuration tools are mounted either way: for those\n" +
			"#   two, a missing file takes away an exception, not the tool.\n" +
			"#\n" +
			"# ★ tools/ is hot-reloaded: the change takes effect on the next turn, no process restart.\n"
	case DirMCP:
		// ★ The MCP example is one file, in the templates package, and the
		//   configuration editor reads it from there — so the editor and the
		//   file cannot drift. The leading underscore marks it as a
		//   scaffold: it is what a new server starts from, not a server
		//   that is installed.
		tpl, err := templates.File("mcps/_example.yaml")
		if err != nil {
			return ""
		}
		return tpl
	case DirSkills:
		// ★ `name` is not in this frontmatter on purpose: the folder name is
		//   the skill's name, and a second copy of it inside the file is a
		//   second thing that can disagree. What the file carries is what the
		//   list shows — a description, and the instructions themselves.
		return "---\ndescription: \n---\n\n# Instructions\n\n" +
			"What this skill does, and when to use it. The body is what the agent\n" +
			"reads when it loads the skill.\n"
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

// ★ The nil guard comes first, before `len(c.Models)`: a nil *Config is a
// legal value here (a process that has not loaded a tree yet still asks for
// the vocabulary), and reading a field through a nil receiver panics. The
// `if c == nil` that used to sit after the `make` did not protect anything.
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
	if c == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(c.Models))
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
	if _, err := ValidateReadPath(rel); err != nil {
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

// agentsLostBy returns the agents the tree on disk can run and the trial
// tree cannot.
//
// ★ A change may **add** to the tree, and it may leave an agent that was
//
//	already broken exactly as it was. What it may not do is take a
//	**working** agent out of it: that is how a worker disappears without
//	anybody deciding that it should.
//
// ★ The comparison is between the two **settled** trees — the ones that
//
//	dropped the agents they cannot run. An agent that was already out is
//	out on both sides, so a change that leaves it alone costs nothing.
func agentsLostBy(before, after *Config, removed []string) []string {
	if before == nil || after == nil {
		return nil
	}
	// ★ An agent the change **removes on purpose** is not lost. The gate
	//   exists for the other case: an agent that used to work and no longer
	//   does because its model, its tools or its parent went away. A user
	//   who deletes agents/task.md meant to delete it — refusing that would
	//   make the file undeletable.
	onPurpose := make(map[string]bool, len(removed))
	for _, id := range removed {
		onPurpose[id] = true
	}
	var lost []string
	for id := range before.Agents {
		if onPurpose[id] {
			continue
		}
		if _, ok := after.Agents[id]; !ok {
			lost = append(lost, id)
		}
	}
	sort.Strings(lost)
	return lost
}

// lostAgentMessage says which agent a change would take out, and why.
func lostAgentMessage(id string, after *Config) string {
	why := ""
	if after != nil {
		why = after.LeftOut()[id]
	}
	if why == "" {
		why = "it is no longer in the tree"
	}
	return fmt.Sprintf("the change would leave the agent %q out of the tree: %s", id, why)
}

// agentsLostByChange returns the agents a change would take out of the tree.
//
// ★ ★ The tree as it is **cannot** be loaded when some file on disk does not
//
//	parse: then nothing is known to be working, and the change may only
//	add. (That is also how a broken file gets fixed — the editor's draft
//	replaces it, so the trial tree is the one that parses.)
//
// ★ `removed` names the agents the change deletes on purpose — they are not
//
//	lost, they are gone because the user said so.
func (s *Service) agentsLostByChange(after *Config, removed []string) []string {
	before, err := s.loadWithMany(nil)
	if err != nil {
		return nil
	}
	return agentsLostBy(before, after, removed)
}

// checkNoAgentLost refuses a change that would take a working agent out of
// the tree.
func (s *Service) checkNoAgentLost(after *Config, rel string, removed []string) error {
	lost := s.agentsLostByChange(after, removed)
	if len(lost) == 0 {
		return nil
	}
	return &WriteError{
		Kind:    ErrMergeRejected,
		Rel:     rel,
		Message: lostAgentMessage(lost[0], after),
	}
}

func (s *Service) Write(rel string, content []byte, baseRev string) (rev string, cfg *Config, err error) {
	if _, err := s.validateWritable(rel); err != nil {
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
	// ★ The gate the tree needs: a change may not take a working agent out
	//   of it. The trial tree loads either way — an agent that cannot run is
	//   left out — so without this the gate would have no teeth.
	if err := s.checkNoAgentLost(trial, rel, nil); err != nil {
		return "", nil, err
	}

	if s.root == "" {
		return "", nil, writeErrf(ErrPathInvalid, rel, "no config root")
	}
	dst := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := persist.AtomicWrite(dst, content, writeTmpSuffix); err != nil {
		return "", nil, err
	}
	// ★ The file landed, so it is **marked**: the next turn reads the
	//   configuration again before it builds, and the change is in it.
	//   ★ The watcher's poll would notice the same thing a moment later,
	//   and a turn that starts in between would run the old tree — so the
	//   mark is what makes "the next turn uses it" a promise instead of a
	//   race.
	s.Mark(rel)

	return Revision(content), trial, nil
}

// Append adds content to the end of a file instead of replacing it.
//
// ★ This exists because a file's whole text has to travel in **one** tool
//
//	call, and one tool call is bounded by the model's output budget. A
//	file bigger than that budget cannot be written at all — and the
//	alternative (a file replaced by half of itself) is worse than not
//	writing it, because nothing says so afterwards.
//
// ★ The result goes through the **same** gates as a whole-file write: the
//
//	tree must still load, a working agent must not disappear, the base
//	revision must still match. So the file on disk is a valid
//	configuration file after **every** piece — never a half-written one.
//
// ★ Appending to a file that does not exist creates it. The model that
//
//	writes a long file in pieces should not have to know whether the
//	first piece is the first piece or the only one.
func (s *Service) Append(rel string, content []byte, baseRev string) (rev string, cfg *Config, err error) {
	if _, err := s.validateWritable(rel); err != nil {
		return "", nil, err
	}
	// ★ The revision is checked **before** the file is read: the read and
	//   the check have to see the same version of the disk.
	if err := s.checkRevision(rel, baseRev); err != nil {
		return "", nil, err
	}
	cur, _, err := s.ReadFile(rel)
	if err != nil {
		return "", nil, err
	}
	full := make([]byte, 0, len(cur)+len(content))
	full = append(full, cur...)
	full = append(full, content...)

	// ★ The secret check reads the **whole** file, not the piece: the file
	//   is what lands on disk, and that is what the check is about.
	if err := s.checkSecretFor(rel, full); err != nil {
		return "", nil, err
	}

	m := &mutation{Rel: rel, Content: full}
	trial, err := s.loadWith(m)
	if err != nil {
		return "", nil, &WriteError{Kind: ErrMergeRejected, Rel: rel, Message: err.Error(), Err: err}
	}
	if err := s.checkNoAgentLost(trial, rel, nil); err != nil {
		return "", nil, err
	}

	if s.root == "" {
		return "", nil, writeErrf(ErrPathInvalid, rel, "no config root")
	}
	dst := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := persist.AtomicWrite(dst, full, writeTmpSuffix); err != nil {
		return "", nil, err
	}
	s.Mark(rel)

	return Revision(full), trial, nil
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
		if _, err := s.validateWritable(f.Rel); err != nil {
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
	// ★ The same gate as the single-file write: a batch may not take a
	//   working agent out of the tree either.
	if lost := s.agentsLostByChange(trial, nil); len(lost) > 0 {
		return "", nil, &WriteError{
			Kind:    ErrMergeRejected,
			Rel:     relsOf(files),
			Message: lostAgentMessage(lost[0], trial),
		}
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
	// ★ The same mark the single write leaves: the files landed, so the
	//   next turn reads the configuration again before it builds.
	for _, f := range files {
		s.Mark(f.Rel)
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

// deletedAgent names the agent a deleted path removes, and "" when the path
// is not an agent file.
//
// ★ Deleting an agent **on purpose** is not the gate's business: the gate
//
//	exists for the agent that stops working by accident. Without this, no
//	agent file could ever be deleted.
func deletedAgent(rel string) []string {
	dir, base := path.Split(rel)
	if dir != DirAgent+"/" {
		return nil
	}
	return []string{strings.TrimSuffix(base, ".md")}
}

// reference is one thing in the tree that names a file, and how it names it.
type reference struct {
	// file is the file that holds the reference.
	file string
	// what says it in the user's terms: `mcps: [fetch]`, `model: openai/fast`.
	what string
}

// referencesTo returns the files in the tree that still name rel.
//
// ★ The three partitions that are named by others, and what names them:
//
//	`mcps/<server>.yaml` ← an agent's `mcps:` list
//	`llm/<provider>.yaml` ← an agent's `model:`
//	`agents/<id>.md` ← another agent's `subagents:` list
//
//	Nothing else is named: a `tools/` file is named by the tool registry, and
//	an agent's `tools:` list names **tools**, not files.
func (c *Config) referencesTo(rel string) []reference {
	dir, base := path.Split(rel)
	var out []reference
	switch dir {
	case DirMCP + "/":
		server := strings.TrimSuffix(base, ".yaml")
		for _, id := range c.AgentNames() {
			a := c.Agents[id]
			if a == nil {
				continue
			}
			for _, name := range a.MCPServers {
				if name == server {
					out = append(out, reference{
						file: agentFile(a, id),
						what: fmt.Sprintf("mcps: [%s]", server),
					})
				}
			}
		}
	case DirLLM + "/":
		provider := strings.TrimSuffix(base, ".yaml")
		for _, id := range c.AgentNames() {
			a := c.Agents[id]
			if a == nil {
				continue
			}
			ref, _ := ParseRef(a.ModelRef)
			if ref.ProviderID == provider {
				out = append(out, reference{
					file: agentFile(a, id),
					what: fmt.Sprintf("model: %s", a.ModelRef),
				})
			}
		}
	case DirAgent + "/":
		id := strings.TrimSuffix(base, ".md")
		for _, other := range c.AgentNames() {
			a := c.Agents[other]
			if a == nil {
				continue
			}
			for _, sub := range a.SubAgents {
				if sub == id {
					out = append(out, reference{
						file: agentFile(a, other),
						what: fmt.Sprintf("subagents: [%s]", id),
					})
				}
			}
		}
	}
	return out
}

// checkNotReferenced refuses a deletion that something else still names.
//
// ★ The tree is read as it is **now**, not as it would be after the delete:
//
//	the references live in files that the deletion does not touch. And the
//	tree that is read is the one on disk — when it does not load, nothing is
//	known to be referring to anything, and the trial load's own error is the
//	only answer there is.
//
// ★ The refusal names the **referrer** and what to do about it. "The tree
//
//	does not load" is a symptom, and a user cannot act on a symptom.
func (s *Service) checkNotReferenced(rel string) error {
	before, err := s.loadWithMany(nil)
	if err != nil {
		return nil
	}
	refs := before.referencesTo(rel)
	if len(refs) == 0 {
		// ★ The default agent is not "referenced" by anybody, but a tree
		//   needs one: removing it leaves a tree that cannot be loaded, and
		//   the load error ("agents/main.md does not exist") reads like a
		//   broken file rather than a refused deletion.
		if base := path.Base(rel); base == DefaultAgentID+".md" &&
			path.Dir(rel) == DirAgent {
			return &WriteError{
				Kind:    ErrMergeRejected,
				Rel:     rel,
				Message: "the default agent cannot be deleted: a tree needs one",
			}
		}
		return nil
	}
	return &WriteError{
		Kind: ErrMergeRejected,
		Rel:  rel,
		Message: fmt.Sprintf("it is still referenced: %s; remove %s first, then delete this file",
			describeReferences(refs), referenceSubject(refs)),
	}
}

// describeReferences names the files that still hold a reference.
//
// ★ The path of the deleted file is **not** repeated: the envelope carries
//
//	it as a field (`WriteError.Rel`), and a message that names it too makes
//	the reader read it twice.
func describeReferences(refs []reference) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, fmt.Sprintf("%s (%s)", r.file, r.what))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return fmt.Sprintf("%d files still name it — %s", len(parts), strings.Join(parts, ", "))
}

// referenceSubject says what has to be removed.
func referenceSubject(refs []reference) string {
	if len(refs) == 1 {
		return "that reference"
	}
	return "those references"
}

func (s *Service) Delete(rel string) (cfg *Config, err error) {
	if _, err := s.validateWritable(rel); err != nil {
		return nil, err
	}
	// ★ **The gate that names the referrer.** It runs *before* the trial
	//   load, because the trial load's error is exactly the symptom this
	//   replaces: deleting `mcps/fetch.yaml` while an agent mounts "fetch"
	//   makes the tree fail to load, and the load error reads like "your
	//   configuration is broken" — with the deleted file's name in front
	//   of it and the agent's nowhere in it.
	//
	//   What the user can act on is the reference: "agents/task.md still
	//   lists this server — remove it there first".
	if err := s.checkNotReferenced(rel); err != nil {
		return nil, err
	}
	m := &mutation{Rel: rel, Delete: true}

	trial, err := s.loadWith(m)
	if err != nil {
		return nil, &WriteError{Kind: ErrMergeRejected, Rel: rel, Message: err.Error(), Err: err}
	}
	// ★ The gate, on the delete path too: removing the provider that an
	//   agent names takes that agent out of the tree just as surely as
	//   pointing it at a model that does not exist.
	if err := s.checkNoAgentLost(trial, rel, deletedAgent(rel)); err != nil {
		return nil, err
	}

	if s.root == "" {
		return nil, writeErrf(ErrPathInvalid, rel, "no config root")
	}
	dst := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.Remove(dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &WriteError{
				Kind:    ErrPathInvalid,
				Rel:     rel,
				Message: "the file does not exist",
			}
		}
		return nil, err
	}
	// ★ A skill is its folder: `skills/name/SKILL.md` is the skill, and an
	//   empty folder left behind is a skill that shows up in nobody's list
	//   and cannot be deleted from the editor. Remove the folder when the
	//   deleted file was the last thing in it.
	if cat, err := ValidateReadPath(rel); err == nil && cat.Nested {
		_ = os.Remove(filepath.Dir(dst))
	}
	return trial, nil
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

// secretLiteralMessage is the one sentence both callers say about a key
// written out in plain text: the save refuses it, and the editor shows it
// before the save is attempted.
const secretLiteralMessage = "api_key must be a reference, not a literal: " +
	"use ${ENV_VAR} or @file:/path so the secret stays out of the repository"

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
	if secretLiteralLine(content) == 0 {
		return nil
	}
	return writeErrf(ErrSecretLiteral, rel, "%s", secretLiteralMessage)
}

// secretLiteralLine reports the 1-based line of a literal api_key, or 0 when
// there is none.
//
// ★ A literal key is not something the YAML decoder objects to: by the time
//
//	it is a "problem" the file has already parsed, and the tree has already
//	loaded. It is refused for a reason that belongs to the repository, not to
//	the parser — which is exactly why it needs its own line number instead of
//	riding on a parse error, and why the editor can show it while the user is
//	still typing.
func secretLiteralLine(content []byte) int {
	for i, line := range strings.Split(string(content), "\n") {
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
		return i + 1
	}
	return 0
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
