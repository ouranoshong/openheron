package kb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// ResultFields 是 openheron 组装检索结果**需要**的字段。
//
// ★ 它是唯一的定处 —— CLI 与 agent 工具各写一遍的话，两边的「出处」字段
//
//	会悄悄分叉，而分叉的表现是「命令行有出处、agent 那边出处是空的」，
//	两边都自洽、无从对证。
//
// ★ 为什么必须显式列出：[RetrieverConfig.ReturnFields] 的语义与 Redis 版
//
//	一致 —— 它**限制**返回哪些字段。File 版其实没有「少读几个字段」的收益
//	（全在内存），但保留这个语义换来的是「换后端时装配层那几行不用改」。
//
// ★ **v2 里不再含 `vector_content`**：没有向量是常态（Embedding 可选），
//
//	而要求一个不存在的字段会把一次能用的检索变成报错。需要向量的地方
//	（`kb status`）直接问 Store，不走这条链路。
var ResultFields = []string{FieldContent, MetaSource, MetaEntry}

// Hit 是一条检索结果，已经是给人/给模型看的形状。
//
// ★ 它带着**完整的 [Entry]**，而不是拆开的几行。理由是章节行号：模型要照着
//
//	`read_file` 的行号去读某一节，而把 sections 摊平成字符串再让输出层拼回来，
//	等于把「行号有没有丢」这件事交给两处各自记得。
//
// ★ 它**不带正文**。v2 的单元是源文件，正文不在目录里（目录只有标题、摘要、
//
//	章节名、出链），而把 `EmbedText` 当正文塞进来会把章节名重复印一遍 ——
//	模型看到同一批标题出现两次，一次带行号一次不带，只会以为那是两样东西。
//	正文由模型照着行号 `read_file` 去读，那也正是行号存在的理由（§14.1）。
type Hit struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	// Title 是文档标题（第一行 `# `）。
	Title string `json:"title,omitempty"`
	// Summary 是摘要。**人可以改它**（§7.2），所以它可能是人写的而不是机器抽的。
	Summary string `json:"summary,omitempty"`
	// Sections 是章节清单，**带行号**（§14.1）。行号是 1-based，指向标题行本身。
	//
	// ★ 它是**展示预算之内的**那些（[maxSectionsShown]），不一定是全部；
	//   被略过的条数在 [Hit.SectionsOmitted] 里。
	Sections []Section `json:"sections,omitempty"`
	// SectionsOmitted 是因为**展示预算**而没列出来的章节数（§11.1）。
	//
	// ★ 它与 [Hit.SectionsTruncated] 是两件事，只是都长成 `[+N]`。区别是
	//   「还有几节我没给你看」与「这份文档的信息在建索引时就丢了」——
	//   后者是事故，前者是常态。
	SectionsOmitted int `json:"sections_omitted,omitempty"`
	// SectionsTruncated 非 0 表示这份文档在建索引时就撞了存储硬线、信息真的
	// 丢了（见 [Entry.SectionsTruncated]）。**非零是告警**，不是正常的截断。
	SectionsTruncated int `json:"sections_truncated,omitempty"`
	// Links 是这份文档的出链（语料内的相对路径）。
	//
	// ★ 它给出「顺着往下读哪几份」，而那是目录相对正文唯一的增量信息 ——
	//   正文要靠 `read_file`，但「该读哪几份」只有出链知道。
	Links []string `json:"links,omitempty"`
	// Stale 表示摘要是人写的、而源文件此后变了（§12.10）。
	Stale bool `json:"stale,omitempty"`
	// Content 是这条命中在索引里的文本（`EmbedText`：标题 + 摘要 + 章节名 +
	// 出链）。**给 `--json` 的调用方用**，工具与人读的渲染不再重复印它 ——
	// 上面那几个字段就是它的展开。
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// instructed 给查询侧加指令前缀。
//
// ★ 它包在**装配层**而不是塞进 FileRetriever：前缀与检索实现无关，Redis 版
//
//	也没有放它的位置。塞进 store 会让「换成 Redis」时这段逻辑留在原地、
//	而两边的行为悄悄分叉。
//
// ★ 只包**查询**侧，不包索引侧。Qwen3-Embedding 这类模型是非对称训练的：
//
//	建索引用原文，查询才加 `Instruct: …\nQuery: …`。加错不会报错，只是
//	召回质量悄悄变差 —— 而那种错最难发现，所以必须有测试钉住它只加一边。
type instructed struct {
	inner  retriever.Retriever
	prefix string
}

