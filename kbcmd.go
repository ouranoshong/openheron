package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"openheron/agentkit/build"
	"openheron/agentkit/obs"
	"openheron/agentkit/spec"
	"openheron/kb"
)

// kbCmd 是 `openheron kb …` 子命令。
//
// ★ 为什么是 Go 子命令而不是 shell 脚本：bash 要自己读配置、拼端点、
//
//	解析 `${ENV}` / `@file:` 密钥 —— 等于把 Go 里已有的逻辑用 bash 重写
//	一遍，而密钥解析重写一遍必错。要 CI 就加 `make kb-index`，别自己写脚本。
//
// ★ 它只需要 config + embedder + store，**不需要** session manager、
//
//	checkpoint store、hertz、agent builder —— 启动路径比 run() 短得多。
func kbCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: openheron kb index [files...] | openheron kb status")
	}
	switch args[0] {
	case "index":
		rest, asJSON := splitJSONFlag(args[1:])
		return kbIndex(ctx, rest, stdout, asJSON)
	case "status":
		_, asJSON := splitJSONFlag(args[1:])
		return kbStatus(ctx, stdout, asJSON)
	case "search":
		rest, asJSON := splitJSONFlag(args[1:])
		return kbSearch(ctx, rest, stdout, asJSON)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, "usage: openheron kb index [--json] [files...] | openheron kb search <query> | openheron kb status [--json]")
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q; want index or status", args[0])
	}
}

func splitJSONFlag(args []string) (rest []string, asJSON bool) {
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, asJSON
}

// kbEnv 是一次 CLI 调用装配出来的东西。
type kbEnv struct {
	cfg     *spec.Config
	svc     *spec.Service
	store   *kb.Store
	idx     *kb.FileIndexer
	corpus  string
	model   string
	batch   int
	headers map[string]string
}

// assemble 读配置并装配索引的写入侧。
func assemble(ctx context.Context) (*kbEnv, error) {
	// ★ 配置先行，与 run() 同一套解析 —— 命令行与服务进程读到的必须是同一份。
	svc, err := spec.NewService("", "")
	if err != nil {
		return nil, err
	}
	cfg, err := svc.Load()
	if err != nil {
		return nil, err
	}
	for _, d := range cfg.Deferred() {
		obs.Logger().Info("[kb] config notice", "notice", d)
	}
	if cfg.KB.Corpus == "" {
		return nil, fmt.Errorf("kb.corpus is not set in %s/spec.yaml; "+
			"nothing to index (this also means the knowledge base is off)", spec.DirKBIndex)
	}

	ref, _ := spec.ParseRef(cfg.KB.Model)
	p, m, err := build.Resolve(cfg, cfg.KB.Model)
	if err != nil {
		return nil, err
	}
	emb, err := build.NewEmbedder(ctx, p, m)
	if err != nil {
		return nil, err
	}

	stateDir, err := svc.DataDir(cfg.Server.DataDir)
	if err != nil {
		return nil, err
	}
	st, err := kb.NewStore(filepath.Join(stateDir, spec.DirKBIndex, cfg.KB.Index))
	if err != nil {
		return nil, err
	}
	// ★ 身份比对在**建索引之前**：换模型之后旧向量还在库里，而跨模型的检索
	//   给出的是看起来正常的错结果。拦住它比事后解释便宜得多。
	if err := st.SetIdentity(ref.String(), emb.Dimensions()); err != nil {
		return nil, err
	}
	ix, err := kb.NewIndexer(ctx, &kb.IndexerConfig{
		Store:         st,
		Embedding:     emb,
		BatchSize:     cfg.KB.BatchSize,
		MaxBatchChars: cfg.KB.MaxBatchChars,
	})
	if err != nil {
		return nil, err
	}
	return &kbEnv{
		cfg: cfg, svc: svc, store: st, idx: ix,
		corpus:  svc.ResolveDir(cfg.KB.Corpus),
		model:   ref.String(),
		batch:   cfg.KB.BatchSize,
		headers: cfg.KB.Splitter.Headers,
	}, nil
}

