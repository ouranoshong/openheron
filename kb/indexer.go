package kb

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
)

const typIndexer = "File"

// IndexerConfig 与 eino-ext 的 RedisIndexerConfig **逐字段对齐**
// （Client ↔ Store 除外，见 docs/kb-design.md §6.1）。
type IndexerConfig struct {
	// Store 是索引的唯一所有者，与 Retriever 共用同一个实例。
	Store *Store

	// DocumentToFields 把文档变成字段集合。
	//
	// ★ **v2 的文件实现不读它** —— 索引走的是 [Entry] 与目录，不是字段集合。
	//	它是为「将来接 Redis 后端时逐字段对齐」留的口（本仓不做 Redis 后端，
	//	见 docs/kb-design.md §1）。**填了也不生效**，所以别填。
	DocumentToFields DocumentToFields

	// BatchSize 是一次向量化请求里的**条数**。缺省 10。
	BatchSize int `json:"batch_size"`

	// MaxBatchChars 是一批文本的字符数上限。
	//
	// ★ v2 的单位是**源文件**而不是块，所以缺省值相应改大（见
	//   [defaultMaxBatchChars]）。字段本身不动 —— 换后端时它照样是
	//   Redis 版那个 MaxBatchChars 的对应物。
	MaxBatchChars int `json:"max_batch_chars"`

	// Embedding 是向量化组件。**v2 起可选**（§8.5）。
	//
	// ★ 为 nil 时索引照常写目录、检索退化成词法。这不是「预留分支」——
	//   词法那一路是真的能用的：§8.2 的符号级打分对标识符类查询（openheron
	//   语料里最常见的一类）是精确的，不依赖任何嵌入服务。
	//
	//   而它**不是**「降级模式」：没有嵌入时检索照样能答，只是同义查询
	//   （「热更新」↔「hot reload」）召回不到。这个边界要让用的人知道，
	//   所以 `kb status` 会报「无向量 · 纯词法」。
	Embedding embedding.Embedder
}

var _ indexer.Indexer = (*FileIndexer)(nil)

// FileIndexer 把文档写进 [Store]。
type FileIndexer struct {
	config *IndexerConfig
}

// StoreRef 返回这只索引器写入的那一份索引。
//
// ★ 不叫 Store —— 那个名字已经给了 eino 的接口方法。
func (i *FileIndexer) StoreRef() *Store { return i.config.Store }

// NewIndexer 造一个文件索引器。
//
// ★ `Embedding` 为 nil **不再报错**（§8.5）：v1 里它是必填，因为那时只有
//
//	向量一条路；v2 有词法兜底，一个没有向量的索引是有用的，不是残缺的。
func NewIndexer(_ context.Context, config *IndexerConfig) (*FileIndexer, error) {
	if config == nil {
		return nil, fmt.Errorf("kb: indexer config not provided")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("kb: store not provided for file indexer")
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.BatchSize < 0 {
		return nil, fmt.Errorf("kb: batch size must be positive, got %d", config.BatchSize)
	}
	if config.MaxBatchChars == 0 {
		config.MaxBatchChars = defaultMaxBatchChars
	}
	if config.MaxBatchChars < 0 {
		return nil, fmt.Errorf("kb: max batch chars must be positive, got %d", config.MaxBatchChars)
	}
	return &FileIndexer{config: config}, nil
}

// Store 实现 [indexer.Indexer]。
//
// ★ **一个 schema.Document = 一个源文件**（§14）。doc.ID 是相对语料根的路径，
//
//	doc.Content 是 [Entry.EmbedText]，doc.MetaData 里放着抽好的 [Entry]。
//
//	为什么还要绕 schema.Document 一圈，而不是直接收 []Entry：**保留 eino
//	接口**。换后端时装配层那几行不用改，靠的就是 FileIndexer 与 Redis 版
//	实现同一个接口。
func (i *FileIndexer) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) (
	ids []string, err error) {

	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	options := indexer.GetCommonOptions(&indexer.Options{
		Embedding: i.config.Embedding,
	}, opts...)

	ctx = callbacks.OnStart(ctx, &indexer.CallbackInput{Docs: docs})

	fresh, err := entriesOf(docs)
	if err != nil {
		return nil, err
	}
	res, err := i.Reconcile(ctx, ReconcileRequest{Fresh: fresh}, options.Embedding)
	if err != nil {
		return nil, err
	}
	_ = res

	ids = make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	callbacks.OnEnd(ctx, &indexer.CallbackOutput{IDs: ids})
	return ids, nil
}

