package kb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/persist"
)

const (
	catalogFile  = "catalog.md"
	sidecarFile  = "catalog.gob"
	IndexVersion = 2

	maxCommitAttempts = 3
)

type Derived struct {
	IndexVersion int

	Model string
	Dim   int

	Entries map[string]DerivedEntry
}

type DerivedEntry struct {
	WroteTitle   string
	WroteSummary string

	ObsSummary string
	ObsMTime   int64
	ObsSize    int64

	EmbedText string

	Vector []float32
}

type Store struct {
	dir string

	run *persist.FileLock

	// mu guards the store's three pieces of state: cat, der and stamp.
	//
	// ★ Two rules follow from the scope, and both of them were broken before
	//	  they were written down:
	//
	//	- Every read takes the lock and every write happens under it —
	//	  including the writes of **derived** values. stamp is the catalog
	//	  file's stat, so refreshing it means stat'ing the file (I/O, which
	//	  must happen outside the lock) and assigning the result (which
	//	  must happen inside it).
	//	- A reader that needs the entries takes a **deep** copy under the
	//	  lock. der.Entries is a map that install and SetVectors mutate in
	//	  place, so a shallow copy is still shared with the writer — and a
	//	  reader iterating a map another goroutine writes is a fatal error,
	//	  not a race the detector merely reports.
	mu  sync.RWMutex
	cat Catalog
	der Derived

	stamp fileStamp

	// runMu serialises the reloads: it makes "the catalog changed on disk"
	// a thing that is re-read once, not once per goroutine that noticed.
	//
	// ★ It is not a data lock — the state it protects is guarded by mu.
	//   It is the "one reload at a time" gate, and the stamp is re-checked
	//   under it because another goroutine may have reloaded in between.
	runMu sync.Mutex

	afterWrite func()
}

type fileStamp struct {
	mtime int64
	size  int64
	valid bool
}

func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("kb: store dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("kb: create store dir: %w", err)
	}
	run, err := persist.OpenFileLock(persist.RunLockPath(dir))
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, run: run, der: Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}}
	if err := s.load(); err != nil {
		_ = run.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.run == nil {
		return nil
	}
	return s.run.Close()
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) StartRun() (stop func(), err error) {
	return s.run.Try()
}

func (s *Store) Catalog() Catalog {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneCatalog(s.cat)
}

func (s *Store) Entries() []Entry {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return normalizeEntries(s.cat.Entries)
}

func (s *Store) Docs() []docState {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := normalizeEntries(s.cat.Entries)
	out := make([]docState, 0, len(entries))
	for _, e := range entries {
		out = append(out, docState{Entry: e, Vector: s.der.Entries[e.Path].Vector})
	}
	return out
}

type docState struct {
	Entry  Entry
	Vector []float32
}

func (s *Store) derOf(path string) (DerivedEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	de, ok := s.der.Entries[path]
	return de, ok
}

func (s *Store) Sidecar() Derived {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneDerived(s.der)
}

func (s *Store) SetIdentity(model string, dim int) error {
	s.mu.RLock()
	prevModel, prevDim := s.der.Model, s.der.Dim
	s.mu.RUnlock()

	var err error
	switch {
	case model != "" && prevModel != "" && model != prevModel:
		err = fmt.Errorf("kb: index at %s was built with model %s but the config says %s; "+
			"vectors from different models are not comparable — "+
			"delete the index directory %s and re-run `openheron kb index`", s.dir, prevModel, model, s.dir)
	case dim > 0 && prevDim > 0 && dim != prevDim:
		err = fmt.Errorf("kb: index at %s was built with dim %d but the config declares %d; "+
			"vectors from different dimensions are not comparable — "+
			"delete the index directory %s and re-run `openheron kb index`", s.dir, prevDim, dim, s.dir)
	}
	if err != nil {
		return err
	}

	s.mu.Lock()
	if model != "" {
		s.der.Model = model
	}
	if dim > 0 {
		s.der.Dim = dim
	}
	s.mu.Unlock()
	return nil
}

type ReconcileRequest struct {
	Corpus string

	Model string
	Dim   int

	Fresh []Entry

	Scope []string

	Embed func(ctx context.Context, entries []Entry) (map[string][]float32, error)
}

type ReconcileResult struct {
	ReEmbedded   int
	Unchanged    int
	Added        int
	Removed      int
	removedPaths []string
	Attempts     int
	Warnings     []string
}

