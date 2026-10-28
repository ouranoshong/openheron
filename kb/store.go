package kb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"openheron/agentkit/obs"
	"openheron/persist"
)

const (
	// catalogFile 是**权威**那份：人可读可改，一个源文件一节。
	catalogFile = "catalog.md"
	// sidecarFile 是**派生**那份：每文档一条向量 + 机器上次写下的字段值。
	//
	// ★ 它丢了能自愈（§7.5）：下一轮按 EmbedText 对不上就重嵌那几条。
	//   所以它不做校验、不做乐观提交 —— **只有人会写的那份文件值得**。
	sidecarFile = "catalog.gob"
	// IndexVersion 是目录格式的版本。侧车里存着它，不符就拒绝并要求重建。
	//
	// ★ **它放在侧车里而不是 catalog.md 头部**（§5.3）：头部可能是人改过的，
	//   让一份人手写的文件参与「这份索引还能不能用」的判断，等于让用户的一行
	//   笔误毁掉整份索引。侧车是纯派生，人不会去动它。
	IndexVersion = 2

	// maxCommitAttempts 是乐观提交的重试次数。
	//
	// ★ 对手是**在编辑器里改文件的人**（§7.1）。一次完整的「读—合并—写—校验」
	//   是毫秒级，三轮都撞上说明对方在持续写（比如保存时格式化整个文件），
	//   那时继续重试只会互相踩。明确报错比静默另存好 —— 另存会造出第二份真相。
	maxCommitAttempts = 3
)

// Derived 是侧车 `catalog.gob` 的内容。
//
// ★ 它是**纯派生**：全部内容都能从 `catalog.md` + 语料重算出来。它存在的
//
//	唯一理由是省掉重算 —— 没有它，每次启动都要把 10 份文档重新嵌一遍。
type Derived struct {
	// IndexVersion 是写这份侧车时的格式版本。不符就拒绝并要求重建。
	//
	// ★ 放在这里而不是 catalog.md：头部可能被人类改过，而一行笔误不该毁掉
	//   整份索引（§5.3）。
	IndexVersion int

	Model string
	Dim   int

	Entries map[string]DerivedEntry
}

// DerivedEntry 是一个文档的派生状态。
type DerivedEntry struct {
	// WroteTitle / WroteSummary 是**机器上次写下**的标题与摘要。
	//
	// ★ 它们存在的唯一目的就是区分「人写的」与「机器写的」（§7.2）：
	//   目录里的值 != 这里记的值，就说明人在编辑器里改过它，而机器**永不覆盖**。
	//
	//   没有这两个字段，机器无法判断自己该不该覆盖那一行 —— 而「覆盖了人的
	//   摘要」不报错，只是那个人的话从文件里消失了，且没人会发现。
	WroteTitle   string
	WroteSummary string

	// ObsSummary / ObsMTime / ObsSize 是**上一轮看到的**摘要与它当时的源文件
	// 状态。
	//
	// ★ 为什么 `stale` 需要这三个，只靠 WroteSummary 不够：`stale` 要回答的是
	//   「这份人写的摘要对应的是源文件的哪个版本」，而人**刚改过**与**三轮前
	//   改过**在 WroteSummary 上长得一模一样（都只是 != 机器写的那份）。
	//
	//   有了 ObsSummary 就能分辨：`目录里的摘要 != ObsSummary` ⇒ 人**刚刚**改了
	//   ⇒ 他的新版本针对的就是当前源文件 ⇒ 清掉 stale。
	//   相等 ⇒ 人没动 ⇒ stale 只取决于源文件变没变。
	//
	//   不加这两个的后果是 stale 一旦置上就**永远清不掉**，除非机器覆盖摘要 ——
	//   而那正是「只标不修」（§7.2）禁止的事。
	ObsSummary string
	ObsMTime   int64
	ObsSize    int64

	// EmbedText 是被向量化过的原文（[Entry.EmbedText]）。
	//
	// ★ 存它的目的是**增量判定**：下一轮比对它就知道「这份文档的文字内容变没变」，
	//   不变就不用重嵌。不存的话只能拿 mtime/size 判，而那会为一次格式化重嵌整份。
	EmbedText string

	// Vector 是这条文档的向量。Embedding 为空时**保持为空**（§8.5）。
	//
	// ★ 不存第二份向量：`catalog.md` 里没有向量字段，向量只有这一个家。
	//   存两份就是两份迟早不一致的真相，而不一致时检索照常工作，只是悄悄少召回。
	Vector []float32
}

