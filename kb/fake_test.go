package kb

import (
	"bytes"
	"context"
	"encoding/gob"
	"hash/fnv"
	"math"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
)

// fakeEmbedder 是确定性的向量化替身。
//
// ★ 它按**词袋**算向量而不是按文本哈希 —— 只有这样检索测试才有意义：
//
//	哈希派生的向量两两之间夹角随机，「召回最相关的」无从断言。词袋让共享
//	词的文本真的更相似，TopK 的排序才可断言。
type fakeEmbedder struct {
	dim int

	mu    sync.Mutex
	calls [][]string // 每次请求的文本条数，用于验分批

	failWhen func(n int) error
	// beforeEach 在每次请求**之前**跑，给测试一个「索引跑到一半」的钩子。
	beforeEach func()
}

var _ embedding.Embedder = (*fakeEmbedder)(nil)

func newFakeEmbedder(dim int) *fakeEmbedder { return &fakeEmbedder{dim: dim} }

func (f *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) (
	[][]float64, error) {

	if f.beforeEach != nil {
		f.beforeEach()
	}

	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), texts...))
	fail := f.failWhen
	f.mu.Unlock()

	if fail != nil {
		if err := fail(len(texts)); err != nil {
			return nil, err
		}
	}
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = f.vector(t)
	}
	return out, nil
}

func (f *fakeEmbedder) GetType() string          { return "Fake" }
func (f *fakeEmbedder) IsCallbacksEnabled() bool { return false }

// batches 返回历次请求的条数序列 —— 验「BatchSize 真的在分批」的唯一手段。
func (f *fakeEmbedder) batches() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.calls))
	for i, c := range f.calls {
		out[i] = len(c)
	}
	return out
}

// embeds 返回历次请求里的全部文本 —— 验「送进去的是不是该送的那些」。
func (f *fakeEmbedder) embeds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c...)
	}
	return out
}

func (f *fakeEmbedder) vector(text string) []float64 {
	v := make([]float64, f.dim)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		// 同一个词落进同一桶要**累加**而不是覆盖，否则词频不影响相似度，
		// 而「这段话反复讲配置」正是我们要能表达的信息。
		v[int(h.Sum32()%uint32(f.dim))]++
	}
	var n float64
	for _, x := range v {
		n += x * x
	}
	if n == 0 {
		return v
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] /= n
	}
	return v
}

// newDoc 造一份**文档级**的文档：`id` 是语料根相对路径，`content` 是这份
// 文档的文字。
//
// ★ v2 的文档必须带着 [Entry] —— [entriesOf] 拒绝没有它的文档，因为章节
//
//	行号无法从 EmbedText 重建（见那边注释）。这里给一份最小可用的 Entry，
//	把 `content` 当摘要装进去，好让 `len(EmbedText()) == len(content)`：
//	这几处用例钉的是**分批预算**与**距离阈值**，两者的判据都必须还是调用方
//	传进来的那段文字，否则断言会去验一个没人关心的拼接结果。
//
//	要验「没有 Entry 会被拒」的用例自己拼裸文档，别用这个函数。
func newDoc(id, content string, meta map[string]any) *schema.Document {
	d := docOf(Entry{Path: id, Summary: content})
	for k, v := range meta {
		d.MetaData[k] = v
	}
	return d
}

// newDocOf 造一个带 [Entry] 的文档 —— v2 的索引器认的是这个。
func newDocOf(e Entry) *schema.Document { return docOf(e) }

// seedEntry 直接造一条已落盘的条目（带向量），绕过流水线。
//
// ★ **必须带 Scope**：不带就是全语料对账，语义是「不在 Fresh 里的都没了」——
//
//	于是连着 seed 两条时第二条会把第一条删掉，而症状是「测试里只有一份文档」，
//	看起来像检索丢结果。
//
// ★ 阈值与打分测试要用它：流水线会把标题、摘要、章节一起拼进 EmbedText，
//
//	而那些测试要的是「这两份文档在词袋下谁更像」这种可断言的关系。
func seedEntry(t *testing.T, st *Store, e Entry, vec []float32) {
	t.Helper()
	if _, err := st.Reconcile(context.Background(), ReconcileRequest{
		Fresh: []Entry{e},
		Scope: []string{e.Path},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if vec != nil {
		if err := st.SetVectors(map[string][]float32{e.Path: vec}); err != nil {
			t.Fatalf("SetVectors: %v", err)
		}
	}
}

// byModel 声明一份索引的身份 —— 就是装配层建索引前做的那件事。
func byModel(t *testing.T, st *Store, model string) {
	t.Helper()
	if err := st.SetIdentity(model, 0); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
}

// writeSidecarWithVersion 手工造一份指定版本的侧车 —— 用来验「版本不符要清向量」。
func writeSidecarWithVersion(path string, ver int) error {
	d := Derived{
		IndexVersion: ver,
		Entries: map[string]DerivedEntry{
			"a.md": {EmbedText: "x", Vector: []float32{1}},
		},
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&d); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
