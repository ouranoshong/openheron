// Package dirpick opens the operating system's own folder chooser.
//
// ★ **Why the server does this job.** A web page can open a directory picker,
//
//	but the browser will not tell it where the chosen directory lives: the
//	File API hands the page the **name** of the folder and the paths inside
//	it, and the File System Access API hands it a handle with a name and
//	nothing else. ★ The absolute path is the one thing the page cannot get,
//	and the absolute path is the one thing a project root has to be.
//
// ★ openheron runs on the user's machine, so the machine is asked: the server
//
//	opens the system's own chooser — the same window a person would open by
//	hand — and the path comes back whole.
//
// ★ It is a **leaf**: it knows about processes and about three operating
//
//	systems, and it knows nothing about openheron.
package dirpick

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

// ErrCancelled says the chooser was closed without a directory.
//
// ★ It is not a failure: closing a chooser is how a person says "not now".
var ErrCancelled = errors.New("no directory was chosen")

// ErrUnavailable says this machine has no chooser to open.
//
// ★ A headless machine — a container, a service account — has no desktop to
//
//	put a window on. That is a fact about the machine, not a bug in it.
var ErrUnavailable = errors.New("this machine has no folder chooser")

// Timeout bounds how long the chooser may stay open.
//
// ★ The chooser waits for a human, so the bound is generous. ★ It is there
//
//	because a request that never ends is worse than one that ends with a
//	reason.
const Timeout = 5 * time.Minute

// startEnv is the environment variable that carries the directory the
// chooser opens at.
//
// ★ The start travels in the environment rather than in the command line
//
//	because PowerShell re-parses the rest of a `-Command` line: a path with
//	a space in it would arrive as two arguments.
const startEnv = "OPENHERON_DIRPICK_START"

// spec is one way to open the chooser.
type spec struct {
	program string
	args    []string
	// env is added to the child's environment.
	env []string
	// cancelCode is the exit status that means "the user closed the
	// chooser". ★ Zero means the chooser exits 0 and says nothing instead.
	cancelCode int
}

// outcome is what a chooser run produced.
type outcome struct {
	stdout string
	stderr string
	code   int
}

// runner opens the chooser and reports what it produced.
//
// ★ It is a seam: the tests replace it, so the three platforms' contracts
//
//	can be checked without a desktop.
type runner func(ctx context.Context, sp spec) (outcome, error)

// Pick opens the operating system's folder chooser and returns the chosen
// directory.
//
// ★ start is where the chooser opens; an empty start means the user's home
//
//	directory. ★ A start that is not there is not a start: the chooser opens
//	at the home directory instead, because a chooser that opens nowhere is
//	worse than one that opens somewhere.
func Pick(ctx context.Context, start string) (string, error) {
	return pick(ctx, runtime.GOOS, start, execRunner, exec.LookPath)
}

// pick is Pick with the platform and the two seams injected.
func pick(ctx context.Context, goos, start string, run runner,
	lookPath func(string) (string, error)) (string, error) {
	start = usableStart(start)
	sp, err := choose(goos, start, lookPath)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	out, err := run(ctx, sp)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("the folder chooser stayed open and was given up on")
		}
		return "", err
	}
	// ★ The byte-order mark is stripped: one of the ways of asking for
	//   UTF-8 on Windows emits it, and a path that starts with three
	//   invisible bytes is a path that does not exist.
	path := strings.TrimSpace(strings.TrimPrefix(out.stdout, "\ufeff"))
	switch {
	case out.code == 0 && path != "":
		return filepath.Clean(path), nil
	case out.code == 0:
		// ★ The chooser says "nothing" by saying nothing: the Windows and
		//   the macOS shape.
		return "", ErrCancelled
	case sp.cancelCode != 0 && out.code == sp.cancelCode && strings.TrimSpace(out.stderr) == "":
		return "", ErrCancelled
	}
	return "", fmt.Errorf("the folder chooser failed: %s", reason(out))
}

