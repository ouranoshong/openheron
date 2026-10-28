package kb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "docs"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// ent 造一条最小可用的条目。
func ent(path, title, summary string) Entry {
	return Entry{Path: path, Title: title, Summary: summary}
}

// reconcile 是测试里对 Reconcile 的薄封装。
func reconcile(t *testing.T, s *Store, req ReconcileRequest) *ReconcileResult {
	t.Helper()
	res, err := s.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

// TestStoreRoundTrip 目录与侧车都要能活过一次进程重启 —— 否则它们只是内存缓存。
func TestStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	byModel(t, s, "local/qwen3")
	reconcile(t, s, ReconcileRequest{
		Corpus: "docs",
		Model:  "local/qwen3",
		Dim:    2,
		Fresh:  []Entry{ent("a.md", "配置写入", "第一条"), ent("b.md", "会话归档", "第二条")},
	})
	if err := s.SetVectors(map[string][]float32{
		"a.md": {1, 0},
		"b.md": {0, 1},
	}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(s2.Entries()); got != 2 {
		t.Fatalf("after reopen entries = %d, want 2", got)
	}
	if got := s2.Sidecar().Model; got != "local/qwen3" {
		t.Errorf("sidecar model = %q, want local/qwen3", got)
	}
	docs := s2.Docs()
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	// ★ 向量必须一起活过来，否则重开一次就变成纯词法 —— 而那不报错。
	if len(docs[0].Vector) != 2 || len(docs[1].Vector) != 2 {
		t.Fatalf("vectors lost on reopen: %v", docs)
	}
}

// TestEmptyStoreIsNotAnError 没有语料不该让整个 agent 起不来。
func TestEmptyStoreIsNotAnError(t *testing.T) {
	s := newTestStore(t)
	if got := len(s.Entries()); got != 0 {
		t.Fatalf("fresh store entries = %d, want 0", got)
	}
	if got := s.Docs(); len(got) != 0 {
		t.Fatalf("docs on empty store = %v, want none", got)
	}
	if s.Catalog().Header.Created.IsZero() {
		// 建出来就该有个时间戳，否则渲染出的「建于 —」是空的。
		t.Error("a fresh store has no creation time")
	}
}

// TestReconcileIsIdempotentByID 主键是路径，重复对账覆盖。
func TestReconcileIsIdempotentByID(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "第一版")}})
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "第二版")}})

	got := s.Entries()
	if len(got) != 1 {
		t.Fatalf("entries = %d after re-reconcile, want 1", len(got))
	}
	if got[0].Summary != "第二版" {
		t.Fatalf("summary = %q, want 第二版", got[0].Summary)
	}
}

// ★ 这是 docs/kb-design.md §6.4 那条：换了 embedding 模型之后旧向量还在库里，
// 而跨模型检索给出的是**看起来正常的错结果**。必须在对账前就拒。
func TestReconcileRejectsModelMismatch(t *testing.T) {
	s := newTestStore(t)
	byModel(t, s, "m1")
	reconcile(t, s, ReconcileRequest{Model: "m1", Dim: 2, Fresh: []Entry{ent("a.md", "t", "s")}})

	_, err := s.Reconcile(context.Background(), ReconcileRequest{
		Model: "m2", Dim: 2, Fresh: []Entry{ent("b.md", "t", "s")},
	})
	if err == nil {
		t.Fatal("a different model was accepted")
	}
	if !strings.Contains(err.Error(), "delete") {
		t.Errorf("error should tell the user how to recover, got: %v", err)
	}
	// ★ 被拒的那次**不能**留下任何痕迹。
	if got := len(s.Entries()); got != 1 {
		t.Fatalf("entries = %d after a rejected reconcile, want 1", got)
	}
	if got := s.Sidecar().Model; got != "m1" {
		t.Errorf("sidecar model = %q after a rejected reconcile, want m1", got)
	}
}