// entriesOf 从文档里取回 [Entry]。
//
// ★ Entry 藏在 MetaData 里而不是从 Content 重建：**章节行号无法从
//
//	EmbedText 重建** —— 后者只装标题、摘要、章节名、链接，行号在抽取时
//	就被丢掉了。重建等于把 §14.1「章节一律带行号」这条承诺悄悄撤销。
func entriesOf(docs []*schema.Document) ([]Entry, error) {
	out := make([]Entry, 0, len(docs))
	for _, d := range docs {
		if d == nil || d.ID == "" {
			return nil, fmt.Errorf("kb: document id not set")
		}
		raw, ok := d.MetaData[MetaEntry]
		if !ok {
			return nil, fmt.Errorf("kb: document %s has no %s in metadata; "+
				"the file indexer needs the extracted Entry", d.ID, MetaEntry)
		}
		e, ok := raw.(Entry)
		if !ok {
			return nil, fmt.Errorf("kb: document %s metadata %s is %T, want kb.Entry",
				d.ID, MetaEntry, raw)
		}
		if e.Path == "" {
			e.Path = d.ID
		}
		if e.Path != d.ID {
			return nil, fmt.Errorf("kb: document %s carries entry for %s; "+
				"the entry path must equal the document id", d.ID, e.Path)
		}
		out = append(out, e)
	}
	return out, nil
}

// Reconcile 是 [FileIndexer] 真正干活的地方：合并 → 向量化 → 落盘。
//
// ★ **它不声明模型身份**。身份由装配层在建索引器之前调 [Store.SetIdentity]
//
//	定下（`kbcmd.go` 与 `main.go` 各一处，都取自配置的 `kb.model`）。这里
//	再声明一次会用**组件类型**（`components.GetType` 给的是 "OpenAI" 这类
//	字符串）去覆盖配置里的模型名，于是身份比对在拿待检项检自己 —— 一个从不
//	生效的检查，且它一生效就是误报。
//
// ★ **向量化排在合并之后**（合并发生在 [Store.Reconcile] 的 ②，向量化在
//
//	②.5）。这不是顺序偏好：要嵌的是**合并后**的条目，而人改过的摘要在合并
//	之前根本不存在 —— 先嵌再合并就只能嵌机器那份，于是他那句摘要对排序永远
//	无效（§7.2）。
//
// ★ 落盘顺序仍然是**先写目录、再补向量**（§7.4）：目录写入是毫秒级，向量化
//
//	是几十秒，而两者之间没有任何东西能真正保证原子性。崩在中间只该留下
//	「目录对了、向量缺几条」—— 下一轮按 EmbedText 判定重嵌，收敛。反过来
//	留下「向量指向上一版文本」的目录，检索会**自信地**答错。
func (i *FileIndexer) Reconcile(ctx context.Context, req ReconcileRequest, emb embedding.Embedder) (
	*ReconcileResult, error) {

	if emb == nil {
		emb = i.config.Embedding
	}

	// ★ 向量化**交给 [Store.Reconcile] 在合并之后调用**（那里 ②.5），因为要嵌
	//   的是合并后的条目 —— 人改过的摘要在那之前还不存在。这里只把「怎么嵌」
	//   递进去；Store 自己不认识 embedder（§8.5），它只知道在正确的时机调一下
	//   这个函数。
	//
	//   `emb` 为 nil 时 [embedAll] 会返回一份「全部为空」的结果，于是侧车里的
	//   向量被清掉、下一轮按 EmbedText 判定重嵌 —— 那条路径与这里同一条。
	req.Embed = func(ctx context.Context, entries []Entry) (map[string][]float32, error) {
		return i.embedAll(ctx, entries, emb)
	}
	return i.config.Store.Reconcile(ctx, req)
}

