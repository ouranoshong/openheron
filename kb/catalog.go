package kb

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 目录文件的格式契约。**渲染与解析只能有一份实现**（[RenderCatalog] /
// [ParseCatalog]）—— 两份实现迟早在某次改动里分叉，而分叉的表现是「写入
// 用新格式、读取按旧格式」，结果是机器把自己写的文件读不懂，且不报错。
//
// ★ 为什么这套格式**宽进严出**：解析刻意宽松（§12.11），渲染刻意严格。
//
//	宽松是为了人能手改 —— 人的编辑器会在文件里加备注、调整空行，而索引不该
//	因为一句无关的话就拒绝加载整份文件。
//	严格是为了机器写的字节永远可预测 —— 乐观提交（§7.3）要靠「我写的那份
//	还在不在」来发现被覆盖，如果渲染里有随机成分（时间戳、map 序），
//	那个比较就永远不成立。
//
// 所以：**人加的东西，渲染不认；机器写的东西，渲染必认。**

const (
	// CatalogFile 是权威目录的文件名。人可读可改。
	CatalogFile = "catalog.md"
	// SidecarFile 是派生侧车：向量 + 机器上次写下的字段值。
	//
	// ★ 它**不需要**乐观提交（§7.5）：纯派生，丢了自愈。而它的存在意义是
	//   区分「人写的」与「机器写的」（[DerivedEntry.WroteSummary]），
	//   那是 catalog.md 本身给不了的信息。
	SidecarFile = "catalog.gob"

	catalogHeader = "# 知识库目录"
	// entryPrefix 是条目标题行的前缀：`## <path>`。
	entryPrefix = "## "
	// quotePrefix 是摘要行的前缀。
	quotePrefix = "> "
)

// 目录条目里每个 key: value 行的键名。写死成常量 —— 渲染与解析各写一遍
// 字面量的话，某个键被改掉而另一处没改，症状是「那个字段静默恒为空」。
const (
	keyTitle     = "title"
	keyMTime     = "mtime"
	keySize      = "size"
	keySections  = "sections"
	keyTruncated = "sections_truncated"
	keyLinks     = "links"
	keyStale     = "stale"
)

// maxSectionsStored 是**存储**层面的章节硬上限，只防极端情况，不是显示预算。
//
// ★ 这个数字改过一次，改的理由是一次实测，值得记下来免得有人再改回去（§6.2）：
//
//	拿 openheron 自己的 docs 跑：10 份文档共 447 节，maxSections = 24 时留下
//	222 节 —— **一半被砍**，而 kb-design.md 砍掉 67 节。这意味着模型查得到
//	「配置分册里讲热更新的那一节」，却查不到「§50 之后的任何一节」，
//	因为它们从来没进过目录。
//
// ★ 根因不是 24 太小，而是**两个预算被塞进了同一个字段**：
//
//	向量想要**全部**章节（多一点信号，边际成本为零）
//	模型显示想要**少数**章节（每节都要花 token）
//
//	v1 把两者当成一件事，于是提高一个必然拉高另一个。v2 拆开了：
//	**目录是无损的记录**，显示预算属于检索层（§11.1），从内存里的
//	Entry 现切，不需要文件也截断。
//
// ★ 为什么不能「目录里就截断」：截断了 catalog.md 就**有损**，有损就做不到
//
//	渲染→解析→渲染逐字节相同，而乐观提交（§7.3 ④）的写后读比较**正是靠
//	这个性质**发现「我写的那份还在不在」。截断会让冲突检测报出一堆并不存在的冲突，
//	而它的表现形式是「索引永远重试三次然后失败」。
//
// ★ 200 这个值只挡荒谬的输入（一份 5000 节的文档会渲染出 125 KB 的一行）。
//
//	本仓库实测最大是 74 节，留了 2.7 倍余量。它被超过时记在
//	[Entry.SectionsTruncated] —— 不说「还有更多」，模型会以为这份文档
//	只有这几节。
const maxSectionsStored = 200