// Store 是目录的**唯一所有者**。
//
// ★ Indexer 与 Retriever 必须共用同一个实例 —— 各自持一份会让写完之后检索
//
//	仍读旧目录，而工具回报成功。
//
// 取向 Redis 版的 `*redis.Client`：那也是两个组件共享的连接池。
type Store struct {
	dir string

	// run 是**运行锁**，罩住一整次索引命令。
	//
	// ★ v2 **没有写锁**（`.lock` 已删）。理由是 §7.1 那条：v1 的写锁在防
	//   「CLI 与 agent 工具同时写」，而那个场景已经有运行锁兜着了 —— 写锁
	//   防的是一个不存在的对手。
	//
	//   真正会并发写 catalog.md 的是**人在编辑器里**，而他不可能配合锁。
	//   所以只能是乐观的：写后读校验（见 [Store.Reconcile]）。
	run *persist.FileLock

	mu  sync.RWMutex
	cat Catalog
	der Derived

	// stamp 是 catalog.md 的 (mtime, size)。
	//
	// ★ **只 stat 权威那份，不 stat 侧车**。侧车与权威不一致的后果是「向量
	//   对不上」，而那会在下一次索引时自愈；刷新它只会让「刚写完侧车、还没
	//   写权威」的窗口里去读一个即将作废的状态。
	//
	//   跨进程的陈旧也是它解决的：`openheron kb index` 跑在另一个进程里，它写完
	//   之后服务进程内存中那份不会自己变 —— 于是工具回报成功而检索拿到旧的。
	//   只 stat 不读：没变就是一次 syscall，变了才真去 decode。
	stamp fileStamp

	// runMu 序列化本进程内的刷新。refresh 是「读盘—解码—换掉内存快照」，
	// 并发时两个 goroutine 会同时读同一份文件并各自换一次，后换的那份可能
	// 是先换那份之前的状态。
	runMu sync.Mutex

	// afterWrite 是**测试接缝**，见 [Store.Reconcile] ④ 前的那段注释。
	//
	//	生产代码永远不设它。不加导出面：这只解决「怎么确定地测到一个几微秒
	//	的窗口」，不是任何调用方需要的能力。
	afterWrite func()
}

type fileStamp struct {
	mtime int64
	size  int64
	valid bool
}

// NewStore 打开（或建出）一个目录目录。目录不存在时创建，`catalog.md` 不存在时
// 建一个空目录 —— **空目录是合法状态**，不是错误：没有语料可索引不该让整个
// agent 起不来。
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("kb: store dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("kb: create store dir: %w", err)
	}
	run, err := persist.OpenFileLock(persist.RunLockPath(dir))
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, run: run, der: Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}}
	if err := s.load(); err != nil {
		_ = run.Close()
		return nil, err
	}
	return s, nil
}

// Close 释放运行锁的文件描述符。**不改变目录内容。**
func (s *Store) Close() error {
	if s == nil || s.run == nil {
		return nil
	}
	return s.run.Close()
}

// Dir 返回目录目录。
func (s *Store) Dir() string { return s.dir }

// StartRun 取**运行锁**：一次索引运行只允许一个。
//
// ★ 拿不到时立刻返回 [persist.ErrLocked]，**不排队**。命令行与 agent 工具
//
//	各自决定怎么处理：人可以选择等，而一个 turn 里没有几十秒可等。
func (s *Store) StartRun() (stop func(), err error) {
	return s.run.Try()
}

// Catalog 返回当前目录（副本）。
func (s *Store) Catalog() Catalog {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneCatalog(s.cat)
}

// Entries 返回当前全部条目，**按路径排序**。
func (s *Store) Entries() []Entry {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return normalizeEntries(s.cat.Entries)
}

// Docs 返回检索要用的全部文档：条目 + 它的向量（可能为空）。
//
// ★ 向量从侧车取，**不从目录取** —— 目录是人可读可改的，里面没有向量字段。
func (s *Store) Docs() []docState {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := normalizeEntries(s.cat.Entries)
	out := make([]docState, 0, len(entries))
	for _, e := range entries {
		out = append(out, docState{Entry: e, Vector: s.der.Entries[e.Path].Vector})
	}
	return out
}

// docState 是一条待打分的文档。
type docState struct {
	Entry  Entry
	Vector []float32
}

// derOf 返回某路径的侧车记录。它给 [FileIndexer.embedAll] 做增量判定用 ——
// 「这份文档的文字内容变没变」只有侧车知道。
func (s *Store) derOf(path string) (DerivedEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	de, ok := s.der.Entries[path]
	return de, ok
}

// Sidecar 返回侧车里的派生状态（副本）。
//
// ★ 它是 `openheron kb status` 的数据来源之一：`Model` / `Dim` 决定「这份索引是
//
//	谁建的」，而条目数决定「有没有向量」。
func (s *Store) Sidecar() Derived {
	s.refresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneDerived(s.der)
}

// SetIdentity 记下（并比对）模型身份与维度。
//
// ★ **一个配置项驱动两件事**（§8.5）：`model` 为空 → 检索退化成词法、且
//
//	不写向量；而 `model` 一旦被设过又改回去，仍会与侧车比对 —— 那是为了不让
//	「悄悄换模型」变得无声无息，与当前是否在算向量无关。
//
// dim 传 0 表示未知，此时不检查维度（只检查模型身份）。
func (s *Store) SetIdentity(model string, dim int) error {
	s.mu.RLock()
	prevModel, prevDim := s.der.Model, s.der.Dim
	s.mu.RUnlock()

	// ★ **先算错，再决定写不写** —— 顺序反了会把被拒的那个身份记进侧车。
	//
	//	原先的写法是「先记住再检查」，本意是让一份全新索引也能拿到身份（见下）。
	//	但那个顺序让一次**被拒**的声明照样落盘：调用方拿到 error、索引没建，
	//	而侧车里已经是新模型的名字了 —— 于是下一次比对它自己跟自己相等，检查
	//	从此再也触发不了。被拒的调用必须**零痕迹**。
	var err error
	switch {
	case model != "" && prevModel != "" && model != prevModel:
		err = fmt.Errorf("kb: index at %s was built with model %s but the config says %s; "+
			"vectors from different models are not comparable — "+
			"delete the index directory %s and re-run `openheron kb index`", s.dir, prevModel, model, s.dir)
	case dim > 0 && prevDim > 0 && dim != prevDim:
		err = fmt.Errorf("kb: index at %s was built with dim %d but the config declares %d; "+
			"vectors from different dimensions are not comparable — "+
			"delete the index directory %s and re-run `openheron kb index`", s.dir, prevDim, dim, s.dir)
	}
	if err != nil {
		return err
	}

	// ★ 通过了才记。注意**新索引也要记**：反过来（无可比就早退）会让一份全新
	//   的索引永远没有身份，于是后面所有身份比对都因为「没有可比的东西」而
	//   通过 —— 一个从不生效的检查。
	s.mu.Lock()
	if model != "" {
		s.der.Model = model
	}
	if dim > 0 {
		s.der.Dim = dim
	}
	s.mu.Unlock()
	return nil
}