func TestReconcileRejectsBadInput(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Reconcile(context.Background(), ReconcileRequest{
		Dim: -1, Fresh: []Entry{ent("a.md", "t", "s")},
	}); err == nil {
		t.Error("negative dim accepted")
	}
	// ★ 空路径的条目必须被拒：路径是主键，而没有主键的条目检索不到、也删不掉。
	if _, err := s.Reconcile(context.Background(), ReconcileRequest{
		Fresh: []Entry{{Title: "t", Summary: "s"}},
	}); err == nil {
		t.Error("an entry with an empty path was accepted")
	}
}

// TestCosineHandlesUnnormalizedVectors 端点不保证归一化（实测 llama-server
// 归一化了，但不能依赖它）。除模长是必须的而不是保险。
func TestCosineHandlesUnnormalizedVectors(t *testing.T) {
	if got := cosine([]float32{1, 1}, []float32{2, 2}); got < 0.9999 {
		t.Errorf("cosine of parallel unnormalized vectors = %v, want ≈1", got)
	}
	if got := cosine([]float32{1, 1}, []float32{-1, -0.01}); got >= 0 {
		t.Errorf("cosine of opposed vectors = %v, want negative", got)
	}
}

// TestCosineZeroVectorIsZero 零向量不能产出 NaN —— NaN 在排序里既不小于也
// 不大于任何数，于是排序结果未定义，却没有任何东西会说。
func TestCosineZeroVectorIsZero(t *testing.T) {
	if got := cosine([]float32{0, 0}, []float32{1, 1}); got != 0 {
		t.Errorf("cosine with a zero vector = %v, want 0 (not NaN)", got)
	}
	if got := cosine(nil, []float32{1}); got != 0 {
		t.Errorf("cosine with nil = %v, want 0", got)
	}
}

func TestSearchRespectsTopK(t *testing.T) {
	rt := newTestRetriever(t, newTestStore(t), nil, 5)
	fresh := make([]Entry, 0, 10)
	vecs := map[string][]float32{}
	for i := 0; i < 10; i++ {
		p := string(rune('a'+i)) + ".md"
		fresh = append(fresh, ent(p, "t", "x"))
		vecs[p] = []float32{1, float32(i) / 10}
	}
	reconcile(t, rt.config.Store, ReconcileRequest{Fresh: fresh})
	if err := rt.config.Store.SetVectors(vecs); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	if got := len(rt.search(parseQuery("x"), 3, []float32{1, 0}, nil)); got != 3 {
		t.Fatalf("TopK 3 returned %d hits", got)
	}
}

// ★ 索引进行中查询不能被阻塞也不能出错 —— 正在建的索引不该让检索不可用。
func TestSearchDuringReconcileIsSafe(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "seed")}})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					// 命中旧快照即可，不要求看到新数据。
					_ = s.Docs()
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "seed")}})
	}
	close(stop)
	wg.Wait()
}

// TestSidecarWrittenAfterCatalog 侧车声明的维度必须与刚落盘的目录对得上。
// 顺序反了的话，崩在两次写之间会留下「目录说 A、侧车是 B」的索引，而加载时
// 一切正常 —— 那正是最难查的一种。
func TestSidecarWrittenAfterCatalog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s, ReconcileRequest{
		Dim:   2,
		Fresh: []Entry{ent("a.md", "t", "s")},
	})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}
	for _, f := range []string{catalogFile, sidecarFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s missing: %v", f, err)
		}
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := s2.Sidecar().Dim; got != 2 {
		t.Errorf("reopened dim = %d, want 2", got)
	}
}

// TestCorruptSidecarSelfHeals 侧车坏了**不是错误**（§7.5）：它是纯派生，
// 丢了自愈。报错会让一个可自愈的状态看起来需要人工干预。
func TestCorruptSidecarSelfHeals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sidecarFile), []byte("not a gob"), 0o644); err != nil {
		t.Fatalf("corrupt sidecar: %v", err)
	}

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen with a corrupt sidecar must not fail: %v", err)
	}
	// 目录还在 —— 那才是权威那份。
	if got := len(s2.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1 (the catalog survives a broken sidecar)", got)
	}
	if got := len(s2.Sidecar().Entries); got != 0 {
		t.Fatalf("sidecar entries = %d, want 0 (it was cleared)", got)
	}
}

