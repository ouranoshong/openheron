package kb

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// 抽取是**纯文本**的，不调 LLM（§6）。
//
// ★ 那条选择省下的东西比看起来多：省下一个 chat 模型（成本、延迟、非确定
//	性），也省下「摘要每次重建都不一样」这件事 —— 而后者会让
//	`catalog.md` 在两轮之间无意义地抖动，人打开目录看到满屏 diff，
//	于是再也不去改它。人可改这个性质不是白送的。

// Extract 从一份源文件抽出一条目录条目。
//
// rel 是相对语料根的路径（同时是主键），body 是文件全文。
//
// ★ **路径一律相对语料根，不带语料目录名**：`config-design.md` 而不是
//
//	`docs/config-design.md`。这不只是好看 —— `catalog.md` 就住在语料根里，
//	于是「语料目录被改名/移动」只改头部一个字段，条目一个都不用动。反过来
//	存项目根相对路径的话，每次挪语料都要重写整个目录，而那件事本身又需要
//	索引器先跑起来 —— 鸡生蛋。
//
// ★ 边界判断也因此不再需要语料目录名：出了根在相对路径里就是 `..`。
//
//	`MetaCorpus` / `withinCorpus` 随之消失，不是重构掉的，是**它们表达的
//	那个概念不再存在**。
//
// ★ 它**不读文件系统**、不 stat、不看配置。调用方（索引流水线）把 mtime 与
//
//	size 查好之后填进 [Entry] 的对应字段。理由是这一层要能纯函数测试 ——
//	一个读盘 + 解析 + 猜测的函数，测它的唯一办法就是造文件。
func Extract(rel, body string) Entry {
	lines, inCode := splitLines(body)

	e := Entry{
		Path:  rel,
		Title: extractTitle(body, lines, inCode),
	}
	e.Summary = extractSummary(body, lines, inCode)
	sections, truncated := extractSections(lines, inCode)
	e.Sections, e.SectionsTruncated = sections, truncated
	e.Links = extractLinks(body, rel)
	return e
}