// embedAll 只向量化**内容真的变了**的那些文档（§7.4）。
//
// ★ 它拿到的是**合并之后的条目**（[ReconcileRequest.Embed]），所以「内容」
// 指的是人看到的那份文本 —— 包括他改过的摘要。这一点是刻意的：摘要进了
// `EmbedText`，它就该影响排序，否则他花时间写的摘要只改了显示。
//
// ★ 判据是 EmbedText 而不是 mtime：编辑器重写整个文件不改正文时 mtime 会
//
//	变而文字没变，那一次重嵌是纯粹的浪费 —— 而浪费的形态是「一次全量索引
//	要跑十几秒而不是零秒」，没人会把它归因到「我不该存 mtime」。
func (i *FileIndexer) embedAll(ctx context.Context, entries []Entry, emb embedding.Embedder) (
	map[string][]float32, error) {

	// ★ **无向量模式**：整个函数变成一次遍历，一个请求都不发（§8.5）。
	//   这不是降级 —— 词法那一路照常工作，而写进侧车的空向量会在下次
	//   配上嵌入时按 EmbedText 判定全部重嵌。
	if emb == nil {
		out := map[string][]float32{}
		for _, e := range entries {
			out[e.Path] = nil
		}
		return out, nil
	}

	need := make([]Entry, 0, len(entries))
	for _, e := range entries {
		// 已经有向量且内容没变 ⇒ 跳过。
		//
		// ★ 比的是侧车里那份指纹（上一轮**合并后**的文字）与这一轮合并后的
		//   文字。人改过摘要时两者不等 ⇒ 重嵌，这正是 §7.2 要的。
		if cur, ok := i.config.Store.derOf(e.Path); ok && cur.EmbedText == e.EmbedText() && len(cur.Vector) > 0 {
			continue
		}
		need = append(need, e)
	}

	out := make(map[string][]float32, len(need))
	batch := make([]Entry, 0, i.config.BatchSize)
	batchedChars := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		vecs, err := embedEntries(ctx, batch, emb, i.config.BatchSize)
		if err != nil {
			return err
		}
		for k, e := range batch {
			out[e.Path] = vecs[k]
		}
		batch = batch[:0]
		batchedChars = 0
		return nil
	}

	for _, e := range need {
		chars := len(e.EmbedText())
		// ★ 单份超过上限时**不报错**，单独成批 —— 那是「这份文档很长」，
		//   报错会让人以为是配置错了。
		if batchedChars+chars > i.config.MaxBatchChars && len(batch) > 0 {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		batch = append(batch, e)
		batchedChars += chars
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// embedEntries 向量化一批条目的 EmbedText。
func embedEntries(ctx context.Context, batch []Entry, emb embedding.Embedder, batchSize int) (
	[][]float32, error) {

	bodies := make([]string, len(batch))
	for i, e := range batch {
		bodies[i] = e.EmbedText()
	}
	ectx := embeddingCtx(ctx, emb)

	out := make([][]float32, len(bodies))
	for start := 0; start < len(bodies); start += batchSize {
		end := start + batchSize
		if end > len(bodies) {
			end = len(bodies)
		}
		vectors, err := emb.EmbedStrings(ectx, bodies[start:end])
		if err != nil {
			// ★ 错误必须指向可操作的方向。只说「embedding failed」的话，
			//   用户看到的是一个放不下请求的端点，而真正的原因是我们把太多
			//   文本塞进了一次调用。**绝不自动降批重试** —— 那会把「慢」
			//   伪装成「成功」。
			return nil, fmt.Errorf("kb: embedding %d texts (%d chars, batch limit %d texts / %d chars) "+
				"failed: %w; lower kb.batch_size or kb.max_batch_chars, or raise the "+
				"embedding server's context size",
				end-start, charsOf(bodies[start:end]), batchSize, charsOf(bodies), err)
		}
		if len(vectors) != end-start {
			return nil, fmt.Errorf("kb: invalid vector length, expected=%d, got=%d", end-start, len(vectors))
		}
		for k, v := range vectors {
			f32 := make([]float32, len(v))
			for i, x := range v {
				f32[i] = float32(x)
			}
			out[start+k] = f32
		}
	}
	return out, nil
}

// embeddingCtx 让向量化这一步在回调链里显示为 embedding 组件 —— 与
// Redis 版同做法，否则观测里会多出一层看不出是什么的 span。
func embeddingCtx(ctx context.Context, emb embedding.Embedder) context.Context {
	runInfo := &callbacks.RunInfo{Component: components.ComponentOfEmbedding}
	if t, ok := components.GetType(emb); ok {
		runInfo.Type = t
	}
	runInfo.Name = runInfo.Type + string(runInfo.Component)
	return callbacks.ReuseHandlers(ctx, runInfo)
}

func charsOf(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

func (i *FileIndexer) GetType() string { return typIndexer }

func (i *FileIndexer) IsCallbacksEnabled() bool { return true }