// TestSidecarIndexVersionMismatchClearsVectors 格式变了 ⇒ 旧向量不可信 ⇒
// 清掉，下一轮重嵌。**不报错**：它同样可自愈。
func TestSidecarIndexVersionMismatchClearsVectors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	// 手工把侧车的 IndexVersion 改成一个不存在的版本。
	if err := writeSidecarWithVersion(filepath.Join(dir, sidecarFile), IndexVersion+1); err != nil {
		t.Fatalf("rewrite sidecar: %v", err)
	}

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(s2.Sidecar().Entries); got != 0 {
		t.Fatalf("vectors survived a version bump: %v", s2.Sidecar().Entries)
	}
	if got := len(s2.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1 (the catalog is untouched)", got)
	}
}

// TestCatalogIndexVersionMismatchRejects 头部的版本不符要**明确拒绝** ——
// 那是「这份目录是别的规则抽的」，而继续解析会得到一份读错了的目录。
func TestCatalogIndexVersionMismatchRejects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := RenderCatalog(Catalog{
		Header: Header{IndexVersion: IndexVersion + 1, Writer: "deadbeef"},
	})
	if err := os.WriteFile(filepath.Join(dir, catalogFile), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewStore(dir)
	if err == nil {
		t.Fatal("a catalog from a newer extraction version was accepted")
	}
	if !strings.Contains(err.Error(), "re-run") {
		t.Errorf("error should say how to recover, got: %v", err)
	}
}

// TestHumanEditSurvivesReindex 这是乐观提交要保的那件事（§7.2）：人改了摘要，
// 机器**永不覆盖**。
func TestHumanEditSurvivesReindex(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "机器写的摘要")}})

	// 人改摘要。
	cat := s.Catalog()
	for i := range cat.Entries {
		cat.Entries[i].Summary = "人写的摘要"
	}
	body := RenderCatalog(cat)
	if err := os.WriteFile(filepath.Join(s.Dir(), catalogFile), body, 0o644); err != nil {
		t.Fatalf("human edit: %v", err)
	}

	// 再跑一次索引，带一份**新的**机器摘要。
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "机器写的新摘要")}})

	got := s.Entries()
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if got[0].Summary != "人写的摘要" {
		t.Fatalf("the machine overwrote the human's summary: %q", got[0].Summary)
	}
	if got[0].Stale {
		t.Error("a human edit against an unchanged source must not be marked stale")
	}
}

// TestHumanEditMarksStaleWhenSourceChanges 摘要是人写的、而源文件此后变了 ⇒
// 标 stale（§12.10）。**只标不修**：机器不通过覆盖摘要来清掉它。
func TestHumanEditMarksStaleWhenSourceChanges(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "机器写的摘要")}})

	// 人改摘要。
	cat := s.Catalog()
	for i := range cat.Entries {
		cat.Entries[i].Summary = "人写的摘要"
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), catalogFile), RenderCatalog(cat), 0o644); err != nil {
		t.Fatalf("human edit: %v", err)
	}

	// 源文件变了（mtime/size 变），而人没再动摘要。
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{{
		Path: "a.md", Title: "标题", Summary: "机器写的摘要", MTime: 99, Size: 99,
	}}})

	got := s.Entries()
	if got[0].Summary != "人写的摘要" {
		t.Fatalf("the machine overwrote the human's summary: %q", got[0].Summary)
	}
	if !got[0].Stale {
		t.Error("a human summary against a changed source must be marked stale")
	}

	// 人再改一次 ⇒ 陈旧被清掉。没有这条，stale 一旦置上就永远清不掉。
	cat = s.Catalog()
	for i := range cat.Entries {
		cat.Entries[i].Summary = "人改过第二次"
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), catalogFile), RenderCatalog(cat), 0o644); err != nil {
		t.Fatalf("second human edit: %v", err)
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{{
		Path: "a.md", Title: "标题", Summary: "机器写的摘要", MTime: 99, Size: 99,
	}}})

	got = s.Entries()
	if got[0].Summary != "人改过第二次" {
		t.Fatalf("summary = %q, want 人改过第二次", got[0].Summary)
	}
	if got[0].Stale {
		t.Error("a fresh human edit must clear the stale flag")
	}
}

