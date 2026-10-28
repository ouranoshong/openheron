package kb

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// 元数据字段名。写死成常量而不是散落字面量 —— 检索侧要按它们过滤与呈现，
// 而「索引侧写一个名字、查询侧读另一个」这种错位不报错，只是永远过滤不到。
const (
	// MetaSource 是相对语料根的路径。
	MetaSource = "source"
	// MetaEntry 是抽好的 [Entry] 本身。
	//
	// ★ 它是一个**结构体**而不是一串字符串键。理由是行号：章节行号要原样
	//   送到工具输出里（§14.1），而拆成散字段的话每加一个字段就要多一处
	//   「索引侧写、查询侧读」的配对 —— 那类错位不报错。
	MetaEntry = "kb_entry"
)

// corpusExt 是纳入索引的扩展名。
//
// ★ **只有 markdown**（§14）。v2 不再切块，所以这条不再是「切分器只有
//
//	markdown 这一种」，而是「目录的抽取规则（标题、摘要、章节、链接）
//	只对 markdown 有意义」。
var corpusExt = ".md"

// IndexOptions 是索引流水线的一次输入。
type IndexOptions struct {
	// Corpus 是语料目录的**绝对**路径。调用方按项目根解析好再传进来 ——
	// 这一层不做相对/绝对的判断，因为那是 config 的 ResolveDir 的职责，
	// 而它有一条硬规则（拒绝绝对路径）不该在这里被绕开。
	Corpus string

	// Headers 是「标题符号 → 元数据字段名」。
	//
	// ★ **v2 起不生效，但仍接受**（§14）。它曾经交给 markdown header
	//   splitter 用；v2 不再切块，于是没有任何东西读它。
	//
	//   保留它是为了让升级不报错 —— 一个旧配置突然变成「未知字段」会让
	//   一次本来能跑的索引直接失败，而它其实什么也没做错。`kb status`
	//   会报「已不生效」，v3 删除。
	Headers map[string]string
}

// Result 是一次索引的结果。
//
// ★ Changed / ReEmbedded / Removed 必须分开报，而不只是给一个总数。调用方
//
//	（agent 工具）要靠 `Changed == 0 && ReEmbedded == 0` 判断「没有变，别再
//	调了」—— 只给「indexed N files」的话，模型无法区分「N 个都变了」与
//	「N 个都没变」，于是会在同一轮里反复调。
type Result struct {
	// Files 是本轮参与索引的文件数（扫到的全部，含没变的）。
	Files int
	// Changed 是 (mtime, size) 变了的文件数。
	//
	// ★ 它是**报告用**的，不参与任何判定（见 [Index] 头）。所以它非零时
	//   ReEmbedded 仍可能是 0 —— 那表示「文件被碰过，但文字一个没变」。
	Changed int
	// Skipped 是没变的文件数。
	Skipped int
	// Removed 是语料里已删除、因而从目录里清掉的文件数。
	Removed int
	// ReEmbedded 是**内容**变了因而重嵌了向量的文档数。
	//
	// ★ 它与 Changed 不是一回事：改了空格、或只改了人维护的摘要时 mtime 会变，
	//   而后者会让 EmbedText 变、前者不会。把两者报成一个数会让「索引花了 12
	//   秒」与「索引什么也没做」看起来一样。
	ReEmbedded int
	// Unchanged 是 EmbedText 没变、因而跳过重嵌的文档数。
	Unchanged int
	// Attempts 是写 catalog.md 的次数。>1 说明真的撞上了并发。
	Attempts int
	// Total 是目录里的总文档数。
	Total int
	// Warnings 是解析 catalog.md 时记下的宽容性告警。
	Warnings []string
	// DurationMS 是本轮耗时。
	DurationMS int64
}

