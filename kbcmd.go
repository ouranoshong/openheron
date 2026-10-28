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

func assemble(ctx context.Context) (*kbEnv, error) {
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

	rel, err := kb.CorpusRel(env.svc.ProjectRoot(), env.corpus, paths)
	if err != nil {
		return err
	}

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
	fmt.Fprintf(stdout,
		"changed %d / skipped %d / removed %d\nfiles %d, re-embedded %d, unchanged %d, catalog holds %d\ntook %s\n",
		res.Changed, res.Skipped, res.Removed, res.Files, res.ReEmbedded,
		res.Unchanged, res.Total, time.Duration(res.DurationMS)*time.Millisecond)
	if res.Changed == 0 && res.ReEmbedded == 0 {
		fmt.Fprintln(stdout, "\nnothing changed; no embedding requests were made")
	}
	if res.Attempts > 1 {
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
			fmt.Fprintf(stdout, "   ! %d headings were dropped when this document was indexed\n",
				h.SectionsTruncated)
		}
		if len(h.Links) > 0 {
			fmt.Fprintf(stdout, "   links: %s\n", strings.Join(h.Links, ", "))
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

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
