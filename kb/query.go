package kb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

var ResultFields = []string{FieldContent, MetaSource, MetaEntry}

type Hit struct {
	ID                string    `json:"id"`
	Source            string    `json:"source"`
	Title             string    `json:"title,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	Sections          []Section `json:"sections,omitempty"`
	SectionsOmitted   int       `json:"sections_omitted,omitempty"`
	SectionsTruncated int       `json:"sections_truncated,omitempty"`
	Links             []string  `json:"links,omitempty"`
	Stale             bool      `json:"stale,omitempty"`
	Content           string    `json:"content"`
	Score             float64   `json:"score"`
}

type instructed struct {
	inner  retriever.Retriever
	prefix string
}

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

func Query(ctx context.Context, rt retriever.Retriever, instruction, query string, topK int) (
	[]Hit, error) {

	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("kb: query must not be empty")
	}
	docs, err := Instruct(rt, instruction).Retrieve(ctx, query, retriever.WithTopK(topK))
	if err != nil {
		return nil, err
	}
	terms := parseQuery(query)

	out := make([]Hit, 0, len(docs))
	for _, d := range docs {
		h := Hit{
			ID:      d.ID,
			Source:  metaString(d, MetaSource),
			Content: d.Content,
			Score:   d.Score(),
		}
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

const maxSectionsShown = 12

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
