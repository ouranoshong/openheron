package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// 工具名。★ 都是**动词**，与工具表里现有的 read_file / write_file /
// write_todos / execute 一致 —— `kb_indexer` 那种 `-er` 结尾读起来是「一个
// 做某事的部件」，那是实现的名字（FileIndexer）而不是动作的名字。
const (
	ToolSearch = "kb_search"
	ToolIndex  = "kb_index"
)

// ── kb_search ────────────────────────────────────────────────────────────

// SearchInput 是 kb_search 的入参。
type SearchInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"A natural-language question about the project's documentation. Ask a full sentence rather than keywords — retrieval quality depends on it far more than on any tuning."`
	TopK  int    `json:"top_k,omitempty" jsonschema_description:"How many passages to return. Omit for the configured default."`
}

// SearchConfig 造 kb_search 所需的东西。
type SearchConfig struct {
	// Retriever 是检索器。
	Retriever retriever.Retriever
	// Instruction 是只加在**查询侧**的指令前缀，见 [Instruct]。
	//
	// ★ 由装配层传进来，而不是让调用方先包一层 [Instruct]：包一层的话就有
	//   两个地方可以放前缀（这里和那里），而放错一边不会报错，只是召回质量
	//   悄悄变差。一个字段，一处生效。
	Instruction string
	// TopK 是缺省召回条数。
	TopK int
}

// NewSearchTool 造 `kb_search`。
//
// ★ 描述里**必须**写明用自然语言提问。实测（docs/kb-design.md §9.3）：同一段
// 内容，自然语句一次就命中，两个关键词两次都错 —— 提问方式对召回质量的影响
// 远大于任何参数调优。而模型默认倾向塞关键词，所以这句提示是功能的一部分，
// 不是文档修辞。
func NewSearchTool(cfg SearchConfig) (tool.InvokableTool, error) {
	if cfg.Retriever == nil {
		return nil, fmt.Errorf("kb: retriever not provided for %s", ToolSearch)
	}
	if cfg.TopK <= 0 {
		cfg.TopK = defaultTopK
	}
	const desc = `Search the project's knowledge base (the documents under its corpus directory) and return the most relevant documents, each as an outline.

Use this to answer questions about how the project is designed, why something is the way it is, or what a document says — especially when you would otherwise have to read many files to find out.

Ask a full natural-language question, the way you would ask a colleague: "why does the config write path refuse to edit the merged view?" rather than "config write merged". Short keyword strings retrieve noticeably worse. Identifiers that must match exactly (a type name, a file name) are the exception — write those verbatim.

Each result carries the source file, its title and summary, its headings with line numbers, and its links to other documents. It is an outline, not the text: to read a section, call read_file with the line number given next to the heading. Results are whole documents, so one hit may cover several of your questions. The score is relative within one call — compare results of the same query, not across queries.`

	return utils.InferTool(ToolSearch, desc,
		func(ctx context.Context, in SearchInput) (string, error) {
			hits, err := Query(ctx, cfg.Retriever, cfg.Instruction, in.Query, pickTopK(in.TopK, cfg.TopK))
			if err != nil {
				return "", err
			}
			return renderHits(hits), nil
		})
}

// ── kb_index ─────────────────────────────────────────────────────────────

// IndexInput 是 kb_index 的入参。
type IndexInput struct {
	Paths []string `json:"paths" jsonschema:"required" jsonschema_description:"Files to re-index, as paths relative to the project root (the same base as the corpus setting), e.g. docs/config-design.md. Must not be empty — a full rebuild is not something this tool does."`
}

// IndexToolConfig 造 kb_index 所需的东西。
type IndexToolConfig struct {
	Store *Store
	// Indexer 是写入侧。它就是 [FileIndexer] —— 流水线本来就与它同层，
	// 隔一个单实现的接口只会多一层看不懂的间接。
	Indexer *FileIndexer
	// Corpus 是语料目录的**绝对**路径。
	Corpus string
	// ProjectRoot 用来把模型给的路径换算成语料相对的。
	ProjectRoot string
	// Headers 是 `kb.splitter.headers`，**v2 起不生效**（§14）。
	//
	// ★ 章节现在按行扫描产出（§6.2），没有任何东西读它。留着只是为了让旧
	//   配置不报错 —— 一个曾经生效的字段突然变成「未知字段」会让一次本来
	//   能跑的索引直接失败，而它其实什么也没做错。`kb status` 会明说这一点。
	Headers map[string]string
}

// NewIndexTool 造 `kb_index`。
//
// ★ `paths` 必填，为空时**不执行**。全量重建要读完整片语料并把每一份都嵌一遍
// （v2 实测：本仓库 11 份 / **约 27 秒**；v1 按块切是 405 块 / 9m49s），而它
// 随语料线性增长。一次几十秒起步的工具调用在 turn 里要么超时、要么被中断，
// 而中断点落在「写了一半」的索引上。CLI 在那，因为人会等、会看进度。
//
// ★ 异步/超时两种替代做法都会让**索引状态在同一会话的不同轮次之间变化** ——
//
//	模型第二轮搜到的和第一轮不一样，而它不知道原因。那比「少一个功能」更难查。
func NewIndexTool(cfg IndexToolConfig) (tool.InvokableTool, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("kb: store not provided for %s", ToolIndex)
	}
	if cfg.Indexer == nil {
		return nil, fmt.Errorf("kb: indexer not provided for %s", ToolIndex)
	}
	if cfg.Corpus == "" {
		return nil, fmt.Errorf("kb: corpus not provided for %s", ToolIndex)
	}
	const desc = `Re-index specific files into the project's knowledge base so that later searches see the current text.