func kbIndex(ctx context.Context, paths []string, stdout io.Writer, asJSON bool) error {
	env, err := assemble(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "corpus: %s\nindex:  %s\nmodel:  %s\n\n",
		env.corpus, filepath.Join(env.store.Dir()), env.model)

	// ★ 命令行给的路径按**项目根**解析，与 `kb.corpus` 自己同一个基准 ——
	//   「corpus 相对项目根」而「要索引的路径相对语料」是两套基准，用户
	//   每次都得先换算一遍，而换算错了的表现是 `docs/docs/x.md` 不存在。
	rel, err := kb.CorpusRel(env.svc.ProjectRoot(), env.corpus, paths)
	if err != nil {
		return err
	}

	// ★ **一次索引只跑一个，第二个直接拒绝**，不排队。
	//
	//	排队听起来更友好，实际更糟：一次全量是几十秒起步、随语料线性增长
	//	（v2 实测本仓库 11 份约 27 秒；v1 按块切是 405 块 / 9m49s），而排队期间
	//	用户看到的是一个**没有进度、也没有解释**的静默等待 —— 他没法区分
	//	「在等」和「卡住了」。拒绝则会立刻说出「谁在跑、pid 是多少」，
	//	而人可以决定等、还是去 `kill`。
	//
	//	★ 运行锁罩的是**整次运行**（读语料、抽取、向量化、写目录），不是那一下
	//	  写盘。v2 没有写锁：真正会并发写 `catalog.md` 的是人在编辑器里，而他
	//	  不可能配合锁 —— 那条路靠乐观提交（§7.3），不靠锁。
	stop, lerr := env.store.StartRun()
	if lerr != nil {
		return fmt.Errorf("another index run is in progress (%w); "+
			"wait for it to finish, or check `openheron kb status`", lerr)
	}
	defer stop()

	res, err := kb.Index(ctx, env.idx, kb.IndexOptions{
		Corpus:  env.corpus,
		Headers: env.headers,
	}, rel)
	if err != nil {
		return err
	}

	if asJSON {
		return json.NewEncoder(stdout).Encode(res)
	}
	// ★ 四类分开报，因为它们指向完全不同的处置：改了要重跑、没改什么都别做、
	//   删了说明语料在动、重嵌说明内容真的变了。全并成一句「indexed N files」
	//   的话哪种都看不出来。
	fmt.Fprintf(stdout,
		"changed %d / skipped %d / removed %d\nfiles %d, re-embedded %d, unchanged %d, catalog holds %d\ntook %s\n",
		res.Changed, res.Skipped, res.Removed, res.Files, res.ReEmbedded,
		res.Unchanged, res.Total, time.Duration(res.DurationMS)*time.Millisecond)
	if res.Changed == 0 && res.ReEmbedded == 0 {
		// ★ 两个都要看：Changed 只说「文件被碰过没」，而重嵌只看 EmbedText。
		//   一个被碰过但文字没变的文件会是 changed=1 / re-embedded=0 ——
		//   只按 changed 判断会多说一次「什么都没做」，而那时向量其实没动。
		fmt.Fprintln(stdout, "\nnothing changed; no embedding requests were made")
	}
	if res.Attempts > 1 {
		// ★ 冲突要**说出来**。它发生时不影响结果（重试收敛了），但它是
		//   「有人同时在改 catalog.md」的唯一证据 —— 而那正是乐观提交要防的。
		fmt.Fprintf(stdout, "\ncatalog.md was written %d times this round (concurrent edits merged)\n",
			res.Attempts)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stdout, "\nwarning: %s\n", w)
	}
	return nil
}

