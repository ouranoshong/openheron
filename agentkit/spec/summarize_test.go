package spec

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// The threshold is spelled three ways, and each one has to land where it
// should.
func TestTheThresholdIsSpelledThreeWays(t *testing.T) {
	cases := []struct {
		raw     string
		percent int
		tokens  int
		off     bool
	}{
		{raw: "104858", tokens: 104858},
		{raw: "80%", percent: 80},
		{raw: "0", off: true},
		{raw: "off", off: true},
		{raw: "never", off: true},
	}
	for _, c := range cases {
		var t1 decl.Threshold
		if err := yaml.Unmarshal([]byte(c.raw), &t1); err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if !t1.Declared {
			t.Fatalf("%s: the threshold must count as declared", c.raw)
		}
		if t1.Percent != c.percent || t1.Tokens != c.tokens || t1.Off != c.off {
			t.Fatalf("%s: got percent=%d tokens=%d off=%v, want %d/%d/%v",
				c.raw, t1.Percent, t1.Tokens, t1.Off, c.percent, c.tokens, c.off)
		}
	}
}

// A threshold that makes no sense is a startup error, not a shrug.
func TestAnImpossibleThresholdIsRejected(t *testing.T) {
	for _, raw := range []string{"120%", "0%", "-1%", "soon", "[]"} {
		var t1 decl.Threshold
		err := yaml.Unmarshal([]byte(raw), &t1)
		if err == nil {
			t.Fatalf("%s must be rejected", raw)
		}
	}
}

// The resolution table: a percentage of the window, an absolute count, off,
// and the agent that says nothing.
func TestSummarizeAtResolvesAgainstTheWindow(t *testing.T) {
	cases := []struct {
		name   string
		th     decl.Threshold
		window int
		want   int
	}{
		{name: "a percentage of the window", th: decl.Threshold{Percent: 80}, window: 100000, want: 80000},
		{name: "an absolute count", th: decl.Threshold{Tokens: 4200}, window: 100000, want: 4200},
		{name: "off", th: decl.Threshold{Off: true}, window: 100000, want: 0},
		{name: "nothing declared", th: decl.Threshold{}, window: 100000, want: 80000},
		{name: "a percentage without a window", th: decl.Threshold{Percent: 80}, window: 0, want: 0},
		{name: "an absolute count without a window", th: decl.Threshold{Tokens: 4200}, window: 0, want: 4200},
	}
	for _, c := range cases {
		if got := SummarizeAt(c.th, c.window); got != c.want {
			t.Fatalf("%s: threshold = %d, want %d", c.name, got, c.want)
		}
	}
}

// The threshold belongs to the agent: the agent's own file carries it, and
// the model profile does not.
func TestTheThresholdIsReadFromTheAgentFile(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
    context_window: 100000
`)
	f.write(t, "agents/main.md", `
---
model: openai/fast
summarize_at: 80%
---
你是助手。
`)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	got := SummarizeAt(cfg.Agents["main"].SummarizeAt, 100000)
	if got != 80000 {
		t.Fatalf("threshold = %d, want 80000", got)
	}
}

// An old configuration keeps loading: the threshold that used to live in
// the model profile is still parsed, and the user is told where it moved.
func TestTheOldModelThresholdIsReportedNotFatal(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
    context_window: 100000
    summarize_at: 80000
`)
	f.write(t, "agents/main.md", minimalAgent)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "summarize_at") && strings.Contains(d, "agents") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the moved threshold must be reported, got %v", cfg.Deferred())
	}
}

// A model without a window leaves the agent without a threshold, and the
// user is told: silently never compacting is the worst of the outcomes.
func TestAnAgentWithoutAWindowIsReported(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
`)
	f.write(t, "agents/main.md", minimalAgent)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "context_window") && strings.Contains(d, "compaction") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the missing window must be reported, got %v", cfg.Deferred())
	}
}

// An absolute token count needs no window, so there is nothing to report.
func TestAnAbsoluteThresholdNeedsNoWindow(t *testing.T) {
	f := newFixture(t)
	f.write(t, "llm/openai.yaml", `
type: openai_compat
base_url: https://api.example.com
api_key: ${TEST_LLM_KEY}
models:
  - id: fast
`)
	f.write(t, "agents/main.md", `
---
model: openai/fast
summarize_at: 80000
---
你是助手。
`)

	cfg, err := f.load(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range cfg.Deferred() {
		if strings.Contains(d, "context_window") {
			t.Fatalf("an absolute threshold needs no window, got %v", d)
		}
	}
}
