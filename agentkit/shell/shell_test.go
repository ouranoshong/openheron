package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ★ The decision has several answers, and each one is a different machine:
//   a unix box, a windows box with a POSIX shell on PATH, a windows box
//   with one outside PATH, a windows box whose only POSIX shell is the
//   WSL launcher, and a windows box with none. A test that only covered
//   the happy path would not notice the fallback turning into the
//   default — and the fallback is the one that runs `ls -la` in cmd.exe.

// probeOf builds a probe whose shell lookup, file lookup and liveness check
// are all stubs.
func probeOf(onPath map[string]string, present map[string]bool, runnable map[string]bool) probe {
	return probe{
		look: func(name string) (string, error) {
			if p, ok := onPath[name]; ok {
				return p, nil
			}
			return "", os.ErrNotExist
		},
		exists: func(path string) bool { return present[path] },
		runs:   func(program string, _ []string) bool { return runnable[program] },
	}
}

// A machine that is not Windows has one answer, and it needs no looking.
func TestDetectOnANonWindowsMachineIsPOSIX(t *testing.T) {
	p := probeOf(nil, nil, nil)
	p.look = func(string) (string, error) {
		t.Fatal("a unix machine must not go looking for a windows shell")
		return "", nil
	}
	p.exists = func(string) bool {
		t.Fatal("a unix machine must not go looking for a windows shell")
		return false
	}
	p.runs = func(string, []string) bool {
		t.Fatal("a unix machine must not go looking for a windows shell")
		return false
	}

	sh := detect("linux", p)
	if sh.Program != "/bin/sh" {
		t.Fatalf("program = %q, want /bin/sh", sh.Program)
	}
	if len(sh.Args) != 1 || sh.Args[0] != "-c" {
		t.Fatalf("args = %v, want [-c]", sh.Args)
	}
	if sh.Windows {
		t.Fatal("a unix shell is not a windows shell")
	}
}

// msysShell names the MSYS2 shell inside a temporary drive, so a test can
// use it as an installation that is there without touching the machine.
func msysShell(t *testing.T) string {
	t.Helper()
	drive := t.TempDir()
	t.Setenv("SystemDrive", drive)
	return filepath.Join(drive, "msys64", "usr", "bin", "sh.exe")
}

// ★ A POSIX shell on PATH wins over every known installation path: it is
//
//	the one the user put there, and its directory is on PATH too.
func TestDetectPrefersThePOSIXShellOnPath(t *testing.T) {
	path := `C:\tools\bin\sh.exe`
	sh := detect("windows", probeOf(
		map[string]string{"sh": path},
		map[string]bool{path: true},
		map[string]bool{path: true},
	))

	if sh.Program != path {
		t.Fatalf("program = %q, want the shell on PATH", sh.Program)
	}
	if len(sh.Args) != 1 || sh.Args[0] != "-c" {
		t.Fatalf("args = %v, want [-c]", sh.Args)
	}
	if sh.Windows {
		t.Fatal("a POSIX shell is not a windows shell")
	}
	if sh.Dir != "" {
		t.Fatalf("dir = %q, want empty: the shell's directory is already on PATH", sh.Dir)
	}
}

// ★ A POSIX shell outside PATH is still a POSIX shell — and it has to bring
//
//	its directory along, or it starts and then cannot find `ls`.
func TestDetectFindsAPOSIXShellOutsidePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ProgramFiles", dir)
	want := filepath.Join(dir, "Git", "usr", "bin", "bash.exe")

	sh := detect("windows", probeOf(
		nil,
		map[string]bool{want: true},
		map[string]bool{want: true},
	))

	if sh.Program != want {
		t.Fatalf("program = %q, want %q", sh.Program, want)
	}
	if sh.Windows {
		t.Fatal("a POSIX shell is not a windows shell")
	}
	if sh.Dir != filepath.Dir(want) {
		t.Fatalf("dir = %q, want %q: the shell cannot find its own utilities without it",
			sh.Dir, filepath.Dir(want))
	}
}

// ★ A candidate that does not run is skipped. Windows ships `bash.exe` in
//
//	System32 on every machine, and on a machine with no WSL distribution
//	it starts, prints an error and exits non-zero — a shell that does not
//	run is worse than no shell at all.
func TestACandidateThatDoesNotRunIsSkipped(t *testing.T) {
	dead := `C:\Windows\System32\bash.exe`
	msys := msysShell(t)

	sh := detect("windows", probeOf(
		map[string]string{"bash": dead},
		map[string]bool{dead: true, msys: true},
		map[string]bool{msys: true},
	))

	if sh.Program != msys {
		t.Fatalf("program = %q, want the shell that runs (%q)", sh.Program, msys)
	}
}

// ★ No POSIX shell at all: cmd.exe runs the commands, and the tool's
//
//	description says so, so the model writes Windows commands.
func TestDetectFallsBackToCmdWhenThereIsNoPOSIXShell(t *testing.T) {
	sh := detect("windows", probeOf(nil, nil, nil))

	if sh.Program != "cmd.exe" {
		t.Fatalf("program = %q, want cmd.exe", sh.Program)
	}
	if len(sh.Args) != 1 || sh.Args[0] != "/c" {
		t.Fatalf("args = %v, want [/c]", sh.Args)
	}
	if !sh.Windows {
		t.Fatal("cmd.exe is a native windows shell, and the description must say so")
	}
}

