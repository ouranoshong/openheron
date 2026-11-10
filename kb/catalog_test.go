package kb

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func sampleCatalog() Catalog {
	t0 := time.Date(2026, 10, 16, 0, 41, 12, 0, time.UTC)
	return Catalog{
		Header: Header{
			Corpus:       "docs",
			Docs:         2,
			Revision:     7,
			Writer:       "3f2a1c",
			IndexVersion: 2,
			Model:        "local/embed",
			Dim:          1024,
			Created:      t0,
			Updated:      t0,
		},
		Entries: []Entry{
			{
				Path:     "config-design.md",
				Title:    "openheron 配置系统设计",
				Summary:  "配置系统：两级合并、写入门、试合并、热更新换代",
				Sections: []Section{{Title: "0. 一句话", Line: 12}, {Title: "8.5 换代的实际接线", Line: 880}},
				Links:    []string{"agent-design.md", "kb-design.md"},
				MTime:    1792074189670806630,
				Size:     41234,
			},
			{
				Path:              "sub/deep.md",
				Title:             "深层文档",
				Sections:          []Section{{Title: "A (B)"}},
				SectionsTruncated: 9,
				Links:             []string{"../config-design.md"},
				MTime:             1,
				Size:              2,
				Stale:             true,
			},
		},
	}
}

func TestCatalogRoundTrip(t *testing.T) {
	want := sampleCatalog()
	want.Sort()

	first := RenderCatalog(want)
	got, err := ParseCatalog(first)
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("round trip produced warnings: %v", got.Warnings)
	}

	if got.Header.Docs != len(want.Entries) {
		t.Errorf("Docs = %d, want %d", got.Header.Docs, len(want.Entries))
	}
	got.Header.Docs = want.Header.Docs
	if !want.Header.Created.Equal(got.Header.Created) {
		t.Errorf("Created = %v, want %v", got.Header.Created, want.Header.Created)
	}
	if !want.Header.Updated.Equal(got.Header.Updated) {
		t.Errorf("Updated = %v, want %v", got.Header.Updated, want.Header.Updated)
	}
	got.Header.Created, got.Header.Updated = want.Header.Created, want.Header.Updated
	if !reflect.DeepEqual(got.Header, want.Header) {
		t.Errorf("Header mismatch:\n got %+v\nwant %+v", got.Header, want.Header)
	}

	if !reflect.DeepEqual(got.Entries, want.Entries) {
		t.Errorf("Entries mismatch:\n got %+v\nwant %+v", got.Entries, want.Entries)
	}

	second := RenderCatalog(got)
	if string(first) != string(second) {
		t.Errorf("render → parse → render is not byte-stable:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
}

func TestRenderCatalogUsesTheEnglishHeader(t *testing.T) {
	out := string(RenderCatalog(sampleCatalog()))
	for _, want := range []string{
		"# Knowledge Base Catalog",
		"corpus_dir: docs",
		"extraction rule v2",
		"model local/embed (dim 1024)",
		"updated at 2026-10-16T00:41:12Z",
		"created at 2026-10-16T00:41:12Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered header is missing %q:\n%s", want, out)
		}
	}
}

// TestParseCatalogAcceptsTheLegacyChineseHeader proves that a catalog.md
// written before the header was translated still parses: without the legacy
// prefixes the corpus, revision, model, dim and timestamps would silently
// become zero.
func TestParseCatalogAcceptsTheLegacyChineseHeader(t *testing.T) {
	body := "# 知识库目录\n\n" +
		"> 语料: docs  ·  1 份文档  ·  rev 3  ·  writer abc  ·  建于 2026-10-15T00:00:00Z\n" +
		"> 抽取规则 v2  ·  模型 local/embed (dim 1024)  ·  更新于 2026-10-16T00:41:12Z\n"

	c, err := ParseCatalog([]byte(body))
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	h := c.Header
	if h.Corpus != "docs" {
		t.Errorf("Corpus = %q, want docs", h.Corpus)
	}
	if h.Revision != 3 {
		t.Errorf("Revision = %d, want 3", h.Revision)
	}
	if h.Writer != "abc" {
		t.Errorf("Writer = %q, want abc", h.Writer)
	}
	if h.IndexVersion != 2 {
		t.Errorf("IndexVersion = %d, want 2", h.IndexVersion)
	}
	if h.Model != "local/embed" {
		t.Errorf("Model = %q, want local/embed", h.Model)
	}
	if h.Dim != 1024 {
		t.Errorf("Dim = %d, want 1024", h.Dim)
	}
	created := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 10, 16, 0, 41, 12, 0, time.UTC)
	if !h.Created.Equal(created) {
		t.Errorf("Created = %v, want %v", h.Created, created)
	}
	if !h.Updated.Equal(updated) {
		t.Errorf("Updated = %v, want %v", h.Updated, updated)
	}
}

