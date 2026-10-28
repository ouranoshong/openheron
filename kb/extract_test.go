package kb

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// kbStyleDoc 是本仓库文档的形态：首行一段 `>` 引用块，之后 `## ` 分册标题。
// 抽取器的四条规则里有三条是为它写的，所以它是最该被钉住的那个形状。
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

	// ★ 主键**原样保留**传进来的 `rel`，不拼语料目录名。这条断言是防一次
	//   真实发生过的偏离：初版把语料前缀留在了路径里（`docs/config-design.md`），
	//   而 `catalog.md` 住在语料根里 —— 那样一来挪动语料就得重写整个目录，
	//   而「重写目录」这件事本身要索引器先跑起来。
	if got, want := e.Path, "config-design.md"; got != want {
		t.Errorf("Path = %q, want %q (corpus-relative, no directory prefix)", got, want)
	}

	if got, want := e.Title, "openheron 配置系统设计"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	// 规则 2 优先于规则 3：分册头部那句是关于「这份文档」的元说明，
	// 「一句话」节才是真正的摘要。顺序反了的话 10 份分册的摘要会全是
	// 「状态：已实施」。
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

// TestExtractSummaryRuleOrder 逐条钉住四条规则。
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

// TestExtractTitleNotFirstLine 标题不能取「第一行非空」。
func TestExtractTitleNotFirstLine(t *testing.T) {
	body := "> 状态：**已实施**\n\n没有一级标题的文档。\n"
	if got := Extract("x.md", body).Title; got != "" {
		t.Errorf("Title = %q, want empty (状态行不是标题)", got)
	}
}

// TestExtractSectionsAreLosslessBelowCap 目录是**无损记录**：安全线以下一节都不砍。
//
// ★ 这条断言对应一次实测修正（§6.2）：原来的上限是 24，拿 openheron 自己的
//
//	docs 跑出来 447 节只留 222 节 —— 一半被砍，模型于是查不到被砍掉的那一
//	半里的任何一节。上限高到 200 之后，实测最大那份（74 节）就整份都在。
//	本仓库自己的 10 份分册必须全部 `SectionsTruncated == 0`，否则就是又把
//	显示预算塞回存储里了。
func TestExtractSectionsAreLosslessBelowCap(t *testing.T) {
	const small = 74 // 实测最大的那份文档的章节数
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
	// 行号要一路对到最后一个 —— 砍的不只是标题，还有「模型能跳过去」这件事。
	if got := e.Sections[small-1].Line; got != small+1 {
		t.Errorf("last section Line = %d, want %d", got, small+1)
	}
}

// TestExtractSectionsCapAndCount 越过硬线时报**准确的**砍掉数。
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
	// 边界：正好 maxSectionsStored 节时不能报「砍掉了 0 个」——那会让 status
	// 永远显示一条不会消失的告警，而没人看的告警等于没有告警。
	b.Reset()
	b.WriteString("# T\n")
	for i := 0; i < maxSectionsStored; i++ {
		b.WriteString("## S" + itoa(i) + "\n")
	}
	if got := Extract("x.md", b.String()).SectionsTruncated; got != 0 {
		t.Errorf("SectionsTruncated = %d at exactly %d sections, want 0", got, maxSectionsStored)
	}
}

// TestExtractSectionsIgnoresNonHeadings 深一层的编号标题、以及伪标题。
func TestExtractSectionsIgnoresNonHeadings(t *testing.T) {
	body := "# T\n\n## Real One\n\n```\n## not a heading, it is in a fence\n```\n\n####TooDeep\n\n#NoSpace\n"
	e := Extract("x.md", body)

	want := []Section{{Title: "Real One", Line: 3}}
	if !reflect.DeepEqual(e.Sections, want) {
		t.Errorf("Sections = %+v, want %+v", e.Sections, want)
	}
}

// TestExtractSectionsSkipsH1 一级标题是文档标题，不是章节。
func TestExtractSectionsSkipsH1(t *testing.T) {
	e := Extract("x.md", "# 文档标题\n\n## 第一节\n")
	if len(e.Sections) != 1 || e.Sections[0].Title != "第一节" {
		t.Errorf("Sections = %+v, want only 第一节", e.Sections)
	}
}