// ★ The WSL launcher is a POSIX shell, but it is the **last** one tried:
//
//	it runs the command in a Linux filesystem, where the drive paths the
//	other tools speak do not exist.
func TestTheWSLLauncherIsTheLastPOSIXChoice(t *testing.T) {
	system := t.TempDir()
	t.Setenv("SystemRoot", system)
	wsl := filepath.Join(system, "System32", "bash.exe")

	msys := msysShell(t)

	sh := detect("windows", probeOf(
		map[string]string{"bash": wsl},
		map[string]bool{wsl: true, msys: true},
		map[string]bool{wsl: true, msys: true},
	))

	if sh.Program != msys {
		t.Fatalf("program = %q, want the shell that shares the filesystem (%q)", sh.Program, msys)
	}
	if sh.WSL {
		t.Fatal("the MSYS2 shell is not the WSL launcher")
	}
}

// When the WSL launcher is the only POSIX shell there is, it is used — and
// the description says where the drives are.
func TestTheWSLLauncherIsUsedWhenThereIsNothingElse(t *testing.T) {
	system := t.TempDir()
	t.Setenv("SystemRoot", system)
	wsl := filepath.Join(system, "System32", "bash.exe")

	sh := detect("windows", probeOf(
		map[string]string{"bash": wsl},
		map[string]bool{wsl: true},
		map[string]bool{wsl: true},
	))

	if sh.Program != wsl {
		t.Fatalf("program = %q, want the WSL launcher", sh.Program)
	}
	if !sh.WSL {
		t.Fatal("the launcher must be marked as WSL")
	}
	if sh.Windows {
		t.Fatal("the WSL launcher is a POSIX shell")
	}
	if !strings.Contains(sh.Note(), "/mnt/c/Users") {
		t.Fatalf("note = %q, want it to say where the drives are", sh.Note())
	}
}

// The description is the only place the dialect can be stated, so it has to
// name the shell and the dialect.
func TestNoteNamesTheDialect(t *testing.T) {
	posix := Shell{Program: "/bin/sh", Args: []string{"-c"}}.Note()
	if !strings.Contains(posix, "/bin/sh") || !strings.Contains(posix, "POSIX") {
		t.Fatalf("note = %q, want it to name the POSIX shell", posix)
	}
	if !strings.Contains(posix, "ls") {
		t.Fatalf("note = %q, want it to name the POSIX commands", posix)
	}

	win := Shell{Program: "cmd.exe", Args: []string{"/c"}, Windows: true}.Note()
	if !strings.Contains(win, "cmd.exe") || !strings.Contains(win, "Windows") {
		t.Fatalf("note = %q, want it to name cmd.exe", win)
	}
	if !strings.Contains(win, "dir") || !strings.Contains(win, "backslashes") {
		t.Fatalf("note = %q, want it to name the windows commands and paths", win)
	}
}

// The backend is given the program and its flags; the log line is one line.
func TestCommandAndString(t *testing.T) {
	sh := Shell{Program: "cmd.exe", Args: []string{"/c"}}
	if got := strings.Join(sh.Command(), " "); got != "cmd.exe /c" {
		t.Fatalf("command = %q, want %q", got, "cmd.exe /c")
	}
	if got := sh.String(); got != "cmd.exe /c" {
		t.Fatalf("string = %q, want %q", got, "cmd.exe /c")
	}
}

// ★ The MSYS2 root is spelled with the separator. `filepath.Join("C:",
//
//	"msys64")` yields `C:msys64` — a path relative to the current
//	directory of the C: drive, which is a different thing entirely.
func TestTheMSYS2PathIsAbsolute(t *testing.T) {
	t.Setenv("SystemDrive", "C:")
	for _, p := range windowsPOSIXShells() {
		if strings.Contains(p, "msys64") {
			if !msysPathIsAbsolute(p) {
				t.Fatalf("path = %q, want an absolute one under the C: drive", p)
			}
			return
		}
	}
	t.Fatal("the list must name the MSYS2 shell")
}

// msysPathIsAbsolute reports whether a path names a place on the C: drive
// rather than a place relative to the current directory of that drive.
//
// ★ The property is **Windows's**, and `filepath.IsAbs` cannot say it on a
//
//	Linux build, where every absolute path starts with a slash. What
//	matters is the drive and the separator right after it: `C:msys64`
//	is relative to the current directory of the drive, and `C:\msys64`
//	— or `C:/msys64` — is not. ★ The separator the code spells it with
//	is the **host's** (the paths are built with filepath.Join), so both
//	spellings have to be read, and a Linux build is covered by the same
//	rule.
func msysPathIsAbsolute(p string) bool {
	rest, ok := strings.CutPrefix(p, "C:")
	if !ok {
		return false
	}
	return rest != "" && (rest[0] == '\\' || rest[0] == '/')
}

// ★ The rule itself, with every spelling the code and the bug produce.
//
//	★ A test that only checked the spelling of the machine it ran on
//	would pass here and fail in CI — or the other way round.
func TestTheMSYS2PathRuleReadsBothSpellings(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{`C:\msys64\usr\bin\sh.exe`, true},
		{"C:/msys64/usr/bin/sh.exe", true},
		{"C:msys64/usr/bin/sh.exe", false},
		{"msys64/usr/bin/sh.exe", false},
		{`C:`, false},
	} {
		if got := msysPathIsAbsolute(tc.path); got != tc.want {
			t.Fatalf("msysPathIsAbsolute(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