// Index 把语料目录里的文档索引进 ix。
//
// paths 非空时只处理这几个文件（相对语料根）；为空则处理全部。
//
// ★ **每个扫到的文件都会进 Fresh，无论变没变**。这是必须的：全语料对账
//
//	（`Scope` 为空）把「在目录里、不在 Fresh 里」一律解释成**源文件被删了**，
//	于是跳过未变文件的写法会让第二次全量索引把整份目录清空 —— 而症状是
//	`changed 0 / skipped 10` 看起来一切正常，只有 `Total` 悄悄归零。
//	省下来的只是一次读（几 KB），换的是上面那个静默清空。
//
// ★ 于是 (mtime, size) 只剩**报告**这一个用途：`Changed` 告诉人「哪些文件被
//
//	碰过」。真正决定要不要重嵌的是 `EmbedText` 比对（见 [applyFresh]），
//	所以一个被碰过但文字没变的文件会 `Changed=1, ReEmbedded=0` —— 那是准确的。
//
// ★ 它取 *FileIndexer 而不是一个接口，也**不另收一个 *Store**：那就是 ix
//
//	自己的那一份，传两遍只会让人以为它们可以不同。
func Index(ctx context.Context, ix *FileIndexer, opts IndexOptions, paths []string) (
	*Result, error) {

	started := time.Now()
	res := &Result{}
	st := ix.StoreRef()

	prev := st.Entries()
	prevFiles := make(map[string]Entry, len(prev))
	for _, e := range prev {
		prevFiles[e.Path] = e
	}

	targets, cerr := collect(opts.Corpus, paths)
	if cerr != nil {
		return nil, cerr
	}

	fresh := make([]Entry, 0, len(targets))
	for _, t := range targets {
		rel := relPath(opts.Corpus, t.path)

		if old, ok := prevFiles[rel]; ok && old.MTime == t.mtime.UnixNano() && old.Size == t.size {
			res.Skipped++
		} else {
			res.Changed++
		}

		// ★ 读盘 + 抽取**不持锁**。它可能跑几秒，而期间本进程没有可写状态 ——
		//   把锁按这么久等于让另一个进程的一次增量索引白等。
		entry, err := extractFile(rel, t)
		if err != nil {
			return nil, err
		}
		fresh = append(fresh, entry)
	}
	res.Files = len(fresh)

	// ★ **一批全部抽完，再一次 Reconcile**。分批的话每批都要写一次
	//   catalog.md，而写盘是乐观提交里唯一可能冲突的一步 —— 把 10 次写
	//   变成 1 次，冲突面就缩到十分之一。
	//
	//   Scope 为空 = 全语料 = 「不在 Fresh 里的就没了」；带 paths 只动这几个。
	res2, err := ix.Reconcile(ctx, ReconcileRequest{
		Corpus: corpusName(opts.Corpus),
		Fresh:  fresh,
		Scope:  paths,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("kb: after %d files this round: %w", res.Changed, err)
	}
	if res2 != nil {
		res.ReEmbedded = res2.ReEmbedded
		res.Unchanged = res2.Unchanged
		res.Attempts = res2.Attempts
		res.Removed = res2.Removed
		res.Warnings = res2.Warnings
	}

	res.Total = len(st.Entries())
	res.DurationMS = time.Since(started).Milliseconds()
	return res, nil
}

// corpusName 从绝对路径里取一个**展示名**（通常是最后一个目录名）。
//
// ★ 它只进 catalog.md 的头部给人辨认，**不参与任何路径计算** —— 条目路径
//
//	一律相对语料根（§6）。所以这里猜一个名字猜错也不要紧。
func corpusName(abs string) string {
	clean := strings.TrimRight(filepath.ToSlash(abs), "/")
	if i := strings.LastIndex(clean, "/"); i >= 0 {
		return clean[i+1:]
	}
	return clean
}

// extractFile 读一个文件并抽成 [Entry]。
func extractFile(rel string, t corpusFile) (Entry, error) {
	body, err := os.ReadFile(t.path)
	if err != nil {
		return Entry{}, fmt.Errorf("kb: read %s: %w", rel, err)
	}
	e := Extract(rel, string(body))
	// ★ MTime / Size 由**扫描时的 stat** 决定，不从 body 推 —— 抽取是纯函数，
	//   而 stat 是这次扫描的观测值。用文件里的字节反推 mtime 是做不到的。
	e.MTime, e.Size = t.mtime.UnixNano(), t.size
	return e, nil
}

// docOf 把 [Entry] 包成一个 eino 文档。
//
// ★ **一个源文件 = 一个 schema.Document**（§14）。doc.Content 是
//
//	[Entry.EmbedText]（被向量化的那段），而完整的 Entry 挂在 MetaData 上 ——
//	后者带着章节行号，那部分无法从 Content 重建。
func docOf(e Entry) *schema.Document {
	return &schema.Document{
		ID:      e.Path,
		Content: e.EmbedText(),
		MetaData: map[string]any{
			MetaEntry:  e,
			MetaSource: e.Path,
		},
	}
}

type corpusFile struct {
	path  string
	mtime time.Time
	size  int64
}

// collect 列出要索引的文件。
func collect(corpus string, paths []string) ([]corpusFile, error) {
	if corpus == "" {
		return nil, fmt.Errorf("kb: corpus is empty")
	}
	info, err := os.Stat(corpus)
	if err != nil {
		// ★ 语料目录不存在**不是错误**：那是「配了但还没建索引」，
		//   与「配错了」是不同的两种状态。前者应该给一个空索引 + 一句话。
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kb: stat corpus %s: %w", corpus, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("kb: corpus %s is not a directory", corpus)
	}

	if len(paths) > 0 {
		out := make([]corpusFile, 0, len(paths))
		for _, p := range paths {
			abs := filepath.Join(corpus, filepath.Clean("/"+p))
			fi, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("kb: stat %s: %w", p, err)
			}
			out = append(out, corpusFile{path: abs, mtime: fi.ModTime(), size: fi.Size()})
		}
		return out, nil
	}

	var out []corpusFile
	err = filepath.WalkDir(corpus, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// ★ 跳过点目录：.git 里可能有几十万文件，而它们不是语料。
			if p != corpus && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != corpusExt {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, corpusFile{path: p, mtime: fi.ModTime(), size: fi.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("kb: walk corpus %s: %w", corpus, err)
	}
	// 稳定顺序 ⇒ 同样的语料每次抽出来同样的顺序，便于比对。
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func relPath(corpus, p string) string {
	r, err := filepath.Rel(corpus, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}