func (s *Store) Reconcile(ctx context.Context, req ReconcileRequest) (*ReconcileResult, error) {
	if req.Dim < 0 {
		return nil, fmt.Errorf("kb: dim must not be negative, got %d", req.Dim)
	}
	if err := s.SetIdentity(req.Model, req.Dim); err != nil {
		return nil, err
	}
	for _, e := range req.Fresh {
		if e.Path == "" {
			return nil, fmt.Errorf("kb: entry with empty path (title %q); "+
				"the path is the primary key of a catalog entry", e.Title)
		}
	}
	mdPath := filepath.Join(s.dir, catalogFile)

	var lastWarnings []string
	for attempt := 1; attempt <= maxCommitAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cur, warnings := s.readCatalog()
		lastWarnings = warnings

		// ★ The derived state is snapshotted under the lock, and the snapshot
		//   is a **deep** copy: s.der.Entries is a map that install and
		//   SetVectors mutate in place, so a shallow copy would still be
		//   shared with the writer — and mergeCatalog iterates it.
		s.mu.RLock()
		der := cloneDerived(s.der)
		s.mu.RUnlock()

		merged, next, res := mergeCatalog(cur, der, req)

		if req.Embed != nil {
			vectors, err := req.Embed(ctx, merged.Entries)
			if err != nil {
				return nil, err
			}
			freshSet := make(map[string]bool, len(req.Fresh))
			for _, e := range req.Fresh {
				freshSet[e.Path] = true
			}
			for p := range vectors {
				if !freshSet[p] {
					res.ReEmbedded++
				}
			}
			embedInto(&next, merged.Entries, vectors)
		}

		res.Unchanged = len(merged.Entries) - res.ReEmbedded

		merged.Header.Revision++
		merged.Header.Updated = nowFn()
		body := RenderCatalog(merged)
		if err := persist.AtomicWrite(mdPath, body, newTmpSuffix()); err != nil {
			return nil, fmt.Errorf("kb: write %s: %w", mdPath, err)
		}

		if s.afterWrite != nil {
			s.afterWrite()
		}

		got, err := os.ReadFile(mdPath)
		if err != nil {
			return nil, fmt.Errorf("kb: read back %s: %w", mdPath, err)
		}
		if !bytes.Equal(got, body) {
			continue
		}

		res.Attempts = attempt
		res.Warnings = lastWarnings
		s.install(merged, next, req, res)
		return res, nil
	}
	return nil, fmt.Errorf("kb: %s was modified while writing (%d attempts); "+
		"your edit collided with an index run — re-run `openheron kb index`",
		mdPath, maxCommitAttempts)
}

func mergeCatalog(cur Catalog, der Derived, req ReconcileRequest) (Catalog, Derived, *ReconcileResult) {
	fresh := make(map[string]Entry, len(req.Fresh))
	for _, e := range req.Fresh {
		fresh[e.Path] = e
	}
	scope := map[string]bool{}
	for _, p := range req.Scope {
		scope[p] = true
	}
	willEmbed := req.Embed != nil

	next := der.clone()
	if next.Entries == nil {
		next.Entries = map[string]DerivedEntry{}
	}
	next.IndexVersion = IndexVersion
	if req.Model != "" {
		next.Model = req.Model
	}
	if req.Dim > 0 {
		next.Dim = req.Dim
	}

	out := cur
	if out.Entries == nil {
		out.Entries = make([]Entry, 0, len(fresh))
	}
	out.Header.Corpus = req.Corpus
	if out.Header.IndexVersion == 0 {
		out.Header.IndexVersion = IndexVersion
	}
	if out.Header.Writer == "" {
		out.Header.Writer = newWriterToken()
	}
	if out.Header.Created.IsZero() {
		out.Header.Created = nowFn()
	}

	res := &ReconcileResult{}

	kept := make([]Entry, 0, len(out.Entries)+len(fresh))
	for _, cur_e := range out.Entries {
		if !scope[cur_e.Path] && len(scope) > 0 {
			kept = append(kept, cur_e)
			continue
		}
		f, ok := fresh[cur_e.Path]
		if !ok {
			delete(next.Entries, cur_e.Path)
			res.Removed++
			res.removedPaths = append(res.removedPaths, cur_e.Path)
			continue
		}
		e := applyFresh(cur_e, f, der.Entries[cur_e.Path], &next, res, willEmbed)
		kept = append(kept, e)
		delete(fresh, cur_e.Path)
	}

	for _, f := range fresh {
		if len(scope) > 0 && !scope[f.Path] {
			continue
		}
		e := applyFresh(Entry{}, f, DerivedEntry{}, &next, res, willEmbed)
		e.Stale = false
		kept = append(kept, e)
		res.Added++
	}

	out.Entries = kept
	return out, next, res
}

