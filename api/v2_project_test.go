package api

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestProjectFilesListsRelativePathsAndRanks(t *testing.T) {
	srv, _, cfgRoot := settingsEnv(t)
	root := filepath.Dir(cfgRoot)

	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("web/src/components/composer.tsx", "export {}")
	write("a/b/composer.tsx", "export {}")
	write("docs/frontend-design.md", "# design")
	write("docs/empty/placeholder.txt", "x")

	link := filepath.Join(root, "link.tsx")
	if err := os.Symlink(filepath.Join(root, "web/src/components/composer.tsx"), link); err != nil {
		t.Fatal(err)
	}

	get := func(rawQuery string) projectFileList {
		t.Helper()
		got := callJSON(t, srv, "GET", "/v2/project/files"+rawQuery, nil)
		if got.code != consts.StatusOK {
			t.Fatalf("%s = %d (%s)", rawQuery, got.code, got.body)
		}
		var out projectFileList
		if err := json.Unmarshal([]byte(got.body), &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", rawQuery, err, got.body)
		}
		return out
	}

	got := get("?q=" + url.QueryEscape("COMPOSER"))
	paths := map[string]bool{}
	for _, f := range got.Files {
		paths[f.Path] = true
		if filepath.IsAbs(f.Path) || strings.Contains(f.Path, "\\") {
			t.Fatalf("path must be relative and slash-separated: %q", f.Path)
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("stat %q: %v", f.Path, err)
		}
		if info.IsDir() {
			t.Fatalf("directories must not be listed: %q", f.Path)
		}
	}
	if !paths["web/src/components/composer.tsx"] || !paths["a/b/composer.tsx"] {
		t.Fatalf("composer files missing: %+v", got.Files)
	}
	if paths["docs/frontend-design.md"] {
		t.Fatalf("non-matching file leaked: %+v", got.Files)
	}
	if paths["link.tsx"] {
		t.Fatalf("symlink must not be listed: %+v", got.Files)
	}
	// Same basename bucket: the shorter path ranks first.
	if got.Files[0].Path != "a/b/composer.tsx" {
		t.Fatalf("first = %q, want a/b/composer.tsx", got.Files[0].Path)
	}

	// A path-only hit ranks below basename hits.
	pathHit := get("?q=" + url.QueryEscape("docs/frontend"))
	if len(pathHit.Files) != 1 || pathHit.Files[0].Path != "docs/frontend-design.md" {
		t.Fatalf("path-only hit = %+v", pathHit.Files)
	}

	one := get("?q=" + url.QueryEscape("components/composer") + "&limit=1")
	if len(one.Files) != 1 || one.Files[0].Path != "web/src/components/composer.tsx" {
		t.Fatalf("components query = %+v", one.Files)
	}
	if one.Truncated {
		t.Fatalf("single match must not be truncated: %+v", one)
	}

	limited := get("?q=composer&limit=1")
	if len(limited.Files) != 1 || !limited.Truncated {
		t.Fatalf("limit=1 = %+v, want one file and truncated", limited)
	}

	// An empty query is BFS order, files only, and still truncates when more
	// files exist than the page size.
	all := get("?limit=2")
	if len(all.Files) != 2 || !all.Truncated {
		t.Fatalf("empty query limit=2 = %+v, want two files and truncated", all)
	}

	// Clamping, not an error: limit=0 means one.
	clamped := get("?limit=0")
	if len(clamped.Files) != 1 {
		t.Fatalf("limit=0 returned %d files", len(clamped.Files))
	}
}

func TestProjectFilesRejectsBadInput(t *testing.T) {
	srv, _, _ := settingsEnv(t)

	if got := callJSON(t, srv, "GET", "/v2/project/files?limit=abc", nil); got.code != consts.StatusBadRequest {
		t.Fatalf("bad limit = %d (%s)", got.code, got.body)
	}
	long := "?q=" + url.QueryEscape(strings.Repeat("x", maxProjectQueryLength+1))
	if got := callJSON(t, srv, "GET", "/v2/project/files"+long, nil); got.code != consts.StatusBadRequest {
		t.Fatalf("long query = %d (%s)", got.code, got.body)
	}
}

func TestProjectFilesWithoutConfigServiceIs503(t *testing.T) {
	srv := newV2Server(t)
	got := callJSON(t, srv, "GET", "/v2/project/files?q=x", nil)
	if got.code != consts.StatusServiceUnavailable {
		t.Fatalf("= %d, want 503 (%s)", got.code, got.body)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "config_unavailable" {
		t.Fatalf("code = %q, want config_unavailable", body.Code)
	}
}