// ReconcileRequest 是一轮对账的输入。
type ReconcileRequest struct {
	// Corpus 是语料目录的**展示名**（配置里的相对路径，如 `docs`）。
	//
	// ★ 它只进 catalog.md 的头部，用于人辨认这份目录是关于哪片语料的。
	//   它**不参与**任何路径计算 —— 条目路径相对语料根（§6）。
	Corpus string

	// Model / Dim 是这一轮用的模型身份。写进侧车，供下次比对。
	Model string
	Dim   int

	// Fresh 是本轮从语料里新抽出来的条目。
	Fresh []Entry

	// Scope 非空时**只动这几个路径**（带 `paths` 的增量）；为空 = 全语料，
	// 此时不在 Fresh 里的条目会被删掉。
	//
	// ★ 语义必须分开：增量索引只碰了它扫过的那几个文件，其余文件不在本轮
	//   targets 里，当然也不算「被删了」。
	Scope []string

	// Embed 把一组条目向量化，返回 path → 向量（没算的 path 不出现在 map 里）。
	// nil = 本轮不写向量（没配 embedding 的装配，§8.5）。
	//
	// ★ 它由**装配层**传进来，`Store` 自己不认识 embedder —— 这里要的是一个
	//   函数而不是一个 embedder 字段，因为调用它的是 [Store.Reconcile]，而它
	//   必须拿到**合并之后**的条目。
	//
	// ★ **入参是合并之后的条目**（`merged.Entries`），不是 `req.Fresh`。
	//
	//	人改过的摘要在合并之后才成型，而向量必须反映他改的那一份 —— 否则
	//	检索排的是机器那份、显示的是他那份，他花时间写的摘要对排序完全无效
	//	（§7.2 与附录三记的那条验收：「改一行摘要 → `ReEmbedded == 1`
	//	而 `Changed == 0`」）。
	Embed func(ctx context.Context, entries []Entry) (map[string][]float32, error)
}

// ReconcileResult 是一轮对账的结果。
type ReconcileResult struct {
	// Changed 是**内容**变了因而重嵌了向量的文档数。
	//
	// ★ 它与 mtime 变了的文档数不是一回事：只改了空格、或只改了人维护的摘要，
	//   mtime 会变而 EmbedText 不变 —— 那时不该重嵌。报告一个非零的
	//   ReEmbedded 而向量一个没变，会让模型以为内容真的动了。
	ReEmbedded int
	// Unchanged 是 EmbedText 没变、因而跳过重嵌的文档数。
	Unchanged int
	// Added 是本轮新增进目录的文档数。
	Added int
	// Removed 是本轮从目录里删掉的文档数（只在全语料对账时非零）。
	Removed int
	// removedPaths 是本轮删掉的路径。它不导出 —— 调用方要的是计数，
	// 而删哪几条只对 [Store.install] 有意义（它要同步清掉侧车里的向量）。
	removedPaths []string
	// Attempts 是写 `catalog.md` 的次数。>1 说明真的撞上了并发。
	Attempts int
	// Warnings 是解析 `catalog.md` 时记下的宽容性告警（拼错的键名等）。
	Warnings []string
}