// TestExtractLinks 链接抽取。
//
// ★ 所有路径都是**相对语料根**的：`a.md` 不是 `docs/a.md`。这个约定值得
//
//	单独写一条测试钉住 —— 一旦有人把语料目录名拼回路径，`catalog.md` 就
//	不再是「跟着语料走」的了，而挪动语料会变成一次全量重写。
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
			// 这是「语料相对」与「项目根相对」最容易搞混的一条：`docs/b.md`
			// 在语料相对的世界里指的是 `docs/docs/b.md`。它在项目根相对的
			// 世界里是合法路径 —— 两种约定下这条链接的含义完全不同，
			// 而搞混的表现是「几条边悄悄指错了地方」。
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
			// 语料根下的文档：`..` 直接出界，没有任何余地。
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
			// ★ 主键原样保留，**不拼语料目录名**（详见 [TestExtractKBStyle]）。
			if e.Path != tc.rel {
				t.Errorf("Path = %q, want %q; the corpus directory name must not be baked in",
					e.Path, tc.rel)
			}
		})
	}
}

// TestEmbedTextExcludesVolatileFields 决定「要不要重嵌」。
func TestEmbedTextExcludesVolatileFields(t *testing.T) {
	body := "# T\n\n> 摘要\n\n## S\n\n见 [b](b.md)。\n"
	base := Extract("a.md", body)
	base.MTime, base.Size = 111, 222

	// 只改 mtime/size（编辑器重写文件、正文一字未改的那种保存）。
	touched := base
	touched.MTime, touched.Size = 999, 888
	if base.EmbedText() != touched.EmbedText() {
		t.Errorf("EmbedText changed when only mtime/size changed:\n%q\n%q",
			base.EmbedText(), touched.EmbedText())
	}

	// 改了摘要就必须变 —— 那是真的需要重嵌。
	edited := base
	edited.Summary = "别的摘要"
	if base.EmbedText() == edited.EmbedText() {
		t.Error("EmbedText did not change when summary changed")
	}
}

// TestExtractAgainstRepoDocs 拿本仓库**自己的** docs 当语料，断言两条不变式。
//
// ★ 它不是覆盖率凑数，而是一次实测的固化：M1 落地时把章节数上限设成 24，
//
//	拿真实语料一跑，10 份文档共 447 节只留 222 节 —— 一半被砍，而被砍掉的
//	那一半里的任何一节，模型永远查不到。那个数字在单元测试里完全看不出来，
//	因为单元测试的文档只有 5 节。
//
// ★ 断言的是**不变式**而不是具体数字：文档会被增删，钉住「447」的测试
//
//	会在每次改文档时失效，然后被人加 `t.Skip` 掉。钉住「每份都全、每份
//	都有摘要」则会在真正的回归时失效，而那正是它该失效的时候。
//
// ★ 语料路径是 ../docs：Go 测试的 CWD 是包目录。它变了一定会**响亮地失败**
//
//	而不是静默跳过 —— 静默跳过的集成测试等于不存在。
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
	// 按下标写而不是 += ：将来有人给子测试加 t.Parallel() 时，+= 会变成一个
	// 只在 -race 下偶发的数据竞争，而那种 bug 值得现在就挡住。
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
			// §6.1 的承诺：每份文档都抽得到摘要，**没有一份会掉队**。
			if e.Summary == "" {
				t.Error("no summary extracted; §6.1 promises every doc in this " +
					"corpus yields one")
			}
			// §6.2：目录是无损记录。这份文档的章节若被砍，就是显示预算又塞回
			// 存储里了 —— 而那个 bug 在小语料上一次都测不出来。
			if e.SectionsTruncated != 0 {
				t.Errorf("sections truncated by %d; either the doc grew past the "+
					"hard cap or the storage cap became a display budget again",
					e.SectionsTruncated)
			}
			if len(e.Sections) == 0 {
				t.Error("no sections extracted")
			}
			// 行号必须指得到东西：模型是拿它去 read_file 的。
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
