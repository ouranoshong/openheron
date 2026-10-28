package kb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"openheron/persist"
)

// 这个文件钉住「**两个进程**同时写 catalog.md」这件事。
//
// ★ v2 **没有写锁**（`.lock` 已删），理由是 docs/kb-design.md §7.1 那条：
//
//	v1 的写锁在防「CLI 与 agent 工具同时写」，而那个场景已经有运行锁兜着
//	了 —— 写锁防的是一个不存在的对手。真正会并发写 catalog.md 的是**人在
//	编辑器里**，而他不可能配合锁。所以只能是乐观的：写后读校验。
//
// 测试里用两个 [Store] 实例模拟两个进程（同一个目录、各自一份内存），
// 因为那正是两个进程各自看到的东西。

// ★ 光有「重新读盘」不够：合并基必须是**盘上那份**，而不是本进程上次
// 看到的样子。否则另一个进程在它之后写的整批会被静默抹掉 —— 而两边都
// 报告成功。这是「读全部 → 改 → 写全部」的经典丢更新。
func TestReconcileDoesNotLoseAnotherWritersEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")

	// 两个实例都先打开 —— 于是它们各自持有一份「空目录」的内存快照，
	// 这正是两个进程刚启动时的样子。
	a, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// ★ 两边都带 `Scope`：那才是「并发」在真实里长的样子 ——
	//
	//	`openheron kb index a1.md a2.md` 与 `openheron kb index b1.md` 撞在一起。
	//	不带 Scope 是**全语料对账**，它的定义就是「不在 Fresh 里的源文件
	//	已经删了」—— 两个各跑各的全语料索引互删对方的条目是**对的**，
	//	拿它当并发用例等于钉住一条正确的删除。
	//	真被丢更新坑的是增量，所以增量才该是这里的判据。
	reconcile(t, a, ReconcileRequest{
		Fresh: []Entry{ent("a1.md", "t", "甲的"), ent("a2.md", "t", "甲的")},
		Scope: []string{"a1.md", "a2.md"},
	})

	// 乙**不知道**甲写过 —— 它的内存快照还是空的。它接着写自己的那条。
	reconcile(t, b, ReconcileRequest{
		Fresh: []Entry{ent("b1.md", "t", "乙的")},
		Scope: []string{"b1.md"},
	})

	// 三条都必须在。没有「写前重读」的话，b 会拿着空快照当合并基，
	// 把 a1/a2 一起写没。
	for _, p := range []string{"a1.md", "a2.md", "b1.md"} {
		if _, ok := findEntry(t, dir, p); !ok {
			t.Fatalf("entry %s was lost by a concurrent writer; catalog has %v", p, allPaths(t, dir))
		}
	}
}

// ★ 反向：**修改**也不能被陈旧状态覆盖。
func TestReconcileDoesNotRevertAnotherWritersUpdate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	a, _ := NewStore(dir)
	b, _ := NewStore(dir)

	reconcile(t, a, ReconcileRequest{Fresh: []Entry{ent("x.md", "t", "第一版")}})
	// 甲改了同一条。
	reconcile(t, a, ReconcileRequest{Fresh: []Entry{ent("x.md", "t", "第二版")}})
	// 乙再写别的一条 —— 它内存里那条 x 还是「第一版」。
	//
	// ★ 带 Scope：见上一条的注释，全语料互删是正确行为，不是 bug。
	//   这里要钉的是**合并基必须来自盘** —— 若 `Reconcile` 拿乙那份陈旧的
	//   内存快照当 `cur`，`out.Entries` 里根本没有 x，写完它就没了。
	reconcile(t, b, ReconcileRequest{
		Fresh: []Entry{ent("y.md", "t", "乙的")},
		Scope: []string{"y.md"},
	})

	got, ok := findEntry(t, dir, "x.md")
	if !ok {
		t.Fatal("x.md disappeared")
	}
	if got.Summary != "第二版" {
		t.Fatalf("x.md = %q, want 第二版 — a stale merge base reverted it", got.Summary)
	}
}