// TestReconcileRemovesDeletedDocs 全语料对账里没出现的条目要清掉。
//
// ★ 不清的后果是「删了一整份文档，它仍然可以被检索到」—— 而那正是本包
//
//	最贵的那类错：自信的、无声的过期答案。
func TestReconcileRemovesDeletedDocs(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{
		Fresh: []Entry{ent("a.md", "a", "a"), ent("b.md", "b", "b")},
	})
	if err := s.SetVectors(map[string][]float32{"a.md": {1, 0}, "b.md": {0, 1}}); err != nil {
		t.Fatalf("SetVectors: %v", err)
	}

	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "a", "a")}})
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want 1", res.Removed)
	}
	if got := len(s.Entries()); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
	// ★ 向量必须跟着删掉，否则侧车里留着一份孤儿。
	if _, ok := s.Sidecar().Entries["b.md"]; ok {
		t.Error("the removed doc kept a orphan vector in the sidecar")
	}
}

// TestScopedReconcileKeepsUntouchedDocs 带 scope 的增量**只动这几个路径**。
//
// ★ 语义必须分开：增量索引只碰了它扫过的那几个文件，其余文件不在本轮
//
//	targets 里，当然也不算「被删了」。
func TestScopedReconcileKeepsUntouchedDocs(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{
		Fresh: []Entry{ent("a.md", "a", "a"), ent("b.md", "b", "b")},
	})

	reconcile(t, s, ReconcileRequest{
		Scope: []string{"a.md"},
		Fresh: []Entry{ent("a.md", "a", "改过的 a")},
	})
	got := s.Entries()
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2 (b.md was not in scope)", len(got))
	}
	for _, e := range got {
		if e.Path == "a.md" && e.Summary != "改过的 a" {
			t.Errorf("a.md summary = %q, want 改过的 a", e.Summary)
		}
	}
}

// TestReconcileDetectsConcurrentWrite 这是乐观提交的核心（§7.3 ④）：写后读
// 比较发现被覆盖，然后重读重合并重试。
//
// ★ **它真的制造并发**：另一个 goroutine 写真的文件、真的走 rename，被安排在
//
//	④ 那个几微秒的窗口里（[Store.afterWrite] 接缝）。不靠「狂写赌撞上」——
//	那种用例没撞上时会**通过**，测的其实是运气。
//
// ★ 干扰方改的是**一条摘要**，不是随手一行字。理由是 §5.2：多余行会被
//
//	宽松解析**静默忽略**（「备注：冒号后面是人话 ⇒ 不像键名 ⇒ 静默忽略」）——
//	拿它当干扰源的话，第一次写就把那句话渲染没了，④ 的比较反而**相等**、
//	判成没被覆盖，于是一个本该被钉住的行为被用一个测不到它的东西来测。
//	摘要是 `key: value`，parse→render 能完整复现，于是覆盖真的会发生、
//	真的会被发现，而重试之后它必须**还在**。
func TestReconcileDetectsConcurrentWrite(t *testing.T) {
	s := newTestStore(t)
	md := filepath.Join(s.Dir(), catalogFile)

	// 在第一次写盘之后、④ 读回之前，由另一个 goroutine 改掉那条摘要。
	var once sync.Once
	s.afterWrite = func() {
		once.Do(func() {
			done := make(chan error, 1)
			go func() {
				b, err := os.ReadFile(md)
				if err != nil {
					done <- err
					return
				}
				cat, err := ParseCatalog(b)
				if err != nil {
					done <- err
					return
				}
				cat.Entries[0].Summary = "人改的摘要"
				done <- os.WriteFile(md, RenderCatalog(cat), 0o644)
			}()
			if err := <-done; err != nil {
				t.Errorf("interference: %v", err)
			}
		})
	}

	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})

	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the conflicting write must be detected)", res.Attempts)
	}

	// ★ 人的那条摘要必须**被重试读回来并保住**，而不是被我们这次写覆盖掉。
	//   这一条同时钉住 ④ 的检测和 §7.2 的字段所有权。
	b, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), "人改的摘要") {
		t.Fatalf("the human's summary was overwritten by the retry:\n%s", b)
	}
}

