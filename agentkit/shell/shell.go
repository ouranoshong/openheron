// Package shell decides which program runs a command line.
//
// ★ The decision is made **once**, when the process starts: the tool's
//
//	description tells the model which dialect to write, and the backend
//	runs the command — so the two have to be the same answer, and they
//	have to be the answer the machine gives at startup rather than a
//	guess made per call.
package shell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Shell is the program a command line is handed to.
type Shell struct {
	// Program is the program: "/bin/sh", "cmd.exe" or a path to a POSIX
	// shell such as `C:\msys64\usr\bin\sh.exe`.
	Program string
	// Args are the flags that come before the command: ["-c"] or ["/c"].
	Args []string
	// Windows reports that the program is a **native** Windows shell: a
	// command line for it is not a POSIX one.
	Windows bool
	// Dir is the directory the shell's own utilities live in, when the
	// shell was found outside PATH. It is given to the child process,
	// because a shell that cannot find its own `ls` runs but does
	// nothing useful.
	Dir string
	// WSL reports that the shell is the Windows Subsystem for Linux
	// launcher: a POSIX shell, but one that runs in a Linux filesystem
	// where the Windows drives are mounted under /mnt.
	WSL bool
}

// Command returns the program and its flags, the way the backend wants them.
func (s Shell) Command() []string {
	out := make([]string, 0, len(s.Args)+1)
	out = append(out, s.Program)
	return append(out, s.Args...)
}

// probe is the set of lookups the detection makes.
//
// ★ They are injected so the Windows branches are testable from any
//
//	platform — and so the "does this shell actually run" question can be
//	answered by a stub instead of by a subprocess.
type probe struct {
	look   func(name string) (string, error)
	exists func(path string) bool
	runs   func(program string, args []string) bool
}

// probeTimeout bounds the liveness check.
//
// ★ The check runs a command, so a shell that is slow to start must not
//
//	hold the process hostage: a WSL launcher boots a distribution, and
//	that can take a while on a cold machine.
const probeTimeout = 5 * time.Second

// probeCommand is the trivial command the liveness check runs.
//
// ★ "exit 0" is a shell builtin, so it works in every dialect — and it
//
//	fails loudly when the program is a stub that prints an error instead
//	of running anything.
const probeCommand = "exit 0"

// Detect picks the shell for this machine.
//
// ★ A POSIX shell wins: the model writes POSIX commands, and a POSIX shell
//
//	is what runs them. Windows has one often enough — Git for Windows,
//	MSYS2 and the WSL launcher all count — and when it is there the
//	commands do not change shape between the platforms.
//
// ★ cmd.exe is the fallback, not the default: it is on every Windows box,
//
//	so a tool that runs the other dialect beats a tool that cannot run.
//	The dialect is not a secret: the tool's description says which one it
//	is, so the model writes the matching commands.
func Detect() Shell {
	return detect(runtime.GOOS, probe{look: lookPath, exists: fileExists, runs: runsCommand})
}

// detect is Detect with the lookups injected.
func detect(goos string, p probe) Shell {
	if goos != "windows" {
		return Shell{Program: "/bin/sh", Args: []string{"-c"}}
	}

	// A POSIX shell on PATH: its directory is on PATH too, so its
	// utilities are found without any help.
	if path, err := p.look("sh"); err == nil && path != "" {
		if s, ok := p.try(Shell{Program: path, Args: []string{"-c"}}); ok {
			return s
		}
	}

	// ★ bash on PATH — unless it is the WSL launcher, which is kept for
	//   last: it is a Linux shell on a Linux filesystem, so the drive
	//   paths the other tools use are not the paths it sees.
	var wsl *Shell
	if path, err := p.look("bash"); err == nil && path != "" {
		candidate := Shell{Program: path, Args: []string{"-c"}}
		if isWSLLauncher(path) {
			candidate.WSL = true
			wsl = &candidate
		} else if s, ok := p.try(candidate); ok {
			return s
		}
	}

	// The installations people actually have. Their directory is handed
	// to the child, or the shell starts and then cannot find `ls`.
	for _, path := range windowsPOSIXShells() {
		if !p.exists(path) {
			continue
		}
		candidate := Shell{Program: path, Args: []string{"-c"}, Dir: filepath.Dir(path)}
		if s, ok := p.try(candidate); ok {
			return s
		}
	}

	// The WSL launcher: a real POSIX shell, when the machine has one.
	if wsl != nil {
		if s, ok := p.try(*wsl); ok {
			return s
		}
	}

	return Shell{Program: "cmd.exe", Args: []string{"/c"}, Windows: true}
}

