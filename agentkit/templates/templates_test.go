package templates

import (
	"testing"
)

func TestEveryTemplateIsThere(t *testing.T) {
	for _, rel := range []string{
		"agents/main.md",
		"agents/task.md",
		"agents/agent-expert.md",
		"agents/skill-expert.md",
		"llm/_generic.yaml",
		"llm/deepseek.yaml",
		"llm/openai.yaml",
		"mcps/_example.yaml",
		"mcps/fetch.yaml",
		"mcps/time.yaml",
		"mcps/deepwiki.yaml",
		"server/config.yaml",
		"tools/filesystem.yaml",
		"tools/http.yaml",
		"tools/web_search.yaml",
		"tools/wikipedia.yaml",
		"tools/kb.yaml",
		"skills/openheron-config-map/SKILL.md",
		"skills/openheron-config-edit/SKILL.md",
		"skills/openheron-config-templates/SKILL.md",
		"skills/openheron-config-validate/SKILL.md",
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

// ★ A scaffold is the source of a skeleton, not a file to install: the
//
//	callers of Dir copy what it returns, so a scaffold in the answer would
//	be written into the user's configuration.
func TestDirSkipsTheScaffolds(t *testing.T) {
	out, err := Dir("mcps")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["mcps/_example.yaml"]; ok {
		t.Errorf("the scaffold must not be in the answer: %v", out)
	}
	for _, rel := range []string{"mcps/fetch.yaml", "mcps/time.yaml", "mcps/deepwiki.yaml"} {
		if _, ok := out[rel]; !ok {
			t.Errorf("%s is missing from %v", rel, out)
		}
	}
	// ★ Has is an exact-path question, so the scaffold is found there.
	if !Has("mcps/_example.yaml") {
		t.Error("the scaffold must still be addressable by path")
	}
}

// ★ skills/ is the one **nested** partition: a skill is a folder whose name
//
//	is the skill's name, so the file sits one level deeper. A flat reader
//	cannot see it, and the editor and `openheron init` have to see it the
//	same way.
func TestNestedReturnsTheSkillFolders(t *testing.T) {
	out, err := Nested("skills")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"skills/openheron-config-map/SKILL.md",
		"skills/openheron-config-edit/SKILL.md",
		"skills/openheron-config-templates/SKILL.md",
		"skills/openheron-config-validate/SKILL.md",
	}
	if len(out) != len(want) {
		t.Fatalf("Nested(skills) = %v, want %d entries", out, len(want))
	}
	for _, rel := range want {
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
