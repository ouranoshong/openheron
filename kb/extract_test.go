package kb

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const kbStyleDoc = `# openheron 配置系统设计

> 状态：**已实施**（P0-P5 完成，验收见 §12）；此后新增 ` + "`config`" + ` / ` + "`llm`" + ` 两层
> 分册索引见 [architecture.md](architecture.md)。

父文档：[architecture.md](architecture.md)

## 0. 一句话

**索引必须与它的语料同生共死** —— 语料是项目根下的相对目录，索引跟着 ` + "`stateDir`" + ` 走。

## 1. 目标与非目标

| 做 | 不做 |
|---|---|
| 两级合并 | 不做合并视图 |

### 8.5 换代的实际接线

先装配再发布。

参见 [agent-design.md](agent-design.md) 与 [kb-design.md](kb-design.md)。
`

func TestExtractKBStyle(t *testing.T) {
	e := Extract("config-design.md", kbStyleDoc)

	if got, want := e.Path, "config-design.md"; got != want {
		t.Errorf("Path = %q, want %q (corpus-relative, no directory prefix)", got, want)
	}

	if got, want := e.Title, "openheron 配置系统设计"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := e.Summary, "**索引必须与它的语料同生共死** —— 语料是项目根下的相对目录，索引跟着 `stateDir` 走。"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	wantSections := []Section{
		{Title: "0. 一句话", Line: 8},
		{Title: "1. 目标与非目标", Line: 12},
		{Title: "8.5 换代的实际接线", Line: 18},
	}
	if !reflect.DeepEqual(e.Sections, wantSections) {
		t.Errorf("Sections = %+v, want %+v", e.Sections, wantSections)
	}
	if e.SectionsTruncated != 0 {
		t.Errorf("SectionsTruncated = %d, want 0", e.SectionsTruncated)
	}
	if got, want := e.Links, []string{"architecture.md", "agent-design.md", "kb-design.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Links = %v, want %v", got, want)
	}
}

func TestExtractSummaryRuleOrder(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "规则1 frontmatter description 胜出",
			body: "---\ntitle: T\ndescription: 从 frontmatter 来的\n---\n\n# H\n\n> 从引用块来的\n",
			want: "从 frontmatter 来的",
		},
		{
			name: "规则2 一句话节胜出",
			body: "# H\n\n> 从引用块来的\n\n## 0. 一句话\n\n真正的摘要\n",
			want: "真正的摘要",
		},
		{
			name: "规则2 认 ## TL;DR",
			body: "# H\n\n## TL;DR\n\n短摘要\n",
			want: "短摘要",
		},
		{
			name: "规则2 跳过节内的标题与列表",
			body: "# H\n\n## 摘要\n\n## 3. 别的小节\n\n> 引用\n\n- 列表项\n\n正文段落\n",
			want: "正文段落",
		},
		{
			name: "规则3 剥掉状态值，保留它后面的话",
			body: "# H\n\n> 状态：**已实施**（P0 完成）；知识库见 [kb-design.md](kb-design.md)\n",
			want: "知识库见 [kb-design.md](kb-design.md)",
		},
		{
			name: "规则3 剥到第一个分句符为止，后面全是内容时保留",
			body: "# H\n\n> 状态：**重设计中** —— 本文档主体描述 v2，未实施\n",
			want: "本文档主体描述 v2，未实施",
		},
		{
			name: "规则3 状态后没有内容时就是空",
			body: "# H\n\n> 状态：**已实施**\n",
			want: "",
		},
		{
			name: "规则3 只取首个引用块",
			body: "# H\n\n> 第一块\n\n正文\n\n> 第二块不该被取\n",
			want: "第一块",
		},
		{
			name: "规则3 代码块里的重定向不算引用",
			body: "# H\n\n```sh\n> /dev/null 2>&1\n```\n\n正文。\n",
			want: "",
		},
		{
			name: "规则4 抽不到就是空，不拿正文凑",
			body: "# H\n\n正文第一段。\n\n正文第二段。\n",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := Extract("x.md", tc.body)
			if e.Summary != tc.want {
				t.Errorf("Summary = %q, want %q", e.Summary, tc.want)
			}
		})
	}
}

