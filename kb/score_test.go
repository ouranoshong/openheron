package kb

import (
	"math"
	"testing"
)

func TestParseQuerySplitsChinesePerCharacter(t *testing.T) {
	got := parseQuery("配置写入为什么不能写合并视图").terms
	want := []string{"配", "置", "写", "入", "为", "什", "么", "不", "能", "合", "并", "视", "图"}
	if len(got) != len(want) {
		t.Fatalf("terms = %q (len %d), want %q (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("term[%d] = %q, want %q (the whole query was cut into one token ⇒ every document scores 0)",
				i, got[i], want[i])
		}
	}
}

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

func TestLexScoreTier1IdentifierSubstring(t *testing.T) {
	e := Entry{
		Path:     "kb-design.md",
		Title:    "openheron 知识库设计",
		Summary:  "一份关于语料的目录",
		Sections: []Section{{Title: "8.4 DistanceThreshold 在融合分下的语义", Line: 1}},
	}
	q := parseQuery("DistanceThreshold")
	if got := lexScore(q, e); got != wIdentSubstring {
		t.Errorf("lexScore = %v, want %v (it appears verbatim in a section title)", got, wIdentSubstring)
	}
}

func TestLexScoreTier2IdentWord(t *testing.T) {
	e := Entry{
		Path:    "checkpoint-store.md",
		Title:   "openheron 组件设计：Checkpoint Store",
		Summary: "checkpoint 在落盘前先写临时文件",
	}
	q := parseQuery("checkpoint")
	got := lexScore(q, e)
	if got != wIdentWord {
		t.Errorf("lexScore = %v, want %v (the whole word is in the summary, but not in a section title or an outgoing link)", got, wIdentWord)
	}
}

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
		t.Errorf("lexScore = %v, want %v (the substring tier catches it first; there is no third tier)", got, wIdentSubstring)
	}

	e2 := Entry{Path: "y.md", Summary: "SetIdentityFoo 是另一个名字"}
	if got := lexScore(parseQuery("SetIdentity"), e2); got != wProseSubstring {
		t.Errorf("lexScore = %v, want %v (a fragment of another word: it cannot reach the whole-word tier, only the substring tier)",
			got, wProseSubstring)
	}
	if got := lexScore(parseQuery("UnrelatedToken"), e2); got != 0 {
		t.Errorf("lexScore = %v, want 0 (a token that does not appear in the document)", got)
	}
}

func TestLexScoreTier4ProseSubstring(t *testing.T) {
	e := Entry{
		Path:    "config-editor-design.md",
		Title:   "配置编辑器",
		Summary: "合并视图只读，因为它同时踩四个坑",
	}
	q := parseQuery("视")
	got := lexScore(q, e)
	if got != wProseSubstring {
		t.Errorf("lexScore = %v, want %v (it only appears as a substring in the summary)", got, wProseSubstring)
	}
}

func TestLexScoreCountsEachTermOnce(t *testing.T) {
	e := Entry{
		Path:    "a.md",
		Title:   "热更新 热更新 热更新",
		Summary: "热更新 热更新 热更新 热更新",
	}
	q := parseQuery("热更新")
	got := lexScore(q, e)
	if got != 1.0 {
		t.Errorf("lexScore = %v, want 1.0 (each of the three characters hits once; the cap is 1)", got)
	}
}

func TestLexScoreNormalizesByTermCount(t *testing.T) {
	all := Entry{Path: "a.md", Summary: "alpha beta gamma delta epsilon"}
	some := Entry{Path: "b.md", Summary: "alpha beta"}
	q := parseQuery("alpha beta gamma delta epsilon")

	if got := lexScore(q, all); got != wIdentWord {
		t.Fatalf("5/5 hits = %v, want %v", got, wIdentWord)
	}

	b := lexScore(q, some)
	want := wIdentWord * 2 / 5
	if math.Abs(b-want) > 1e-9 {
		t.Fatalf("2/5 hits = %v, want %v (the tokens that did not hit are not in the denominator)", b, want)
	}
	if b >= wIdentWord {
		t.Fatalf("2/5 hits = %v reached the full tier %v — the denominator was dropped", b, wIdentWord)
	}

	more := Entry{Path: "c.md", Summary: "alpha beta gamma"}
	half := lexScore(parseQuery("alpha beta gamma delta epsilon"), more)
	full := lexScore(parseQuery("alpha beta"), more)
	if full <= half {
		t.Errorf("2/2 hits (%v) is not above 3/5 hits (%v) — dividing by the total did not take effect", full, half)
	}
	if math.Abs(half-wIdentWord*3/5) > 1e-9 {
		t.Errorf("3/5 hits = %v, want ≈%v", half, wIdentWord*3/5)
	}
}

func TestLexScoreEmptyQueryIsZero(t *testing.T) {
	e := Entry{Path: "a.md", Title: "标题", Summary: "摘要"}
	if got := lexScore(parseQuery(""), e); got != 0 {
		t.Errorf("lexScore(empty query) = %v, want 0", got)
	}
	if got := lexScore(parseQuery("   "), e); got != 0 {
		t.Errorf("lexScore(blank query) = %v, want 0", got)
	}
}

func TestCosineZeroVectorIsNotNaN(t *testing.T) {
	if got := cosine(nil, []float32{1, 2}); got != 0 {
		t.Errorf("cosine(nil, …) = %v, want 0", got)
	}
	if got := cosine([]float32{0, 0, 0}, []float32{1, 2, 3}); got != 0 {
		t.Errorf("cosine(zero vector, …) = %v, want 0", got)
	}
	if got := cosine([]float32{1, 2}, []float32{1}); got != 0 {
		t.Errorf("cosine(mismatched dimensions) = %v, want 0", got)
	}
	got := cosine([]float32{1, 0}, []float32{1, 0})
	if math.IsNaN(got) || math.Abs(got-1) > 1e-9 {
		t.Errorf("cosine(identical vectors) = %v, want 1", got)
	}
}