// TestParseCatalogAcceptsThePreRenameCorpusHeader proves that a catalog.md
// written before the `corpus` → `corpus_dir` rename still parses.
//
// ★ The rename was to the **configuration key**; the catalog.md header is a
//
//	file format, and a rename does not rewrite the files already on disk.
//	Without this case the corpus of every existing index would silently
//	become "" — and an empty corpus is "the knowledge base is off".
func TestParseCatalogAcceptsThePreRenameCorpusHeader(t *testing.T) {
	for _, prefix := range []string{"corpus:", "语料:", "语料目录:"} {
		body := "# Knowledge Base Catalog\n\n" +
			"> " + prefix + " docs  ·  1 document  ·  rev 3  ·  writer abc\n"

		c, err := ParseCatalog([]byte(body))
		if err != nil {
			t.Fatalf("ParseCatalog(%q): %v", prefix, err)
		}
		if c.Header.Corpus != "docs" {
			t.Errorf("%q: Corpus = %q, want docs", prefix, c.Header.Corpus)
		}
		if c.Header.Revision != 3 {
			t.Errorf("%q: Revision = %d, want 3", prefix, c.Header.Revision)
		}
	}
}

func TestRenderNormalizesButKeepsDocumentOrder(t *testing.T) {
	a := sampleCatalog()

	b := sampleCatalog()
	b.Entries[0], b.Entries[1] = b.Entries[1], b.Entries[0]
	b.Entries[1].Links = []string{"kb-design.md", "agent-design.md"}

	if string(RenderCatalog(a)) != string(RenderCatalog(b)) {
		t.Error("render depends on entry or link order; those are sets and must be normalized")
	}

	c := sampleCatalog()
	s := c.Entries[0].Sections
	c.Entries[0].Sections = []Section{s[1], s[0]}
	if string(RenderCatalog(a)) == string(RenderCatalog(c)) {
		t.Error("render normalized section order; sections are document order and must be preserved")
	}
}

func TestParseCatalogLenient(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantPath []string
		wantWarn int
	}{
		{
			name: "a human-added note line is ignored without a warning",
			body: catalogHead() + `
## a.md

> 摘要

title: A
mtime: 1
size: 2
sections: S (L1)
links:
stale: false
这里是给人自己看的备注，不是 key: value
`,
			wantPath: []string{"a.md"},
			wantWarn: 0,
		},
		{
			name:     "a missing key does not error, it yields the zero value",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\ntitle: A\n",
			wantPath: []string{"a.md"},
			wantWarn: 0,
		},
		{
			name:     "a summary written as > without a space still reads",
			body:     catalogHead() + "\n## a.md\n\n>摘要没空格\n\ntitle: A\n",
			wantPath: []string{"a.md"},
			wantWarn: 0,
		},
		{
			name:     "a misspelled key records one warning and the entry stays",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\nsumary: 拼错了\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "a non-integer mtime records one warning and the entry stays",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\nmtime: 昨天\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "an ambiguous stale value records one warning and is not taken as true",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\nstale: yes\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "an empty path on a ## line skips the whole section",
			body:     catalogHead() + "\n## \n\n> 摘要\n\n## a.md\n\n> 摘要\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "multiple summaries keep the first one and record a warning",
			body:     catalogHead() + "\n## a.md\n\n> 第一条\n\n> 第二条\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseCatalog([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseCatalog returned an error: %v (lenient parsing must not fail because a human typed it wrong)", err)
			}
			var got []string
			for _, e := range c.Entries {
				got = append(got, e.Path)
			}
			if !reflect.DeepEqual(got, tc.wantPath) {
				t.Errorf("paths = %v, want %v", got, tc.wantPath)
			}
			if len(c.Warnings) != tc.wantWarn {
				t.Errorf("Warnings = %v, want %d of them", c.Warnings, tc.wantWarn)
			}
		})
	}
}