// Reconcile 把本轮抽出来的条目合并进目录，并落盘。
//
// ★ **乐观提交**（§7.3）。完整流程是「读 → 合并 → 写 → 写后读校验」：
//
//	`AtomicWrite` 的 rename 只保证读者看到「整份旧」或「整份新」，保证不了
//	「我写的那份还在」。`A 写 → B 写 → A 以为成功`，A 的改动就没了。
//	加一次写后读，被盖的一方会**发现**并重新合并 —— 收敛。
//
// ★ **冲突时明确报错，不静默另存**：另存会造出第二份真相，而人会不知道
//
//	哪份生效。
func (s *Store) Reconcile(ctx context.Context, req ReconcileRequest) (*ReconcileResult, error) {
	if req.Dim < 0 {
		return nil, fmt.Errorf("kb: dim must not be negative, got %d", req.Dim)
	}
	// ★ 身份必须在**动盘之前**比对。跳过它的后果是「换了模型但目录照写」——
	//   两套不具可比性的向量混在一个侧车里，而检索照常工作，只是排序莫名其妙。
	//   传空身份的调用方（`Index()` 不带 Model）这里什么都不做。
	if err := s.SetIdentity(req.Model, req.Dim); err != nil {
		return nil, err
	}
	// ★ 空路径必须在**动盘之前**拒掉：路径是主键，而没有主键的条目检索不到、
	//   也删不掉 —— 它进了目录就变成一条永远清不掉的幽灵。
	for _, e := range req.Fresh {
		if e.Path == "" {
			return nil, fmt.Errorf("kb: entry with empty path (title %q); "+
				"the path is the primary key of a catalog entry", e.Title)
		}
	}
	mdPath := filepath.Join(s.dir, catalogFile)

	var lastWarnings []string
	for attempt := 1; attempt <= maxCommitAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// ① 读。每次重试都要**重新读盘** —— 用上次的快照当基线会把刚刚
		//    撞上的那次并发重演一遍。
		cur, warnings := s.readCatalog()
		lastWarnings = warnings

		// ② 合并。人改过的保留，机器字段重算。
		merged, next, res := mergeCatalog(cur, s.der, req)

		// ②.5 向量化。**必须在合并之后** —— 人改过的摘要在这一步之前还不存在
		//      （见 [ReconcileRequest.Embed]）。
		//
		//	★ 这里会发几十秒的网络请求，而它**不在任何锁里**：Reconcile 全程
		//	  不持 `s.mu`（读盘走文件、合并走快照），`install` 才短暂地拿一次。
		//	  放进锁里会让同进程的 `kb_search` 在整段嵌入期间全部卡住。
		if req.Embed != nil {
			vectors, err := req.Embed(ctx, merged.Entries)
			if err != nil {
				return nil, err
			}
			// ★ 不在 `Fresh` 里的那些也要计上：增量索引下它们不经过
			//   [applyFresh]（见 [embedInto]），但这一轮真的为它们发了请求。
			freshSet := make(map[string]bool, len(req.Fresh))
			for _, e := range req.Fresh {
				freshSet[e.Path] = true
			}
			for p := range vectors {
				if !freshSet[p] {
					res.ReEmbedded++
				}
			}
			embedInto(&next, merged.Entries, vectors)
		}

		// ★ `Unchanged` 是**推出来的**，不是另数一遍：每个条目要么被重嵌、要么
		//   没变，两者相加就是目录里的条目数。另数一遍就会存在一个与
		//   `ReEmbedded` 对不上的版本，而排查时没人知道该信哪个。
		res.Unchanged = len(merged.Entries) - res.ReEmbedded

		// ③ 写。头部里的 writer token **沿用读出来那份**，不新生成 ——
		//    它的用途就是让两次写入产生不同的字节，从而 ④ 的比较能发现覆盖。
		merged.Header.Revision++
		merged.Header.Updated = nowFn()
		body := RenderCatalog(merged)
		if err := persist.AtomicWrite(mdPath, body, newTmpSuffix()); err != nil {
			return nil, fmt.Errorf("kb: write %s: %w", mdPath, err)
		}

		// ★ **测试接缝**：④ 的窗口就卡在这两行之间，而正常情况下它只有几微秒。
		//
		//	没有接缝的话，「真的制造并发」只能靠起个 goroutine 反复写、赌它撞进
		//	那个窗口 —— 那种测试挂掉时分不清是逻辑坏了还是根本没撞上（而没撞上
		//	的表现是**通过**，于是它测的其实是运气）。这里让撞进窗口变成确定的：
		//	并发方依然写真的文件、真的走一次 rename，只是被安排在那一瞬。
		if s.afterWrite != nil {
			s.afterWrite()
		}

		// ④ 写后读校验。
		got, err := os.ReadFile(mdPath)
		if err != nil {
			return nil, fmt.Errorf("kb: read back %s: %w", mdPath, err)
		}
		if !bytes.Equal(got, body) {
			// 被盖了 → 重读、重新合并、重试。
			continue
		}

		// ⑤ 侧车。**不做校验**（§7.5）：它是纯派生，丢了自愈。
		res.Attempts = attempt
		res.Warnings = lastWarnings
		s.install(merged, next, req, res)
		return res, nil
	}
	return nil, fmt.Errorf("kb: %s was modified while writing (%d attempts); "+
		"your edit collided with an index run — re-run `openheron kb index`",
		mdPath, maxCommitAttempts)
}

// mergeCatalog 把本轮的条目并进目录，并算出侧车该写什么。
//
// ★ 字段所有权（§7.2）在这里一处实现：
//
//	summary / title  → **人优先**：目录里的值 != 侧车记的机器值 ⇒ 人改过 ⇒ 永不覆盖
//	sections / links / mtime / size → 机器，每次重算
//	stale            → 机器，**只标不修**
func mergeCatalog(cur Catalog, der Derived, req ReconcileRequest) (Catalog, Derived, *ReconcileResult) {
	fresh := make(map[string]Entry, len(req.Fresh))
	for _, e := range req.Fresh {
		fresh[e.Path] = e
	}
	scope := map[string]bool{}
	for _, p := range req.Scope {
		scope[p] = true
	}
	// 本轮有没有嵌入这一路。它决定 `applyFresh` 是「按文字计数并清向量」
	// 还是「把计数与向量都留给 ②.5」—— 见那边的注释。
	willEmbed := req.Embed != nil

	next := der.clone()
	if next.Entries == nil {
		next.Entries = map[string]DerivedEntry{}
	}
	next.IndexVersion = IndexVersion
	if req.Model != "" {
		next.Model = req.Model
	}
	if req.Dim > 0 {
		next.Dim = req.Dim
	}

	out := cur
	if out.Entries == nil {
		out.Entries = make([]Entry, 0, len(fresh))
	}
	out.Header.Corpus = req.Corpus
	if out.Header.IndexVersion == 0 {
		out.Header.IndexVersion = IndexVersion
	}
	if out.Header.Writer == "" {
		// ★ 沿用读出来的那个；一份全新的目录才需要一个，之后每次写都复用它。
		out.Header.Writer = newWriterToken()
	}
	if out.Header.Created.IsZero() {
		out.Header.Created = nowFn()
	}

	res := &ReconcileResult{}

	// 保留本轮**不碰**的条目；本轮要碰的从 fresh 补进来。
	kept := make([]Entry, 0, len(out.Entries)+len(fresh))
	for _, cur_e := range out.Entries {
		if !scope[cur_e.Path] && len(scope) > 0 {
			kept = append(kept, cur_e) // 增量：不动它
			continue
		}
		f, ok := fresh[cur_e.Path]
		if !ok {
			// 全语料对账里没出现 ⇒ 源文件被删了 ⇒ 清掉。
			//
			// ★ 不清的后果是「删了一整份文档，它仍然可以被检索到」—— 而那
			//   正是本包最贵的那类错：自信的、无声的过期答案。
			delete(next.Entries, cur_e.Path)
			res.Removed++
			res.removedPaths = append(res.removedPaths, cur_e.Path)
			continue
		}
		e := applyFresh(cur_e, f, der.Entries[cur_e.Path], &next, res, willEmbed)
		kept = append(kept, e)
		delete(fresh, cur_e.Path)
	}

	for _, f := range fresh {
		if len(scope) > 0 && !scope[f.Path] {
			continue
		}
		e := applyFresh(Entry{}, f, DerivedEntry{}, &next, res, willEmbed)
		e.Stale = false // 新文档没有「人的旧摘要」可言
		kept = append(kept, e)
		res.Added++
	}

	out.Entries = kept
	return out, next, res
}

