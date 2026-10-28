package build

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func toolContext(name string) *adk.ToolContext       { return &adk.ToolContext{Name: name} }
func deniedToolContext(name string) *adk.ToolContext { return toolContext(name) }

type stubTool struct {
	name string
}

func (s *stubTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s *stubTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", nil
}

func namesOf(tools []tool.BaseTool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if info, err := t.Info(context.Background()); err == nil {
			out = append(out, info.Name)
		}
	}
	return out
}

func TestPolicyTierOrder(t *testing.T) {
	p := NewPolicy(
		[]string{"write_file", "execute"},
		[]string{"execute"},
	)
	cases := map[string]Tier{
		"read_file":  TierAllow,
		"ls":         TierAllow,
		"write_file": TierAsk,
		"execute":    TierDeny,
	}
	for name, want := range cases {
		if got := p.TierOf(name); got != want {
			t.Errorf("TierOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPolicyFallsBackToAllow(t *testing.T) {
	p := NewPolicy([]string{"write_file"}, nil)
	for _, name := range []string{"brand_new_tool", "shell", "delete_everything"} {
		if got := p.TierOf(name); got != TierAllow {
			t.Errorf("the unlisted %q must fall back to allow, got %q", name, got)
		}
	}
	if got := p.TierOf("write_file"); got != TierAsk {
		t.Errorf("the listed %q must stay ask, got %q", "write_file", got)
	}
}

func TestDenyBeatsDisable(t *testing.T) {
	tbl := newTable("main",
		[]string{"execute"},
		[]string{"execute", "write_file"},
	)
	if got := tbl.Blocked("execute"); got != LayerDeny {
		t.Fatalf("a tool present in both layers must resolve to deny, got %q", got)
	}
	if got := tbl.Blocked("write_file"); got != LayerDisable {
		t.Fatalf("a tool disabled only at the disable layer must report %q, got %q", LayerDisable, got)
	}
	if got := tbl.Blocked("read_file"); got != LayerNone {
		t.Fatalf("an unlisted tool must not be blocked, got %q", got)
	}
}

func TestFilterRemovesByLayer(t *testing.T) {
	tbl := newTable("main",
		[]string{"execute"},
		[]string{"write_file", "write_todos"},
	)
	tools := []tool.BaseTool{
		&stubTool{name: "read_file"},
		&stubTool{name: "execute"},
		&stubTool{name: "write_file"},
		&stubTool{name: "write_todos"},
	}

	kept, removed := tbl.Filter(tools)
	got := strings.Join(namesOf(kept), ",")

	if got != "read_file" {
		t.Fatalf("read_file must be the one kept, got %q", got)
	}
	if removed[LayerDeny] != 1 {
		t.Errorf("the outer layer must remove 1, got %d", removed[LayerDeny])
	}
	if removed[LayerDisable] != 2 {
		t.Errorf("the inner layer must remove 2, got %d", removed[LayerDisable])
	}
}

func TestDeniedToolStaysDeniedForSubAgents(t *testing.T) {
	globalDeny := []string{"execute"}

	parent := newTable("main", globalDeny, nil)
	child := newTable("writer", globalDeny, []string{"write_todos"})

	tools := []tool.BaseTool{&stubTool{name: "execute"}, &stubTool{name: "read_file"}}

	for _, tbl := range []*Table{parent, child} {
		kept, _ := tbl.Filter(tools)
		if names := namesOf(kept); strings.Contains(strings.Join(names, ","), "execute") {
			t.Fatalf("* agent %q regained execute despite the global deny: %v", tbl.Name, names)
		}
	}
}

func TestDisableIsScopedToItsOwnAgent(t *testing.T) {
	writer := newTable("writer", nil, []string{"write_file"})
	reader := newTable("reader", nil, nil)

	tools := []tool.BaseTool{&stubTool{name: "write_file"}}

	if kept, _ := writer.Filter(tools); len(kept) != 0 {
		t.Errorf("the writer must drop write_file, still has %v", namesOf(kept))
	}
	if kept, _ := reader.Filter(tools); len(kept) != 1 {
		t.Errorf("* the reader must be unaffected by the writer's disable, got %v", namesOf(kept))
	}
}

func TestFilterIsNoopWithoutRules(t *testing.T) {
	tbl := newTable("main", nil, nil)
	tools := []tool.BaseTool{&stubTool{name: "read_file"}}
	kept, removed := tbl.Filter(tools)
	if len(kept) != 1 || removed != nil {
		t.Fatalf("with no rules it must return unchanged, got %v / %v", namesOf(kept), removed)
	}
}

func TestFilterKeepsToolsThatCannotReportName(t *testing.T) {
	tbl := newTable("main", []string{"mystery"}, nil)
	kept, _ := tbl.Filter([]tool.BaseTool{&brokenTool{}})
	if len(kept) != 1 {
		t.Fatal("a tool that cannot report its name must be kept (over-keeping beats wrongly stripping)")
	}
}

type brokenTool struct{ stubTool }

func (b *brokenTool) Info(context.Context) (*schema.ToolInfo, error) {
	return nil, errBroken
}

var errBroken = &brokenErr{}

type brokenErr struct{}

func (*brokenErr) Error() string { return "cannot describe itself" }

func TestFilterMiddlewareDropsDeniedToolInBeforeAgent(t *testing.T) {
	tbl := newTable("main", []string{"execute"}, nil)
	mw := newFilterMiddleware(tbl)

	runCtx := &adk.ChatModelAgentContext{
		Tools: []tool.BaseTool{&stubTool{name: "read_file"}, &stubTool{name: "execute"}},
	}
	_, out, err := mw.BeforeAgent(context.Background(), runCtx)
	if err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(namesOf(out.Tools), ","); names != "read_file" {
		t.Fatalf("after BeforeAgent only read_file must remain, got %q", names)
	}
}

func TestApprovalMiddlewareRefusesDeniedToolEvenIfFilterMissed(t *testing.T) {
	p := NewPolicy([]string{"write_file"}, []string{"execute"})
	mw := newApprovalMiddleware(p)

	endpoint := func(context.Context, string, ...tool.Option) (string, error) {
		t.Fatal("★ a denied tool must never reach the endpoint")
		return "", nil
	}
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), endpoint, deniedToolContext("execute"))
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := wrapped(context.Background(), "{}")
	if runErr == nil || !strings.Contains(runErr.Error(), "denied by policy") {
		t.Fatalf("a denied tool must error out immediately, got %v", runErr)
	}
}

func TestApprovalMiddlewareLetsAllowTierThrough(t *testing.T) {
	p := NewPolicy([]string{"write_file"}, nil)
	mw := newApprovalMiddleware(p)

	called := false
	endpoint := func(context.Context, string, ...tool.Option) (string, error) {
		called = true
		return "ok", nil
	}
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), endpoint, toolContext("read_file"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped(context.Background(), "{}"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("a tool on the allow tier must go straight to the endpoint")
	}
}
