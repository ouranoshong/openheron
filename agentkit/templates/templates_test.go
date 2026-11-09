package templates

import (
	"testing"
)

func TestEveryTemplateIsThere(t *testing.T) {
	for _, rel := range []string{
		"agents/main.md",
		"agents/task.md",
		"llm/_generic.yaml",
		"llm/deepseek.yaml",
		"llm/openai.yaml",
		"mcps/example.yaml",
		"server/config.yaml",
		"tools/filesystem.yaml",
		"tools/http.yaml",
		"tools/web_search.yaml",
		"tools/wikipedia.yaml",
		"tools/kb.yaml",
	} {
		if !Has(rel) {
			t.Errorf("%s is missing", rel)
			continue
		}
		if _, err := File(rel); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
}

func TestDirReturnsThePathsTheCallersUse(t *testing.T) {
	out, err := Dir("tools")
	if err != nil {
		t.Fatal(err)
	}
	// ★ The keys keep the directory: the editor's manifest and the init
	//   plan both address a template by the path it is written to.
	for _, rel := range []string{"tools/http.yaml", "tools/kb.yaml"} {
		if _, ok := out[rel]; !ok {
			t.Errorf("%s is missing from %v", rel, out)
		}
	}
}

func TestRenderExpandsThePlaceholders(t *testing.T) {
	out, err := Render("model: {{model}}\napi_key: {{api_key_env}}\n",
		map[string]string{"model": "deepseek/flash", "api_key_env": "DEEPSEEK_API_KEY"},
		nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "model: deepseek/flash\napi_key: ${DEEPSEEK_API_KEY}\n"
	if out != want {
		t.Fatalf("rendered = %q, want %q", out, want)
	}
}

func TestRenderRefusesAMissingValue(t *testing.T) {
	if _, err := Render("model: {{model}}\n", nil, nil); err == nil {
		t.Fatal("a placeholder with no value must be an error")
	}
}