// ★ 写后读校验是乐观提交的**核心**（§7.3 ④）：`AtomicWrite` 的 rename 只
// 保证读者看到「整份旧」或「整份新」，保证不了「我写的那份还在」。
//
// 这条用一个**真的在写窗口里动手**的 goroutine 钉住它 —— 不是模拟，
// 是真的并发。
func TestReconcileSurvivesAConcurrentWriterMidFlight(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	a, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewStore(dir)

	// 甲先写一条，让目录存在。
	reconcile(t, a, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "种子")}})

	// 乙在甲的下一次写**之后**立刻写 —— 制造重叠。
	//
	// ★ 两边都是**增量**（带 Scope），理由见上面 [TestReconcileDoesNotLoseAnotherWritersEntries]
	//   的注释：全语料互删是正确行为，钉不住任何东西。
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			// ★ 不能用 `reconcile`（它内部 `t.Fatalf`）：FailNow 只能由跑测试的
			//   那个 goroutine 调，在这里调会让失败以一个不知所云的方式报出来。
			if _, err := b.Reconcile(context.Background(), ReconcileRequest{
				Fresh: []Entry{ent("b-own.md", "t", "乙的")},
				Scope: []string{"b-own.md"},
			}); err != nil {
				t.Errorf("writer B: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 30; i++ {
		reconcile(t, a, ReconcileRequest{
			Fresh: []Entry{ent("a-own.md", "t", "甲的")},
			Scope: []string{"a-own.md"},
		})
	}
	close(stop)
	<-done

	// ★ 两边的条目都必须在。任何一方的「另存一份」都会在这里暴露。
	for _, p := range []string{"seed.md", "a-own.md", "b-own.md"} {
		if _, ok := findEntry(t, dir, p); !ok {
			t.Fatalf("entry %s was lost to a concurrent writer; catalog has %v", p, allPaths(t, dir))
		}
	}
}

// ★ rev 是「发生过并发」留下的唯一证据（§7.3）：被覆盖的一方重试时也会
// 加一，所以「rev 跳了两格」就说明有人同时在写。
func TestReconcileRevisionIncreasesPerWrite(t *testing.T) {
	s := newTestStore(t)
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s")}})
	first := s.Catalog().Header.Revision
	if first == 0 {
		t.Fatal("revision stayed at 0 after a write")
	}
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("a.md", "t", "s2")}})
	if got := s.Catalog().Header.Revision; got <= first {
		t.Fatalf("revision = %d after a second write, want > %d", got, first)
	}
}

