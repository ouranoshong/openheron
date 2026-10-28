package kb

import (
	"math"
	"testing"
)

// 这个文件钉住 docs/kb-design.md §8.2 的**符号级加权**。实际是三档：
//
//	1.00  identifier 子串
//	0.80  标识符边界内整词
//	0.40  标题/摘要子串
//
// §8.2 的表里那档 0.60「章节名整词」**不实现** —— 见
// [TestLexScoreHasNoSectionWordTier]：它与第一档比的是同一片文本，永远排不到。
//
// ★ 它必须是**单元测试**而不是只靠召回用例：档位里任何一档变成不可达，
//   召回用例照样可能全绿 —— 因为不可达的档位只是让分数**变平**，而变平的
//   分数在「谁排第一」这种断言上看不出来（并列时按路径升序，路径恰好是
//   对的那次就混过去了）。

func TestParseQuerySplitsChinesePerCharacter(t *testing.T) {
	got := parseQuery("配置写入为什么不能写合并视图").terms
	// 中文按字切，且**去重**（写 出现两次只算一次 —— 否则重复词会把分数抬高
	// 到与「命中更多字段」无法区分）。
	want := []string{"配", "置", "写", "入", "为", "什", "么", "不", "能", "合", "并", "视", "图"}
	if len(got) != len(want) {
		t.Fatalf("terms = %q (len %d), want %q (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("term[%d] = %q, want %q（整份切成了一个符号 ⇒ 所有文档 0 分）",
				i, got[i], want[i])
		}
	}
}