func TestParseCatalogRejectsNonCatalog(t *testing.T) {
	if _, err := ParseCatalog([]byte("这是一份普通的 markdown，不是目录。\n")); err == nil {
		t.Error("ParseCatalog accepted a file with no catalog header; that means a " +
			"corrupted catalog loads as an empty one instead of being noticed")
	}
}

func TestParseCatalogSummaryIsSingleLine(t *testing.T) {
	c := sampleCatalog()
	c.Entries[0].Summary = "第一行\n第二行"
	out := string(RenderCatalog(c))

	if strings.Contains(out, "> 第一行\n第二行") {
		t.Error("summary rendered across two lines; that breaks lenient parsing")
	}
	got, err := ParseCatalog([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if want := "第一行 第二行"; got.Entries[0].Summary != want {
		t.Errorf("Summary = %q, want %q", got.Entries[0].Summary, want)
	}
}

func TestSectionParseKeepBrackets(t *testing.T) {
	cases := []struct {
		in    string
		want  Section
		notes string
	}{
		{"配置语义（热更新）", Section{Title: "配置语义（热更新）"}, "名字里的全角括号"},
		{"§12.2.2 为什么 5xx (L40)", Section{Title: "§12.2.2 为什么 5xx", Line: 40}, "结尾的行号"},
		{"A (L0)", Section{Title: "A (L0)"}, "行号 0 不是有效行号，整串当名字"},
		{"A (L)", Section{Title: "A (L)"}, "没有数字"},
		{"x (L5) (L9)", Section{Title: "x (L5)", Line: 9}, "取最后一个"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseSection(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseSection(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSectionsTruncatedRendered(t *testing.T) {
	c := sampleCatalog()
	out := string(RenderCatalog(c))
	if !strings.Contains(out, "[+9]") {
		t.Errorf("truncation not rendered; the model would think the doc has 1 section:\n%s", out)
	}
	got, err := ParseCatalog([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if got.Entries[1].SectionsTruncated != 9 {
		t.Errorf("SectionsTruncated = %d, want 9", got.Entries[1].SectionsTruncated)
	}
}

func TestRenderOwnershipFormat(t *testing.T) {
	out := string(RenderCatalog(sampleCatalog()))
	idx := strings.Index(out, "## config-design.md")
	if idx < 0 {
		t.Fatal("entry heading missing")
	}
	sec := out[idx:]

	if !strings.Contains(sec, "\n> 配置系统：两级合并、写入门、试合并、热更新换代\n") {
		t.Errorf("summary must render as a `> ` blockquote:\n%s", sec)
	}
	for _, k := range []string{keyTitle, keyMTime, keySize, keySections, keyLinks, keyStale} {
		if !strings.Contains(sec, "\n"+k+": ") {
			t.Errorf("key %q must render as its own `key: value` line:\n%s", k, sec)
		}
	}
	if strings.Contains(sec, "> "+keyTitle+":") {
		t.Error("a machine field leaked into the human-owned summary block")
	}
}

func catalogHead() string {
	return "# Knowledge Base Catalog\n\n> corpus: docs  ·  1 document  ·  rev 1  ·  writer abc  ·  created at 2026-10-16T00:41:12Z\n" +
		"> extraction rule v2  ·  updated at 2026-10-16T00:41:12Z\n"
}
