package kb

import (
	"context"
	"fmt"
	"openheron/agentkit/obs"
	"path/filepath"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

const typRetriever = "File"

// RetrieverConfig 与 eino-ext 的 RedisRetrieverConfig **逐字段对齐**
// （`Dialect` 与 `ImplOptions.FilterQuery` 除外，见 docs/kb-design.md §6.1）。
type RetrieverConfig struct {
	// Store 是索引的唯一所有者，与 Indexer 共用同一个实例。
	Store *Store

	// Index 是索引名。Redis 版用它选 FT index，本包对应目录名。
	Index string

	// VectorField 是查询时用哪个向量字段。缺省 "vector_content"。
	VectorField string

	// DistanceThreshold 控制召回门槛。
	//
	// ★ **nil 有语义**：nil = 按 TopK 取前 K；非 nil = 只召回融合分不低于
	//   阈值的结果，然后才取 TopK。Redis 版同样是 VECTOR_RANGE 与 KNN 二选一。
	//
	// ★ **v2 起它的语义变了**：距离 = 1 - cosine（§8.5）。而 v2 的融合分
	//   是 `0.5*lex + 0.5*cos`，无向量时退化成纯 lex —— 那时**距离**这个
	//   说法没有意义，于是无向量时**忽略这个阈值**并报一条告警。
	//
	//   忽略而不是报错：一个配了阈值的人在没有嵌入服务时照样想检索，而
	//   报错会把一次能用的查询变成不能用。
	DistanceThreshold *float64

	// ReturnFields 限制返回哪些字段。缺省 ["content", "vector_content"]。
	ReturnFields []string

	// TopK 限制返回条数。缺省 5。
	TopK int

	// Embedding 是向量化组件。**v2 起可选**（§8.5）。
	//
	// ★ 为 nil 时查询不被向量化，检索退化成词法。这不是降级模式 ——
	//   词法那一路真的能用，见 [lexScore]。
	Embedding embedding.Embedder

	// DocumentConverter 把命中转成文档。nil 时用 [DefaultDocumentConverter]。
	//
	// ★ 位置与名字与 Redis 版一致，**参数类型不能一致**（那边是 redis.Document）——
	//   硬造一个中间 raw 类型比不对齐更糟：它会让两边的转换器也无法互换。
	DocumentConverter func(ctx context.Context, m Match) (*schema.Document, error)
}

var _ retriever.Retriever = (*FileRetriever)(nil)

// Match 是一条检索命中：条目 + 它的向量 + 融合分。
//
// ★ 它**不再带 Fields**。v1 里 Fields 是「一个记录有哪些键值」的通用容器，
//
//	而 v2 的单元是源文件，它的全部内容就是一个 [Entry] —— 通用容器在这里
//	只会多出一层「字段名对不上」的可能。
type Match struct {
	Entry  Entry
	Vector []float32
	Score  float64
}

// FileRetriever 从 [Store] 里检索。
type FileRetriever struct {
	config *RetrieverConfig
}

// NewRetriever 造一个文件检索器。
//
// ★ `Embedding` 为 nil **不再报错**（§8.5）：与索引侧同一个理由 —— 词法
//
//	那一路是真的能用的，一个没有向量的检索器不是残缺的。
func NewRetriever(_ context.Context, config *RetrieverConfig) (*FileRetriever, error) {
	if config == nil {
		return nil, fmt.Errorf("kb: retriever config not provided")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("kb: store not provided for file retriever")
	}
	if config.TopK == 0 {
		config.TopK = defaultTopK
	}
	if config.TopK < 0 {
		return nil, fmt.Errorf("kb: top k must be positive, got %d", config.TopK)
	}
	if config.VectorField == "" {
		config.VectorField = FieldVector
	}
	if len(config.ReturnFields) == 0 {
		// ★ 缺省与 Redis 版一字不差（它也是这两个），因为那是**联合默认值**。
		//   openheron 自己要的出处字段由 [ResultFields] 在装配层显式传入 ——
		//   把它塞进这个默认值会让「File 版默认回得比 Redis 版多」，
		//   换后端时行为悄悄变了。
		config.ReturnFields = []string{FieldContent, FieldVector}
	}
	if config.DocumentConverter == nil {
		config.DocumentConverter = DefaultDocumentConverter(config.ReturnFields)
	}
	// ★ §8.4：配了门槛又配了 embedder，那个门槛这一轮起就不生效了。
	//
	//	**说出来，不静默** —— 静默的后果是用户以为自己在调召回门槛，而实际
	//	什么都没调（融合分上没有距离的量纲）。这里报一次，而不是每次查询报：
	//	那是配置的事实，不是每次检索的事实。
	if config.Embedding != nil && config.DistanceThreshold != nil {
		obs.Logger().Warn("[kb] DistanceThreshold is ignored while an embedding is configured; " +
			"the fused score has no cosine-distance dimension (see docs/kb-design.md §8.4)")
	}
	// ★ Index 与 Store 是两份说法，不一致就**报错**而不是忽略。
	//   留着它是为了与 Redis 版对齐，可一旦允许「配了但不生效」，它就成了
	//   一个会安静地撒谎的字段 —— 那正是本包要根治的那类失效。
	if config.Index != "" && config.Index != filepath.Base(config.Store.Dir()) {
		return nil, fmt.Errorf("kb: retriever index %q does not match store dir %q",
			config.Index, filepath.Base(config.Store.Dir()))
	}
	return &FileRetriever{config: config}, nil
}

// Retrieve 实现 [retriever.Retriever]。
func (r *FileRetriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) (
	docs []*schema.Document, err error) {

	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	co := retriever.GetCommonOptions(&retriever.Options{
		TopK:           &r.config.TopK,
		ScoreThreshold: r.config.DistanceThreshold,
		Embedding:      r.config.Embedding,
	}, opts...)

	ctx = callbacks.OnStart(ctx, &retriever.CallbackInput{
		Query:          query,
		TopK:           *co.TopK,
		ScoreThreshold: co.ScoreThreshold,
	})

	// ★ **词法那一路永远跑**（§8.2）。它不依赖任何嵌入服务，而精确匹配
	//   （`DistanceThreshold` 这类标识符）恰恰是余弦猜不出来的那一半。
	terms := parseQuery(query)

	var qvec []float32
	if co.Embedding != nil {
		// ★ 有嵌入但**侧车一条向量都没有**时，不发那次查询嵌入请求：
		//   发了也是白发（没有向量可比），而一次网络往返的失败会让一次
		//   本来能用的词法检索直接报错。
		if len(r.config.Store.Docs()) > 0 && hasAnyVector(r.config.Store) {
			vectors, err := co.Embedding.EmbedStrings(embeddingCtx(ctx, co.Embedding), []string{query})
			if err != nil {
				return nil, fmt.Errorf("kb: embedding query failed: %w", err)
			}
			if len(vectors) != 1 {
				return nil, fmt.Errorf("kb: invalid vector count for one query, got=%d", len(vectors))
			}
			qvec = make([]float32, len(vectors[0]))
			for i, x := range vectors[0] {
				qvec[i] = float32(x)
			}
		}
	}

	hits := r.search(terms, *co.TopK, qvec, thresholdOf(co))

	docs = make([]*schema.Document, 0, len(hits))
	for _, h := range hits {
		d, err := r.config.DocumentConverter(ctx, h)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}

	callbacks.OnEnd(ctx, &retriever.CallbackOutput{Docs: docs})
	return docs, nil
}

// hasAnyVector 报告侧车里有没有至少一条向量。
func hasAnyVector(st *Store) bool {
	for _, d := range st.Docs() {
		if len(d.Vector) > 0 {
			return true
		}
	}
	return false
}

// search 对全部文档打分并取前 topK。
//
// ★ **全量线性扫描**，不做近似。§8.1 的判断是：语料是「几份到几百份文档」，
//
//	而每条打分是一次字符串查找加一次向量点积 —— 459 节、10 份文档的实测
//	语料下，一次查询的耗时在微秒量级。引入 ANN 索引要持久化一份结构，
//	而那份结构会与侧车不一致，且不一致时检索照常工作只是悄悄变差。
func (r *FileRetriever) search(terms queryTerms, topK int, qvec []float32, threshold *float64) []Match {
	docs := r.config.Store.Docs()
	out := make([]Match, 0, topK)
	for _, d := range docs {
		s := scoreDoc(terms, d.Entry, d.Vector, qvec)
		// ★ 下限门槛。到这里 `threshold` 只可能来自「没有 embedder」那条路
		//   （见 [thresholdOf]），于是 `s` 就是纯词法分，量纲是明确的。
		if threshold != nil && s < *threshold {
			continue
		}
		out = append(out, Match{Entry: d.Entry, Vector: d.Vector, Score: s})
	}
	sortMatches(out)
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

// sortMatches 按分数降序、路径升序。
//
// ★ **路径是第二排序键**。并列时按 map 遍历序输出的话，同一个查询两次跑
//
//	出的顺序可能不同 —— 而那会让「TopK 稳定」这个性质悄悄失效，表现为
//	「模型说第 2 条是 A，下一轮说第 2 条是 B」，两边都对，无从对证。
func sortMatches(ms []Match) {
	// 插入排序：topK 通常是个位数，而完整排序对长切片是 O(n log n)。
	// 这里选插入是因为它在「几乎有序」时最快，而打分结果通常就是这样。
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0; j-- {
			a, b := &ms[j-1], &ms[j]
			better := a.Score < b.Score ||
				(a.Score == b.Score && a.Entry.Path > b.Entry.Path)
			if !better {
				break
			}
			*a, *b = *b, *a
		}
	}
}

// DefaultDocumentConverter 按 returnFields 组装文档。
//
// ★ **它不再有「字段不在库里就报错」这个行为**，因为 v2 的返回字段是
//
//	[Entry] 的一部分而不是一份键值表。保留这个签名是为了与 Redis 版的
//	defaultResultParser 对齐：换后端时装配层那几行不用改。
//
// thresholdOf 决定这一轮 `DistanceThreshold` 生不生效，是 §8.4 那条决定的
// 唯一实现处。
//
// ★ 它的字面语义是**余弦距离**（越小越像，`1 - cosine`），而 Score 是
//
//	**相似度**，还是 `0.5*词法 + 0.5*余弦` 的融合 —— 融合之后这个字段不再
//	有对应量纲，方向甚至相反：拿一个「越小越像」的数去卡一个「越大越像」的
//	分数，会把「卡得住」和「卡不住」理解反，而**用户收不到任何提示**。
//
// 所以：
//
//   - **有 embedder ⇒ 忽略**。融合分上没有可用的量纲，套上就是骗人。
//   - **无 embedder ⇒ 生效**，作为词法分的下限门槛。那时分数就是纯词法分，
//     「至少要有这么多相关度才值得回给模型」是明确的。
//
// ★ 按**配置**判（`co.Embedding`），不按「侧车里到底有没有向量」判：
//
//	后者会让人配了门槛、先跑一次纯词法索引时生效，向量化之后**悄悄失效**，
//	而那正是本包要根治的那类静默变化。
func thresholdOf(co *retriever.Options) *float64 {
	if co.Embedding != nil {
		return nil
	}
	return co.ScoreThreshold
}

// DefaultDocumentConverter 把一条命中转成 eino 文档。
func DefaultDocumentConverter(returnFields []string) func(context.Context, Match) (*schema.Document, error) {
	return func(_ context.Context, hit Match) (*schema.Document, error) {
		d := &schema.Document{
			ID:       hit.Entry.Path,
			Content:  hit.Entry.EmbedText(),
			MetaData: map[string]any{},
		}
		for _, f := range returnFields {
			switch f {
			case FieldVector:
				// ★ **无向量时不报错**（§8.5）。v1 里这里会报「requested but
				//   record has no vector」，而 v2 没有向量是常态而非错误。
				if len(hit.Vector) == 0 {
					continue
				}
				out := make([]float64, len(hit.Vector))
				for i, x := range hit.Vector {
					out[i] = float64(x)
				}
				d = d.WithDenseVector(out)
			case FieldContent:
				// content 已经在 d.Content 里了。
			case MetaSource:
				d.MetaData[MetaSource] = hit.Entry.Path
			case MetaEntry:
				d.MetaData[MetaEntry] = hit.Entry
			default:
				// ★ 点名了库里没有的字段 ⇒ **报错**，不是悄悄少回一个。
				//
				//	静默的代价是调用方读到一个 nil 而不知道为什么 —— 而它
				//	多半是拼错了，本该当场发现。（v1 里这个 default 分支不存在，
				//	因为字段集合是写死的；v2 允许调用方带自己的字段，于是
				//	「不认识」必须有定义。）
				return nil, fmt.Errorf("kb: return field %q is not carried by the file retriever; "+
					"known fields: %s, %s, %s, %s", f, FieldContent, FieldVector, MetaSource, MetaEntry)
			}
		}
		if d.MetaData[MetaSource] == nil {
			d.MetaData[MetaSource] = hit.Entry.Path
		}
		d = d.WithScore(hit.Score)
		return d, nil
	}
}

func (r *FileRetriever) GetType() string { return typRetriever }

func (r *FileRetriever) IsCallbacksEnabled() bool { return true }
