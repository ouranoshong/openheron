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
	if len(list) != 2 || list[0] != "alpha" || list[1] != "beta" {
		t.Fatalf("skills = %v, want [alpha beta]", list)
	}
}

// ★ The list is like every other list on an agent: **the default is none**.
//
//	An absent list and an empty list mean the same thing, so there is no
//	pointer and no third state.
func TestAnAgentWithoutASkillListHasNoSkill(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents["main"].Skills) != 0 {
		t.Fatalf("an undeclared list must mean no skill, got %v", cfg.Agents["main"].Skills)
	}
}

func TestAnEmptySkillListMeansNoSkill(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	f.write(t, "agents/main.md", strings.Replace(minimalAgent,
		"model: openai/fast", "model: openai/fast\nskills: []", 1))

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents["main"].Skills) != 0 {
		t.Fatalf("skills = %v, want an empty list", cfg.Agents["main"].Skills)
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
	if len(spec.Skills) != 1 || spec.Skills[0] != "alpha" {
		t.Fatalf("the agent spec must carry the skill list, got %v", spec.Skills)
	}
}