func TestExtractTitleNotFirstLine(t *testing.T) {
	body := "> 状态：**已实施**\n\n没有一级标题的文档。\n"
	if got := Extract("x.md", body).Title; got != "" {
		t.Errorf("Title = %q, want empty (the status line is not a title)", got)
	}
}

func TestExtractSectionsAreLosslessBelowCap(t *testing.T) {
	const small = 74
	var b strings.Builder
	b.WriteString("# T\n")
	for i := 0; i < small; i++ {
		b.WriteString("## S" + itoa(i) + "\n")
	}
	e := Extract("x.md", b.String())
	if len(e.Sections) != small {
		t.Errorf("len(Sections) = %d, want %d; sections must not be truncated "+
			"below the hard cap", len(e.Sections), small)
	}
	if e.SectionsTruncated != 0 {
		t.Errorf("SectionsTruncated = %d, want 0", e.SectionsTruncated)
	}
	if got := e.Sections[small-1].Line; got != small+1 {
		t.Errorf("last section Line = %d, want %d", got, small+1)
	}
}

func TestExtractSectionsCapAndCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("# T\n")
	total := maxSectionsStored + 50
	for i := 0; i < total; i++ {
		b.WriteString("## S")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	e := Extract("x.md", b.String())

	if len(e.Sections) != maxSectionsStored {
		t.Fatalf("len(Sections) = %d, want %d", len(e.Sections), maxSectionsStored)
	}
	if e.SectionsTruncated != total-maxSectionsStored {
		t.Errorf("SectionsTruncated = %d, want %d", e.SectionsTruncated, total-maxSectionsStored)
	}
	b.Reset()
	b.WriteString("# T\n")
	for i := 0; i < maxSectionsStored; i++ {
		b.WriteString("## S" + itoa(i) + "\n")
	}
	if got := Extract("x.md", b.String()).SectionsTruncated; got != 0 {
		t.Errorf("SectionsTruncated = %d at exactly %d sections, want 0", got, maxSectionsStored)
	}
}

func TestExtractSectionsIgnoresNonHeadings(t *testing.T) {
	body := "# T\n\n## Real One\n\n```\n## not a heading, it is in a fence\n```\n\n####TooDeep\n\n#NoSpace\n"
	e := Extract("x.md", body)

	want := []Section{{Title: "Real One", Line: 3}}
	if !reflect.DeepEqual(e.Sections, want) {
		t.Errorf("Sections = %+v, want %+v", e.Sections, want)
	}
}

func TestExtractSectionsSkipsH1(t *testing.T) {
	e := Extract("x.md", "# 文档标题\n\n## 第一节\n")
	if len(e.Sections) != 1 || e.Sections[0].Title != "第一节" {
		t.Errorf("Sections = %+v, want only 第一节", e.Sections)
	}
}

