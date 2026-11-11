package fsx

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TestShellHelperProcess is the program the test below runs **as** a shell.
//
// ★ It is deliberately not a shell: it prints the arguments it was given and
//
//	stops. A real shell would execute the command, and then the output would
//	look the same whichever program was configured — the test would prove
//	nothing. This one only echoes, so the command can be seen arriving.
func TestShellHelperProcess(t *testing.T) {
	if os.Getenv("FSX_TEST_SHELL") != "1" {
		t.Skip("this is the helper program, not a test")
	}
	fmt.Println(strings.Join(os.Args[1:], " "))
	os.Exit(0)
}

// ★ The command must reach the **configured** program: the tool's
//
//	description names one dialect and the backend runs another, and the
//	model is told the wrong thing.
func TestExecuteRunsTheConfiguredProgram(t *testing.T) {
	t.Setenv("FSX_TEST_SHELL", "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{
		Root:  t.TempDir(),
		Shell: []string{self, "-test.run=TestShellHelperProcess"},
	})
	if err != nil {
		t.Fatal(err)
	}

	out := runExecute(t, b, "the-command-marker")
	if !strings.Contains(out, "the-command-marker") {
		t.Fatalf("the configured program never saw the command: %q", out)
	}
}

// Without a configured shell the backend keeps the POSIX default, so a
// caller that says nothing gets the behaviour it had before.
func TestTheDefaultShellIsPOSIX(t *testing.T) {
	b := newTestBackend(t, t.TempDir())
	if got := strings.Join(b.shell, " "); got != "/bin/sh -c" {
		t.Fatalf("shell = %q, want %q", got, "/bin/sh -c")
	}
}

// ★ A shell found outside PATH cannot find its own utilities, so its
//
//	directory goes first in the child's PATH.
func TestTheChildPathStartsWithTheShellDirectory(t *testing.T) {
	dir := t.TempDir()
	b, err := New(Config{Root: t.TempDir(), Shell: []string{"sh", "-c"}, ShellPathDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	cmd, _, _, err := b.initStreamingCmd(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	want := "PATH=" + dir + string(os.PathListSeparator)
	found := false
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the child's PATH must start with %q, got %v", want, cmd.Env)
	}
}

// runExecute runs a command and collects what came back.
func runExecute(t *testing.T, b *Local, command string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sr, err := b.ExecuteStreaming(ctx, &filesystem.ExecuteRequest{Command: command})
	if err != nil {
		t.Fatalf("ExecuteStreaming: %v", err)
	}
	defer sr.Close()

	var sb strings.Builder
	for {
		chunk, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if chunk != nil {
			sb.WriteString(chunk.Output)
		}
	}
	return sb.String()
}