// sectionSep 是章节名与行号之间的分隔符。
//
// ★ 用括号而不是 `:` 是因为 `:` 会与 `§12.2.2` 这类章节名里的标点混在一
//
//	起，而这一行的读者是模型 —— 它要能一眼把「名字」和「行号」分开，好
//	直接把行号填进 read_file。
const sectionSep = " (L"

// Header 是 catalog.md 头部那一块**机器写**的元信息。
//
// ★ 它与条目分开，因为它是关于「这份目录」的，而条目是关于「某份文档」的。
//
//	人改摘要时不该碰到它，机器重写条目时也不该把它清掉 —— 这正是把两者
//	分开渲染的理由。
type Header struct {
	// Corpus 是语料目录的相对路径，展示用。
	Corpus string
	// Docs 是条目数。
	Docs int
	// Revision 每次写入 +1。
	//
	// ★ 它是「发生过并发」留下的唯一证据（§7.3）：被覆盖的一方重试时也会
	//   加一，所以「rev 跳了两格」就说明有人同时在写。
	Revision int64
	// Writer 是这次写入的 token。
	//
	// ★ 它的用途是**让并发的两次写入产生不同的字节**，从而写后读比较能
	//   发现覆盖（§7.3 ④）。它不承担「这是谁写的」——那需要进程身份，
	//   而跨进程的进程身份对本包没用。
	//
	//   §7.5 末尾记了更彻底的替代方案（内容派生的 id），那会让本字段
	//   变得多余。M2 落地时按那条改。
	Writer string
	// IndexVersion 是抽取规则版本。
	IndexVersion int
	// Model / Dim 是建索引时的模型身份与观测维度。零值 = 纯词料模式。
	Model string
	Dim   int
	// Created 是这份目录的建立时间（首次写入后不再更新）。
	Created time.Time
	// Updated 是最近一次写入的时间。
	Updated time.Time
}

// Entry 是 catalog.md 里的一个源文件。
//
// ★ 它同时是三样东西：**渲染的产物**、**解析的结果**、**索引的产物**。
//
//	三合一不是省事 —— 少一次 `Fields` ↔ `Entry` ↔ markdown 的往返转换，
//	而每加一层往返就要多一处「字段名不一致」的容身之处。
//
// ★ 字段所有权（§7.2）：[Entry.Summary] 与 [Entry.Title] **人优先**，
//
//	其余机器每次重算。[Entry.Stale] 只标不修（§12.10）。
type Entry struct {
	// Path 是相对语料根的路径，**同时是主键**。一个源文件一节。
	Path string

	Title   string
	Summary string

	// Sections 是章节清单，**带行号**（§14.1）。行号是给模型的：`read_file`
	// 的参数就是它，所以不指到位置就等于把「判断这一行」的成本原样退回去。
	Sections []Section
	// SectionsTruncated 表示章节数超过 [maxSectionsStored]，被砍掉了多少。
	//
	// ★ 正常情况下**它应该是 0** —— 目录是无损记录，砍掉是安全网触发了，
	//   而不是预算用完了。非零时它同时是 `kb status` 的一条告警：这份文档
	//   大得已经查不全了。
	//
	// ★ 0 表示没砍。它必须显式存在而不是「Sections 短于实际即视为砍过」——
	//   后者让「正好 200 节」与「5000 节被砍成 200」长得一模一样。
	SectionsTruncated int

	Links []string
	MTime int64
	Size  int64

	// Stale = 摘要是人写的、而源文件此后变了（§12.10）。
	Stale bool
}

// Section 是一个章节：名字 + 标题所在行（1-based）。
//
// ★ 行号是**独立字段**而不是渲染时才加的：它要被嵌入（模型据此判断该读
//
//	哪里），要被比较（改了行号就说明源文件动过），也要在人改摘要时保持
//	不变。渲染时才生成的话，那三件事都做不到。
type Section struct {
	Title string
	// Line 是 1-based 行号，指向标题行本身。0 = 未知（渲染时省略 "(L0)"）。
	Line int
}

