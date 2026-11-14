// Package projects holds the project registry: the table that says which
// sessions belong to which project.
//
// ★ The table is the **only** truth about that relation. ★ A session's own
//
//	record does not remember its project, and there is no second answer to
//	arbitrate between — two answers would be a split nobody can resolve.
//
// ★ A project is a **directory**, not a configuration tree. The configuration
//
//	is shared by every project, so the table does not carry a configuration
//	root; it carries the directory the agent works in.
package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ouranoshong/openheron/persist"
)

const (
	// FileEnv names the table's location.
	//
	// ★ It is an environment variable because the table is a **user-level**
	//
	//	file: a test needs to move it without moving anything else, and a
	//	user with two openheron installations needs the same.
	FileEnv = "OPENHERON_PROJECTS_FILE"

	// FileName is the table's name inside the data directory.
	FileName = "projects.json"

	// tableVersion is the file format's version.
	//
	// ★ It is written on every save, so a file written by an older openheron
	//
	//	is recognisable as such.
	tableVersion = 1

	// tmpSuffix is the temporary-file suffix of a direct write. It matches
	// the configuration service's one, so a half-written table is never
	// mistaken for a table.
	tmpSuffix = ".tmp"
)

// ErrNotFound is returned when a project id names nothing.
var ErrNotFound = errors.New("project not found")

// Project is one registered project.
//
// ★ The field order is the file's field order, and it is the shape the API
//
//	speaks in too — one type, one answer.
type Project struct {
	// ID is the project's stable name. It is derived from the directory it
	// points at, and a name that is already taken gets a suffix.
	ID string `json:"id"`
	// Name is what the interface shows. It is the directory's name by
	// default, and the user may change it.
	Name string `json:"name"`
	// Root is the directory the agent works in. It is absolute.
	Root string `json:"root"`
	// Order is the project's place in the list. Lower comes first.
	Order int `json:"order"`
	// Sessions are the ids of the sessions that belong to this project.
	//
	// ★ It has **no** `omitempty`: a project with no sessions yet writes
	//
	//	`"sessions": []`, and an empty array says "this project has no
	//	sessions" while a missing field says "this file is old".
	Sessions []string `json:"sessions"`
	// ConfigDir is the project's own configuration tree.
	//
	// ★ **Reserved, not implemented.** The field is read and written so the
	//   shape is settled, and nothing resolves it yet: the configuration is
	//   shared, and a project-level tree is a proposal that has not landed.
	ConfigDir string `json:"configDir,omitempty"`
}

// Table is the file on disk.
type Table struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

// Location returns where the table lives.
//
// ★ The environment variable wins over the data directory: the table is a
//
//	user-level file, and moving it is a thing an operator does.
func Location(dataDir string) string {
	if v := strings.TrimSpace(os.Getenv(FileEnv)); v != "" {
		return v
	}
	return filepath.Join(dataDir, FileName)
}

// Registry reads and writes the table.
//
// ★ It is safe for concurrent use: the process-level mutex serializes the
//
//	goroutines of one process, and the file lock serializes the processes.
type Registry struct {
	mu   sync.Mutex
	path string
	lock *persist.FileLock
}

// Open prepares the registry for the table at path.
//
// ★ The table itself is **not** created here: an empty table is a fact, and
//
//	writing it is the first change's business.
func Open(path string) (*Registry, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("projects: the table's path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("projects: resolve %q: %w", path, err)
	}
	lock, err := persist.OpenFileLock(persist.LockPath(filepath.Dir(abs)))
	if err != nil {
		return nil, err
	}
	return &Registry{path: abs, lock: lock}, nil
}

// Path returns the table's location.
func (r *Registry) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Close releases the lock file.
func (r *Registry) Close() error {
	if r == nil || r.lock == nil {
		return nil
	}
	return r.lock.Close()
}

// ── reading ────────────────────────────────────────────────────────────────

// Snapshot is the table as it was on disk, read once.
//
// ★ It is the shape the read path wants: one read, many lookups. ★ Reading
//
//	the table per lookup would read the file per session, and a list of a
//	hundred sessions would read it a hundred times.
type Snapshot struct {
	Projects []Project
	byID     map[string]Project
	bySess   map[string]string
}

// Get returns one project by id.
func (s *Snapshot) Get(id string) (Project, bool) {
	if s == nil {
		return Project{}, false
	}
	p, ok := s.byID[id]
	return p, ok
}

// ProjectOf returns the project a session belongs to.
//
// ★ The second answer is "no project": a session that is in no project's
//
//	list belongs to no project, and the caller falls back to the default
//	project root.
func (s *Snapshot) ProjectOf(sessionID string) (Project, bool) {
	if s == nil || sessionID == "" {
		return Project{}, false
	}
	id, ok := s.bySess[sessionID]
	if !ok {
		return Project{}, false
	}
	p, ok := s.byID[id]
	return p, ok
}