// Instruct 用一个指令前缀包住检索器。prefix 为空时原样返回。
func Instruct(inner retriever.Retriever, prefix string) retriever.Retriever {
	if prefix == "" || inner == nil {
		return inner
	}
	return &instructed{inner: inner, prefix: prefix}
}

func (i *instructed) Retrieve(ctx context.Context, query string, opts ...retriever.Option) (
	[]*schema.Document, error) {
	return i.inner.Retrieve(ctx, i.prefix+query, opts...)
}

// Query 检索并转成 [Hit]。
//
// ★ 它是**唯一**的检索入口 —— CLI 与 agent 工具都走这里。各写一遍的话，
//
//	「命令行能召回而 agent 召不回」会变成两个各自自洽的入口，无从对证。
func Query(ctx context.Context, rt retriever.Retriever, instruction, query string, topK int) (
	[]Hit, error) {

	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("kb: query must not be empty")
	}
	docs, err := Instruct(rt, instruction).Retrieve(ctx, query, retriever.WithTopK(topK))
	if err != nil {
		return nil, err
	}
	// 查询切一次，全部文档复用 —— 挑章节要用它。
	terms := parseQuery(query)

	out := make([]Hit, 0, len(docs))
	for _, d := range docs {
		h := Hit{
			ID:      d.ID,
			Source:  metaString(d, MetaSource),
			Content: d.Content,
			Score:   d.Score(),
		}
		// ★ 章节与标题从 [Entry] 取，**不从散落的字符串键取**。理由是行号：
		//   它要原样送到模型面前（§14.1），而拆成散键的话每加一个字段就要
		//   多一处「索引侧写、查询侧读」的配对。
		if e, ok := d.MetaData[MetaEntry].(Entry); ok {
			h.Title = e.Title
			h.Summary = e.Summary
			h.Sections = pickSections(terms, e, maxSectionsShown)
			h.SectionsOmitted = len(e.Sections) - len(h.Sections)
			h.SectionsTruncated = e.SectionsTruncated
			h.Links = e.Links
			h.Stale = e.Stale
			if h.Source == "" {
				h.Source = e.Path
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// maxSectionsShown 是一条命中最多列几节（§11.1）。
//
// ★ 这是**显示预算**，与 [Entry.SectionsTruncated] 那条**存储硬线**（200）
// 不是一回事。两者都长成 `[+N]` 的样子，但一个说「这一条还有几节没给你看」、
// 一个说「这份文档的信息建索引时就丢了」。混在一起的话，看到 `[+18]` 时无法
// 判断是哪种 —— 前者正常，后者是该去查「这份文档为什么长成这样」的事故。
//
// ★ 为什么必须有它：实测本仓库 10 份文档的章节数最多 81 节，而 `top_k` 缺省 8。
// 全列就是每次检索几百行标题，其中绝大多数与问的那件事无关。
const maxSectionsShown = 12

// pickSections 从一份文档的章节里挑出要展示的那些。
//
// ★ 超预算时按**与查询的相关度**挑，不按「前 N 节」：目录里存了全部章节，
// 所以有素材挑；而「前 12 节」在长文档上系统性偏向开头 —— 那恰好是最不需要
// 提示的部分（标题、目标、非目标）。
//
// ★ 挑完**按原顺序**输出。这是提纲，顺序本身就是信息；按分数排会让模型看到
// 一堆与文档结构无关的标题，而它要据此决定去读哪一节。
func pickSections(q queryTerms, e Entry, max int) []Section {
	if len(e.Sections) <= max {
		return e.Sections
	}
	type scored struct {
		i int
		s float64
	}
	all := make([]scored, 0, len(e.Sections))
	for i, s := range e.Sections {
		// ★ 用一个只装这一节的 Entry 去打分，复用同一套符号级加权 —— 另写一份
		//   「章节怎么算相关」的启发式就等于第二个真相。
		all = append(all, scored{i, lexScore(q, Entry{Sections: []Section{s}})})
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].s > all[b].s })
	picked := all[:max]
	sort.Slice(picked, func(a, b int) bool { return picked[a].i < picked[b].i })

	out := make([]Section, 0, max)
	for _, p := range picked {
		out = append(out, e.Sections[p.i])
	}
	return out
}

func metaString(d *schema.Document, key string) string {
	if d.MetaData == nil {
		return ""
	}
	v, ok := d.MetaData[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