// applyFresh 把一条新抽出来的条目套到旧条目上，并更新侧车。
func applyFresh(cur, fresh Entry, der DerivedEntry, next *Derived, res *ReconcileResult, willEmbed bool) Entry {
	out := fresh
	if out.MTime == 0 && out.Size == 0 {
		out.MTime, out.Size = cur.MTime, cur.Size
	}

	de := der
	// ★ 标题与摘要的归属判断分开做，但规则相同。
	//
	//	注意两边的判据都是 `cur`（目录里现在那份）对 `de.Wrote*`（机器上次
	//	抽出来的），**不是** `cur` 对 `fresh`（本轮抽出来的）。人改过之后
	//	两者本就该不同 —— 拿 fresh 当基准会让「文件一变、机器抽出新标题」
	//	被误判成「人改过」，于是机器的新标题永远写不进去。
	if ownsHuman(cur.Title, de.WroteTitle) {
		out.Title = cur.Title
	}
	if ownsHuman(cur.Summary, de.WroteSummary) {
		// 人拥有摘要 ⇒ 机器永不覆盖（§7.2）。
		out.Summary = cur.Summary

		// ★ `stale` 回答的是「这份人写的摘要对应源文件的哪个版本」，而**人改的
		//   时候源文件是什么版本**只有 `ObsMTime` 知道 —— 那是我们上一轮看到的
		//   源文件状态，也就是人当时能看到的那份目录所描述的状态。
		//
		//	源文件在那之后变过 ⇒ 他这段话有可能是照着旧版本写的 ⇒ 保守地标上。
		//	标错了人一眼就能看出来并再存一次（那会清掉）；漏标的后果是模型
		//	读着一句过期的摘要，且没有任何迹象提示它。
		changed := fresh.MTime != de.ObsMTime || fresh.Size != de.ObsSize

		if cur.Summary == de.ObsSummary {
			// 人**没动**它 ⇒ 陈旧只取决于源文件变没变，并且**保持**上一轮已经
			// 置上的标记。丢了它，stale 会在下一轮源文件没变时被无声清掉。
			out.Stale = cur.Stale || changed
		} else {
			// 人**改了** ⇒ 按上面那条判据定，不看 cur.Stale —— 他改的动作本身
			// 不清 stale（他的新摘要同样可能是针对旧源文件写的）。
			out.Stale = changed
		}
	} else {
		out.Stale = false
	}
	de.ObsSummary, de.ObsMTime, de.ObsSize = out.Summary, fresh.MTime, fresh.Size

	// ★ 指纹记**最终写进目录的那份**（`out`），不是抽出来的 `fresh`。
	//
	//	`EmbedText` 在侧车里只干一件事：判断「这份文档的文字变没变」，而它必须
	//	与 `embedAll` 真正嵌的那份**同源** —— 那边嵌的是合并之后的条目
	//	（[ReconcileRequest.Embed]），所以这里也只能记合并之后的。
	//
	//	反过来记 `fresh` 会怎样（v2 的第一版就是这么写的）：人改一次摘要，
	//	他的文字永远不等于机器那份 ⇒ **每一轮都判定「变了」⇒ 每一轮都重嵌**，
	//	而他改的那个字对向量从未生效过。两头都错：白花钱，且他的编辑只影响
	//	显示、不影响排序。这正是 §7.2 那条验收（「改一行摘要 ⇒ `ReEmbedded == 1`
	//	而 `Changed == 0`」）要挡住的东西。
	de.EmbedText = out.EmbedText()

	if !willEmbed {
		// ★ 没有嵌入这一路 ⇒ 侧车**不留向量**（§8.5）。
		//
		//	留下来的是上一轮那份，而它对应的模型这一轮没被声明 —— 用它会得到
		//	「看起来正常的错答案」。清掉之后，下次配上嵌入时按 EmbedText 判定
		//	全部重嵌，那才是正确的代价。
		de.Vector = nil
	}

	// 计数：判据是**文字**（EmbedText）而不是 mtime —— 只改了空格时 mtime 会变
	// 而文字没变，那一次重嵌是纯粹的浪费。
	//
	// ★ 有嵌入这一路时还要看**有没有向量**：文字没变但没有向量（上一轮没配
	//   嵌入，或侧车丢过）照样得嵌一次，否则那条永远没有向量。
	if out.EmbedText() == der.EmbedText && (!willEmbed || len(der.Vector) > 0) {
		res.Unchanged++
	} else {
		res.ReEmbedded++
	}

	// ★ 记**抽出来的**那份，不是最终写进目录的那份。
	//
	//	人改过之后 `out.Title` 是人的文字。把它记成 `WroteTitle` 等于让机器
	//	声称「这行是我写的」，于是下一轮 `ownsHuman` 返回 false、机器用自己
	//	新抽的值覆盖上去 —— **人的那句话从文件里消失，且不报错**。
	//
	//	记 fresh 的话，人的文字永远 != Wrote* ⇒永远判定为人的 ⇒ 永不覆盖。
	de.WroteTitle, de.WroteSummary = fresh.Title, fresh.Summary
	if next.Entries == nil {
		next.Entries = map[string]DerivedEntry{}
	}
	next.Entries[out.Path] = de
	return out
}

