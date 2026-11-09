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

const (
	ToolSearch = "kb_search"
	ToolIndex  = "kb_index"
)

// ── kb_search ────────────────────────────────────────────────────────────

type SearchInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"A natural-language question about the project's documentation. Ask a full sentence rather than keywords — retrieval quality depends on it far more than on any tuning."`
	TopK  int    `json:"top_k,omitempty" jsonschema_description:"How many passages to return. Omit for the configured default."`
}

type SearchConfig struct {
	Retriever   retriever.Retriever
	Instruction string
	TopK        int
}

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

type IndexInput struct {
	Paths []string `json:"paths" jsonschema:"required" jsonschema_description:"Files to re-index, as paths relative to the project root (the same base as the corpus_dir setting), e.g. docs/config-design.md. Must not be empty — a full rebuild is not something this tool does."`
}

type IndexToolConfig struct {
	Store       *Store
	Indexer     *FileIndexer
	CorpusDir   string
	ProjectRoot string
	Headers     map[string]string
}

func NewIndexTool(cfg IndexToolConfig) (tool.InvokableTool, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("kb: store not provided for %s", ToolIndex)
	}
	if cfg.Indexer == nil {
		return nil, fmt.Errorf("kb: indexer not provided for %s", ToolIndex)
	}
	if cfg.CorpusDir == "" {
		return nil, fmt.Errorf("kb: corpus directory not provided for %s", ToolIndex)
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
			rel, err := CorpusRel(cfg.ProjectRoot, cfg.CorpusDir, in.Paths)
			if err != nil {
				return "", err
			}
			stop, err := cfg.Store.StartRun()
			if err != nil {
				return "", fmt.Errorf("another indexing run is in progress (%w); "+
					"retry after it finishes, or run `openheron kb status` in the meantime", err)
			}
			defer stop()

			res, err := Index(ctx, cfg.Indexer, IndexOptions{
				CorpusDir: cfg.CorpusDir,
				Headers:   cfg.Headers,
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

// CorpusRel resolves paths against the corpus directory and returns them
// relative to it.
//
// ★ The corpus directory is the **root** the index is allowed to touch: a
//
//	path that escapes it is refused, because indexing it would put
//	documents in the index that the corpus does not contain.
//
// isAbsPath reports whether a path was written as "from the root". On Windows
// filepath.IsAbs("/x") is false because a drive is missing, but a caller that
// wrote the leading separator meant an absolute path.
func isAbsPath(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\")
}

func CorpusRel(projectRoot, corpusDir string, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := p
		// ★ isAbsPath, not filepath.IsAbs: on Windows "/proj/docs/a.md" is
		//   not absolute by the stdlib rule (no drive), yet joining it onto
		//   the project root would put it under the corpus a second time.
		if !isAbsPath(abs) {
			abs = filepath.Join(projectRoot, p)
		}
		abs = filepath.Clean(abs)
		rel, err := filepath.Rel(corpusDir, abs)
		if err != nil {
			return nil, fmt.Errorf("kb: %s is not under the corpus %s", p, corpusDir)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("kb: %s is outside the corpus %s; "+
				"only files under the corpus can be indexed", p, corpusDir)
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
			fmt.Fprintf(&b, "! %d headings were dropped when this document was indexed; "+
				"they are not in the catalog\n", h.SectionsTruncated)
		}
		if len(h.Links) > 0 {
			fmt.Fprintf(&b, "links: %s\n", strings.Join(h.Links, ", "))
		}
	}
	return b.String()
}
