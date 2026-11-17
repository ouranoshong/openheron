package fsx

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	// ★ The working directory is part of what the command did: a program
	//	that answers "where am I" has to answer with the truth.
	if wd, err := os.Getwd(); err == nil {
		fmt.Println("CWD=" + wd)
	}
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

	out := runExecute(t, b, context.Background(), "the-command-marker")
	if !strings.Contains(out, "the-command-marker") {
		t.Fatalf("the configured program never saw the command: %q", out)
	}
}

// ★ The command runs in **the round's** project root: the directory the
//
//	prompt names and the write boundary uses. ★ The backend is built once
//	and shared by every round, so a working directory baked into it is the
//	root of the last round that used it — and a command that runs
//	somewhere else than the model was told produces output that does not
//	mean what it says.
func TestExecuteRunsInTheRoundsProjectRoot(t *testing.T) {
	t.Setenv("FSX_TEST_SHELL", "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	roundRoot := t.TempDir()
	b, err := New(Config{
		// ★ The backend's own root is a different directory: it is the
		//   fallback for a context that carries no root.
		Root:  t.TempDir(),
		Shell: []string{self, "-test.run=TestShellHelperProcess"},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithProjectRoot(context.Background(), roundRoot)
	out := runExecute(t, b, ctx, "the-command-marker")

	want, err := canonical(roundRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CWD="+want) {
		t.Fatalf("the command must run in the round's project root %q, got %q", want, out)
	}
}

// ★ Without a round root the backend's own root holds — that is what the
//
//	fallback is for.
func TestExecuteWithoutARoundRootRunsInTheBackendsRoot(t *testing.T) {
	t.Setenv("FSX_TEST_SHELL", "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	backendRoot := t.TempDir()
	b, err := New(Config{
		Root:  backendRoot,
		Shell: []string{self, "-test.run=TestShellHelperProcess"},
	})
	if err != nil {
		t.Fatal(err)
	}

	out := runExecute(t, b, context.Background(), "the-command-marker")

	want, err := canonical(backendRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CWD="+want) {
		t.Fatalf("the command must run in the backend's root %q, got %q", want, out)
	}
}

// ★ A directory that does not exist is reported as what it is: the raw
//
//	failure is a chdir error from the operating system, and it names a path
//	without saying why.
func TestExecuteReportsAMissingProjectRoot(t *testing.T) {
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

	missing := filepath.Join(t.TempDir(), "gone")
	ctx := WithProjectRoot(context.Background(), missing)
	_, _, _, err = b.initStreamingCmd(ctx, "the-command-marker")
	if err == nil {
		t.Fatal("a missing project root must be reported")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("the error must say what is wrong, got: %v", err)
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
func runExecute(t *testing.T, b *Local, ctx context.Context, command string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
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