// TestReconcileReportsAttemptsAttempts > 1 说明真的撞上了并发。
func TestReconcileReportsAttempts(t *testing.T) {
	s := newTestStore(t)
	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	if res.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 on a clean run", res.Attempts)
	}
}

// TestReconcileCountsReEmbeddedVsUnchanged 重嵌的判据是**文字**，不是 mtime：
// 只挪了时间戳 ⇒ 不重嵌；换了一个字 ⇒ 重嵌。
//
// ★ 人改摘要**也不**重嵌 —— 这条最容易写反。摘要在 `EmbedText` 里，但侧车
//
//	记的是**抽出来那份**的指纹（见 [applyFresh]），而 `embedAll` 算向量用的
//	同样是 `req.Fresh`。人的编辑改的是目录里的 `out.Summary`，两边都不碰 ⇒
//	一次网络调用都不发。
//
//	反过来（拿 `out` 当指纹）会怎样：每一轮 `embedAll` 判定「变了」⇒ 重嵌 ⇒
//	Reconcile 又把指纹写回人的文字 ⇒ **每一轮都重嵌一遍**，且每轮都报
//	ReEmbedded=1，看起来像语料一直在动。
func TestReconcileCountsReEmbeddedVsUnchanged(t *testing.T) {
	s := newTestStore(t)
	md := filepath.Join(s.Dir(), catalogFile)

	// 新条目 ⇒ 从无到有，必然重嵌。
	res := reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})
	if res.ReEmbedded != 1 || res.Unchanged != 0 {
		t.Fatalf("first index: re-embedded=%d unchanged=%d, want 1/0",
			res.ReEmbedded, res.Unchanged)
	}

	// ★ 人改目录里的摘要 ⇒ **文字变了** ⇒ 这一轮要重嵌（§7.2），且人那句必须留下。
	//
	//	v2 的第一版这里是反的（「人改摘要不重嵌」），理由是向量只从源文件来。
	//	那条性质好维护，但它让人的编辑**只改显示、不改排序** —— 摘要进了
	//	`EmbedText`，他改的那句话却不影响任何一次相似度计算。设计选的是
	//	「向量反映人看到的那份」，所以这里必须是 1/0。
	//
	//	★ 没有 embedder 时 `ReEmbedded` 数的是「文字变了几条」——那是这一档
	//	  能数的唯一东西。有 embedder 时它数的是「真的发了几个请求」，见
	//	  [TestHumanSummaryEditReEmbeds]。
	cat, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	parsed, err := ParseCatalog(cat)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	parsed.Entries[0].Summary = "人写的摘要"
	if err := os.WriteFile(md, RenderCatalog(parsed), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})
	if res.ReEmbedded != 1 || res.Unchanged != 0 {
		t.Fatalf("human summary edit: re-embedded=%d unchanged=%d, want 1/0",
			res.ReEmbedded, res.Unchanged)
	}
	if got := s.Entries()[0].Summary; got != "人写的摘要" {
		t.Fatalf("human summary = %q, want it kept", got)
	}

	// ★ 再跑一遍**什么都不改**的 ⇒ 指纹与合并后的文字一致 ⇒ 不重嵌。
	//   没有这一条，上面那条会与「每轮都重嵌」无法区分。
	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})
	if res.ReEmbedded != 0 || res.Unchanged != 1 {
		t.Fatalf("no-op re-run: re-embedded=%d unchanged=%d, want 0/1",
			res.ReEmbedded, res.Unchanged)
	}

	// ★ 只动时间戳与大小（等价于原样重存一遍）⇒ 文字没变 ⇒ 不重嵌。
	//   这是「按内容而不是按 mtime」这条判据的正面用例。
	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{
		{Path: "a.md", Title: "标题", Summary: "正文", MTime: 99, Size: 4242},
	}})
	if res.ReEmbedded != 0 || res.Unchanged != 1 {
		t.Fatalf("mtime-only change: re-embedded=%d unchanged=%d, want 0/1",
			res.ReEmbedded, res.Unchanged)
	}

	// 换文字（新章节 ⇒ 换了 EmbedText）⇒ 重嵌。
	res = reconcile(t, s, ReconcileRequest{Fresh: []Entry{
		{Path: "a.md", Title: "标题", Summary: "正文", Sections: []Section{{Title: "新章", Line: 1}}},
	}})
	if res.ReEmbedded != 1 || res.Unchanged != 0 {
		t.Fatalf("text change: re-embedded=%d unchanged=%d, want 1/0",
			res.ReEmbedded, res.Unchanged)
	}
}