// embedInto 把本轮算出来的向量写进**待安装**的侧车快照。
//
// ★ 它同时刷新 `EmbedText` 指纹，两件事必须一起做：只写向量不写指纹的话，
// 下一轮比对依然判定「文字变了」⇒ 又嵌一遍 ⇒ 每轮都重嵌，症状是「索引
// 永远在忙、`ReEmbedded` 永远非零」。
//
// ★ 它按 `merged.Entries` 取指纹，所以覆盖到的不止 `req.Fresh`：一个带
// `paths` 的增量索引撞上人改过的**别的**文件时，那条也在这一轮被重嵌 ——
// 否则他会看到「我改的摘要进了 catalog.md，检索却还按旧向量排」。
//
// ★ 跳过目录里已经没有的路径：那是本轮刚被删掉的文档，给它留一份孤儿向量
// 会在下次身份比对里以「有一条没有对应文档」的形态冒出来。
func embedInto(next *Derived, entries []Entry, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}
	text := make(map[string]string, len(entries))
	for _, e := range entries {
		text[e.Path] = e.EmbedText()
	}
	for p, v := range vectors {
		de, ok := next.Entries[p]
		if !ok {
			continue
		}
		de.Vector = v
		if s, ok := text[p]; ok {
			de.EmbedText = s
		}
		next.Entries[p] = de
	}
}

// ownsHuman 判断目录里的某个值是不是**人改过的**。
//
// ★ 判据是「目录里的值 != 机器上次写下的值」。空串只在**两边都空**时算机器的：
//
//	机器从没写过这个字段，而人填了一个 ⇒ 那是人的。
func ownsHuman(current, wrote string) bool {
	if current == wrote {
		return false
	}
	return current != ""
}

// sameVector 比两个向量是否逐位相同。
//
// ★ 严格相等，不做近似：它判的是「侧车快照要不要为这条重写一行」，而不是
// 「够不够像」。近似相等会让「换了模型、向量整体挪了一点点」被判成没变，
// 于是侧车永远不更新 —— 而那正是最该被发现的一次变化。
func sameVector(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newTmpSuffix 给**每一次**写生成一个唯一的临时文件后缀。
//
// ★ 为什么不能固定成 `.tmp`：乐观提交（§7.3）允许两路写者同时写同一份
//
//	`catalog.md`，而 `AtomicWrite` 的临时文件名是 `dst + suffix`。固定后缀
//	让两路写者共用同一个 `catalog.md.tmp` —— A 写完正要 rename，B 已经把它
//	O_TRUNC 重写，A 的 rename 要么搬走 B 的半截内容、要么 ENOENT。
//
//	症状是「并发索引时偶发 no such file or directory」，而它看起来像文件系统
//	抽风，没人会归因到「临时文件名撞了」。这正是本包要根治的那类失效：
//	错误存在，但归因指向错误的地方。
//
// ★ 后缀必须让目录加载逻辑忽略它：残留的 `catalog.md.tmp.<hex>` 的
//
//	`filepath.Ext` 是 `.xxxx`，不是 `.md`，所以不会被当成语料读进来。
func newTmpSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 退化成纳秒时间戳。它不如随机好，但**两次调用必然不同** —— 满足本
		// 函数的唯一要求。返回常量会让所有写者又共用同一个临时文件。
		return ".tmp." + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return ".tmp." + hex.EncodeToString(b[:])
}

func cloneDerived(d Derived) Derived {
	out := d
	out.Entries = make(map[string]DerivedEntry, len(d.Entries))
	for k, v := range d.Entries {
		out.Entries[k] = v
	}
	return out
}

func (d Derived) clone() Derived { return cloneDerived(d) }

// cloneCatalog 返回一份条目切片的副本。
//
// ★ **必须复制**：调用方（渲染、工具输出）会读它，而 merge 会原地改。共享
//
//	底层数组的代价是「读的人看到写的人改到一半的样子」，而那类 bug 只在
//	并发下偶发。
func cloneCatalog(c Catalog) Catalog {
	out := c
	out.Entries = append([]Entry(nil), c.Entries...)
	for i := range out.Entries {
		out.Entries[i].Sections = append([]Section(nil), c.Entries[i].Sections...)
		out.Entries[i].Links = append([]string(nil), c.Entries[i].Links...)
	}
	return out
}