func kbSearch(ctx context.Context, args []string, stdout io.Writer, asJSON bool) error {
	query := strings.Join(args, " ")
	if query == "" {
		return fmt.Errorf("usage: openheron kb search <query>")
	}
	svc, err := spec.NewService("", "")
	if err != nil {
		return err
	}
	cfg, err := svc.Load()
	if err != nil {
		return err
	}
	if cfg.KB.Corpus == "" {
		return fmt.Errorf("kb.corpus is not set; the knowledge base is off")
	}
	// ★ 检索**必须**走与 agent 工具同一条 kb.Query。命令行说「能召回」而
	//   agent 那边召不回时，两个入口各自自洽、无从对证。
	env, err := assembleSearch(ctx, svc, cfg)
	if err != nil {
		return err
	}
	hits, err := kb.Query(ctx, env.rt, cfg.KB.QueryInstruction, query, cfg.KB.TopK)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(hits)
	}
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "no hits (is the index built? see `openheron kb status`)")
		return nil
	}
	for i, h := range hits {
		fmt.Fprintf(stdout, "%d. %s\n", i+1, h.Source)
		if h.Title != "" && h.Title != h.Source {
			fmt.Fprintf(stdout, "   %s\n", h.Title)
		}
		fmt.Fprintf(stdout, "   score %.4f\n", h.Score)
		if h.Summary != "" {
			fmt.Fprintf(stdout, "   %s\n", strings.ReplaceAll(h.Summary, "\n", "\n   "))
		}
		if h.Stale {
			fmt.Fprintln(stdout, "   (summary is hand-written and the source file has changed since)")
		}
		if len(h.Sections) > 0 {
			fmt.Fprintf(stdout, "   sections: %s\n", sectionsLine(h))
		}
		if h.SectionsTruncated > 0 {
			// ★ 与 `[+N more]` 刻意写成不同形状：这个是事故（建索引时就丢了），
			//   那个是正常的显示预算。同形会让告警看起来像一次截断。
			fmt.Fprintf(stdout, "   ! %d headings were dropped when this document was indexed\n",
				h.SectionsTruncated)
		}
		if len(h.Links) > 0 {
			fmt.Fprintf(stdout, "   links: %s\n", strings.Join(h.Links, ", "))
		}
		// ★ 不再印 `h.Content`。它就是上面那几个字段的拼接（标题 + 摘要 +
		//   章节名 + 出链），印出来等于让同一批标题出现两次、一次带行号一次
		//   不带 —— 读的人只会以为那是两样东西。
		fmt.Fprintln(stdout)
	}
	return nil
}

// sectionsLine 把章节排成目录里那一行的样子（`名 (L行号) · …`）。
//
// ★ 行号必须进来（§14.1）：命令行这一路人是照着它去 read_file 的。
func sectionsLine(h kb.Hit) string {
	var b strings.Builder
	for i, s := range h.Sections {
		if i > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(s.String())
	}
	if h.SectionsOmitted > 0 {
		fmt.Fprintf(&b, "  [+%d more]", h.SectionsOmitted)
	}
	return b.String()
}

type searchEnv struct {
	store *kb.Store
	rt    *kb.FileRetriever
}

func assembleSearch(ctx context.Context, svc *spec.Service, cfg *spec.Config) (*searchEnv, error) {
	p, m, err := build.Resolve(cfg, cfg.KB.Model)
	if err != nil {
		return nil, err
	}
	emb, err := build.NewEmbedder(ctx, p, m)
	if err != nil {
		return nil, err
	}
	stateDir, err := svc.DataDir(cfg.Server.DataDir)
	if err != nil {
		return nil, err
	}
	st, err := kb.NewStore(filepath.Join(stateDir, spec.DirKBIndex, cfg.KB.Index))
	if err != nil {
		return nil, err
	}
	rt, err := kb.NewRetriever(ctx, &kb.RetrieverConfig{
		Store: st, Embedding: emb, Index: cfg.KB.Index, TopK: cfg.KB.TopK,
		ReturnFields: kb.ResultFields,
	})
	if err != nil {
		return nil, err
	}
	return &searchEnv{store: st, rt: rt}, nil
}