// EmbedText 返回这条条目**被向量化**的文本。
//
// ★ 它只包含机器抽出来的东西，**不含** `mtime` / `size` —— 那两个字段
//
//	每次保存都会变（编辑器重写文件不改正文），而向量据此重算是纯粹的浪费：
//	正文一个字没改，向量却变了，于是 `ReEmbedded` 报一个非零数，模型会
//	以为内容真的动了。
//
// ★ 它也不含 `Summary` 的空值行 —— 空摘要对余弦没有贡献，却会让不同
//
//	文档的文本长度差得很远。
func (e Entry) EmbedText() string {
	var b strings.Builder
	if e.Title != "" {
		b.WriteString(e.Title)
		b.WriteString("\n")
	}
	if e.Summary != "" {
		b.WriteString(e.Summary)
		b.WriteString("\n")
	}
	for _, s := range e.Sections {
		b.WriteString(s.Title)
		b.WriteString("\n")
	}
	if len(e.Links) > 0 {
		b.WriteString("links: ")
		b.WriteString(strings.Join(e.Links, " "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// splitLines 切行，并标出**哪些行在代码围栏里**。
//
// ★ 围栏必须认，否则代码示例里的 `## 这是注释` 会被当成章节 —— 而 openheron
//
//	的 docs 里全是 bash/markdown 代码块，里面有 `##` 开头的注释行。症状是
//	目录里出现一串假章节，行号全指向代码块，模型照着去读，读到一段注释。
//
// ★ 返回的 [Section.Line] 是 1-based，所以渲染时用 i+1。这个不一致是
//
//	markdown 行号的惯例（编辑器与 `read_file` 都从 1 数），照它。
func splitLines(body string) (lines []string, inCode []bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if body == "" {
		return nil, nil
	}
	lines = strings.Split(body, "\n")
	inCode = make([]bool, len(lines))
	open := false
	for i, line := range lines {
		if isFence(line) {
			//	围栏行本身不算「在代码里」—— 它是代码块的第一行（```go），
			//	而 `## ` 开头的围栏行是合法的标题写法（`` ## `` → `##`）。
			open = !open
			continue
		}
		inCode[i] = open
	}
	return lines, inCode
}

func isFence(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

var (
	// frontmatterRe 认 YAML frontmatter。openheron 的 docs 没用，但别人的语料会。
	frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---`)
	// descRe 取 frontmatter 里的 description。
	descRe = regexp.MustCompile(`(?m)^description:\s*(.+?)\s*$`)
	// h1Re 取第一个一级标题作标题候选。
	h1Re = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	// headingRe 认 markdown 标题。**要求标题符号后有空格** —— CommonMark
	// 的规则，也让 `#hashtag` 与 `#include <x>` 不会被误认成标题。
	//
	// ★ 层级只到 4：深到 `#####` 的标题在本仓库里是文档内部的细分，
	//   列进目录会让 24 条的预算被尾部吃光。
	headingRe = regexp.MustCompile(`^(#{1,4})[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	// mdLinkRe 认 markdown 链接。这是 §6.3 的唯一输入。
	//
	// ★ 只取目标像 `.md` 的：`[x](foo.md#bar)` 的锚点要在解析后剥掉，
	//   而 `[x](foo.md)` 与 `[x](https://e.com/foo.md)` 长得一样，判据
	//   放在解析之后（[resolveLink]）而不是正则里。
	mdLinkRe = regexp.MustCompile(`\]\(\s*<?([^)>\s]+(?:\.md))>?[^)]*\)`)
	// tldrHeadings 是「摘要标题」的别名集合，规则 2 的匹配对象。
	tldrHeadings = []string{"0. 一句话", "一句话", "tldr", "tl;dr", "summary", "概要", "摘要"}
)

// extractTitle 取文档标题。
//
// 顺序：frontmatter `title:` → 第一个 `# ` 标题 → 文档名（去扩展名）。
//
// ★ **不用「第一行非空」**：openheron 的分册第一行是 `> 状态：…`，而它是关于
//
//	**这份文档**的元说明，不是它的名字。用它当标题的话，10 份文档的标题
//	会全都是「状态：已实施」—— 那与没有标题等价。
func extractTitle(body string, lines []string, inCode []bool) string {
	if m := frontmatterRe.FindStringSubmatch(body); m != nil {
		if t := matchField(m[1], "title"); t != "" {
			return t
		}
	}
	for i, line := range lines {
		if inCode[i] {
			continue
		}
		if m := h1Re.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// extractSummary 按四条规则取摘要，命中即停（§6.1）。
func extractSummary(body string, lines []string, inCode []bool) string {
	// 规则 1：frontmatter description。
	if m := frontmatterRe.FindStringSubmatch(body); m != nil {
		if d := matchField(m[1], "description"); d != "" {
			return d
		}
	}

	// 规则 2：`## 0. 一句话` 之类的节，取它的第一段正文。
	if s := sectionSummary(lines, inCode); s != "" {
		return s
	}

	// 规则 3：首个 `>` 块的首行。
	if s := quoteSummary(lines, inCode); s != "" {
		return s
	}

	// 规则 4：没有 → 空。**不拿正文的前 N 个字凑** —— 那不是摘要，那是
	// 片段，而一个说得不准的摘要比没有摘要更坏：它会让文档看起来像是被
	// 概括过，而其实没有。
	return ""
}

// matchField 从 frontmatter 里取一个标量字段。
//
// ★ 只认顶层（不缩进）且带引号的或裸的。**刻意不引 YAML 库**：为一个
//
//	字段引入 yaml 依赖不划算，而 frontmatter 本来就是「我们只认这几个
//	约定」的一部分 —— 认不出就落到规则 2、3，不报错。
func matchField(fm, key string) string {
	for _, line := range strings.Split(fm, "\n") {
		if line != strings.TrimLeft(line, " \t") {
			continue // 缩进的属于嵌套结构，不是顶层标量
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if v != "" {
			return v
		}
	}
	return ""
}

// sectionSummary 取「一句话 / TL;DR」类章节的第一段正文。
func sectionSummary(lines []string, inCode []bool) string {
	for i, line := range lines {
		if inCode[i] {
			continue
		}
		m := headingRe.FindStringSubmatch(line)
		if m == nil || len(m[1]) < 2 {
			continue // 只认 ## 及更深；# 是文档标题
		}
		if !isTldrHeading(m[2]) {
			continue
		}
		// 往下取第一段非空的普通正文，跳过标题、列表、表格、引用。
		for j := i + 1; j < len(lines); j++ {
			if inCode[j] {
				continue
			}
			t := strings.TrimSpace(lines[j])
			if t == "" || isStructural(t) {
				continue
			}
			return t
		}
	}
	return ""
}

// isStructural 判断一行是不是**结构性内容**（标题、列表、表格、引用、分隔线）。
//
// ★ 列表判据是「符号 + 空格/行尾」，不是「以符号开头」——
//
//	`⚠️ **这是重点**` 与 `- 列表项` 的区别就在那一个空格上。
//	只判 `HasPrefix(t, "*")` 的话，openheron 自己的 `## 0. 一句话` 全都被跳过
//	（它们的第一句几乎都拿 `**` 开头），于是规则 2 静默失效、10 份分册的
//	摘要全都降级成「状态：已实施」。这是本文件里最容易写错的一处判据。
func isStructural(t string) bool {
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, ">") || strings.HasPrefix(t, "|") {
		return true
	}
	if t == "---" || t == "***" || t == "___" {
		return true
	}
	for _, b := range []string{"-", "*", "+"} {
		rest, ok := strings.CutPrefix(t, b)
		if !ok {
			continue
		}
		if rest == "" || isBlank(rune(rest[0])) {
			return true
		}
	}
	// 有序列表：`1. ` / `12) `
	if i := strings.Index(t, "."); i > 0 && allDigits(t[:i]) {
		if rest := t[i+1:]; rest != "" && isBlank(rune(rest[0])) {
			return true
		}
	}
	if i := strings.Index(t, ")"); i > 0 && allDigits(t[:i]) {
		if rest := t[i+1:]; rest != "" && isBlank(rune(rest[0])) {
			return true
		}
	}
	return false
}

// isTldrHeading 判断一个标题是不是「摘要」标题。
func isTldrHeading(title string) bool {
	t := normHeading(title)
	for _, a := range tldrHeadings {
		if t == a {
			return true
		}
	}
	return false
}

// normHeading 归一化标题用于比较：去编号、去空白、转小写。
//
// ★ 去编号是必要的：openheron 写的是 `## 0. 一句话`，别人写的是 `## 摘要`。
//
//	只按字面量匹配的话，同一个意思要认两遍，而漏认一条的后果是这一份
//	文档走不到规则 2 —— 静默地降级到规则 3。
func normHeading(title string) string {
	t := strings.TrimSpace(strings.ToLower(title))
	// 去掉前导编号：`0.` `1.2` `§3` `3、` `一、`
	for _, p := range []string{"§", "一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"} {
		if strings.HasPrefix(t, p) {
			t = strings.TrimSpace(strings.TrimPrefix(t, p))
			break
		}
	}
	i := strings.Index(t, ".")
	if i > 0 && allDigits(t[:i]) {
		t = strings.TrimSpace(t[i+1:])
	}
	// 去掉尾部的编号修饰：`（架构）` 之类保留，它常常是关键词。
	return strings.Join(strings.Fields(t), " ")
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// quoteSummary 取首个 `>` 块的首行。
//
// ★ 要剥掉 `状态：**已实施**` 这类前缀：它是对**人**的状态说明，混进摘要
//
//	会稀释向量（§6.1）。而对词法检索**更好**：「已实施」在 10 份文档里
//	是极高频词，出现在每份的摘要里等于没出现。
func quoteSummary(lines []string, inCode []bool) string {
	seen := false
	for i, line := range lines {
		if inCode[i] {
			//	代码块里的 `>` 是 shell 的重定向。认它当引用块会让整个
			//	摘要变成一行 `> /dev/null`。
			continue
		}
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, ">") {
			// ★ 引用块结束。第一个块之后不再找 —— 否则会把正文里随手写的
			//   「> 引用某句话」当成摘要。
			if seen {
				return ""
			}
			continue
		}
		seen = true
		if s := trimStatusPrefix(trimQuotes(t)); s != "" {
			return s
		}
	}
	return ""
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, ">") {
		s = strings.TrimSpace(strings.TrimPrefix(s, ">"))
	}
	return strings.TrimSpace(strings.TrimSuffix(s, ">"))
}

// statusLabel 是摘要里要剥掉的状态标签。
const statusLabel = "状态"

// trimStatusPrefix 剥掉 `状态：…` 的**状态值**，保留真正有信息量的部分。
//
// ★ 剥的是「**状态**：**已实施**（K1–K4）；」这一段 —— 标签、紧跟其后的
//
//	那个加粗段、以及到第一处句读边界为止的部分。**保留后面的话**：
//	`状态：**重设计中** —— 本文档主体描述 v2，未实施` 里，
//	「重设计中」是状态、「本文档主体描述 v2」是内容。
//
// ★ 而「已实施」在 10 份分册里都出现，留在摘要里等于没出现：它对区分这
//
//	10 份文档没有任何贡献（§6.1）。而「重设计中」只在一份里出现。
//
// ★ `；` 与 `。` 的角色不同，不能一起当切割点：
//
//	`；` 是**分隔符**（状态之后还有内容）→ 取它**后面**
//	`。` 是**终止符**（那句话到此结束）→ 只在结尾去掉，取它**前面**
//
//	混为一谈的后果很具体：`状态：**重设计中** —— 本档主体描述 v2，未实施`
//	在「按 `。` 切、取后面」下会返回空 —— 句号后面本来就没有内容。
//
// ★ 而 `，`, **不进切割集**：中文里它是分句符，「本文档主体描述 v2，
//
//	未实施」整句都是内容。
//
// ★ 它**只**剥紧跟标签的那一个加粗段，不去猜状态的结束在哪 ——
//
//	`状态：已实施。知识库见 …` 这种写法不在本仓库的约定里。猜错的
//	代价是摘要被砍成半句，留着的代价只是多几个字。后者便宜得多。
func trimStatusPrefix(s string) string {
	rest, ok := strings.CutPrefix(s, statusLabel)
	if !ok {
		return s
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimLeft(rest, "：:")
	rest = strings.TrimSpace(rest)

	if body, ok := strings.CutPrefix(rest, "**"); ok {
		if _, tail, found := strings.Cut(body, "**"); found {
			rest = tail
		}
	}

	// 分隔符：取它后面。**从 i 而不是 i+1 开始** —— IndexAny 给的是**字节**
	// 下标，而 `；` 是三个字节，i+1 会落在 rune 中间，产出一串乱码。
	if i := strings.IndexAny(rest, "；;"); i >= 0 {
		return trimLead(rest[i:])
	}
	// 没有分隔符：剩下的整个都是内容，只去掉收尾的句号。
	rest = strings.TrimSpace(rest)
	rest = strings.TrimRight(rest, "。")
	return trimLead(rest)
}

func trimLead(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "；;。·—-– ")
	return strings.TrimSpace(s)
}

// extractSections 列出章节，按文档序。**默认全收**，只有超过
// [maxSectionsStored] 才砍（那是个安全网，不是预算 —— 见常量上的说明）。
//
// ★ **按行扫描，不经过 markdown 切分器**（§6.2）：切分器不产出行号，而
//
//	行号是模型唯一能直接用的跳转信息（§14.1）。而摘要规则 2 需要的标题
//	也在手上，不复用反而省掉「切出来的正文再丢掉」。
//
// ★ 那个「复用切分器」的 v1 理由（要正文）已经随粒度改变而失效 ——
//
//	**粒度换了，理由就得跟着换，不能沿用**。
func extractSections(lines []string, inCode []bool) ([]Section, int) {
	var out []Section
	total := 0
	for i, line := range lines {
		if inCode[i] {
			continue // 代码块里的 `## ` 是注释，不是章节（见 splitLines）
		}
		m := headingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// 一级标题是文档标题，不是章节 —— 它已经在 [Entry.Title] 里，
		// 再列一遍会让「章节」与「标题」两个概念在目录里混起来。
		if len(m[1]) < 2 {
			continue
		}
		total++
		if len(out) >= maxSectionsStored {
			continue // 继续数，但不再收 —— 要报准确的砍掉数
		}
		out = append(out, Section{Title: strings.TrimSpace(m[2]), Line: i + 1})
	}
	return out, total - len(out)
}

// extractLinks 抽出指向语料内其他文档的链接。
//
// ★ 这些边**已经写在文本里**，v1 只是看不见它们（§6.3）。openheron 自己的
//
//	docs 实测：78 处链接写法，去重后 48 条唯一边（architecture.md 出链 9
//	条最多，checkpoint-store.md 1 条最少）。而这个规模下它们是**唯一有效
//	的边类型** —— XERJ 那条实测数据说：10 份文档的语料上，任何读「文档
//	说了什么」的边检测器都产不出一条。
func extractLinks(body, rel string) []string {
	matches := mdLinkRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		l, ok := resolveLink(m[1], rel)
		if !ok || l == rel || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveLink 把一条链接解析成语料内的相对路径。
//
// ★ 三件事必须在这里做，缺一件就会造出**指向不存在的东西的边**：
//
//	剥锚点     `x.md#section` → `x.md`
//	判协议     `https://e.com/x.md` → 不是语料内的边
//	按当前文档解析相对路径   `a/b.md` 里的 `../c.md` 是语料根的 c.md
//
// ★ 「语料内」必须按**解析后的路径**判，不能按字符串判：`../a.md` 与
//
//	`a.md` 长得完全不同，但指向的东西可能一个在内一个在外。
//
// ★ 边界判据只有一条：**清理后以 `..` 开头就出了语料根**。不需要知道语料
//
//	目录叫什么名字 —— 路径本身已经是相对语料根的（§6.3），而「出了根」在
//	相对路径里就是 `..`。拿目录名去前缀匹配是多引入一个概念去表达同一件事，
//	而它一旦写错，错的方式是「某几条边被静默剔除」。
func resolveLink(target, from string) (string, bool) {
	// 剥锚点与查询。
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return "", false
	}
	// ★ 带 scheme 的一律不是语料内的边。注意 `//host/x.md`（协议相对）
	//   也要排除 —— 它以 `//` 开头但没有 scheme。
	if strings.Contains(target, "://") || strings.HasPrefix(target, "//") {
		return "", false
	}
	// Windows 盘符路径同理。
	if len(target) > 1 && target[1] == ':' {
		return "", false
	}

	rel := target
	if strings.HasPrefix(target, "/") {
		// 语料根锚点：`/a.md` 是语料根的 a.md。
		rel = strings.TrimPrefix(target, "/")
	} else if from != "" {
		rel = path.Clean(path.Dir(from) + "/" + target)
	}
	rel = strings.TrimPrefix(rel, "./")

	// 出了语料根。记进去就等于「有一条边指向一个不存在的文档」，而那在
	// 检索时表现为一处打不开的引用。
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	// 不是文件（`.`、空串、或解析回了目录本身）。
	if rel == "." || rel == "" {
		return "", false
	}
	return rel, true
}

// metaStrings 抽 MetaData 里的字符串切片，任何非字符串元素都按 fmt.Sprint
// 转换 —— 而不是整条丢弃。
//
// ★ 不整条丢弃是有理由的：MetaData 里的切片来自 `defaultDocumentToFields`
// 与调用方，`any` 装的东西不受我们控制，悄悄丢一整条比转成字符串糟。
func metaStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if e == nil {
				continue
			}
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}

// metaInt 取 MetaData 里的整数，接受 int/int64 等。
func metaInt(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case float64:
		// JSON 往返之后 int 会变成 float64 —— 那一路是从接口来的时候。
		return int64(t)
	case float32:
		return int64(t)
	case uint64:
		return int64(t)
	default:
		return 0
	}
}

// isBlank 判一个 rune 是不是可当分隔符的空白。
func isBlank(r rune) bool { return unicode.IsSpace(r) }