// try reports whether the candidate is a shell that runs.
//
// ★ The liveness check is not decoration: Windows ships `bash.exe` in
//
//	System32 on every machine, and on a machine with no WSL distribution
//	it starts, prints an error and exits non-zero. A candidate that does
//	not run is worse than no candidate — the model would be told it is a
//	POSIX shell and every command would fail.
func (p probe) try(s Shell) (Shell, bool) {
	if !p.runs(s.Program, s.Args) {
		return s, false
	}
	return s, true
}

// windowsPOSIXShells lists where a POSIX shell usually lives on Windows.
//
// ★ Git for Windows and MSYS2 are the two installations that are actually
//
//	around, and both keep their shell under `usr\bin` next to the POSIX
//	utilities. The list is a **fallback**: a shell on PATH is found first,
//	and this only catches the machine whose shell was never put on PATH.
func windowsPOSIXShells() []string {
	var out []string
	if dir := os.Getenv("ProgramFiles"); dir != "" {
		out = append(out, filepath.Join(dir, "Git", "usr", "bin", "bash.exe"))
	}
	if dir := os.Getenv("ProgramFiles(x86)"); dir != "" {
		out = append(out, filepath.Join(dir, "Git", "usr", "bin", "bash.exe"))
	}
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		out = append(out, filepath.Join(dir, "Programs", "Git", "usr", "bin", "bash.exe"))
	}
	// ★ The MSYS2 root is spelled with the separator: `filepath.Join("C:",
	//   "msys64")` yields `C:msys64`, which is **relative to the current
	//   directory of the C: drive** — a path that means something else
	//   entirely depending on where the process was started.
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	out = append(out, filepath.Join(drive+string(os.PathSeparator), "msys64", "usr", "bin", "sh.exe"))
	if home := os.Getenv("USERPROFILE"); home != "" {
		out = append(out, filepath.Join(home, "msys64", "usr", "bin", "sh.exe"))
	}
	return out
}

// isWSLLauncher reports whether the program is the Windows Subsystem for
// Linux launcher.
//
// ★ It is the `bash.exe` that sits in the Windows directory on every
//
//	machine — with or without a distribution installed — and it is not a
//	shell in the ordinary sense: it hands the command line to a Linux
//	distribution, and the drives live under /mnt there.
func isWSLLauncher(path string) bool {
	root := os.Getenv("SystemRoot")
	if root == "" {
		return false
	}
	dir := filepath.Dir(path)
	for _, system := range []string{"System32", "SysWOW64"} {
		if strings.EqualFold(dir, filepath.Join(root, system)) {
			return true
		}
	}
	return false
}

// lookPath finds a program the way the operating system does.
func lookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// fileExists reports whether a path is a file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// runsCommand reports whether the program can run a trivial command.
func runsCommand(program string, args []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, program, append(append([]string{}, args...), probeCommand)...)
	return cmd.Run() == nil
}

// Note is the sentence the model reads: which shell it is writing for.
//
// ★ It belongs in the **tool's description**, not in the agent's prompt:
//
//	the description is the text that sits next to the argument the model
//	fills in, so it is read at the moment the command is written.
func (s Shell) Note() string {
	if s.Windows {
		return "The shell on this machine is " + s.Program + ": a native Windows shell. " +
			"Write Windows commands (dir, type, copy, del, where) and Windows paths " +
			"(backslashes), not POSIX ones (ls, cat, cp, rm, grep)."
	}
	note := "The shell on this machine is " + s.Program + ": a POSIX shell. " +
		"Write POSIX shell commands (ls, cat, cp, mv, grep) and POSIX paths (forward slashes)."
	if s.WSL {
		// ★ The launcher runs in a Linux filesystem: a path that starts
		//   with a drive letter does not exist there, and the model would
		//   otherwise copy one straight out of another tool's answer.
		note += " It runs inside the Windows Subsystem for Linux, where a Windows drive " +
			"is mounted under /mnt: C:\\Users is /mnt/c/Users there."
	}
	return note
}

// String renders the shell for a log line.
func (s Shell) String() string {
	return strings.TrimSpace(s.Program + " " + strings.Join(s.Args, " "))
}