func kbStatus(ctx context.Context, stdout io.Writer, asJSON bool) error {
	// ★ status 不需要 embedder：它只读本地索引，而建一个 embedder 会去
	//   解析模型引用 —— 于是「索引建好了吗」这个问题会在向量化服务没起时
	//   也答不出来，而那恰恰是最需要知道答案的时候。
	svc, err := spec.NewService("", "")
	if err != nil {
		return err
	}
	cfg, err := svc.Load()
	if err != nil {
		return err
	}
	if cfg.KB.Corpus == "" {
		fmt.Fprintln(stdout, "knowledge base is off (kb.corpus is not set)")
		return nil
	}
	stateDir, err := svc.DataDir(cfg.Server.DataDir)
	if err != nil {
		return err
	}
	st, err := kb.NewStore(filepath.Join(stateDir, spec.DirKBIndex, cfg.KB.Index))
	if err != nil {
		return err
	}
	cat := st.Catalog()
	entries := st.Entries()
	der := st.Sidecar()

	// ★ 只读 catalog.md，**不建 embedder** —— 建它会去解析模型引用，于是
	//   「索引建好了吗」这个问题在向量化服务没起时也答不出来，而那恰恰
	//   是最需要知道答案的时候。
	//
	//   侧车是**纯派生**，读它只是为了报「有没有向量」。读不到不算错
	//   （§7.5：丢了自愈）。
	stale, truncated := 0, 0
	sources := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Stale {
			stale++
		}
		if e.SectionsTruncated > 0 {
			truncated++
		}
		sources = append(sources, fmt.Sprintf("%s (%d sections)", e.Path, len(e.Sections)))
	}

	if asJSON {
		return json.NewEncoder(stdout).Encode(struct {
			Corpus     string   `json:"corpus"`
			Index      string   `json:"index"`
			Model      string   `json:"model"`
			Dim        int      `json:"dim"`
			Docs       int      `json:"docs"`
			Sections   int      `json:"sections"`
			Vectors    int      `json:"vectors"`
			Stale      int      `json:"stale"`
			Truncated  int      `json:"truncated"`
			Revision   int64    `json:"revision"`
			IndexVer   int      `json:"index_version"`
			Warnings   []string `json:"warnings,omitempty"`
			Sources    []string `json:"sources"`
			HeadersOff bool     `json:"splitter_headers_ineffective"`
		}{svc.ResolveDir(cfg.KB.Corpus), st.Dir(), der.Model, der.Dim,
			len(entries), countSections(entries), countVectors(der),
			stale, truncated, cat.Header.Revision, cat.Header.IndexVersion,
			cat.Warnings, sources, len(cfg.KB.Splitter.Headers) > 0})
	}

	fmt.Fprintf(stdout, "corpus: %s\nindex:  %s\n", svc.ResolveDir(cfg.KB.Corpus), st.Dir())
	fmt.Fprintf(stdout, "model:  %s\n", orDash(der.Model))
	fmt.Fprintf(stdout, "dim:    %d", der.Dim)
	if der.Dim == 0 {
		fmt.Fprint(stdout, " (none; lexical-only retrieval)")
	}
	fmt.Fprintf(stdout, "\ndocs:  %d, sections: %d, vectors: %d\n",
		len(entries), countSections(entries), countVectors(der))
	fmt.Fprintf(stdout, "rev:   %d, extraction rules v%d\n",
		cat.Header.Revision, cat.Header.IndexVersion)

	for _, s := range sources {
		fmt.Fprintf(stdout, "  %s\n", s)
	}

	// ★ 三条派生信息都只存在于 catalog.md 头部 / 侧车里，别处看不到 ——
	//   不在这里说，它们就成了「看起来在工作、实际什么都没做」的字段。
	fmt.Fprintln(stdout)
	if len(cfg.KB.Splitter.Headers) > 0 {
		fmt.Fprintln(stdout, "config note: splitter.headers is no longer effective "+
			"(sections come from line scanning since v2)")
	}
	if stale > 0 {
		fmt.Fprintf(stdout, "stale: %d doc(s) — the summary is hand-written and the "+
			"source file has changed since (see `stale: true` entries in catalog.md)\n", stale)
	}
	if truncated > 0 {
		fmt.Fprintf(stdout, "truncated: %d doc(s) exceed the %s section cap and are "+
			"incomplete in the catalog\n", truncated, "max")
	}
	for _, w := range cat.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func countSections(entries []kb.Entry) int {
	n := 0
	for _, e := range entries {
		n += len(e.Sections)
	}
	return n
}

func countVectors(d kb.Derived) int {
	n := 0
	for _, e := range d.Entries {
		if len(e.Vector) > 0 {
			n++
		}
	}
	return n
}
