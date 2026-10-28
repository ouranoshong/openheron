package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const reviewWithoutArgs = "Review the current working tree changes. Focus on correctness, error handling, and regressions. Read the files you need before judging."

func TestCommandsManifest(t *testing.T) {
	srv := newV2Server(t)
	got := callJSON(t, srv, "GET", "/v2/commands", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	// Templates are server-side only; the manifest must not leak them.
	if strings.Contains(got.body, "{{args}}") || strings.Contains(got.body, "Review the current working tree") {
		t.Fatalf("template leaked into /v2/commands: %s", got.body)
	}

	var out commandList
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(out.Commands) != len(commandRegistry) {
		t.Fatalf("commands = %d, want %d", len(out.Commands), len(commandRegistry))
	}

	kinds := map[commandKind]int{}
	for _, cmd := range out.Commands {
		kinds[cmd.Kind]++
		switch cmd.Kind {
		case commandKindAction:
			if cmd.Action == "" {
				t.Fatalf("action command %q has no action", cmd.Name)
			}
		case commandKindPrompt:
			if cmd.Action != "" || cmd.ArgumentHint == "" {
				t.Fatalf("prompt command %q = %+v", cmd.Name, cmd)
			}
		default:
			t.Fatalf("unknown kind %q for %q", cmd.Kind, cmd.Name)
		}
	}
	if kinds[commandKindAction] == 0 || kinds[commandKindPrompt] == 0 {
		t.Fatalf("need both action and prompt commands: %v", kinds)
	}
}

func TestExpandCommand(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "prompt with args",
			in:   "/review src/a.go",
			want: reviewWithoutArgs + "\nsrc/a.go",
		},
		{
			name: "prompt name is case insensitive",
			in:   "/Review src/a.go",
			want: reviewWithoutArgs + "\nsrc/a.go",
		},
		{
			name: "prompt without args drops placeholder line",
			in:   "/review",
			want: reviewWithoutArgs,
		},
		{
			name: "absolute path is not a command",
			in:   "/home/user/file.go",
			want: "/home/user/file.go",
		},
		{
			name: "unknown command is left alone",
			in:   "/nope x",
			want: "/nope x",
		},
		{
			name: "bare slash is left alone",
			in:   "/",
			want: "/",
		},
		{
			name:    "action is rejected",
			in:      "/new",
			wantErr: true,
		},
		{
			name:    "action with args is rejected",
			in:      "/new extra",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandCommand(tc.in)
			if tc.wantErr {
				if !errors.Is(err, errCommandNotRunnable) {
					t.Fatalf("err = %v, want errCommandNotRunnable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expandCommand(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestV2RunExpandsPromptCommand(t *testing.T) {
	srv, _ := newV2RunServer(t)
	id := createThread(t, srv)
	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "/review src/a.go",
	})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	assertLastUserText(t, srv, reviewWithoutArgs+"\nsrc/a.go")
}

func TestV2RunRejectsActionCommand(t *testing.T) {
	srv, _ := newV2RunServer(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "/new",
	})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeCommandNotRunnable {
		t.Fatalf("code = %q, want %q", code, codeCommandNotRunnable)
	}
}

func TestV2RunLeavesUnknownSlashAlone(t *testing.T) {
	tests := []string{"/home/user/file.go", "/nope hello"}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			srv, _ := newV2RunServer(t)
			id := createThread(t, srv)
			got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
				"threadId": id,
				"query":    query,
			})
			if got.code != consts.StatusOK {
				t.Fatalf("= %d (%s)", got.code, got.body)
			}
			assertLastUserText(t, srv, query)
		})
	}
}

// ★ /skill <name> [task]：第一个词是 skill 名，其余是任务。
//
//	模板要能分开拿到这两样 —— 粘在一起的话，模板里就会出现
//	「skill `refactor 拆一下`」这种读不通的东西。
func TestExpandSkillCommandSplitsTheName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "name only",
			in:   "/skill refactor",
			want: "Use the `refactor` skill for this task. Read its instructions with the skill tool first, then follow them.",
		},
		{
			name: "name and task",
			in:   "/skill refactor 把这个函数拆开",
			want: "Use the `refactor` skill for this task. Read its instructions with the skill tool first, then follow them.\n把这个函数拆开",
		},
		{
			name: "the command is case insensitive",
			in:   "/Skill refactor 拆一下",
			want: "Use the `refactor` skill for this task. Read its instructions with the skill tool first, then follow them.\n拆一下",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandCommand(tc.in)
			if err != nil {
				t.Fatalf("expandCommand(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("expandCommand(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// 别的命令的 {{args}} 是「命令名之后的全部」—— 不能被 {{name}} 的拆分吃掉。
func TestExpandOtherPromptKeepsAllArgs(t *testing.T) {
	got, err := expandCommand("/review src/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "\nsrc/a.go") {
		t.Fatalf("the args must survive, got %q", got)
	}
}
