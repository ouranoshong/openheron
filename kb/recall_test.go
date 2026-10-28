package kb

import (
	"context"
	"path/filepath"
	"testing"
)

// 这个文件钉住 docs/kb-design.md §13 **M3 的最后一条验收**：v1 附录二最后一行
// 记的那两条召回，在文档级（v2）必须仍然命中。
//
// ★ 为什么它值得一个测试而不是一句「实测通过」：附录二记的是 v1 在**段落级**
//
//	上的命中，而 v2 把单元换成了**源文件**（§4）。这是整个 v2 唯一的**收益侧**
//	风险 —— 代价（丢掉段落精度）已经明确接受了，剩下的问题只是「拿到手的
//	够不够好」。
//
// ★ **这个文件证明不了召回质量**，它证明的是「目标没有从结果里掉出去」。
//
//	能证明召回质量的那次运行用的是真模型，记在 kb-design.md 附录三（两条都
//	第 1 位）。这里跑的是替身 embedder 词袋 —— 它没有语义，拿它断言名次等于
//	拿一个与真实模型无关的东西当判据。**别把这里的绿当成附录三那条的替代品。**
//
// ★ **语料就是本仓库的 `docs/`**，和 [TestExtractAgainstRepoDocs] 一样：
//
//	拿合成语料测召回等于测一个自己造出来的关系，而这两条查询的价值恰恰
//	在于它们是**人真的会问的话**。路径变了一定响亮地失败，不静默跳过 ——
//	静默跳过的集成测试等于不存在。

// recallCases 是附录二最后一行的两条召回用例，逐字照抄（v1 的验收记录）。
//
// ★ **不断言「必须第一」**，只断言「进了结果」+「打分真的发生了」。
//
//	先前这里有一条「标识符型查询在无模型时必须第一」的断言，它删了：那条断言
//	当时成立靠的是语料里恰好没有更长的提纲压过它 —— 往 `kb-design.md` 里加一节
//	（§11.4）就把它翻过来了。翻的理由是设计里**明确接受的取舍**：没有 IDF 意味着
//	常见词不被降权（§8.2），于是「为/什/么/要/给/看」这些字在长提纲里命中越多分
//	越高；而 `internal` / `error` 只出现在 `api-v2-design.md` 的**正文**里，
//	词法这一层根本看不到（§8.1：只比对 title + summary + sections + links）。
//
//	拿语料形状当断言的一部分，就会得到一条「改文档就失效、然后被人 `t.Skip`
//	掉」的用例。这两条的真实排名由附录三那次**真模型**运行给出。
var recallCases = []struct {
	query string
	// want 是目标文档在语料根下的相对路径。断言到**文件**，不断言到
	// `§0 一句话` / `§12.2.2` 这种节：v2 的检索单元是文件，返回的也只可能
	// 是文件 —— 拿节去断言等于断言一件 v1 的产物，而那正是这次要换掉的东西。
	want string
}{
	{query: "配置写入为什么不能写合并视图", want: "config-editor-design.md"},
	{query: "为什么 5xx 要给客户端看 internal error", want: "api-v2-design.md"},
}

func TestRealCorpusRecallMatchesTheV1Record(t *testing.T) {
	corpus := repoCorpusDir(t)
	abs, err := filepath.Abs(corpus)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	// ★ 只用来确认语料还在原地。**不用它当索引入参** —— [Index] 是递归扫的，
	//   只数 `docs/*.md` 会漏掉 `docs/vendor/README.md`（实测语料是 11 份）。
	if paths, err := filepath.Glob(filepath.Join(abs, "*.md")); err != nil {
		t.Fatalf("glob: %v", err)
	} else if len(paths) == 0 {
		t.Fatalf("found no documents under %s; either the corpus moved or "+
			"this test's assumption about the working directory broke", corpus)
	}

	// 两种装配各建一份索引，跑全部查询。
	modes := []struct {
		name string
		emb  *fakeEmbedder
	}{
		// ★ 纯词法：**没有模型**时的召回。v1 做不到这一档 —— 它只有向量，
		//   没有 embedder 就什么都没有。这一档钉的是 v2 新增的那条路真的在
		//   工作，而不是「配了 `Embedding: nil` 却悄悄什么都不查」。
		{name: "lexical", emb: nil},
		// ★ 融合：钉住加了余弦那一半之后，词法的命中**不会被挤掉**，
		//   也不会 panic（替身 embedder 是词袋，不是 Qwen3-Embedding）。
		{name: "fused", emb: newFakeEmbedder(64)},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			st := newTestStore(t)
			ix := newTestIndexer(t, st, mode.emb, 10)
			if _, err := Index(context.Background(), ix, testIndexOpts(abs), nil); err != nil {
				t.Fatalf("Index: %v", err)
			}
			// ★ topK 用**生产实际的条数**（config 的 kb.top_k 默认 8），
			//   不是 kb 包的兜底值 5（那个只在 TopK 为 0 时用，openheron 从不走）。
			//
			//   用 5 等于**比生产更严**：语料从 11 份长到 14 份后，
			//   「配置写入为什么不能写合并视图」的正主 config-editor-design.md
			//   在融合模式下排到第 6（见 kb-design.md §8.2：没有 IDF，余弦那半边
			//   对中文是哈希碰撞的运气），于是被挤出前 5 —— 但生产返回 8 条，
			//   它**在结果里**。
			//
			//   ★ 这不是「放宽断言让测试变绿」：这条用例钉的是「词法命中会不会
			//     被融合挤掉」，而**生产给用户看几条**才是这件事的答案。按 5 断言
			//     是在断言一个生产不存在的更严场景。
			rt := newTestRetriever(t, st, mode.emb, 8)

			for _, c := range recallCases {
				docs, err := rt.Retrieve(context.Background(), c.query)
				if err != nil {
					t.Fatalf("Retrieve(%q): %v", c.query, err)
				}

				// ★ 先把整份排名记下来 —— 失败时没有它，就只有一个
				//   「want 3 got -1」，而排查需要知道**排在目标前面的是谁**。
				ids := idsOf(docs)
				t.Logf("%q → %v", c.query, ids)

				rank := -1
				for i, d := range docs {
					if d.ID == c.want {
						rank = i
						break
					}
				}
				if rank < 0 {
					t.Errorf("%q 里 %s 没有进结果（v1 附录二记它命中）；排名=%v",
						c.query, c.want, ids)
					continue
				}
				if rank == 0 {
					continue
				}
				// ★ 不是第一名时，要求**打分真的发生了**：分词坏掉时全部文档得
				//   0 分，而 0 分之下 TopK 照样返回几条（并列按路径升序），于是
				//   「目标在结果里」**照样通过**。查一嘴分数才分得清「召回到」
				//   与「碰巧排在前几」。
				if docs[0].Score() <= 0 {
					t.Errorf("%q 的全部得分为 0 —— 打分没生效（并列按路径排）：%v",
						c.query, ids)
				}
			}
		})
	}
}