func applyFresh(cur, fresh Entry, der DerivedEntry, next *Derived, res *ReconcileResult, willEmbed bool) Entry {
	out := fresh
	if out.MTime == 0 && out.Size == 0 {
		out.MTime, out.Size = cur.MTime, cur.Size
	}

	de := der
	if ownsHuman(cur.Title, de.WroteTitle) {
		out.Title = cur.Title
	}
	if ownsHuman(cur.Summary, de.WroteSummary) {
		out.Summary = cur.Summary

		changed := fresh.MTime != de.ObsMTime || fresh.Size != de.ObsSize

		if cur.Summary == de.ObsSummary {
			out.Stale = cur.Stale || changed
		} else {
			out.Stale = changed
		}
	} else {
		out.Stale = false
	}
	de.ObsSummary, de.ObsMTime, de.ObsSize = out.Summary, fresh.MTime, fresh.Size

	de.EmbedText = out.EmbedText()

	if !willEmbed {
		de.Vector = nil
	}

	if out.EmbedText() == der.EmbedText && (!willEmbed || len(der.Vector) > 0) {
		res.Unchanged++
	} else {
		res.ReEmbedded++
	}

	de.WroteTitle, de.WroteSummary = fresh.Title, fresh.Summary
	if next.Entries == nil {
		next.Entries = map[string]DerivedEntry{}
	}
	next.Entries[out.Path] = de
	return out
}

func embedInto(next *Derived, entries []Entry, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}
	text := make(map[string]string, len(entries))
	for _, e := range entries {
		text[e.Path] = e.EmbedText()
	}
	for p, v := range vectors {
		de, ok := next.Entries[p]
		if !ok {
			continue
		}
		de.Vector = v
		if s, ok := text[p]; ok {
			de.EmbedText = s
		}
		next.Entries[p] = de
	}
}

func ownsHuman(current, wrote string) bool {
	if current == wrote {
		return false
	}
	return current != ""
}

func sameVector(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func newTmpSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ".tmp." + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return ".tmp." + hex.EncodeToString(b[:])
}

func cloneDerived(d Derived) Derived {
	out := d
	out.Entries = make(map[string]DerivedEntry, len(d.Entries))
	for k, v := range d.Entries {
		out.Entries[k] = v
	}
	return out
}

func (d Derived) clone() Derived { return cloneDerived(d) }

func cloneCatalog(c Catalog) Catalog {
	out := c
	out.Entries = append([]Entry(nil), c.Entries...)
	for i := range out.Entries {
		out.Entries[i].Sections = append([]Section(nil), c.Entries[i].Sections...)
		out.Entries[i].Links = append([]string(nil), c.Entries[i].Links...)
	}
	return out
}

func (s *Store) install(cat Catalog, next Derived, req ReconcileRequest, res *ReconcileResult) {
	touched := make(map[string]bool, len(req.Fresh)+len(res.removedPaths))
	for _, e := range req.Fresh {
		touched[e.Path] = true
	}

	// ★ The stamp is the catalog file's stat, and the catalog has already
	//   been written — so it can be stat'ed here, before the lock, and the
	//   result assigned inside it. Stat'ing under the lock would hold it
	//   across I/O, and assigning it after the unlock would write a guarded
	//   field outside the guard.
	stamp, _ := statStamp(filepath.Join(s.dir, catalogFile))

	s.mu.Lock()
	if s.der.Entries == nil {
		s.der.Entries = make(map[string]DerivedEntry, len(touched))
	}
	for p, de := range next.Entries {
		old, had := s.der.Entries[p]
		if !had || old.EmbedText != de.EmbedText || !sameVector(old.Vector, de.Vector) {
			touched[p] = true
		}
	}

	for p := range touched {
		if de, ok := next.Entries[p]; ok {
			s.der.Entries[p] = de
			if len(de.Vector) > 0 {
				s.der.Dim = len(de.Vector)
			}
		}
	}
	for _, p := range res.removedPaths {
		delete(s.der.Entries, p)
	}
	s.der.IndexVersion = IndexVersion
	if req.Model != "" {
		s.der.Model = req.Model
	}
	if req.Dim > 0 {
		s.der.Dim = req.Dim
	}
	s.cat = cloneCatalog(cat)
	s.stamp = stamp
	s.mu.Unlock()

	if err := s.saveSidecar(); err != nil {
		obs.Logger().Warn("[kb] sidecar write failed, vectors will be rebuilt next run", "err", err)
	}
}

func (s *Store) SetVectors(vectors map[string][]float32) error {
	if len(vectors) == 0 {
		return nil
	}
	s.mu.Lock()
	for path, v := range vectors {
		de := s.der.Entries[path]
		de.Vector = v
		s.der.Entries[path] = de
		if len(v) > 0 {
			s.der.Dim = len(v)
		}
	}
	s.mu.Unlock()
	return s.saveSidecar()
}

