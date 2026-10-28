package kb

import (
	"context"
	"path/filepath"
	"testing"
)

var recallCases = []struct {
	query string
	want  string
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
	if paths, err := filepath.Glob(filepath.Join(abs, "*.md")); err != nil {
		t.Fatalf("glob: %v", err)
	} else if len(paths) == 0 {
		t.Fatalf("found no documents under %s; either the corpus moved or "+
			"this test's assumption about the working directory broke", corpus)
	}

	modes := []struct {
		name string
		emb  *fakeEmbedder
	}{
		{name: "lexical", emb: nil},
		{name: "fused", emb: newFakeEmbedder(64)},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			st := newTestStore(t)
			ix := newTestIndexer(t, st, mode.emb, 10)
			if _, err := Index(context.Background(), ix, testIndexOpts(abs), nil); err != nil {
				t.Fatalf("Index: %v", err)
			}
			rt := newTestRetriever(t, st, mode.emb, 8)

			for _, c := range recallCases {
				docs, err := rt.Retrieve(context.Background(), c.query)
				if err != nil {
					t.Fatalf("Retrieve(%q): %v", c.query, err)
				}

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
					t.Errorf("%q: %s is not in the results (the v1 appendix 2 records it as a hit); rank=%v",
						c.query, c.want, ids)
					continue
				}
				if rank == 0 {
					continue
				}
				if docs[0].Score() <= 0 {
					t.Errorf("%q: every score is 0 — scoring did not take effect (ties are ordered by path): %v",
						c.query, ids)
				}
			}
		})
	}
}