// TestStoreRefreshesFromOtherProcess 服务进程内存里那份要能被别的进程写的变化
// 刷掉 —— 否则工具回报成功而检索拿到旧的。
func TestStoreRefreshesFromOtherProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s1, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reconcile(t, s1, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})

	// 第二个 Store 当成另一个进程。
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	// ★ 带 Scope：见 [TestReconcileDoesNotLoseAnotherWritersEntries] 的注释 ——
	//   两个全语料索引互删是正确行为，这里要钉的是「s1 得看见别人写的」。
	reconcile(t, s2, ReconcileRequest{
		Fresh: []Entry{ent("b.md", "t", "s")},
		Scope: []string{"b.md"},
	})

	// s1 要能看到 s2 写的那条。
	if got := len(s1.Entries()); got != 2 {
		t.Fatalf("first store saw %d entries, want 2 (it must notice the other process)", got)
	}
}

// TestStartRunIsExclusive 运行锁一次只允许一个索引运行。
//
// ★ 判据是**两个 Store 实例**（各自打开自己的 fd），因为那才对应「两个进程」。
//
//	同一个实例上连着拿两次是**可重入的**（[persist.FileLock] 用 depth 计数，
//	见那边注释：flock 按 fd 计锁，内层 LOCK_UN 会把外层的锁也放掉）——
//	拿它当互斥判据会让测试断言一件与实现刻意相反的事。
func TestStartRunIsExclusive(t *testing.T) {
	s := newTestStore(t)
	other, err := NewStore(s.Dir())
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	defer other.Close()

	stop, err := s.StartRun()
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := other.StartRun(); err == nil {
		t.Fatal("a second instance acquired the run lock")
	}
	stop()
	if _, err := other.StartRun(); err != nil {
		t.Fatalf("StartRun after release: %v", err)
	}
}

// TestStoreCloseReleasesRunLock Close 要放掉运行锁，否则进程退不干净。
func TestStoreCloseReleasesRunLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.StartRun(); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := s2.StartRun(); err != nil {
		t.Fatalf("a stale run lock survived Close: %v", err)
	}
}

// TestDerOfFeedsIncrement 侧车记着上一轮的 EmbedText —— 那是增量判定的依据。
func TestDerOfFeedsIncrement(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "标题", "正文")}})

	de, ok := s.derOf("a.md")
	if !ok {
		t.Fatal("derOf missed a reconciled path")
	}
	if de.EmbedText == "" {
		t.Error("the sidecar recorded no EmbedText; incremental detection would never fire")
	}
	if de.WroteSummary != "正文" {
		t.Errorf("WroteSummary = %q, want 正文 (it is how human edits are detected)", de.WroteSummary)
	}
	if de.ObsSummary != "正文" {
		t.Errorf("ObsSummary = %q, want 正文 (it is how staleness is cleared)", de.ObsSummary)
	}
}

// TestOwnsHuman 边界：空串只在两边都空时算机器的。
func TestOwnsHuman(t *testing.T) {
	cases := []struct {
		current, wrote string
		want           bool
	}{
		{"", "", false},   // 机器写的空，人没填
		{"a", "", true},   // 机器没写过，人填了 ⇒ 人的
		{"a", "a", false}, // 机器写的，人没动
		{"b", "a", true},  // 人改了
		{"", "a", false},  // ★ 人把内容清空了 —— 我们认不出来，保守地当机器的
	}
	for _, c := range cases {
		if got := ownsHuman(c.current, c.wrote); got != c.want {
			t.Errorf("ownsHuman(%q, %q) = %v, want %v", c.current, c.wrote, got, c.want)
		}
	}
}