// String 渲染成目录里那一行的样子：`名字 (L行号)`。
//
// ★ 它是 [Section] 唯一的渲染形式，渲染与解析共用 —— 一旦两处不一致，
// 人就会看到能显示却解不回来的行号。
func (s Section) String() string {
	if s.Line <= 0 {
		return s.Title
	}
	return s.Title + sectionSep + strconv.Itoa(s.Line) + ")"
}

// Catalog 是解析出来的整份目录。
//
// ★ 它是**权威**，而 [Sidecar] 是派生的。两者分开放是为了让「人改了什么」
//
//	与「机器算了什么」在类型上就分得开 —— 混在一个结构里时，代码里就会出现
//	「这个字段该信谁的」这种没法用类型回答的问题（§7.2）。
type Catalog struct {
	Header  Header
	Entries []Entry
	// Warnings 是解析时发现的问题（§12.11 的坏节、拼错的 key）。
	//
	// ★ 它们**不**让解析失败。拒绝整份文件的代价是：一个人在编辑器里按错
	//   一个键，整个索引就废了 —— 而那份文件是他自己写的，让他能一边看着
	//   一边修才对。部分成功 + 明确报告，是这个场景的正确姿态。
	Warnings []string
}

// Get 返回某路径的条目。
func (c *Catalog) Get(path string) (Entry, bool) {
	for _, e := range c.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return Entry{}, false
}

// Sort 按路径稳定排序。目录里条目的顺序**必须**由路径决定（[RenderCatalog]
// 里已经保证），这里给解析出来的结果也排一遍是为了让「渲染 → 解析」往返
// 后仍然一致。
func (c *Catalog) Sort() {
	sort.Slice(c.Entries, func(i, j int) bool { return c.Entries[i].Path < c.Entries[j].Path })
}

// RenderCatalog 渲染成 catalog.md 的字节。
//
// ★ 输出里**只有** [Header.Updated] 一个墙上时钟。它随每次写入变化，于是
//
//	「同一份语料两次跑出的文件不逐字节相同」—— 这正是写后读比较需要的：
//	并发的两次写入必须产生不同的字节，否则发现不了覆盖。
//
//	反过来，**除它之外的一切都必须由内容决定**：条目顺序按路径、章节顺序
//	按文档序、链接去重后排序。任何一处 map 遍历序都会让渲染不可复现，
//	而不可复现的渲染没法与「我是不是覆盖了别人」这个问题对上。
func RenderCatalog(c Catalog) []byte {
	entries := normalizeEntries(c.Entries)
	var b strings.Builder

	b.WriteString(catalogHeader)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "> 语料: %s  ·  %d 份文档  ·  rev %d  ·  writer %s  ·  建于 %s\n",
		orNone(c.Header.Corpus), len(entries), c.Header.Revision,
		orNone(c.Header.Writer), stamp(c.Header.Created))
	b.WriteString("> 由 `openheron kb index` 维护。**摘要那一行可以直接改** —— 改了立刻生效，向量会自己重算。\n")
	b.WriteString("> 其余 `key: value` 行是派生的，会被覆盖。\n")
	fmt.Fprintf(&b, "> 抽取规则 v%d", c.Header.IndexVersion)
	if c.Header.Model != "" {
		fmt.Fprintf(&b, "  ·  模型 %s (dim %d)", c.Header.Model, c.Header.Dim)
	}
	fmt.Fprintf(&b, "  ·  更新于 %s\n", stamp(c.Header.Updated))

	for _, e := range entries {
		b.WriteString("\n")
		b.WriteString(entryPrefix)
		b.WriteString(e.Path)
		b.WriteString("\n\n")

		// ★ 摘要写成一行。这是**格式要求**，不是风格：多行的 `> ` 在宽松
		//   解析下无法确定到哪结束，于是要么猜（猜错静默丢内容），要么
		//   收紧解析（与「人加备注不弄坏」冲突）。单行 + 宽松解析，两个
		//   约束同时满足。
		if s := oneLine(e.Summary); s != "" {
			b.WriteString(quotePrefix)
			b.WriteString(s)
			b.WriteString("\n\n")
		}

		b.WriteString(keyTitle + ": " + oneLine(e.Title) + "\n")
		fmt.Fprintf(&b, "%s: %d\n", keyMTime, e.MTime)
		fmt.Fprintf(&b, "%s: %d\n", keySize, e.Size)
		b.WriteString(keySections + ": " + renderSections(e.Sections, e.SectionsTruncated) + "\n")
		if e.SectionsTruncated > 0 {
			fmt.Fprintf(&b, "%s: %d\n", keyTruncated, e.SectionsTruncated)
		}
		b.WriteString(keyLinks + ": " + strings.Join(e.Links, ", ") + "\n")
		fmt.Fprintf(&b, "%s: %t\n", keyStale, e.Stale)
	}
	return []byte(b.String())
}