func TestExtractLinks(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		body string
		want []string
	}{
		{
			name: "剥锚点",
			rel:  "a.md",
			body: "见 [b](b.md#第-3-节)。",
			want: []string{"b.md"},
		},
		{
			name: "跳过 http 链接，哪怕它以 .md 结尾",
			rel:  "a.md",
			body: "[远端](https://example.com/docs/b.md) 与 [相对](b.md)。",
			want: []string{"b.md"},
		},
		{
			name: "跳过协议相对链接",
			rel:  "a.md",
			body: "[x](//cdn.example.com/b.md)",
			want: nil,
		},
		{
			name: "相对路径按当前文档解析",
			rel:  "sub/a.md",
			body: "[同级](b.md) [上一级](../c.md) [语料根](../e.md)",
			want: []string{"sub/b.md", "c.md", "e.md"},
		},
		{
			name: "语料根锚点 /x.md 解析成根下的 x.md",
			rel:  "sub/a.md",
			body: "[锚](/architecture.md)",
			want: []string{"architecture.md"},
		},
		{
			name: "向上爬出语料根的被剔除",
			rel:  "sub/a.md",
			body: "[出去](../../outside.md) [留下](b.md)",
			want: []string{"sub/b.md"},
		},
		{
			name: "链接把语料目录名写进去了，就照它字面的意思解析",
			rel:  "sub/a.md",
			body: "[字面](../docs/b.md)",
			want: []string{"docs/b.md"},
		},
		{
			name: "自引用不算边",
			rel:  "a.md",
			body: "见 [自己](a.md)。",
			want: nil,
		},
		{
			name: "非 md 目标不算边",
			rel:  "a.md",
			body: "[代码](../main.go) [配置](../x.yaml)",
			want: nil,
		},
		{
			name: "去重且保持首次出现的顺序",
			rel:  "a.md",
			body: "[b](b.md) [c](c.md) [b again](b.md)",
			want: []string{"b.md", "c.md"},
		},
		{
			name: "语料根下的文档不能往上爬",
			rel:  "a.md",
			body: "[上](../b.md) [下](sub/c.md)",
			want: []string{"sub/c.md"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := Extract(tc.rel, tc.body)
			if !reflect.DeepEqual(e.Links, tc.want) {
				t.Errorf("Links = %v, want %v", e.Links, tc.want)
			}
			if e.Path != tc.rel {
				t.Errorf("Path = %q, want %q; the corpus directory name must not be baked in",
					e.Path, tc.rel)
			}
		})
	}
}

func TestEmbedTextExcludesVolatileFields(t *testing.T) {
	body := "# T\n\n> 摘要\n\n## S\n\n见 [b](b.md)。\n"
	base := Extract("a.md", body)
	base.MTime, base.Size = 111, 222

	touched := base
	touched.MTime, touched.Size = 999, 888
	if base.EmbedText() != touched.EmbedText() {
		t.Errorf("EmbedText changed when only mtime/size changed:\n%q\n%q",
			base.EmbedText(), touched.EmbedText())
	}

	edited := base
	edited.Summary = "别的摘要"
	if base.EmbedText() == edited.EmbedText() {
		t.Error("EmbedText did not change when summary changed")
	}
}

func TestExtractAgainstRepoDocs(t *testing.T) {
	corpus := repoCorpusDir(t)
	paths, err := filepath.Glob(filepath.Join(corpus, "*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("found no documents under %s; either the corpus moved or "+
			"this test's assumption about the working directory broke", corpus)
	}

	var totalSections int
	counts := make([]int, len(paths))
	for i, p := range paths {
		base := filepath.Base(p)
		t.Run(base, func(t *testing.T) {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			e := Extract(base, string(b))
			counts[i] = len(e.Sections)

			if e.Title == "" {
				t.Error("no title extracted; the doc has no `# ` heading at line 1")
			}
			if e.Summary == "" {
				t.Error("no summary extracted; §6.1 promises every doc in this " +
					"corpus yields one")
			}
			if e.SectionsTruncated != 0 {
				t.Errorf("sections truncated by %d; either the doc grew past the "+
					"hard cap or the storage cap became a display budget again",
					e.SectionsTruncated)
			}
			if len(e.Sections) == 0 {
				t.Error("no sections extracted")
			}
			for _, s := range e.Sections {
				if s.Line < 1 || s.Line > bytes.Count(b, []byte("\n"))+1 {
					t.Errorf("section %q has out-of-range Line %d", s.Title, s.Line)
				}
			}
		})
	}
	for _, n := range counts {
		totalSections += n
	}
	t.Logf("extracted %d sections from %d documents", totalSections, len(paths))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
