package kb

import (
	"math"
	"strings"
	"unicode"
)

// 打分权重。**符号级**（§8.2），从精确到宽泛三档。
//
// ★ 它们不归一化、不做 IDF：那需要语料级统计，也就需要持久化统计，而
//
//	§8.1 的承诺是「不持久化任何东西」。代价是没有 IDF 意味着常见词不被降权，
//	换来的是「没有一份会悄悄过期的统计文件」。
//
// ★ 顺序**不能**颠倒 —— 它们表达的是「这个命中有多确定」：
//
//	1.00  identifier 子串     `DistanceThreshold` 在章节名里逐字出现
//	0.80  标识符边界内整词    `checkpoint` 作为整个标识符出现在提纲里
//	0.40  标题/摘要子串      摘要里提到「合并视图」—— 只说明在讲这个话题
//
// ★ §8.2 的表里还有一档 0.60「章节名整词」，这里**没有**对应分支 —— 那不是
//
//	漏写，是那一档在逻辑上排不到。`hasWord(章节名, t)` 成立就蕴含
//	`Contains(章节名, t)`，而章节名整个在 identifier 语料里（标识符大多住在
//	章节名里），于是「整词」这个更严的条件**永远被更松的子串条件先接住**。
//	写上它就是一个「读代码的人以为它生效」的权重，比不写更糟。§8.2 记了这条。
const (
	wIdentSubstring = 1.0
	wIdentWord      = 0.8
	wProseSubstring = 0.4
)

// queryTerms 是一次查询解析出来的符号。
//
// ★ 解析一次、对全部文档复用 —— 每个文档重解析一遍的话，10 份文档的查询
//
//	要做 10 次切分，而切分结果只取决于查询本身。
type queryTerms struct {
	// terms 是去重后的查询符号。**去重**很关键：`go mod go mod tidy` 里的
	// `go mod` 只该算一次，否则重复词会把分数抬高到与「命中更多字段」无法区分。
	terms []string
}