// normalizeEntries 把条目排成一个与构造顺序无关的顺序。
//
// ★ 「归一化放在渲染里」而不是「约定调用方先排」：前者是一条**可检查**
//
//	的性质（渲染两次得到同样的字节），后者是一条要靠每个调用方都记得
//	遵守的约定 —— 而写后读比较（§7.3 ④）正是靠这个性质发现覆盖的。
//
// 排序规则**两处不一样**，这是有意的：
//
//	条目按路径排   —— 目录是个集合，没有「先读哪一份」的含义
//	章节**不排**   —— 它是文档序，排序会毁掉「第 1 节在第 3 节前面」这件事，
//	                  而那正是模型要照着跳的
//	链接排序去重   —— 同一个目标出现两次没有信息量，而抽取时的顺序取决于
//	                  正文怎么写，那不是这条边的属性
func normalizeEntries(in []Entry) []Entry {
	out := make([]Entry, len(in))
	for i, e := range in {
		out[i] = e
		out[i].Links = sortedUnique(e.Links)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func sortedUnique(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return dedupAdjacent(out)
}

// ParseCatalog 解析 catalog.md。
//
// ★ 宽松：认不出的行**忽略**并记一条 warning，而不是报错（§12.11）。
//
//	理由是这份文件归人所有 —— 一个键拼错、删掉一行标题、多加一句备注，
//	都不该让整份目录加载失败。
//
// ★ 严格：一行只能是一个 key: value 或一条摘要。多余内容记 warning。
//
//	宽松到能容错，与宽松到能吞掉真实错误之间有线，这里踩在线上。
func ParseCatalog(b []byte) (Catalog, error) {
	var c Catalog
	c.Warnings = nil

	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	seenHeader := false
	var cur *Entry
	curPath := ""

	flush := func() {
		if cur != nil {
			c.Entries = append(c.Entries, *cur)
		}
		cur, curPath = nil, ""
	}

	for i, line := range lines {
		ln := i + 1

		if !seenHeader {
			if strings.TrimSpace(line) == catalogHeader {
				seenHeader = true
			}
			continue
		}

		// 条目标题行。
		if strings.HasPrefix(line, entryPrefix) {
			flush()
			p := strings.TrimSpace(strings.TrimPrefix(line, entryPrefix))
			if p == "" {
				c.Warnings = append(c.Warnings,
					fmt.Sprintf("line %d: `## ` with no path, section ignored", ln))
				continue
			}
			if strings.ContainsAny(p, "\r\n") {
				c.Warnings = append(c.Warnings,
					fmt.Sprintf("line %d: path %q contains a line break, section ignored", ln, p))
				continue
			}
			cur = &Entry{Path: p}
			curPath = p
			continue
		}

		// 头部：第一个条目标题行之前的所有非空 `>` 行。
		if cur == nil {
			if strings.HasPrefix(strings.TrimSpace(line), ">") {
				parseHeaderLine(&c.Header, line)
			}
			continue
		}

		// 条目内。
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			// ★ 第一条 `>` 是摘要，后面的忽略。多条摘要时保留第一条：
			//   人在摘要里续了一行是很自然的事，而丢掉**后**面那些会
			//   静默丢掉人刚敲的字。
			if cur.Summary != "" {
				c.Warnings = append(c.Warnings, fmt.Sprintf(
					"%s: extra summary line at %d kept the first one: %s", curPath, ln, oneLine(trimmed)))
				continue
			}
			s := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			cur.Summary = s
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		k = strings.TrimSpace(k)
		if !ok || !looksLikeKey(k) {
			// ★ 不是 key: value —— 大概率是人在备注。**这不是警告**，
			//   忽略就对了，报出来只会让 status 永远不干净。
			//
			//   `looksLikeKey` 那道判据是必需的：备注里只要有一个冒号
			//   （「备注，不是 key: value」就是），朴素的 Cut 会切出
			//   一个含中文与空格的东西当键名，于是每一句中文备注都变成
			//   一条「unknown key」警告 —— 而 status 的输出会因此永远
			//   不干净，于是没人看它，于是真正的拼错也一起被淹掉。
			continue
		}
		applyEntryKey(cur, k, v, ln, curPath, &c.Warnings)
	}
	flush()

	if !seenHeader {
		return Catalog{}, fmt.Errorf("kb: not a catalog file: %q not found", catalogHeader)
	}
	c.Sort()
	c.Header.Docs = len(c.Entries)
	return c, nil
}

// applyEntryKey 把一行 key: value 落到条目上。
//
// ★ 拼错的 key 记 warning 并**忽略**，而不是当成新字段存下来：存下来会
//
//	让「摘要拼成 `sumary:`」变成「有一项要渲染，但没人知道它是什么」，
//	而人看到的是自己的话不见了，且没有任何东西会说。
func applyEntryKey(e *Entry, k, v string, ln int, path string, warnings *[]string) {
	switch k {
	case keyTitle:
		e.Title = strings.TrimSpace(v)
	case keyMTime:
		e.MTime = parseI64(strings.TrimSpace(v), ln, k, path, warnings)
	case keySize:
		e.Size = parseI64(strings.TrimSpace(v), ln, k, path, warnings)
	case keySections:
		s, n := parseSections(strings.TrimSpace(v))
		e.Sections = s
		e.SectionsTruncated = n
	case keyTruncated:
		if t := parseInt(strings.TrimSpace(v), ln, k, path, warnings); t > 0 {
			e.SectionsTruncated = t
		}
	case keyLinks:
		e.Links = splitList(strings.TrimSpace(v))
	case keyStale:
		b := strings.TrimSpace(v)
		// ★ 只认 true / false 两种拼法。`1`、`yes`、`TRUE` 都记警告 ——
		//   在一个人手写的文件里，含糊的真值只会让人以为它生效了。
		switch b {
		case "true":
			e.Stale = true
		case "false":
			e.Stale = false
		default:
			*warnings = append(*warnings, fmt.Sprintf(
				"%s: %s: expected true or false, got %q (treated as false)", path, k, b))
		}
	default:
		*warnings = append(*warnings, fmt.Sprintf(
			"%s: line %d: unknown key %q ignored", path, ln, k))
	}
}

// parseHeaderLine 从头部的一行里取元信息。
//
// ★ 头部是**机器**写的，所以这里逐个 key 认；认不出就忽略（头部没有
//
//	人工备注的位置）。
func parseHeaderLine(h *Header, line string) {
	for _, part := range strings.Split(stripQuote(line), "  ·  ") {
		applyHeaderSegment(h, strings.TrimSpace(part))
	}
}

// applyHeaderSegment 认头部的一个 ` · ` 分段。
//
// ★ 每个分段都**自描述**：认它的判据只在这个分段自己身上，不依赖相邻分段。
//
//	反例（曾经的写法）是把「抽取规则 vN · 模型 X · 更新于 T」当成一个分段，
//	于是「模型」在「没有抽取规则的那一行」上就认不出来，而那要求解析器
//	记住「抽取规则」出现过一次 —— 一旦渲染改了行序，rev 与 writer 就一起
//	静默变成零值。往返测试就是被那个结构钓出来的。
func applyHeaderSegment(h *Header, part string) {
	switch {
	case part == "":
		return
	case strings.HasPrefix(part, "语料:"):
		h.Corpus = strings.TrimSpace(strings.TrimPrefix(part, "语料:"))
	case strings.HasPrefix(part, "rev "):
		h.Revision = int64(parseInt(strings.TrimPrefix(part, "rev "), 0, "rev", "", nil))
	case strings.HasPrefix(part, "writer "):
		h.Writer = strings.TrimSpace(strings.TrimPrefix(part, "writer "))
	case strings.HasPrefix(part, "抽取规则 v"):
		h.IndexVersion = parseInt(strings.TrimPrefix(part, "抽取规则 v"), 0, "IndexVersion", "", nil)
	case strings.HasPrefix(part, "模型 "):
		rest := strings.TrimPrefix(part, "模型 ")
		if i := strings.Index(rest, " (dim "); i >= 0 {
			h.Model = rest[:i]
			h.Dim = parseInt(
				strings.TrimSuffix(strings.TrimPrefix(rest[i+len(" (dim "):], ""), ")"),
				0, "dim", "", nil)
		} else {
			h.Model = rest
		}
	case strings.HasPrefix(part, "更新于 "):
		h.Updated = parseStamp(strings.TrimPrefix(part, "更新于 "))
	case strings.HasPrefix(part, "建于 "):
		h.Created = parseStamp(strings.TrimPrefix(part, "建于 "))
	default:
		// ★ 「N 份文档」故意不认：文档数由解析器按实际条目数回填，
		//   认了它就允许一份头部说自己有 10 份而实际只有 2 份 —— 那是
		//   status 会照抄、人也会相信的数字。
	}
}

func stripQuote(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, ">") {
		s = strings.TrimSpace(strings.TrimPrefix(s, ">"))
	}
	return strings.TrimSpace(s)
}

// looksLikeKey 判断一段文本能不能是一个字段键名。
//
// ★ 判据：ASCII 小写字母开头，只含 `[a-z0-9_]`。
//
//	目录里每一个真键都长这样（`title` / `mtime` / `sections_truncated`），
//	而**人写的备注几乎不可能长这样** —— 中文、空格、标点都会让它落选。
//
//	这一道判据让两种失误彻底分开：
//	`sumary: x`   → 像键 → **警告**（拼错了，快去改）
//	`备注：x`      → 不像键 → **静默忽略**（那是备注）
//	混为一谈的后果是 status 永远不干净，而没人看的 status 等于没有 status。
func looksLikeKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '_':
			// 数字与下划线不能开头。
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func renderSections(ss []Section, truncated int) string {
	parts := make([]string, 0, len(ss)+1)
	for _, s := range ss {
		parts = append(parts, s.String())
	}
	out := strings.Join(parts, " · ")
	if truncated > 0 {
		out += fmt.Sprintf(" · [+%d]", truncated)
	}
	return out
}

// parseSections 是 [renderSections] 的逆运算。
//
// ★ 行号**可有可无**：手写的 `章节: 一 · 二` 要能读回来成两个无行号的
//
//	章节。而渲染时省略 `+N`（truncated 为 0）也要能读回来 —— 两者都不
//	报错，因为这份文件人会手改。
func parseSections(v string) ([]Section, int) {
	var out []Section
	truncated := 0
	if v == "" {
		return nil, 0
	}
	for _, part := range strings.Split(v, " · ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, ok := strings.CutPrefix(part, "[+"); ok {
			if i := strings.Index(n, "]"); i >= 0 {
				if t := parseInt(strings.TrimSpace(n[:i]), 0, "sections_truncated", "", nil); t > 0 {
					truncated = t
				}
			}
			continue
		}
		out = append(out, parseSection(part))
	}
	return out, truncated
}

// parseSection 拆出「名字 (L行号)」。
//
// ★ 只认**结尾**的 `(L数字)`。名字中间出现的 `(L12)` 不拆 —— 章节名本身
//
//	可能带括号，认错位置会切出一个假的名字与一个假的行号。
func parseSection(part string) Section {
	if !strings.HasSuffix(part, ")") {
		return Section{Title: part}
	}
	i := strings.LastIndex(part, sectionSep)
	if i <= 0 {
		return Section{Title: part}
	}
	line := parseInt(part[i+len(sectionSep):len(part)-1], 0, "section line", "", nil)
	if line <= 0 {
		return Section{Title: part}
	}
	return Section{Title: strings.TrimSpace(part[:i]), Line: line}
}

// splitList 拆 `a, b, c` 形式的一行列表，去空去重。
func splitList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return dedupAdjacent(out)
}

