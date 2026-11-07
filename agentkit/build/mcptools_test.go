package build

import (
	"sort"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

func mcpCfg(name string, mut ...func(*decl.MCPServerCfg)) *decl.MCPServerCfg {
	c := &decl.MCPServerCfg{
		Name:      name,
		Transport: decl.MCPTransportStdio,
		Command:   "true",
	}
	for _, m := range mut {
		m(c)
	}
	return c
}

// ── the namespace ───────────────────────────────────────────────────────

func TestAnMCPNameIsTheServersOwnNameUnderTheNamespace(t *testing.T) {
	plan := mcpPlan(mcpCfg("github"), []string{"ls"})
	if len(plan.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(plan.Tools))
	}
	got := plan.Tools[0].Exposed
	if got != "mcp__github__ls" {
		t.Fatalf("exposed name = %q, want mcp__github__ls", got)
	}
}

func TestAnMCPNameReplacesTheCharactersTheModelRefuses(t *testing.T) {
	plan := mcpPlan(mcpCfg("github"), []string{"github.create_issue"})
	exposed := plan.Tools[0].Exposed
	if exposed != "mcp__github__github_create_issue" {
		t.Fatalf("exposed name = %q, want the dot replaced by an underscore", exposed)
	}
	if strings.Contains(exposed, ".") {
		t.Fatalf("exposed name = %q, a dot is not a legal function name", exposed)
	}
}

func TestAnMCPNameKeepsTheServersOwnCase(t *testing.T) {
	plan := mcpPlan(mcpCfg("github"), []string{"CreateIssue"})
	if got := plan.Tools[0].Exposed; got != "mcp__github__CreateIssue" {
		t.Fatalf("exposed name = %q, want the case preserved", got)
	}
}

func TestAnMCPNameAtTheBoundaryIsKept(t *testing.T) {
	// mcp__ + server + __ + tool is exactly 64 characters.
	server := "s"
	tool := strings.Repeat("t", 64-len("mcp__")-len(server)-len("__"))
	plan := mcpPlan(mcpCfg(server), []string{tool})
	if len(plan.Tools) != 1 || plan.Tools[0].Reason != "" {
		t.Fatalf("a name of exactly 64 characters must be kept: %+v", plan.Tools)
	}
}

func TestAnMCPNameOverTheBoundaryIsSkipped(t *testing.T) {
	server := "s"
	tool := strings.Repeat("t", 65-len("mcp__")-len(server)-len("__"))
	plan := mcpPlan(mcpCfg(server), []string{tool})
	if len(plan.Tools) != 1 {
		t.Fatalf("tools = %d, want the skipped one to be reported", len(plan.Tools))
	}
	if plan.Tools[0].Reason != MCPSkipNameTooLong {
		t.Fatalf("reason = %q, want %q", plan.Tools[0].Reason, MCPSkipNameTooLong)
	}
}

func TestTwoToolsThatSanitizeAlikeLeaveTheSecondOneSkipped(t *testing.T) {
	plan := mcpPlan(mcpCfg("github"), []string{"a.b", "a:b"})
	kept, skipped := 0, 0
	for _, tool := range plan.Tools {
		switch tool.Reason {
		case "":
			kept++
		case MCPSkipNameCollision:
			skipped++
		default:
			t.Fatalf("unexpected reason %q", tool.Reason)
		}
	}
	if kept != 1 || skipped != 1 {
		t.Fatalf("kept = %d, skipped = %d, want 1 and 1", kept, skipped)
	}
}

// ── the file's own vocabulary ───────────────────────────────────────────

func TestTheFileAsksForItsOwnToolsAndNotTheExposedOnes(t *testing.T) {
	plan := mcpPlan(mcpCfg("github", func(c *decl.MCPServerCfg) {
		c.Tools = []string{"create_issue"}
	}), []string{"create_issue", "delete_repo"})

	if len(plan.Tools) != 1 {
		t.Fatalf("tools = %d, want only the one the file asks for", len(plan.Tools))
	}
	if plan.Tools[0].Own != "create_issue" {
		t.Fatalf("own name = %q, want create_issue", plan.Tools[0].Own)
	}
}

func TestAnAbsentToolListMeansEveryTool(t *testing.T) {
	plan := mcpPlan(mcpCfg("github"), []string{"a", "b"})
	if len(plan.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(plan.Tools))
	}
}

// ── the tier ────────────────────────────────────────────────────────────

func TestEverythingAsksAndTheFileNamesTheExceptions(t *testing.T) {
	plan := mcpPlan(mcpCfg("github", func(c *decl.MCPServerCfg) {
		c.Allows = []string{"read_issue"}
	}), []string{"read_issue", "delete_repo"})

	tiers := map[string]Tier{}
	for _, tool := range plan.Tools {
		tiers[tool.Own] = tool.Tier
	}
	if tiers["read_issue"] != TierAllow {
		t.Fatalf("read_issue tier = %q, want allow", tiers["read_issue"])
	}
	if tiers["delete_repo"] != TierAsk {
		t.Fatalf("delete_repo tier = %q, want ask", tiers["delete_repo"])
	}
}

