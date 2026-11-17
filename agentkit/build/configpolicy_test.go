package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// ★ The configuration tools ask until their file says otherwise: there is
//
//	no code list that names them any more.
func TestTheConfigToolsAskUntilTheirFileSaysOtherwise(t *testing.T) {
	bare := &spec.Config{}
	if got := policyOf(bare).TierOf(decl.ToolNameConfigGet); got != TierAsk {
		t.Fatalf("without the file %s must ask, got %q", decl.ToolNameConfigGet, got)
	}

	cfg := &spec.Config{Tools: decl.ToolsCfg{
		Config: &decl.ConfigToolsCfg{Allows: []string{decl.ToolNameConfigGet}},
	}}
	if got := policyOf(cfg).TierOf(decl.ToolNameConfigGet); got != TierAllow {
		t.Fatalf("the file names %s, so it must run without a nod, got %q",
			decl.ToolNameConfigGet, got)
	}
	if got := policyOf(cfg).TierOf(decl.ToolNameConfigValidate); got != TierAsk {
		t.Fatalf("the file does not name %s, so it must ask, got %q",
			decl.ToolNameConfigValidate, got)
	}
}

// ★ `config_write` is granted by the file like every other tool of the
//
//	family: the file that says which tools run without a nod says it about
//
//	this one too. ★ The rule that named one tool as the exception made it
//
//	the one that behaved unlike its five siblings, and it was a rule
//
//	somebody had to remember.
func TestTheFileMayGrantConfigWrite(t *testing.T) {
	bare := &spec.Config{}
	if got := policyOf(bare).TierOf(decl.ToolNameConfigWrite); got != TierAsk {
		t.Fatalf("without the file config_write must ask, got %q", got)
	}

	cfg := &spec.Config{Tools: decl.ToolsCfg{
		Config: &decl.ConfigToolsCfg{Allows: []string{decl.ToolNameConfigWrite}},
	}}
	if got := policyOf(cfg).TierOf(decl.ToolNameConfigWrite); got != TierAllow {
		t.Fatalf("the file names config_write, so it must run without a nod, got %q", got)
	}
}

// ★ The limit is the provider's default until the file says otherwise.
func TestTheConfigReadLimitComesFromTheFile(t *testing.T) {
	if got := configReadBytesLimit(nil); got != defaultConfigReadBytes {
		t.Fatalf("without a file the limit must be the default, got %d", got)
	}
	cfg := &spec.Config{Tools: decl.ToolsCfg{
		Config: &decl.ConfigToolsCfg{MaxReadBytes: 4096},
	}}
	if got := configReadBytesLimit(cfg); got != 4096 {
		t.Fatalf("the file's value must win, got %d", got)
	}
}

// configToolBatch builds the tool batch of one tree.
func configToolBatch(t *testing.T, cfg *spec.Config, svc *spec.Service, projectRoot string) map[string]tool.InvokableTool {
	t.Helper()
	batch, err := defaultToolRegistry(nil).Build(context.Background(), toolEnv{
		cfg: cfg, svc: svc, projectRoot: projectRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]tool.InvokableTool{}
	for _, b := range batch {
		it, ok := b.(tool.InvokableTool)
		if !ok {
			t.Fatalf("%T is not invokable", b)
		}
		info, err := it.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out[info.Name] = it
	}
	return out
}

// ★ `config_get` **refuses** a file bigger than the limit: it must not
//
//	return half a file with a matching revision, because that is what turns
//
//	a read into a half-written file.
func TestConfigGetRefusesAFileBiggerThanTheLimit(t *testing.T) {
	svc, _, _ := configToolEnv(t)

	big := "---\nmodel: openai/fast\n---\n" + strings.Repeat("x", 2048) + "\n"
	p := filepath.Join(svc.Root(), spec.DirAgent, "big.md")
	if err := os.WriteFile(p, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Write("tools/config.yaml", []byte("max_read_bytes: 1024\n"), ""); err != nil {
		t.Fatalf("write the limit: %v", err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	tools := configToolBatch(t, cfg, svc, filepath.Dir(svc.Root()))

	_, err = callTool(t, tools, decl.ToolNameConfigGet, `{"path":"agents/big.md"}`)
	if err == nil {
		t.Fatal("a file bigger than the limit must be refused, not truncated")
	}
	if !strings.Contains(err.Error(), "at most 1024") {
		t.Fatalf("the message must carry the limit in force, got %q", err)
	}
}

// ★ The same file is refused because of the **file**, and not because of the
//
//	code: raise the limit and the answer comes back whole.
func TestARaisedLimitLetsTheSameFileThrough(t *testing.T) {
	svc, _, _ := configToolEnv(t)

	big := "---\nmodel: openai/fast\n---\n" + strings.Repeat("x", 2048) + "\n"
	p := filepath.Join(svc.Root(), spec.DirAgent, "big.md")
	if err := os.WriteFile(p, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Write("tools/config.yaml", []byte("max_read_bytes: 8192\n"), ""); err != nil {
		t.Fatalf("write the limit: %v", err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	tools := configToolBatch(t, cfg, svc, filepath.Dir(svc.Root()))

	out, err := callTool(t, tools, decl.ToolNameConfigGet, `{"path":"agents/big.md"}`)
	if err != nil {
		t.Fatalf("a file inside the limit must come back whole: %v", err)
	}
	if !strings.Contains(out, strings.Repeat("x", 2048)) {
		t.Fatal("the answer must hold the whole file")
	}
}