// dedupAdjacent 去重，要求入参**已排序**。
func dedupAdjacent(ss []string) []string {
	out := ss[:0]
	for i, s := range ss {
		if i > 0 && s == ss[i-1] {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// oneLine 压成一行。
//
// ★ 摘要与标题都必须是单行（§5.2），而**合并**换行比丢弃好：人写摘要时
//
//	按了回车，丢掉后半句是静默丢数据，接起来至少不丢。
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.Contains(s, "\n") {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
}

func orNone(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format(time.RFC3339)
}

// nowFn 是墙上时钟。它是一个**变量**而不是直接调用 time.Now()，因为测试要
// 能把它冻住 —— 目录的字节里唯一会随时间变的就是它，而冻住之后「两次写入
// 产生不同字节」这件事就得靠别的东西保证（见 [newWriterToken]）。
var nowFn = time.Now

// newWriterToken 生成一个让并发写入**产生不同字节**的 token。
//
// ★ 它的唯一职责是让写后读比较（§7.3 ④）能发现覆盖。所以它必须满足一条：
//
//	两次写入**必然**不同。随机 6 hex 满足；内容派生的 id（§7.5 末尾那个
//	更彻底的方案）在「同一份内容写两次」时**不**满足 —— 那时两次写入字节
//	相同，而比较会误判成「没被覆盖」。
//
//	所以这里用随机而不是内容哈希：**判断的是「这一瞬间盘上那份还在不在」，
//	不是「内容对不对」**，后者由 RenderCatalog 自己的确定性负责。
func newWriterToken() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read 失败时退化成时间戳的低 3 字节。它不如随机好，但**仍会
		// 随写入变化** —— 满足本函数的唯一要求。而返回一个空串会让每次
		// 写入的字节都一样，那比弱随机更糟。
		return fmt.Sprintf("%06x", time.Now().UnixNano()&0xffffff)
	}
	return hex.EncodeToString(b[:])
}

func parseStamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseI64 解析整数，失败时记一条 warning。
//
// ★ 解析失败**保留零值并报告**，不返回错误：字段坏掉的条目仍然有用
//
//	（摘要、章节、链接都在），为了一行坏数字丢掉整份文档是更大的错。
func parseI64(s string, ln int, key, path string, warnings *[]string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		warnBadInt(warnings, ln, key, path, s)
		return 0
	}
	return n
}

// parseInt 与 [parseI64] 同行为，产出 int。
func parseInt(s string, ln int, key, path string, warnings *[]string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		warnBadInt(warnings, ln, key, path, s)
		return 0
	}
	return n
}

func warnBadInt(warnings *[]string, ln int, key, path, raw string) {
	if warnings == nil {
		return
	}
	if ln > 0 && path != "" {
		*warnings = append(*warnings, fmt.Sprintf(
			"%s: line %d: %s: %q is not an integer, treated as 0", path, ln, key, raw))
		return
	}
	*warnings = append(*warnings, fmt.Sprintf("%s: %q is not an integer, treated as 0", key, raw))
}