// Snapshot reads the table.
func (r *Registry) Snapshot(ctx context.Context) (*Snapshot, error) {
	if r == nil {
		return nil, errors.New("projects: no registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	table, err := r.read()
	if err != nil {
		return nil, err
	}
	return snapshotOf(table), nil
}

// List returns the projects, in their order.
func (r *Registry) List(ctx context.Context) ([]Project, error) {
	snap, err := r.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snap.Projects, nil
}

// RootOf returns the project root a session works in.
//
// ★ An empty string is the answer "this session belongs to no project" —
//
//	the caller falls back to the default project root. ★ The root comes from
//	the table and not from the session record: the table is the only truth
//	about the relation.
//
// ★ It is **one read**: the table is a file, and a round must not read it
//
//	once per tool call.
func (r *Registry) RootOf(ctx context.Context, sessionID string) (string, error) {
	snap, err := r.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	p, ok := snap.ProjectOf(sessionID)
	if !ok {
		return "", nil
	}
	return p.Root, nil
}

func snapshotOf(t *Table) *Snapshot {
	snap := &Snapshot{
		byID:   make(map[string]Project, len(t.Projects)),
		bySess: make(map[string]string),
	}
	out := make([]Project, 0, len(t.Projects))
	for _, p := range t.Projects {
		if p.Sessions == nil {
			p.Sessions = []string{}
		}
		snap.byID[p.ID] = p
		for _, id := range p.Sessions {
			snap.bySess[id] = p.ID
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	snap.Projects = out
	return snap
}

// read loads the table from disk.
//
// ★ A table that is not there is an **empty** table, not an error: the first
//
//	project is what creates the file.
func (r *Registry) read() (*Table, error) {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return &Table{Version: tableVersion, Projects: []Project{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: read %s: %w", r.path, err)
	}
	var t Table
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("projects: parse %s: %w", r.path, err)
	}
	if t.Version == 0 {
		t.Version = tableVersion
	}
	if t.Projects == nil {
		t.Projects = []Project{}
	}
	return &t, nil
}

// write saves the table.
//
// ★ The write is atomic, and the file is written whole: a table that is half
//
//	written is a table nobody can read, and the next read would say so.
func (r *Registry) write(t *Table) error {
	t.Version = tableVersion
	if t.Projects == nil {
		t.Projects = []Project{}
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("projects: encode the table: %w", err)
	}
	b = append(b, '\n')
	if err := persist.AtomicWrite(r.path, b, tmpSuffix); err != nil {
		return fmt.Errorf("projects: write %s: %w", r.path, err)
	}
	return nil
}

// mutate changes the table under both locks.
//
// ★ The order is: the process's mutex, then the file's lock, then a **fresh
//
//	read**. ★ The re-read is the part that matters — the table is a
//	user-level file, and the user may have edited it while this process was
//	running. ★ A write that did not re-read would take their edit with it.
func (r *Registry) mutate(ctx context.Context, fn func(*Table) error) error {
	if r == nil {
		return errors.New("projects: no registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	release, err := r.lock.Lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	table, err := r.read()
	if err != nil {
		return err
	}
	if err := fn(table); err != nil {
		return err
	}
	return r.write(table)
}

// ── writing ────────────────────────────────────────────────────────────────

// Patch is a change to one project.
//
// ★ The fields are pointers because "not mentioned" and "set to the zero
//
//	value" are two different things — a rename that says nothing must not
//	erase the name.
type Patch struct {
	Name  *string
	Root  *string
	Order *int
}

// Create adds a project.
//
// ★ The id is derived from the root's directory name, and a name that is
//
//	already taken gets a suffix: the id is what the interface and the URL
//	carry, so it has to be unique and it has to be readable.
func (r *Registry) Create(ctx context.Context, name, root string) (Project, error) {
	abs, err := resolveRoot(root)
	if err != nil {
		return Project{}, err
	}
	var out Project
	err = r.mutate(ctx, func(t *Table) error {
		if p, ok := projectAtRoot(t, abs); ok {
			out = p
			return nil
		}
		id := uniqueID(t, defaultName(abs))
		p := Project{
			ID:       id,
			Name:     strings.TrimSpace(name),
			Root:     abs,
			Order:    nextOrder(t),
			Sessions: []string{},
		}
		if p.Name == "" {
			p.Name = defaultName(abs)
		}
		t.Projects = append(t.Projects, p)
		out = p
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	return out, nil
}

// Ensure returns the project for a root, adding it when it is not there.
//
// ★ It is what `openheron <dir>` uses: the user named a directory, and a
//
//	named directory is a project — ★ without a registration the sessions
//	would have nowhere to belong.
func (r *Registry) Ensure(ctx context.Context, root string) (Project, error) {
	abs, err := resolveRoot(root)
	if err != nil {
		return Project{}, err
	}
	var out Project
	err = r.mutate(ctx, func(t *Table) error {
		if p, ok := projectAtRoot(t, abs); ok {
			out = p
			return nil
		}
		p := Project{
			ID:       uniqueID(t, defaultName(abs)),
			Name:     defaultName(abs),
			Root:     abs,
			Order:    nextOrder(t),
			Sessions: []string{},
		}
		t.Projects = append(t.Projects, p)
		out = p
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	return out, nil
}

// Update changes one project.
//
// ★ The id does not change: it is what the sessions and the URL carry, and
//
//	an id that moves is an id that breaks both.
func (r *Registry) Update(ctx context.Context, id string, patch Patch) (Project, error) {
	var out Project
	err := r.mutate(ctx, func(t *Table) error {
		for i := range t.Projects {
			p := &t.Projects[i]
			if p.ID != id {
				continue
			}
			if patch.Name != nil {
				name := strings.TrimSpace(*patch.Name)
				if name == "" {
					return errors.New("the project name must not be empty")
				}
				p.Name = name
			}
			if patch.Root != nil {
				abs, err := resolveRoot(*patch.Root)
				if err != nil {
					return err
				}
				if other, ok := projectAtRoot(t, abs); ok && other.ID != p.ID {
					return fmt.Errorf("another project already points at %s", abs)
				}
				p.Root = abs
			}
			if patch.Order != nil {
				p.Order = *patch.Order
			}
			out = *p
			return nil
		}
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	})
	if err != nil {
		return Project{}, err
	}
	return out, nil
}

// Delete removes one project.
//
// ★ Its sessions are **not** deleted: they fall back to "no project", and
//
//	they work in the default project root. ★ A project is a label on a
//	directory, and removing the label must not remove the work.
func (r *Registry) Delete(ctx context.Context, id string) error {
	return r.mutate(ctx, func(t *Table) error {
		for i := range t.Projects {
			if t.Projects[i].ID != id {
				continue
			}
			t.Projects = append(t.Projects[:i], t.Projects[i+1:]...)
			return nil
		}
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	})
}

// Attach puts a session into a project.
//
// ★ The list only **grows**: a session that is already in another project
//
//	is refused, because ★ moving a session is not a thing this table does —
//	its transcript, its workspace and its checkpoint belong to the project
//	it was born in, and there is no good answer to where they would live
//	after the move. ★ Changing project means a new session.
func (r *Registry) Attach(ctx context.Context, sessionID, projectID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("the session id must not be empty")
	}
	return r.mutate(ctx, func(t *Table) error {
		idx := -1
		for i := range t.Projects {
			if t.Projects[i].ID == projectID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, projectID)
		}
		for i := range t.Projects {
			if t.Projects[i].ID == projectID {
				continue
			}
			for _, id := range t.Projects[i].Sessions {
				if id == sessionID {
					return fmt.Errorf("the session already belongs to the project %q", t.Projects[i].ID)
				}
			}
		}
		for _, id := range t.Projects[idx].Sessions {
			if id == sessionID {
				return nil
			}
		}
		t.Projects[idx].Sessions = append(t.Projects[idx].Sessions, sessionID)
		return nil
	})
}

// Detach removes a session from the project that holds it.
//
// ★ It is the way a session goes back to "no project" — the session itself
//
//	is never deleted by this table.
func (r *Registry) Detach(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("the session id must not be empty")
	}
	return r.mutate(ctx, func(t *Table) error {
		for i := range t.Projects {
			list := t.Projects[i].Sessions
			for j := range list {
				if list[j] != sessionID {
					continue
				}
				t.Projects[i].Sessions = append(list[:j], list[j+1:]...)
				return nil
			}
		}
		return nil
	})
}

// ── helpers ────────────────────────────────────────────────────────────────

// resolveRoot turns a project's root into an absolute, cleaned path.
//
// ★ An empty root is the **default project root** — the one the process
//
//	runs on — and the caller fills it in.
func resolveRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("the project root must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("projects: resolve the root %q: %w", root, err)
	}
	return filepath.Clean(abs), nil
}

// defaultName is the directory's own name.
func defaultName(root string) string {
	base := filepath.Base(filepath.Clean(root))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "project"
	}
	return base
}

// uniqueID derives an id from a name and makes it unique.
//
// ★ The suffix is a counter and not a random string: the id is read by
//
//	people (in a URL, in a log line), and `openheron-2` says more than
//	`openheron-8f3a`.
func uniqueID(t *Table, name string) string {
	base := slug(name)
	if base == "" {
		base = "project"
	}
	taken := make(map[string]bool, len(t.Projects))
	for _, p := range t.Projects {
		taken[p.ID] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// slug turns a name into an id.
//
// ★ Lowercase letters, digits and dashes: an id goes into a URL and into a
//
//	file name, and a name with a space in it is a name somebody has to quote.
func slug(name string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// projectAtRoot finds the project that points at a root.
//
// ★ The comparison is on the cleaned absolute paths, and on Windows it is
//
//	case-insensitive: the same directory named two ways is the same
//	directory.
func projectAtRoot(t *Table, root string) (Project, bool) {
	for _, p := range t.Projects {
		if sameRoot(p.Root, root) {
			return p, true
		}
	}
	return Project{}, false
}

func sameRoot(a, b string) bool {
	ca := filepath.Clean(a)
	cb := filepath.Clean(b)
	if ca == cb {
		return true
	}
	return strings.EqualFold(ca, cb)
}

// nextOrder is the order a new project gets: after everything there is.
func nextOrder(t *Table) int {
	max := -1
	for _, p := range t.Projects {
		if p.Order > max {
			max = p.Order
		}
	}
	return max + 1
}
