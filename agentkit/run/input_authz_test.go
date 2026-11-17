package run

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/authz"
	"github.com/ouranoshong/openheron/contract"
)

// ★ The model has to know which level it runs under — not for looks, but so
//
//	that it takes the shorter road: in the middle level it writes the file
//	instead of shelling out to write it.
func TestThePromptNamesTheAuthorizationMode(t *testing.T) {
	c := InputConfig{ProjectRoot: "/default/root"}

	for _, tc := range []struct {
		mode contract.AuthorizationMode
		want string
	}{
		{contract.AuthzDefault, "Approval mode: default"},
		{contract.AuthzTrusted, "Approval mode: trusted"},
		{contract.AuthzFull, "Approval mode: full"},
	} {
		ctx := authz.WithMode(context.Background(), tc.mode)
		in, err := c.buildInput(ctx, inputParams{ProjectRoot: "/work/beta"}, schema.UserMessage("hello"))
		if err != nil {
			t.Fatal(err)
		}
		prompt := in.Messages[0].Content
		if !strings.Contains(prompt, tc.want) {
			t.Fatalf("the prompt must name the mode %q:\n%s", tc.want, prompt)
		}
	}
}

// ★ The default level says its line too. A model that knows "this level
//
//	asks" does not think the tool is broken when it stops.
func TestTheDefaultModeIsNamedToo(t *testing.T) {
	c := InputConfig{ProjectRoot: "/default/root"}

	in, err := c.buildInput(context.Background(), inputParams{}, schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.Messages[0].Content, "Approval mode: default") {
		t.Fatalf("the default mode must be named:\n%s", in.Messages[0].Content)
	}
}

// ★ The line is **environment**, not the question: it sits at the end of
//
//	the `[Context]` block, before the attachments.
func TestTheModeLineSitsInTheContextBlock(t *testing.T) {
	c := InputConfig{ProjectRoot: "/default/root"}

	in, err := c.buildInput(context.Background(), inputParams{}, schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := in.Messages[0].Content
	ctxAt := strings.Index(prompt, "[Context]")
	modeAt := strings.Index(prompt, "Approval mode:")
	if ctxAt < 0 || modeAt < 0 {
		t.Fatalf("the prompt must carry the context block and the mode line:\n%s", prompt)
	}
	if modeAt < ctxAt {
		t.Fatalf("the mode line must sit inside the context block:\n%s", prompt)
	}
}