// ★ 中英混排要**冲干净**：`配置go` 不能变成一个符号，否则它不会作为子串
//
//	出现在任何文档里 —— 一次精确匹配就这么丢了。
func TestParseQueryFlushesIdentAcrossHan(t *testing.T) {
	got := parseQuery("openheron 配置go v2").terms
	want := []string{"openheron", "配", "置", "go", "v2"}
	if len(got) != len(want) {
		t.Fatalf("terms = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("term[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestLexScoreTier1IdentifierSubstring 那个词**逐字出现在标识符语料里**。
//
//	`DistanceThreshold` 是 §8.2 点名的例子：它在文档里的关系不写在任何一句
//	自然语言里，语义那一路猜不出来 —— 所以它值 1.00。
func TestLexScoreTier1IdentifierSubstring(t *testing.T) {
	e := Entry{
		Path:     "kb-design.md",
		Title:    "openheron 知识库设计",
		Summary:  "一份关于语料的目录",
		Sections: []Section{{Title: "8.4 DistanceThreshold 在融合分下的语义", Line: 1}},
	}
	q := parseQuery("DistanceThreshold")
	if got := lexScore(q, e); got != wIdentSubstring {
		t.Errorf("lexScore = %v, want %v（逐字出现在章节名里）", got, wIdentSubstring)
	}
}

// TestLexScoreTier2IdentWord 标识符**整词**命中，但只出现在标题/摘要里 ——
// 那时它不在标识符语料（章节名 + 出链）中，值 0.80。
func TestLexScoreTier2IdentWord(t *testing.T) {
	e := Entry{
		Path:    "checkpoint-store.md",
		Title:   "openheron 组件设计：Checkpoint Store",
		Summary: "checkpoint 在落盘前先写临时文件",
	}
	q := parseQuery("checkpoint")
	got := lexScore(q, e)
	if got != wIdentWord {
		t.Errorf("lexScore = %v, want %v（整词在摘要里，但不在章节名/出链里）", got, wIdentWord)
	}
}

// TestLexScoreHasNoSectionWordTier 钉住 §8.2 那一档 0.60「章节名整词」**不存在**。
//
// ★ 这条用例不是在测某个权重，而是在测「它为什么必须不存在」：`hasWord(章节名, t)`
//
//	成立就蕴含 `Contains(章节名, t)`，而章节名整个在 identifier 语料里，于是
//	更严的「整词」条件永远被更松的「子串」条件先接住 —— 0.60 排不到，是一个
//	读代码的人会以为生效的权重。
//
// ★ 用 `_` 分隔的片段当靶子，是因为它是唯一可能**看起来**该落进第三档的东西：
//
//	`lib_sse_ts` 里的 `sse` 两侧都是 `_`，不是标识符字符，所以「标识符整词」
//	不成立；但「子串」这一档不要求边界，于是它照样拿到 1.00。**这就是结论**：
//	想让 0.60 派上用场，得先让子串那一档看不见它，而两者比的是同一片文本。
func TestLexScoreHasNoSectionWordTier(t *testing.T) {
	e := Entry{
		Path:    "x.md",
		Title:   "标题",
		Summary: "摘要",
		Sections: []Section{
			{Title: "lib_sse_ts 分帧", Line: 1},
		},
	}
	got := lexScore(parseQuery("sse"), e)
	if got != wIdentSubstring {
		t.Errorf("lexScore = %v, want %v（子串档先接住，没有第三档）", got, wIdentSubstring)
	}

	// 反过来验一遍边界判定本身没坏：标识符整词那档**仍然**要求两侧是非
	// 标识符字符。`SetIdentityFoo` 里的 `SetIdentity` 是另一个名字的一截 ——
	// 它拿不到 0.80，只拿得到最弱的 0.40（摘要子串）。
	//
	// ★ 期望值**不是 0**：`strings.Contains` 这一档不要求边界，所以「它是
	//   别人的一截」只会让分数变低，不会让它消失。要验「完全没命中」得用一个
	//   文档里根本不出现的符号，那是 [TestLexScoreEmptyQueryIsZero] 之外的另一件事。
	e2 := Entry{Path: "y.md", Summary: "SetIdentityFoo 是另一个名字"}
	if got := lexScore(parseQuery("SetIdentity"), e2); got != wProseSubstring {
		t.Errorf("lexScore = %v, want %v（别人的一截：拿不到整词档，只拿得到子串档）",
			got, wProseSubstring)
	}
	// 完全不相干的符号 ⇒ 三档都没命中 ⇒ 0。
	if got := lexScore(parseQuery("UnrelatedToken"), e2); got != 0 {
		t.Errorf("lexScore = %v, want 0（文档里不出现的符号）", got)
	}
}

// TestLexScoreTier4ProseSubstring 标题/摘要里的**子串**命中 —— 最弱的一档：
// 只说明「这份文档在讲这个话题」。
//
// ★ 中文按字切之后这一档是中文查询的**主力**：一个汉字通常夹在汉字中间，
//
//	两边都是标识符字符，于是「标识符整词」永远不成立。
func TestLexScoreTier4ProseSubstring(t *testing.T) {
	e := Entry{
		Path:    "config-editor-design.md",
		Title:   "配置编辑器",
		Summary: "合并视图只读，因为它同时踩四个坑",
	}
	q := parseQuery("视")
	got := lexScore(q, e)
	if got != wProseSubstring {
		t.Errorf("lexScore = %v, want %v（只在摘要里作为子串出现）", got, wProseSubstring)
	}
}

// ★ 每个查询符号只加一次分：命中三遍与命中一遍同分。这是刻意的 ——
//
//	词频在没有 IDF 的情况下会把「反复提到的常见词」抬成主导（§8.2）。
func TestLexScoreCountsEachTermOnce(t *testing.T) {
	e := Entry{
		Path:    "a.md",
		Title:   "热更新 热更新 热更新",
		Summary: "热更新 热更新 热更新 热更新",
	}
	q := parseQuery("热更新")
	got := lexScore(q, e)
	if got != 1.0 {
		t.Errorf("lexScore = %v, want 1.0（三个字各命中一次，上限就是 1）", got)
	}
}

// ★ 除以符号总数是为了让「命中 2/2」高于「命中 3/5」—— 否则一个含五个词的
//
//	长查询天然比短查询分高，而那与相关性无关（§8.2）。
//
// ★ 用拉丁标识符当靶子，**不用中文**：中文按字切，`热更新 装配` 是五个符号
//
//	而不是两个 —— 拿它写这条用例会把「命中率」算成一个自己都没预期的数，
//	而症状是「断言想验分母、实际验的是分词」。逐字切这件事由
//	[TestParseQuerySplitsChinesePerCharacter] 单独钉。
func TestLexScoreNormalizesByTermCount(t *testing.T) {
	// 五个符号，五个全中。
	all := Entry{Path: "a.md", Summary: "alpha beta gamma delta epsilon"}
	// 同一片提纲，只中两个。
	some := Entry{Path: "b.md", Summary: "alpha beta"}
	q := parseQuery("alpha beta gamma delta epsilon")

	if got := lexScore(q, all); got != wIdentWord {
		t.Fatalf("5/5 命中 = %v, want %v", got, wIdentWord)
	}

	// 关键那半边：**没命中的符号也要进分母**。
	b := lexScore(q, some)
	want := wIdentWord * 2 / 5
	if math.Abs(b-want) > 1e-9 {
		t.Fatalf("2/5 命中 = %v, want %v（未命中的符号没有进分母）", b, want)
	}
	if b >= wIdentWord {
		t.Fatalf("2/5 命中 = %v 达到了满档 %v —— 分母丢了", b, wIdentWord)
	}

	// 「命中 2/2」必须高于「命中 3/5」：这才是除以总数的全部意义。
	//
	// ★ 比大小，不比等号：这条用例验的是**次序**（除以总数让长查询不占便宜），
	//   而 `wIdentWord*3/5` 走的是乘除、`sum/len` 走的是累加，两条路差在
	//   尾数上（0.4800000000000001 vs 0.48）。拿等号去断言等于把「次序对不对」
	//   换成「浮点这两条路是否逐位相同」—— 后者不是这里要钉的东西。
	more := Entry{Path: "c.md", Summary: "alpha beta gamma"}
	half := lexScore(parseQuery("alpha beta gamma delta epsilon"), more)
	full := lexScore(parseQuery("alpha beta"), more)
	if full <= half {
		t.Errorf("命中 2/2 (%v) 没有高于命中 3/5 (%v) —— 除以总数没生效", full, half)
	}
	// 3/5 的位置仍然要对得上：允许尾数误差，不允许差一整档。
	if math.Abs(half-wIdentWord*3/5) > 1e-9 {
		t.Errorf("3/5 命中 = %v, want ≈%v", half, wIdentWord*3/5)
	}
}

// ★ 空查询不除零，直接 0 分 —— 0 分意味着「并列」，而并列在 sortMatches 里
//
//	按路径升序稳定排，不会变成随机顺序。
func TestLexScoreEmptyQueryIsZero(t *testing.T) {
	e := Entry{Path: "a.md", Title: "标题", Summary: "摘要"}
	if got := lexScore(parseQuery(""), e); got != 0 {
		t.Errorf("lexScore(空查询) = %v, want 0", got)
	}
	if got := lexScore(parseQuery("   "), e); got != 0 {
		t.Errorf("lexScore(空白查询) = %v, want 0", got)
	}
}

// ★ 零向量返回 0 而不是 NaN —— NaN 在排序里既不小于也不大于任何数，
//
//	于是排序结果**未定义**，却没有任何东西会说（§8 余弦的注释同理）。
func TestCosineZeroVectorIsNotNaN(t *testing.T) {
	if got := cosine(nil, []float32{1, 2}); got != 0 {
		t.Errorf("cosine(nil, …) = %v, want 0", got)
	}
	if got := cosine([]float32{0, 0, 0}, []float32{1, 2, 3}); got != 0 {
		t.Errorf("cosine(零向量, …) = %v, want 0", got)
	}
	if got := cosine([]float32{1, 2}, []float32{1}); got != 0 {
		t.Errorf("cosine(维度不等) = %v, want 0", got)
	}
	got := cosine([]float32{1, 0}, []float32{1, 0})
	if math.IsNaN(got) || math.Abs(got-1) > 1e-9 {
		t.Errorf("cosine(相同向量) = %v, want 1", got)
	}
}