func (s *Store) saveSidecar() error {
	s.mu.RLock()
	der := cloneDerived(s.der)
	s.mu.RUnlock()

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&der); err != nil {
		return fmt.Errorf("kb: encode sidecar: %w", err)
	}
	if err := persist.AtomicWrite(filepath.Join(s.dir, sidecarFile), buf.Bytes(), newTmpSuffix()); err != nil {
		return fmt.Errorf("kb: write sidecar: %w", err)
	}
	return nil
}

func (s *Store) readCatalog() (Catalog, []string) {
	b, err := os.ReadFile(filepath.Join(s.dir, catalogFile))
	if err != nil {
		return Catalog{Header: Header{IndexVersion: IndexVersion}}, nil
	}
	c, err := ParseCatalog(b)
	if err != nil {
		obs.Logger().Error("[kb] catalog.md is unreadable, refusing to overwrite it", "err", err)
		return Catalog{Header: Header{IndexVersion: IndexVersion}}, []string{
			fmt.Sprintf("catalog.md is unreadable: %v", err),
		}
	}
	if c.Header.IndexVersion == 0 {
		c.Header.IndexVersion = IndexVersion
	}
	return c, c.Warnings
}

// load reads the catalog and the sidecar into the store.
//
// ★ It does not take the lock: it runs while the store is being opened,
//
//	before anybody else can reach it. The lazy path — refresh — is the one
//	that can race, and that one is locked.
func (s *Store) load() error {
	b, err := os.ReadFile(filepath.Join(s.dir, catalogFile))
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("kb: read %s: %w", filepath.Join(s.dir, catalogFile), err)
		}
		s.cat = Catalog{Header: Header{IndexVersion: IndexVersion, Created: nowFn(), Writer: newWriterToken()}}
	} else {
		c, perr := ParseCatalog(b)
		if perr != nil {
			return fmt.Errorf("kb: parse %s: %w", filepath.Join(s.dir, catalogFile), perr)
		}
		if c.Header.IndexVersion != 0 && c.Header.IndexVersion != IndexVersion {
			return fmt.Errorf("kb: %s was written by extraction rules v%d but this build understands v%d; "+
				"remove %s and re-run `openheron kb index`",
				filepath.Join(s.dir, catalogFile), c.Header.IndexVersion, IndexVersion, s.dir)
		}
		if c.Header.IndexVersion == 0 {
			c.Header.IndexVersion = IndexVersion
		}
		s.cat = c
	}
	if st, ok := statStamp(filepath.Join(s.dir, catalogFile)); ok {
		s.stamp = st
	}

	sb, err := os.ReadFile(filepath.Join(s.dir, sidecarFile))
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("kb: read %s: %w", filepath.Join(s.dir, sidecarFile), err)
		}
		s.der = Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}
		return nil
	}
	var der Derived
	if err := gob.NewDecoder(bytes.NewReader(sb)).Decode(&der); err != nil {
		obs.Logger().Warn("[kb] sidecar is unreadable, vectors will be rebuilt next run", "err", err)
		der = Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}
	}
	if der.Entries == nil {
		der.Entries = map[string]DerivedEntry{}
	}
	if der.IndexVersion != 0 && der.IndexVersion != IndexVersion {
		obs.Logger().Warn("[kb] sidecar was written by another build; vectors will be rebuilt",
			"written_by_version", der.IndexVersion, "this_build_version", IndexVersion)
		der = Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}
	}
	if der.IndexVersion == 0 {
		der.IndexVersion = IndexVersion
	}
	s.der = der
	return nil
}

func (s *Store) refresh() {
	st, ok := statStamp(filepath.Join(s.dir, catalogFile))
	if !ok {
		return
	}
	s.mu.RLock()
	same := s.stamp == st
	s.mu.RUnlock()
	if same {
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.RLock()
	same = s.stamp == st
	s.mu.RUnlock()
	if same {
		return
	}

	b, err := os.ReadFile(filepath.Join(s.dir, catalogFile))
	if err != nil {
		return
	}
	c, err := ParseCatalog(b)
	if err != nil {
		return
	}

	var der Derived
	if sb, err := os.ReadFile(filepath.Join(s.dir, sidecarFile)); err == nil {
		if err := gob.NewDecoder(bytes.NewReader(sb)).Decode(&der); err != nil {
			der = Derived{}
		}
	}
	if der.Entries == nil {
		der.Entries = map[string]DerivedEntry{}
	}

	s.mu.Lock()
	s.cat = c
	s.der = der
	s.stamp = st
	s.mu.Unlock()
}

func statStamp(p string) (fileStamp, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{mtime: fi.ModTime().UnixNano(), size: fi.Size(), valid: true}, true
}

var registerGob = []any{
	Derived{},
	DerivedEntry{},
	[]float32{},
}

func init() {
	for _, v := range registerGob {
		gob.Register(v)
	}
}