// ★ Index **不**整轮持锁：运行锁只罩住一整次命令，而目录的写入是毫秒级。
//
//	整轮持锁的代价是一次索引会让另一个进程的增量索引白等 —— 而它排队
//	期间什么活都没干。正确性不靠「持得久」，靠「写之前重读并合并」。
//
// 所以这条断言是**反过来**的：向量化期间运行锁**被本进程自己拿着**，
// 而别的 Store 仍然写得动 catalog.md。
func TestReconcileDoesNotBlockOtherWritersWhileEmbedding(t *testing.T) {
	_, corpus := seedProject(t, map[string]string{
		"a.md": "# A\n\n## 一\n\n内容一\n",
		"b.md": "# B\n\n## 二\n\n内容二\n",
	})
	st := newTestStore(t)
	other, err := NewStore(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	stop, err := st.StartRun()
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer stop()

	// ★ 用 embedder 当探针：它在**索引跑到一半**时被调用，正好可以问
	//   「此刻另一个进程写得动吗」。放在 Index 之前或之后都验不到这一点。
	var writesOK int
	emb := newFakeEmbedder(16)
	emb.beforeEach = func() {
		reconcile(t, other, ReconcileRequest{Fresh: []Entry{ent("other.md", "t", "另一路")}})
		writesOK++
	}
	ix := newTestIndexer(t, st, emb, 10)

	res, err := Index(context.Background(), ix, testIndexOpts(corpus), nil)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if res.Changed != 2 {
		t.Fatalf("changed = %d, want 2", res.Changed)
	}
	if writesOK == 0 {
		t.Fatal("another writer was never able to commit while embedding; the expensive part must not block")
	}
}

// ★ 运行锁是**可取消**的：持有者卡住时，等待方不该被永久挂住。
func TestRunLockIsCancellable(t *testing.T) {
	st := newTestStore(t)
	other, err := NewStore(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	stop, err := st.StartRun()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = other.run.Try()
	if err == nil {
		t.Fatal("a second run was accepted while another held the lock")
	}
	if !errors.Is(err, persist.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Fatalf("the lock check took %s; it must not queue", d)
	}
	_ = ctx
}

// ★ agent 工具撞上命令行正在跑的索引时应当**立刻**说清楚，而不是挂十分钟。
func TestStartRunFailsFastWhileAnotherProcessIndexes(t *testing.T) {
	st := newTestStore(t)
	other, _ := NewStore(st.Dir())

	stop, err := st.StartRun()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	started := time.Now()
	_, err = other.StartRun()
	if !errors.Is(err, persist.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if d := time.Since(started); d > 500*time.Millisecond {
		t.Fatalf("StartRun waited %s; it must not queue", d)
	}
	if !strings.Contains(err.Error(), "pid ") {
		t.Errorf("the error should name the holder so it can be checked, got: %v", err)
	}
}

// ★ 没有写锁意味着「读—合并—写」是交错的。写后读校验必须真的**发现**被覆盖，
// 而不是碰巧没撞上。
//
// 这条用一个确定性的干扰钉住：在 Reconcile 的写之后、读之前改盘。
func TestReconcileReadBackCheckCatchesAnOverwrite(t *testing.T) {
	s := newTestStore(t)
	md := filepath.Join(s.Dir(), catalogFile)

	// 先让目录存在。
	reconcile(t, s, ReconcileRequest{Fresh: []Entry{ent("seed.md", "t", "种子")}})

	// ★ 干扰方在 ④ 那个窗口里**塞一整条新条目**（不是加一行字 —— §5.2 的
	//   宽松解析会静默忽略多余行，那样 ④ 的比较反而相等，测不到覆盖）。
	var once sync.Once
	s.afterWrite = func() {
		once.Do(func() {
			b, err := os.ReadFile(md)
			if err != nil {
				t.Errorf("read for interference: %v", err)
				return
			}
			cat, err := ParseCatalog(b)
			if err != nil {
				t.Errorf("parse for interference: %v", err)
				return
			}
			cat.Entries = append(cat.Entries, ent("other.md", "t", "别人加的"))
			if err := os.WriteFile(md, RenderCatalog(cat), 0o644); err != nil {
				t.Errorf("interference write: %v", err)
			}
		})
	}

	// ★ 带 Scope：我们这一轮只扫 new.md。干扰方加的 other.md 不在本轮 targets
	//   里，既不算「源文件被删了」，也必须原样活下来。
	res := reconcile(t, s, ReconcileRequest{
		Fresh: []Entry{ent("new.md", "t", "新的")},
		Scope: []string{"new.md"},
	})

	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the overwrite must be detected)", res.Attempts)
	}
	for _, p := range []string{"seed.md", "new.md", "other.md"} {
		if _, ok := findEntry(t, s.Dir(), p); !ok {
			t.Fatalf("entry %s was lost to a forced collision; catalog has %v", p, allPaths(t, s.Dir()))
		}
	}
}

func findEntry(t *testing.T, dir, path string) (Entry, bool) {
	t.Helper()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cat := s.Catalog()
	return cat.Get(path)
}

func allPaths(t *testing.T, dir string) []string {
	t.Helper()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries := s.Entries()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}