// usableStart keeps a start that is a directory and replaces the rest with
// the home directory.
func usableStart(start string) string {
	start = strings.TrimSpace(start)
	if start != "" {
		if info, err := os.Stat(start); err == nil && info.IsDir() {
			return start
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return start
}

// choose picks the chooser for a platform.
//
// ★ The candidates are tried in order, and the first one that is installed
//
//	wins: a machine with both zenity and kdialog gets zenity, and a machine
//	with neither is told so plainly.
func choose(goos, start string, lookPath func(string) (string, error)) (spec, error) {
	candidates := candidatesFor(goos, start)
	if goos == "windows" {
		// ★ The absolute path is the fallback: a service account's PATH is
		//   often shorter than the one a person has.
		if root := os.Getenv("SystemRoot"); root != "" {
			candidates = append(candidates, spec{
				program: filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
				args:    windowsArgs(start),
				env:     []string{startEnv + "=" + start},
			})
		}
	}
	for _, c := range candidates {
		if _, err := lookPath(c.program); err != nil {
			continue
		}
		return c, nil
	}
	return spec{}, ErrUnavailable
}

// candidatesFor is the choosers of a platform, in the order they are tried.
func candidatesFor(goos, start string) []spec {
	switch goos {
	case "windows":
		return []spec{{
			program: "powershell.exe",
			args:    windowsArgs(start),
			env:     []string{startEnv + "=" + start},
		}}
	case "darwin":
		return []spec{{
			program: "osascript",
			args:    darwinArgs(start),
		}}
	default:
		return linuxCandidates(start)
	}
}

// linuxCandidates is the two choosers a Linux desktop may have.
func linuxCandidates(start string) []spec {
	return []spec{
		{
			program:    "zenity",
			args:       []string{"--file-selection", "--directory", "--title=Choose the project directory", "--filename=" + start},
			cancelCode: 1,
		},
		{
			program:    "kdialog",
			args:       []string{"--getexistingdirectory", start, "--title", "Choose the project directory"},
			cancelCode: 1,
		},
	}
}

// windowsScript is the PowerShell program that opens the chooser.
//
// ★ Three things in it are deliberate:
//   - the **output encoding** is UTF-8 without a byte-order mark, because a
//     path may hold any character the user's keyboard can produce — a home
//     directory with a Chinese name is the ordinary case, not the exotic
//     one;
//   - the script **always ends with exit status 0**, so "the chooser was
//     closed" is told by an empty answer rather than by a failure;
//   - the start directory comes from the **environment**, so a path with a
//     quote or a space in it cannot become code.
const windowsScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = 'Choose the project directory'
$dialog.ShowNewFolderButton = $true
$start = $env:OPENHERON_DIRPICK_START
if ($start -and (Test-Path -LiteralPath $start)) { $dialog.SelectedPath = $start }
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($dialog.SelectedPath)
}
`

// windowsArgs builds the PowerShell call.
//
// ★ The script travels as `-EncodedCommand` — base64 of UTF-16LE — because
//
//	`-Command` takes the rest of the command line and **re-parses** it: a
//	path with a space in it would arrive as two arguments.
func windowsArgs(start string) []string {
	return []string{"-NoProfile", "-STA", "-EncodedCommand", encodePowerShell(windowsScript)}
}

// encodePowerShell encodes a script for -EncodedCommand.
//
// ★ PowerShell reads that argument as base64 of UTF-16LE, and it is the
//
//	only form in which a script survives untouched.
func encodePowerShell(script string) string {
	raw := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(raw)*2)
	for _, u := range raw {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// darwinScript is the AppleScript that opens the chooser.
//
// ★ A closed chooser is the error number -128, and it is caught and turned
//
//	into an empty answer: "not now" is not a failure.
const darwinScript = `on run argv
  set startPath to ""
  if (count of argv) > 0 then set startPath to item 1 of argv
  try
    if startPath is "" then
      set chosen to choose folder with prompt "Choose the project directory"
    else
      set chosen to choose folder with prompt "Choose the project directory" default location (POSIX file startPath)
    end if
  on error number -128
    return ""
  end try
  return POSIX path of chosen
end run`

// darwinArgs builds the osascript call.
//
// ★ osascript hands the arguments after the script to the run handler as
//
//	they are, so a path with a space in it stays one argument.
func darwinArgs(start string) []string {
	return []string{"-e", darwinScript, start}
}

// reason says why the chooser failed.
func reason(out outcome) string {
	msg := strings.TrimSpace(out.stderr)
	if msg == "" {
		msg = strings.TrimSpace(out.stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", out.code)
	}
	return msg
}

// execRunner opens the chooser as a child process.
func execRunner(ctx context.Context, sp spec) (outcome, error) {
	cmd := exec.CommandContext(ctx, sp.program, sp.args...)
	cmd.Env = append(os.Environ(), sp.env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := outcome{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return out, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		out.code = exit.ExitCode()
		return out, nil
	}
	return out, err
}
