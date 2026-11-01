package spec

import (
	"strings"
	"testing"
)

// The allow-list has to tell three states apart: "not declared" (every
// skill), "declared and empty" (no skill) and "declared" (exactly these).
// A copy that loses the first two apart silently widens the scope back.
func TestTheSkillListIsReadFromTheAgentFile(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", strings.Replace(minimalAgent,
		"model: openai/fast", "model: openai/fast\nskills: [alpha, beta]", 1))

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	list := cfg.Agents["main"].Skills
	if list == nil {
		t.Fatal("the skill list must be read")
	}
	if len(*list) != 2 || (*list)[0] != "alpha" || (*list)[1] != "beta" {
		t.Fatalf("skills = %v, want [alpha beta]", *list)
	}
}

func TestAnAgentWithoutASkillListHasNoList(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents["main"].Skills != nil {
		t.Fatalf("an undeclared list must stay nil, got %v", *cfg.Agents["main"].Skills)
	}
}

func TestAnEmptySkillListIsNotTheSameAsNoList(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", strings.Replace(minimalAgent,
		"model: openai/fast", "model: openai/fast\nskills: []", 1))

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	list := cfg.Agents["main"].Skills
	if list == nil {
		t.Fatal("an empty list must not be read as an undeclared one")
	}
	if len(*list) != 0 {
		t.Fatalf("skills = %v, want an empty list", *list)
	}
}

func TestTheSkillListReachesTheAgentSpec(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", strings.Replace(minimalAgent,
		"model: openai/fast", "model: openai/fast\nskills: [alpha]", 1))

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := cfg.agentSpecOf("main")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Skills == nil || len(*spec.Skills) != 1 || (*spec.Skills)[0] != "alpha" {
		t.Fatalf("the agent spec must carry the skill list, got %v", spec.Skills)
	}
}
