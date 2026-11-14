package api

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Bounds chosen so a query on a huge tree still answers promptly. They are
// deliberately not directory exclusions: the endpoint lists every file, and
// truncation is reported instead of silently hiding whole subtrees.
const (
	maxProjectScanEntries = 50000
	maxProjectCandidates  = 2000
	maxProjectQueryLength = 256
)

type projectFile struct {
	Path string `json:"path"`
}

type projectFileList struct {
	Files     []projectFile `json:"files"`
	Truncated bool          `json:"truncated"`
}

type projectCandidate struct {
	rel  string
	base string
}

// v2ProjectFiles lists project files for the composer's @-mention picker.
//
// The project root is the directory that contains .openheron (see
// config.Service.ProjectRoot), not the session workspace: the picker is for
// "what can I reference in this project", while uploads live per session.
//
// ★ The project is named by the **query**: the picker lists the files of
//
//	one project, and the session on screen decides which one. ★ Without it
//	the scope decides — the directory this process serves — and a process
//	with no scope falls back to the default project root.
func (s *Server) v2ProjectFiles(ctx context.Context, c *app.RequestContext) {
	if s.cfgSvc == nil {
		s.v2Fail(c, consts.StatusServiceUnavailable, "config_unavailable",
			"config service not wired", false)
		return
	}

	q := strings.TrimSpace(c.Query("q"))
	if len(q) > maxProjectQueryLength {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "query is too long", false)
		return
	}

	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "limit must be a number", false)
			return
		}
		limit = n
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}

	root, status, code, msg := s.projectRootOf(ctx, c.Query("project"))
	if status != consts.StatusOK {
		s.v2Fail(c, status, code, msg, false)
		return
	}
	c.JSON(consts.StatusOK, listProjectFiles(root, strings.ToLower(q), limit))
}

// projectRootOf resolves the directory the @-mention picker lists.
//
// ★ The table is the only truth about the relation, so the root comes from
//
//	there: a project that was moved is listed under its new directory.
//
// ★ The three answers, in order: the project the query names; the project
//
//	this process focuses on; the default project root of the process.
func (s *Server) projectRootOf(ctx context.Context, id string) (string, int, string, string) {
	id = strings.TrimSpace(id)
	snap := s.projectSnapshot(ctx)

	if id != "" {
		if snap == nil {
			return "", consts.StatusServiceUnavailable, codeInternal, "the project registry is not wired"
		}
		p, ok := snap.Get(id)
		if !ok {
			return "", consts.StatusNotFound, codeProjectNotFound, "no such project: " + id
		}
		return p.Root, consts.StatusOK, "", ""
	}

	if scope := strings.TrimSpace(s.scopeProjectID); scope != "" && snap != nil {
		if p, ok := snap.Get(scope); ok {
			return p.Root, consts.StatusOK, "", ""
		}
	}
	return s.cfgSvc.DefaultProjectRoot(), consts.StatusOK, "", ""
}

// listProjectFiles walks the tree breadth-first so shallow files surface
// before deep ones. It never follows symlinks: a link could leave the project
// or form a cycle, and the picker only needs real project files.
func listProjectFiles(root, q string, limit int) projectFileList {
	cands := make([]projectCandidate, 0, limit)
	queue := []string{root}
	scanned := 0
	truncated := false

outer:
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		entries, err := os.ReadDir(dir)
		if err != nil {
			// A directory we cannot read is not a reason to fail the whole
			// listing; the rest of the project is still useful.
			continue
		}
		sortProjectEntries(entries)

		for _, entry := range entries {
			if scanned >= maxProjectScanEntries {
				truncated = true
				break outer
			}
			scanned++

			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			if entry.IsDir() {
				queue = append(queue, filepath.Join(dir, name))
				continue
			}

			rel, err := filepath.Rel(root, filepath.Join(dir, name))
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(rel), q) {
				continue
			}

			// For an empty query every file matches, so stop at the first
			// file beyond the requested page; there is no ranking to do.
			if q == "" && len(cands) >= limit {
				truncated = true
				break outer
			}
			if len(cands) >= maxProjectCandidates {
				truncated = true
				break outer
			}
			cands = append(cands, projectCandidate{rel: rel, base: strings.ToLower(name)})
			if q != "" && len(cands) == maxProjectCandidates {
				truncated = true
				break outer
			}
		}
	}

	if q != "" {
		sort.SliceStable(cands, func(i, j int) bool {
			a, b := cands[i], cands[j]
			ra, rb := projectMatchRank(a.base, q), projectMatchRank(b.base, q)
			if ra != rb {
				return ra < rb
			}
			if len(a.rel) != len(b.rel) {
				return len(a.rel) < len(b.rel)
			}
			return a.rel < b.rel
		})
	}

	if len(cands) > limit {
		cands = cands[:limit]
		truncated = true
	}

	files := make([]projectFile, 0, len(cands))
	for _, cand := range cands {
		files = append(files, projectFile{Path: cand.rel})
	}
	return projectFileList{Files: files, Truncated: truncated}
}

// projectMatchRank buckets basename hits ahead of path-only hits; within a
// bucket the caller breaks ties by path length and then lexically.
func projectMatchRank(base, q string) int {
	switch {
	case strings.HasPrefix(base, q):
		return 0
	case strings.Contains(base, q):
		return 1
	default:
		return 2
	}
}

// sortProjectEntries keeps the walk deterministic: directories first (so
// their files land on the next BFS level), visible before hidden, then name.
func sortProjectEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		ad, bd := a.IsDir(), b.IsDir()
		if ad != bd {
			return ad
		}
		if ad {
			ah, bh := strings.HasPrefix(a.Name(), "."), strings.HasPrefix(b.Name(), ".")
			if ah != bh {
				return !ah
			}
		}
		return a.Name() < b.Name()
	})
}