// parseQuery 把查询切成符号。
//
// ★ 切法：**字母数字下划线连成一串，中文每个字单独成一个符号**。注意这两半
//
//	的判据**不是**同一个 —— 语料侧的 `hasIdentWord` 用 [isIdentRune]（含
//	中文，那是判边界用的），而这里中文要先摘出来按字切。混用会让整句中文
//	变成一个符号，见下面 switch 里的注释。
//
// ★ 中文按**字**而不是按词 —— 没有分词器（§12.5 已经因此拒绝过一次）。
//
//	按字的代价是「热更新」切成「热」「更」「新」，单字命中太宽；好处是
//	「配置热更新」与「热更新」能通过共享的字重叠上，而这正是没有分词器时
//	唯一能拿到的召回。
func parseQuery(q string) queryTerms {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			add(b.String())
			b.Reset()
		}
	}
	for _, r := range q {
		switch {
		// ★ 中文**必须排在 `isIdentRune` 前面**。
		//
		//	`unicode.IsLetter('配')` 是 true —— 汉字在 Unicode 里就是 Letter(Lo)。
		//	把 `isIdentRune` 放在前面的话，这一支**永远轮不到**，于是整句
		//	「配置写入为什么不能写合并视图」被当成**一个**符号，而它不会作为
		//	子串出现在任何文档里 ⇒ 所有文档都是 0 分 ⇒ 排序退化成按路径升序，
		//	查询像没发生过一样。这正是 M3 验收（v1 附录二那两条召回）抓出来的：
		//	中文查询**一条都召不回**。
		//
		//	`isIdentRune` 把中文算作标识符字符是对的，但那是**语料侧**的事
		//	（`hasIdentWord` 靠它判边界）；查询侧要的是按字切。
		case unicode.Is(unicode.Han, r):
			flush() // 先冲掉手上这串英文，免得 `配置go` 变成一个符号
			add(string(r))
		case isIdentRune(r):
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return queryTerms{terms: out}
}

// scoreDoc 算一份文档与查询的**融合**分（§8.2）。
//
// ★ **永远与余弦融合，不以任何一路单独定胜负**。两路各管一半：
//
//	向量负责同义（「热更新」与「hot reload」），符号负责精确
//	（`DistanceThreshold` 这种语义猜不出来的东西）。
//
// ★ 融合权重固定 0.5/0.5，**不去平衡两个量纲**。理由是符号分永不为负且
//
//	普遍偏小，于是 0.5*lex + 0.5*cos 天然由 cos 主导 —— 而这正是该有的
//	行为：自然语言查询上符号平庸，标识符查询上符号精确，两件事不在同一批
//	查询上竞争，不需要一方让步。
//
// ★ 一路缺席时用另一路，**不做加权平均**。无向量时把 cos 当 0 会把所有
//
//	分数压半，于是 TopK 的相对顺序不变但 DistanceThreshold 的门槛失真 ——
//	而门槛是用户显式配的，悄悄改变它的含义是本包要根治的那类失效。
func scoreDoc(q queryTerms, e Entry, vec []float32, qvec []float32) float64 {
	lex := lexScore(q, e)
	if len(vec) == 0 || len(qvec) == 0 || len(vec) != len(qvec) {
		return lex
	}
	cos := cosine(vec, qvec)
	if lex == 0 {
		return cos
	}
	return 0.5*lex + 0.5*cos
}

// lexScore 是符号级打分（§8.2）。
//
// ★ **每个查询符号取它命中的最高档**，然后除以符号总数。
//
//	除以总数是为了让「命中 2/2」高于「命中 3/5」—— 否则一个含五个词的
//	长查询天然比短查询分高，而那与相关性无关。
//
// ★ 一个符号**只加一次分**：命中三遍与命中一遍同分。这是刻意的 ——
//
//	词频在没有 IDF 的情况下会把「反复提到的常见词」抬成主导。
//
// ★ 三档的文本**不能互相同含**，否则上面那句「取最高档」就是空话 ——
//
//	第一个 case 一旦包住后面几个，后面那几个写不写都一样。见下面各文本的注释。
func lexScore(q queryTerms, e Entry) float64 {
	if len(q.terms) == 0 {
		return 0
	}

	// ★ identifier 语料 = 标题 + 章节名 + 出链，**刻意不含摘要**。
	//
	//	含摘要会一次性废掉下面两档：`hasIdentWord(x, t)` 内部先找子串，找不到
	//	直接返回 false —— 它排在 `strings.Contains(x, t)` 之后时**永远轮不到**；
	//	而摘要并进 x 之后 `proseText ⊆ x`，最弱那档也没了。三档一起退化成
	//	「命中几个符号 / 总共几个」，权重全变成装饰。
	//
	//	更要紧的是**分数会变平**：摘要里随口一提与章节名里的精确标识符同为
	//	1.0，而这一层唯一的产出就是把这两件事分开。
	identText := e.Title + "\n" + sectionTitles(e) + linkText(e)
	// 整词判定放在**全部提纲**上：标题与摘要里同样会出现完整的标识符
	// （`persist.FileLock` 写进摘要也是它）—— 这一档比上面窄（要求边界），
	// 又比下面宽（覆盖摘要），正好落在中间。
	outline := e.EmbedText()
	// 标题 + 摘要：最弱的一档只说明「这份文档在讲这个话题」。
	//
	//	中文按字切之后这一档是中文查询的**主力**：一个汉字夹在汉字中间，
	//	两边都是标识符字符，「标识符整词」对它永远不成立。
	proseText := e.Title + "\n" + e.Summary

	var sum float64
	for _, t := range q.terms {
		switch {
		case strings.Contains(identText, t):
			sum += wIdentSubstring
		case hasIdentWord(outline, t):
			sum += wIdentWord
		case strings.Contains(proseText, t):
			sum += wProseSubstring
		}
	}
	return sum / float64(len(q.terms))
}

// linkText 是出链那一段文本，[lexScore] 的 identifier 语料里用。
//
// ★ 出链是目录里**最像标识符**的东西：文件名与锚点逐字出现在那里，
//
//	而「`api-v2-design.md` 里是怎么写的」正是 openheron 查询的典型形状。
func linkText(e Entry) string {
	if len(e.Links) == 0 {
		return ""
	}
	return strings.Join(e.Links, " ") + "\n"
}

func sectionTitles(e Entry) string {
	var b strings.Builder
	for _, s := range e.Sections {
		b.WriteString(s.Title)
		b.WriteString("\n")
	}
	return b.String()
}

// isIdentRune 判定一个 rune 是否属于「标识符」字符集。
//
// ★ **字母数字下划线，含中文**。中文放进来是为了**语料侧**的边界判定：
// openheron 的语料是中英混排，「热更新」这类词在正文里连续出现，把中文算作
// 标识符字符才能让「一个标识符」覆盖整个连续中文串。
//
// ★ 这一条**只管语料侧**。查询侧不是这套切法 —— [parseQuery] 把中文按单字
// 摘出来（见那边的注释：混用会让整句中文变成一个符号，中文查询全 0 分）。
// 两处判据不同是刻意的，不是不一致。
func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// hasIdentWord 判定 word 是否**在标识符边界内**等于 identText 里的某个词。
//
// ★ 「标识符边界」= 非标识符字符。`SetIdentity` 在 `func (s *Store) SetIdentity(`
//
//	里算命中，而在 `SetIdentityFoo` 里不算 —— 后者是另一个名字，报它是错的。
func hasIdentWord(text, word string) bool {
	if word == "" {
		return false
	}
	start := 0
	for {
		i := strings.Index(text[start:], word)
		if i < 0 {
			return false
		}
		lo := start + i
		hi := lo + len(word)
		if (lo == 0 || !isIdentRune(rune(text[lo-1]))) &&
			(hi >= len(text) || !isIdentRune(rune(text[hi]))) {
			return true
		}
		start = lo + 1
		if start >= len(text) {
			return false
		}
	}
}

// ★ 这里曾有一个 `hasWord`（按空白/标点当边界的整词匹配），是 §8.2 那档
//	0.60「章节名整词」的谓词。它被删了 —— 见上面常量块里那段：那一档永远
//	排不到，留着一个无处可调的谓词只会让人以为它在起作用。

// cosine 算两个向量的余弦相似度，返回 **[-1, 1]**。
//
// ★ 任一为零向量时返回 0 —— 而不是 NaN。零向量是嵌入服务出错时的典型产物，
//
//	把它变成 NaN 会让整批分数变成 NaN，而 NaN 在排序里既不小于也不大于任何数：
//	排序结果未定义，却没有任何东西会说。
func cosine(a, b []float32) float64 {
	n := len(a)
	if n == 0 || len(b) != n {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// normalize 把向量缩放到单位长度。
//
// ★ 存**原样**向量、检索时算余弦，而不是存归一化后的 —— 前者让侧车里的
//
//	向量可以直接喂给别的余弦实现，而后者会让「这份向量的模长」这个信息
//	永久丢失（有些嵌入服务的模长携带置信度）。
func normalize(v []float32) []float32 {
	var sq float64
	for _, x := range v {
		sq += float64(x) * float64(x)
	}
	if sq == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sq))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}