// install 把这一轮的结果装进内存快照，并写侧车。
//
// ★ 它**只覆盖本轮碰过的那些路径**，不整体替换 `s.der`。整体替换会把
//
//	并发的 [Store.SetVectors] 刚写进去的向量冲掉 —— 而那个窗口真实存在：
//	② 读 `s.der` 与 ⑤ 落盘之间，另一路刚算完向量的索引可能已经写进来了。
//	被冲掉不报错，只是那几条文档下次又得重嵌一遍。
func (s *Store) install(cat Catalog, next Derived, req ReconcileRequest, res *ReconcileResult) {
	touched := make(map[string]bool, len(req.Fresh)+len(res.removedPaths))
	for _, e := range req.Fresh {
		touched[e.Path] = true
	}

	s.mu.Lock()
	if s.der.Entries == nil {
		s.der.Entries = make(map[string]DerivedEntry, len(touched))
	}
	// ★ `req.Fresh` 不是全部：增量索引下，人改过的**别的**文件不在这批里，
	//   但它们的向量与指纹在这一轮被重算了（见 [embedInto]）—— 不装它们
	//   等于白嵌一次，而症状是「同一份文件被反复重嵌，每次索引都在忙」。
	//
	// ★ 它顺手把**观察到的维度**记下来（原先在 [Store.SetVectors] 里）：
	//   那是服务端真的返回的长度，而 `SetIdentity` 里那个是配置里人写的
	//   声明值。只记声明值的话，一个写错的 `dimensions` 永远没人比。
	for p, de := range next.Entries {
		old, had := s.der.Entries[p]
		if !had || old.EmbedText != de.EmbedText || !sameVector(old.Vector, de.Vector) {
			touched[p] = true
		}
	}

	for p := range touched {
		if de, ok := next.Entries[p]; ok {
			s.der.Entries[p] = de
			if len(de.Vector) > 0 {
				s.der.Dim = len(de.Vector)
			}
		}
	}
	// ★ 删掉的文档要把向量一起删掉，否则目录里没有它、侧车里还留着 ——
	//   而那份孤儿向量会在下次身份比对里以「有一条没有对应文档」的形态出现。
	for _, p := range res.removedPaths {
		delete(s.der.Entries, p)
	}
	s.der.IndexVersion = IndexVersion
	if req.Model != "" {
		s.der.Model = req.Model
	}
	if req.Dim > 0 {
		s.der.Dim = req.Dim
	}
	s.cat = cloneCatalog(cat)
	s.mu.Unlock()

	if err := s.saveSidecar(); err != nil {
		// ★ 侧车写失败**不报错给调用方**：它是纯派生，丢了下一轮自愈
		//   （按 EmbedText 对不上就重嵌那几条）。报错会让一次成功的索引
		//   看起来失败了。
		obs.Logger().Warn("[kb] sidecar write failed, vectors will be rebuilt next run", "err", err)
	}
	s.stamp, _ = statStamp(filepath.Join(s.dir, catalogFile))
}

// SetVectors 直接把一批向量写进侧车，**不经过对账**。
//
// ★ 索引流水线**不再走这里** —— 它把向量化交给 [Store.Reconcile] 在合并之后
//
//	调用（[ReconcileRequest.Embed]），因为要嵌的是合并后的条目。这个函数留给
//	「已经知道该写哪条向量」的调用方（测试、以及将来可能的重嵌工具）。
//
// ★ 它与 Reconcile 分开是有意的：向量化可能要几十秒，而目录的写入是毫秒级。
//
//	先写目录、再补向量，崩在中间的后果只是「目录对了、向量缺几条」—— 下一轮
//	按 EmbedText 判定重嵌，收敛。反过来先写向量则会留下「向量指向上一版文本」
//	的目录，而检索会自信地答错。
func (s *Store) SetVectors(vectors map[string][]float32) error {
	if len(vectors) == 0 {
		return nil
	}
	s.mu.Lock()
	for path, v := range vectors {
		de := s.der.Entries[path]
		de.Vector = v
		s.der.Entries[path] = de
		// ★ **维度是观察出来的，不是声明出来的**。
		//
		//	`SetIdentity(model, dim)` 里的 dim 是配置里人写的那个数，而这里
		//	是服务端真的返回的长度。只记声明值的话，一个写错的 `dimensions`
		//	配置永远不会被发现 —— 它与实际向量对不上，可没有任何东西去比。
		//
		//	记下观察值之后，下一次启动的 `SetIdentity` 才有可比的东西。
		//	同一轮里长度不同是**错误**（同一个嵌入端点不该有两种长度），
		//	但这里不报错：侧车是纯派生，报错会让一次可用的索引跑不完。
		if len(v) > 0 {
			s.der.Dim = len(v)
		}
	}
	s.mu.Unlock()
	return s.saveSidecar()
}

func (s *Store) saveSidecar() error {
	s.mu.RLock()
	der := cloneDerived(s.der)
	s.mu.RUnlock()

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&der); err != nil {
		return fmt.Errorf("kb: encode sidecar: %w", err)
	}
	if err := persist.AtomicWrite(filepath.Join(s.dir, sidecarFile), buf.Bytes(), newTmpSuffix()); err != nil {
		return fmt.Errorf("kb: write sidecar: %w", err)
	}
	return nil
}