func TestAnEmptyAllowsListCoversTheToolsTheServerHasNotOfferedYet(t *testing.T) {
	// The server offers two tools today; the file allows none of them.
	plan := mcpPlan(mcpCfg("github"), []string{"a", "b"})
	for _, tool := range plan.Tools {
		if tool.Tier != TierAsk {
			t.Fatalf("%s tier = %q, want ask", tool.Own, tool.Tier)
		}
	}
	// And a tool the server adds tomorrow asks on its first day: the
	// namespace's default is ask, and the file names nothing.
	p := NewPolicy(allowList(mcpAllowedNames(decl.MCPFiles{"github": mcpCfg("github")})))
	if got := p.TierOf("mcp__github__added_tomorrow"); got != TierAsk {
		t.Fatalf("a tool nobody allows must ask, got %q", got)
	}
}

func TestTheTierIsAnswerableWhileTheServerIsUnreachable(t *testing.T) {
	files := decl.MCPFiles{"github": mcpCfg("github", func(c *decl.MCPServerCfg) {
		c.Allows = []string{"read_issue"}
	})}
	p := NewPolicy(allowList(mcpAllowedNames(files)))

	if got := p.TierOf("mcp__github__read_issue"); got != TierAllow {
		t.Fatalf("tier = %q, want allow", got)
	}
	if got := p.TierOf("mcp__github__delete_repo"); got != TierAsk {
		t.Fatalf("tier = %q, want ask", got)
	}
}

// ★ There is no global deny any more: a tool nobody names asks.
func TestAnMCPNameNobodyNamesAsks(t *testing.T) {
	p := NewPolicy(nil)
	if got := p.TierOf("mcp__github__delete_repo"); got != TierAsk {
		t.Fatalf("tier = %q, want ask", got)
	}
}

// allowList turns a set of names into the exception list.
func allowList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ── the agent's narrowing ───────────────────────────────────────────────

func TestAnAbsentServerListMeansEveryConfiguredServer(t *testing.T) {
	tbl := newTable("main", nil)
	tbl.denyMCPNamespaces(nil, []string{"github", "files"})
	if len(tbl.denyMCP) != 0 {
		t.Fatalf("an absent list must not deny anything, denied %v", tbl.denyMCP)
	}
}

func TestAnEmptyServerListDeniesEveryConfiguredServer(t *testing.T) {
	empty := []string{}
	tbl := newTable("main", nil)
	tbl.denyMCPNamespaces(&empty, []string{"github", "files"})
	if got := tbl.Blocked("mcp__github__read_issue"); got != LayerMCPScope {
		t.Fatalf("blocked = %q, want %q", got, LayerMCPScope)
	}
}

func TestTheServerNarrowingDeniesTheNamespacePrefixOnly(t *testing.T) {
	list := []string{"github_x"}
	tbl := newTable("main", nil)
	tbl.denyMCPNamespaces(&list, []string{"github", "github_x"})

	if got := tbl.Blocked("mcp__github__read_issue"); got != LayerMCPScope {
		t.Fatalf("blocked = %q, want %q", got, LayerMCPScope)
	}
	// ★ A server name may not contain a run of underscores, so the prefix
	//   of one server cannot reach another one.
	if got := tbl.Blocked("mcp__github_x__read_issue"); got != LayerNone {
		t.Fatalf("blocked = %q, want none", got)
	}
}

func TestTheServerNarrowingIsReportedBeforeTheToolRule(t *testing.T) {
	list := []string{"other"}
	tbl := newTable("main", []string{"mcp__github__read_issue"})
	tbl.denyMCPNamespaces(&list, []string{"github"})

	got := tbl.Blocked("mcp__github__read_issue")
	if got != LayerMCPScope {
		t.Fatalf("blocked = %q, want %q — the server narrowing is the coarser one", got, LayerMCPScope)
	}
	if reasonOf(got) != "mcps" {
		t.Fatalf("reason = %q, want mcps", reasonOf(got))
	}
}

// ── the prefix patterns ─────────────────────────────────────────────────

func TestAPrefixRuleReachesTheToolsOfOneServerOnly(t *testing.T) {
	tbl := newTable("main", []string{"mcp__github__*"})
	if got := tbl.Blocked("mcp__github__read_issue"); got != LayerDisable {
		t.Fatalf("blocked = %q, want %q", got, LayerDisable)
	}
	if got := tbl.Blocked("mcp__files__read_file"); got != LayerNone {
		t.Fatalf("blocked = %q, want none", got)
	}
}

func TestAWildcardPrefixReachesEveryMCPTool(t *testing.T) {
	tbl := newTable("main", []string{"mcp__*"})
	if got := tbl.Blocked("mcp__github__read_issue"); got != LayerDisable {
		t.Fatalf("blocked = %q, want %q", got, LayerDisable)
	}
	if got := tbl.Blocked("mcp__files__read_file"); got != LayerDisable {
		t.Fatalf("blocked = %q, want %q", got, LayerDisable)
	}
	if got := tbl.Blocked("execute"); got != LayerNone {
		t.Fatalf("blocked = %q, want none — the pattern is the MCP namespace", got)
	}
}

func TestAnExactEntryStillRemovesTheTool(t *testing.T) {
	tbl := newTable("main", []string{"mcp__github__read_issue"})
	if got := tbl.Blocked("mcp__github__read_issue"); got != LayerDisable {
		t.Fatalf("blocked = %q, want %q", got, LayerDisable)
	}
}
