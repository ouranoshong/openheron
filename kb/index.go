package kb

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

const (
	MetaSource = "source"
	MetaEntry  = "kb_entry"
)

var corpusExt = ".md"

type IndexOptions struct {
	// CorpusDir is the directory to index.
	//
	// ★ It is a **path**, not the corpus itself: the corpus is the set of
	//   documents under it, and the name of that set is derived from this
	//   path's last segment (see corpusName). The configuration key says
	//   the same thing — `corpus_dir`.
	CorpusDir string

	Headers map[string]string
}

type Result struct {
	Files      int
	Changed    int
	Skipped    int
	Removed    int
	ReEmbedded int
	Unchanged  int
	Attempts   int
	Total      int
	Warnings   []string
	DurationMS int64
}

func Index(ctx context.Context, ix *FileIndexer, opts IndexOptions, paths []string) (
	*Result, error) {

	started := time.Now()
	res := &Result{}
	st := ix.StoreRef()

	prev := st.Entries()
	prevFiles := make(map[string]Entry, len(prev))
	for _, e := range prev {
		prevFiles[e.Path] = e
	}

	targets, cerr := collect(opts.CorpusDir, paths)
	if cerr != nil {
		return nil, cerr
	}

	fresh := make([]Entry, 0, len(targets))
	for _, t := range targets {
		rel := relPath(opts.CorpusDir, t.path)

		if old, ok := prevFiles[rel]; ok && old.MTime == t.mtime.UnixNano() && old.Size == t.size {
			res.Skipped++
		} else {
			res.Changed++
		}

		entry, err := extractFile(rel, t)
		if err != nil {
			return nil, err
		}
		fresh = append(fresh, entry)
	}
	res.Files = len(fresh)

	res2, err := ix.Reconcile(ctx, ReconcileRequest{
		Corpus: corpusName(opts.CorpusDir),
		Fresh:  fresh,
		Scope:  paths,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("kb: after %d files this round: %w", res.Changed, err)
	}
	if res2 != nil {
		res.ReEmbedded = res2.ReEmbedded
		res.Unchanged = res2.Unchanged
		res.Attempts = res2.Attempts
		res.Removed = res2.Removed
		res.Warnings = res2.Warnings
	}

	res.Total = len(st.Entries())
	res.DurationMS = time.Since(started).Milliseconds()
	return res, nil
}

func corpusName(abs string) string {
	clean := strings.TrimRight(filepath.ToSlash(abs), "/")
	if i := strings.LastIndex(clean, "/"); i >= 0 {
		return clean[i+1:]
	}
	return clean
}

func extractFile(rel string, t corpusFile) (Entry, error) {
	body, err := os.ReadFile(t.path)
	if err != nil {
		return Entry{}, fmt.Errorf("kb: read %s: %w", rel, err)
	}
	e := Extract(rel, string(body))
	e.MTime, e.Size = t.mtime.UnixNano(), t.size
	return e, nil
}

func docOf(e Entry) *schema.Document {
	return &schema.Document{
		ID:      e.Path,
		Content: e.EmbedText(),
		MetaData: map[string]any{
			MetaEntry:  e,
			MetaSource: e.Path,
		},
	}
}

type corpusFile struct {
	path  string
	mtime time.Time
	size  int64
}

func collect(corpusDir string, paths []string) ([]corpusFile, error) {
	if corpusDir == "" {
		return nil, fmt.Errorf("kb: corpus directory is empty")
	}
	info, err := os.Stat(corpusDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kb: stat corpus directory %s: %w", corpusDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("kb: corpus directory %s is not a directory", corpusDir)
	}

	if len(paths) > 0 {
		out := make([]corpusFile, 0, len(paths))
		for _, p := range paths {
			abs := filepath.Join(corpusDir, filepath.Clean("/"+p))
			fi, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("kb: stat %s: %w", p, err)
			}
			out = append(out, corpusFile{path: abs, mtime: fi.ModTime(), size: fi.Size()})
		}
		return out, nil
	}

	var out []corpusFile
	err = filepath.WalkDir(corpusDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != corpusDir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != corpusExt {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, corpusFile{path: p, mtime: fi.ModTime(), size: fi.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("kb: walk corpus directory %s: %w", corpusDir, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func relPath(corpusDir, p string) string {
	r, err := filepath.Rel(corpusDir, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}