Use this after you change a document that is part of the corpus, then search again. Omit nothing: list the files you changed.

It is incremental: only the files you name are re-read, and a document is re-embedded only when its text actually changed — a plain re-save that does not alter the text costs nothing. A document whose summary was hand-edited in catalog.md is also re-embedded, even if it is not in your list, so that the vectors match what the catalog shows. Passing no paths is refused: a full rebuild reads and re-embeds the entire corpus and does not belong inside a turn (use the ` + "`openheron kb index`" + ` command instead).

The result reports how many files were touched, how many documents were re-embedded, how many were left alone, and how many documents the index now holds. If re-embedded is 0 then nothing was recomputed and there is no reason to call this again.`

	return utils.InferTool(ToolIndex, desc,
		func(ctx context.Context, in IndexInput) (string, error) {
			if len(in.Paths) == 0 {
				return "", fmt.Errorf("paths must not be empty: a full rebuild reads and re-embeds the whole " +
					"corpus and must not run inside an agent turn; run `openheron kb index` instead " +
					"(it prints progress and is resumable)")
			}
			rel, err := CorpusRel(cfg.ProjectRoot, cfg.Corpus, in.Paths)
			if err != nil {
				return "", err
			}
			// ★ **不排队**，与命令行同一把运行锁。撞上正在跑的索引时立刻说清楚，
			//   而不是把一个 turn 挂在那里 —— 一次全量是几十秒起步（随语料线性
			//   增长），而挂到最后大概率是被超时掐掉，那时模型只知道「工具失败了」。
			stop, err := cfg.Store.StartRun()
			if err != nil {
				return "", fmt.Errorf("another indexing run is in progress (%w); "+
					"retry after it finishes, or run `openheron kb status` in the meantime", err)
			}
			defer stop()

			res, err := Index(ctx, cfg.Indexer, IndexOptions{
				Corpus:  cfg.Corpus,
				Headers: cfg.Headers,
			}, rel)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(res)
			if err != nil {
				return "", err
			}
			return string(b), nil
		})
}

// CorpusRel 把**项目根相对**的路径换算成**语料相对**的路径。
//
// ★ 两个入口（本工具与 `openheron kb index`）共用它。各写一遍的话，模型与用户
//
//	会各自面对一套不同的基准，而算错的表现是 `docs/docs/x.md` 不存在 ——
//	实测第一次跑就撞上了。
//
// ★ 语料**外面**的路径必须报错，不能放行。放行的后果不是「多索引一个文件」，
//
//	而是它下一次**全语料**对账会被删除清扫扫掉（清扫按「catalog.md 里有、
//	本轮没出现」判，而它下一次的全量根本不会出现）。于是「索引了却永远留不住」，
//	且没有任何提示。
func CorpusRel(projectRoot, corpus string, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(projectRoot, p)
		}
		abs = filepath.Clean(abs)
		rel, err := filepath.Rel(corpus, abs)
		if err != nil {
			return nil, fmt.Errorf("kb: %s is not under the corpus %s", p, corpus)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("kb: %s is outside the corpus %s; "+
				"only files under the corpus can be indexed", p, corpus)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

func pickTopK(got, def int) int {
	if got > 0 {
		return got
	}
	return def
}

// renderHits 把命中排成给模型读的文本。
//
// ★ 每条都带上**出处**与**章节行号**。没有出处的检索结果模型无法核对，
//
//	用户也无法追溯 —— 而那种结果看上去仍然正常，只是「某处这么说」而已。
//
// ★ 章节行号必须进输出（§14.1）：`read_file` 的参数就是行号，而这里不指到
//
//	位置，模型就得自己把「这一节」翻译成「第几行」—— 那正是我们本该替它
//	做掉的那一步。
func renderHits(hits []Hit) string {
	if len(hits) == 0 {
		return "No matching passages. The corpus may not be indexed yet — see `openheron kb status`."
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n\n")
		}
		where := h.Source
		if h.Title != "" && h.Title != h.Source {
			where += " — " + h.Title
		}
		fmt.Fprintf(&b, "[%d] %s  (score %.3f)\n", i+1, where, h.Score)
		if h.Summary != "" {
			fmt.Fprintf(&b, "%s\n", strings.TrimSpace(h.Summary))
		}
		if h.Stale {
			// ★ **明确说出来**，不要只留一个字段。人改过的摘要在源文件变了
			//   之后可能已经不准了，而模型读到一句过期的摘要时没有任何迹象
			//   提示它 —— 除了这一行。
			b.WriteString("(summary is hand-written and the source file has changed since)\n")
		}
		if len(h.Sections) > 0 {
			b.WriteString("sections: ")
			for j, s := range h.Sections {
				if j > 0 {
					b.WriteString(" · ")
				}
				b.WriteString(s.String())
			}
			if h.SectionsOmitted > 0 {
				fmt.Fprintf(&b, "  [+%d more]", h.SectionsOmitted)
			}
			b.WriteString("\n")
		}
		if h.SectionsTruncated > 0 {
			// ★ 与上面那个 `[+N]` **刻意写成完全不同的样子**（§11.1）：这个是
			//   事故 —— 建索引时就撞了存储硬线，那些章节**在目录里不存在**，
			//   模型照着读永远查不到。用同样的写法会让一条告警看起来像一次
			//   正常的显示截断。
			fmt.Fprintf(&b, "! %d headings were dropped when this document was indexed; "+
				"they are not in the catalog\n", h.SectionsTruncated)
		}
		if len(h.Links) > 0 {
			fmt.Fprintf(&b, "links: %s\n", strings.Join(h.Links, ", "))
		}
	}
	return b.String()
}
