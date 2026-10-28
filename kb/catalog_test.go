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

// TestCatalogRoundTrip 是本文件最要紧的一条：渲染 → 解析 → 渲染必须**逐字节
// 相同**。
//
// ★ 它是乐观提交（§7.3 ④）能成立的前提 —— 写后读比较依赖「我写的那份
//
//	还在」，而「还在」就是逐字节相同。若往返不等，冲突检测会报出一堆
//	**并不存在**的冲突，而它的表现形式是「索引永远重试三次然后失败」。
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

	// Header：Docs 由解析器按条目数回填，其余逐字段比。
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

// TestRenderNormalizesButKeepsDocumentOrder 渲染归一化**该归一化的**，留
// 住**不该归一化的**。
//
// ★ 这是「渲染可预测」的另一半：如果两次渲染同样的内容得到不同的字节，
//
//	写后读比较就分不清「我写的还在」与「我写的是同一份的另一个副本」，
//	而后者会被当成冲突 —— 于是并发一次都不发生，重试先耗尽。
//
// ★ 但章节顺序**必须**保留文档序 —— 排序会毁掉「第 1 节在第 3 节前面」
//
//	这件事，而那正是模型照着跳的依据。
func TestRenderNormalizesButKeepsDocumentOrder(t *testing.T) {
	a := sampleCatalog()

	b := sampleCatalog()
	b.Entries[0], b.Entries[1] = b.Entries[1], b.Entries[0] // 条目换序
	b.Entries[1].Links = []string{"kb-design.md", "agent-design.md"}

	if string(RenderCatalog(a)) != string(RenderCatalog(b)) {
		t.Error("render depends on entry or link order; those are sets and must be normalized")
	}

	// 章节换序**必须**改渲染结果，否则就说明归一化做过头了。
	c := sampleCatalog()
	s := c.Entries[0].Sections
	c.Entries[0].Sections = []Section{s[1], s[0]}
	if string(RenderCatalog(a)) == string(RenderCatalog(c)) {
		t.Error("render normalized section order; sections are document order and must be preserved")
	}
}

// TestParseCatalogLenient 宽松解析的验收（§12.11）。
func TestParseCatalogLenient(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantPath []string
		wantWarn int
	}{
		{
			name: "人加的备注行被忽略且不报警告",
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
			name:     "缺少 key 不报错，取零值",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\ntitle: A\n",
			wantPath: []string{"a.md"},
			wantWarn: 0,
		},
		{
			name:     "摘要写成 > 而非 > 也能读",
			body:     catalogHead() + "\n## a.md\n\n>摘要没空格\n\ntitle: A\n",
			wantPath: []string{"a.md"},
			wantWarn: 0,
		},
		{
			name:     "拼错的 key 记一条警告，条目仍留下",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\nsumary: 拼错了\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "非整数的 mtime 记一条警告，条目仍留下",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\nmtime: 昨天\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "stale 的含糊写法记一条警告，不当成 true",
			body:     catalogHead() + "\n## a.md\n\n> 摘要\n\nstale: yes\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "空路径的 ## 行跳过整节",
			body:     catalogHead() + "\n## \n\n> 摘要\n\n## a.md\n\n> 摘要\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
		{
			name:     "多条摘要保留第一条并报警告",
			body:     catalogHead() + "\n## a.md\n\n> 第一条\n\n> 第二条\n",
			wantPath: []string{"a.md"},
			wantWarn: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseCatalog([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseCatalog returned an error: %v (宽松解析不该因人写错而失败)", err)
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

// TestParseCatalogSummaryIsSingleLine 摘要在渲染里必须是单行。
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
	// 换行被合并成空格，而不是丢掉后半句 —— 后者是静默丢数据。
	if want := "第一行 第二行"; got.Entries[0].Summary != want {
		t.Errorf("Summary = %q, want %q", got.Entries[0].Summary, want)
	}
}

// TestSectionParseKeepsBrackets 名字里的括号不该被当成行号。
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

// TestSectionsTruncatedRendered 砍掉数必须出现在渲染里。
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

// TestRenderOwnershipFormat 摘要用 `>`、其余用 `key: value` ——
// 格式本身就是所有权声明（§5.2）。
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
	// ★ 反过来也要成立：机器字段**不得**出现在 `>` 块里。反了的话解析
	//   会把 title 当摘要，而那份文件人在编辑。
	if strings.Contains(sec, "> "+keyTitle+":") {
		t.Error("a machine field leaked into the human-owned summary block")
	}
}

func catalogHead() string {
	return "# 知识库目录\n\n> 语料: docs  ·  1 份文档  ·  rev 1  ·  writer abc  ·  建于 2026-10-16T00:41:12Z\n" +
		"> 抽取规则 v2  ·  更新于 2026-10-16T00:41:12Z\n"
}