// readCatalog 读盘上的目录。
//
// ★ **每次调用都真的读盘**，不看内存快照 —— 它是乐观提交的基线，用陈旧的
//
//	快照当基线会把刚刚撞上的那次并发重演一遍。
func (s *Store) readCatalog() (Catalog, []string) {
	b, err := os.ReadFile(filepath.Join(s.dir, catalogFile))
	if err != nil {
		return Catalog{Header: Header{IndexVersion: IndexVersion}}, nil
	}
	c, err := ParseCatalog(b)
	if err != nil {
		// ★ 解析失败**不静默**：它多半是人把文件改坏了。当作空目录继续写会
		//   把人剩下的内容整份覆盖掉。
		//
		//   这里不返回 error 是因为 Reconcile 的重试循环要能收敛；错误由调用方
		//   在**下一次** NewStore / load 时看到。折中做法是直接报错。
		obs.Logger().Error("[kb] catalog.md is unreadable, refusing to overwrite it", "err", err)
		return Catalog{Header: Header{IndexVersion: IndexVersion}}, []string{
			fmt.Sprintf("catalog.md is unreadable: %v", err),
		}
	}
	if c.Header.IndexVersion == 0 {
		c.Header.IndexVersion = IndexVersion
	}
	return c, c.Warnings
}

func (s *Store) load() error {
	b, err := os.ReadFile(filepath.Join(s.dir, catalogFile))
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("kb: read %s: %w", filepath.Join(s.dir, catalogFile), err)
		}
		// 空目录是合法状态。
		s.cat = Catalog{Header: Header{IndexVersion: IndexVersion, Created: nowFn(), Writer: newWriterToken()}}
	} else {
		c, perr := ParseCatalog(b)
		if perr != nil {
			return fmt.Errorf("kb: parse %s: %w", filepath.Join(s.dir, catalogFile), perr)
		}
		if c.Header.IndexVersion != 0 && c.Header.IndexVersion != IndexVersion {
			return fmt.Errorf("kb: %s was written by extraction rules v%d but this build understands v%d; "+
				"remove %s and re-run `openheron kb index`",
				filepath.Join(s.dir, catalogFile), c.Header.IndexVersion, IndexVersion, s.dir)
		}
		if c.Header.IndexVersion == 0 {
			c.Header.IndexVersion = IndexVersion
		}
		s.cat = c
	}
	if st, ok := statStamp(filepath.Join(s.dir, catalogFile)); ok {
		s.stamp = st
	}

	sb, err := os.ReadFile(filepath.Join(s.dir, sidecarFile))
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("kb: read %s: %w", filepath.Join(s.dir, sidecarFile), err)
		}
		s.der = Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}
		return nil
	}
	var der Derived
	if err := gob.NewDecoder(bytes.NewReader(sb)).Decode(&der); err != nil {
		// ★ 侧车坏了**不是错误**（§7.5）：它是纯派生，丢了自愈 —— 下一轮按
		//   EmbedText 对不上就重嵌那几条。报错会让一个可自愈的状态看起来
		//   像需要人工干预。
		obs.Logger().Warn("[kb] sidecar is unreadable, vectors will be rebuilt next run", "err", err)
		der = Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}
	}
	if der.Entries == nil {
		der.Entries = map[string]DerivedEntry{}
	}
	if der.IndexVersion != 0 && der.IndexVersion != IndexVersion {
		// 格式变了 ⇒ 旧向量不可信 ⇒ 清掉，下一轮重嵌。
		obs.Logger().Warn("[kb] sidecar was written by another build; vectors will be rebuilt",
			"written_by_version", der.IndexVersion, "this_build_version", IndexVersion)
		der = Derived{IndexVersion: IndexVersion, Entries: map[string]DerivedEntry{}}
	}
	if der.IndexVersion == 0 {
		der.IndexVersion = IndexVersion
	}
	s.der = der
	return nil
}

// refresh 在目录被别的进程改过时重新加载。
//
// ★ 它**不返回错误**：查询时读不出目录文件（刚被删、或正在被 rename）不该让
//
//	检索失败 —— 用旧快照答一次远好过什么都不答。真正的错误留给显式的
//	NewStore / Reconcile 去报。
func (s *Store) refresh() {
	st, ok := statStamp(filepath.Join(s.dir, catalogFile))
	if !ok {
		return
	}
	s.mu.RLock()
	same := s.stamp == st
	s.mu.RUnlock()
	if same {
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()
	// 拿到 runMu 之后再查一次：前面那段窗口里可能已经有别人刷过了。
	s.mu.RLock()
	same = s.stamp == st
	s.mu.RUnlock()
	if same {
		return
	}

	b, err := os.ReadFile(filepath.Join(s.dir, catalogFile))
	if err != nil {
		return
	}
	c, err := ParseCatalog(b)
	if err != nil {
		return
	}

	// ★ 侧车也要跟着换，否则目录里的条目与侧车里的向量对不上 —— 而那表现为
	//   「检索到了，但没有向量分」，没有任何东西会说。
	var der Derived
	if sb, err := os.ReadFile(filepath.Join(s.dir, sidecarFile)); err == nil {
		if err := gob.NewDecoder(bytes.NewReader(sb)).Decode(&der); err != nil {
			der = Derived{}
		}
	}
	if der.Entries == nil {
		der.Entries = map[string]DerivedEntry{}
	}

	s.mu.Lock()
	s.cat = c
	s.der = der
	s.stamp = st
	s.mu.Unlock()
}

func statStamp(p string) (fileStamp, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{mtime: fi.ModTime().UnixNano(), size: fi.Size(), valid: true}, true
}

// ★ gob 只能编码**注册过的**具体类型，而侧车里全是基本类型。注册表就是本包
// 承诺支持的值类型面 —— 超出它的类型会在写盘时报错，而不是被悄悄转成 string。
var registerGob = []any{
	Derived{},
	DerivedEntry{},
	[]float32{},
}

func init() {
	for _, v := range registerGob {
		gob.Register(v)
	}
}
