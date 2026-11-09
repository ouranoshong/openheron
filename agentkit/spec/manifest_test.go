package spec

import (
	"strings"
	"testing"
)

// The manifest is the one answer three callers share: /v2/settings,
// `openheron init`, and the config_* tools. This pins it at the layer they
// read, so a change that only keeps the HTTP handler happy still fails here.
func TestManifestDescribesEveryPartitionAndFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "tools/http.yaml", "methods: [get]\n")

	s := f.service(t)
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}

	m, err := s.Manifest(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(m.Categories) != len(Categories) {
		t.Fatalf("categories = %d, want %d", len(m.Categories), len(Categories))
	}
	byID := map[string]ManifestCategory{}
	for _, c := range m.Categories {
		byID[c.ID] = c
	}
	if byID[DirServer].Editable {
		t.Fatal("server/ must be listed as read-only")
	}
	if byID[DirServer].Note == "" {
		t.Fatal("a read-only partition must say why")
	}
	if byID[DirServer].Note != "" && byID[DirTools].Note != "" {
		t.Fatal("an editable partition must not carry the read-only note")
	}

	// Items are the files that exist, and a missing partition yields none
	// rather than an error: a fresh tree has no tools/ files.
	paths := map[string]ManifestItem{}
	for _, it := range m.Items {
		paths[it.Path] = it
	}
	for _, rel := range []string{"llm/openai.yaml", "agents/main.md", "tools/http.yaml"} {
		it, ok := paths[rel]
		if !ok {
			t.Fatalf("the manifest is missing %s: %v", rel, m.Items)
		}
		if !it.Exists || it.Revision == "" {
			t.Fatalf("%s must exist and carry a revision: %+v", rel, it)
		}
		if !it.Hot {
			t.Fatalf("%s is in a hot partition, so Hot must be true", rel)
		}
	}

	// Both kinds of template key are present: the section ("agents") and the
	// per-file entry ("tools/http.yaml").
	if m.Templates[DirAgent] == "" {
		t.Fatal("the manifest must carry a section skeleton for agents")
	}
	if !strings.Contains(m.Templates[DirAgent], "openai/fast") {
		t.Fatalf("the agent skeleton must name the live models: %q", m.Templates[DirAgent])
	}
	if m.Templates["tools/http.yaml"] == "" {
		t.Fatalf("the manifest must carry a per-path template: %v", m.Templates)
	}

	if len(m.ModelRefs) == 0 || m.ModelRefs[0] != "openai/fast" {
		t.Fatalf("modelRefs = %v, want the live models", m.ModelRefs)
	}
	if m.ConfigVersion == "" {
		t.Fatal("the manifest must carry the hot-partition fingerprint")
	}
}

// A process with no configuration loaded yet still gets a manifest: the
// vocabulary is empty, and nothing panics. `openheron init` runs in exactly
// this state — it is the command that creates the tree the manifest
// describes.
func TestManifestWithoutALiveConfig(t *testing.T) {
	f := newFixture(t)
	s := f.service(t)

	m, err := s.Manifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Categories) != len(Categories) {
		t.Fatalf("categories = %d, want %d", len(m.Categories), len(Categories))
	}
	if len(m.ModelRefs) != 0 {
		t.Fatalf("modelRefs = %v, want none", m.ModelRefs)
	}
	if len(m.Items) != 0 {
		t.Fatalf("items = %v, want none in an empty tree", m.Items)
	}
	if m.Templates[DirMCP] == "" {
		t.Fatal("the templates do not depend on the live config, so they must still be there")
	}
}
