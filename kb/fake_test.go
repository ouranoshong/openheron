package kb

import (
	"bytes"
	"context"
	"encoding/gob"
	"hash/fnv"
	"math"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
)

type fakeEmbedder struct {
	dim int

	mu    sync.Mutex
	calls [][]string

	failWhen   func(n int) error
	beforeEach func()
}

var _ embedding.Embedder = (*fakeEmbedder)(nil)

func newFakeEmbedder(dim int) *fakeEmbedder { return &fakeEmbedder{dim: dim} }

func (f *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) (
	[][]float64, error) {

	if f.beforeEach != nil {
		f.beforeEach()
	}

	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), texts...))
	fail := f.failWhen
	f.mu.Unlock()

	if fail != nil {
		if err := fail(len(texts)); err != nil {
			return nil, err
		}
	}
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = f.vector(t)
	}
	return out, nil
}

func (f *fakeEmbedder) GetType() string          { return "Fake" }
func (f *fakeEmbedder) IsCallbacksEnabled() bool { return false }

func (f *fakeEmbedder) batches() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.calls))
	for i, c := range f.calls {
		out[i] = len(c)
	}
	return out
}

func (f *fakeEmbedder) embeds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c...)
	}
	return out
}

func (f *fakeEmbedder) vector(text string) []float64 {
	v := make([]float64, f.dim)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[int(h.Sum32()%uint32(f.dim))]++
	}
	var n float64
	for _, x := range v {
		n += x * x
	}
	if n == 0 {
		return v
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] /= n
	}
	return v
}

func newDoc(id, content string, meta map[string]any) *schema.Document {
	d := docOf(Entry{Path: id, Summary: content})
	for k, v := range meta {
		d.MetaData[k] = v
	}
	return d
}

func newDocOf(e Entry) *schema.Document { return docOf(e) }

func seedEntry(t *testing.T, st *Store, e Entry, vec []float32) {
	t.Helper()
	if _, err := st.Reconcile(context.Background(), ReconcileRequest{
		Fresh: []Entry{e},
		Scope: []string{e.Path},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if vec != nil {
		if err := st.SetVectors(map[string][]float32{e.Path: vec}); err != nil {
			t.Fatalf("SetVectors: %v", err)
		}
	}
}

func byModel(t *testing.T, st *Store, model string) {
	t.Helper()
	if err := st.SetIdentity(model, 0); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
}

func writeSidecarWithVersion(path string, ver int) error {
	d := Derived{
		IndexVersion: ver,
		Entries: map[string]DerivedEntry{
			"a.md": {EmbedText: "x", Vector: []float32{1}},
		},
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&d); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
