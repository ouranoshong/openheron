package dirpick

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// These cover the chooser's contract without a desktop: the three platforms
// build different commands, and the answers they give back mean different
// things. ★ The contract that matters most is the one that is easy to get
// wrong: **a closed chooser is not a failure**, and a path with a space in
// it is one argument, not two.

// fakeRunner answers with a fixed outcome.
func fakeRunner(out outcome, err error) runner {
	return func(context.Context, spec) (outcome, error) { return out, err }
}

// found is a lookPath that finds everything.
func found(string) (string, error) { return "/usr/bin/whatever", nil }

// missing is a lookPath that finds nothing.
func missing(string) (string, error) { return "", errors.New("not found") }

func TestWindowsPicksTheDirectoryThroughTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	var seen spec
	run := func(_ context.Context, sp spec) (outcome, error) {
		seen = sp
		return outcome{stdout: dir + "\r\n"}, nil
	}
	got, err := pick(context.Background(), "windows", dir, run, found)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if got != dir {
		t.Fatalf("pick = %q, want %q", got, dir)
	}
	if seen.program != "powershell.exe" {
		t.Fatalf("program = %q, want powershell.exe", seen.program)
	}
	// ★ The start travels in the environment, never in the command line:
	//   PowerShell re-parses the rest of a -Command line, and a path with a
	//   space in it would arrive as two arguments.
	if !contains(seen.env, startEnv+"="+dir) {
		t.Fatalf("env = %v, want %s=%s", seen.env, startEnv, dir)
	}
	for _, a := range seen.args {
		if strings.Contains(a, dir) {
			t.Fatalf("the command line carries the path: %q", a)
		}
	}
	// ★ The script is encoded, so that it survives the round trip intact.
	if len(seen.args) != 4 || seen.args[2] != "-EncodedCommand" {
		t.Fatalf("args = %v, want -NoProfile -STA -EncodedCommand <script>", seen.args)
	}
	raw, err := base64.StdEncoding.DecodeString(seen.args[3])
	if err != nil {
		t.Fatalf("the script is not base64: %v", err)
	}
	if got := decodeUTF16LE(raw); !strings.Contains(got, "FolderBrowserDialog") {
		t.Fatalf("the script does not open a chooser: %q", got)
	}
}

func TestTheScriptAsksForUTF8WithoutAByteOrderMark(t *testing.T) {
	// ★ A path may hold any character the keyboard can produce, and a home
	//   directory with a Chinese name is the ordinary case.
	if !strings.Contains(windowsScript, "[System.Text.UTF8Encoding]::new($false)") {
		t.Fatal("the script does not ask for UTF-8 without a byte-order mark")
	}
	// ★ "The chooser was closed" is told by an empty answer, not by a
	//   failure — so the script must not throw on a cancel.
	if !strings.Contains(windowsScript, "DialogResult]::OK") {
		t.Fatal("the script does not compare the dialog's result")
	}
}

func TestABomInFrontOfThePathIsStripped(t *testing.T) {
	dir := t.TempDir()
	run := fakeRunner(outcome{stdout: "\ufeff" + dir}, nil)
	got, err := pick(context.Background(), "windows", dir, run, found)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if got != dir {
		t.Fatalf("pick = %q, want %q", got, dir)
	}
}

func TestAClosedChooserIsNotAFailure(t *testing.T) {
	// ★ The Windows and the macOS shape: exit 0 and no answer.
	run := fakeRunner(outcome{}, nil)
	_, err := pick(context.Background(), "windows", t.TempDir(), run, found)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

func TestTheZenityCancelCodeIsAlsoACancel(t *testing.T) {
	// ★ The Linux shape: the chooser exits 1 and says nothing.
	run := fakeRunner(outcome{code: 1}, nil)
	_, err := pick(context.Background(), "linux", t.TempDir(), run, found)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

func TestAFailureCarriesTheChoosersOwnWords(t *testing.T) {
	run := fakeRunner(outcome{code: 1, stderr: "cannot open display"}, nil)
	_, err := pick(context.Background(), "linux", t.TempDir(), run, found)
	if err == nil || errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want a failure", err)
	}
	if !strings.Contains(err.Error(), "cannot open display") {
		t.Fatalf("err = %v, want the chooser's own words", err)
	}
}

func TestAMachineWithNoChooserIsToldSo(t *testing.T) {
	run := fakeRunner(outcome{}, nil)
	_, err := pick(context.Background(), "linux", t.TempDir(), run, missing)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestTheStartThatIsNotThereIsNotAStart(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	var seen spec
	run := func(_ context.Context, sp spec) (outcome, error) {
		seen = sp
		return outcome{stdout: home}, nil
	}
	_, err = pick(context.Background(), "windows", filepath.Join(home, "no-such-place"), run, found)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !contains(seen.env, startEnv+"="+home) {
		t.Fatalf("env = %v, want the home directory %q", seen.env, home)
	}
}

func TestTheMacChooserGetsItsStartAsOneArgument(t *testing.T) {
	dir := t.TempDir()
	var seen spec
	run := func(_ context.Context, sp spec) (outcome, error) {
		seen = sp
		return outcome{stdout: dir}, nil
	}
	if _, err := pick(context.Background(), "darwin", dir, run, found); err != nil {
		t.Fatalf("pick: %v", err)
	}
	if seen.program != "osascript" {
		t.Fatalf("program = %q, want osascript", seen.program)
	}
	// ★ The path is the last argument, whole: a space in it stays inside
	//   one argument.
	if len(seen.args) != 3 || seen.args[2] != dir {
		t.Fatalf("args = %v, want the path as the last argument", seen.args)
	}
}

func TestZenityComesBeforeKdialog(t *testing.T) {
	got := candidatesFor("linux", "/tmp")
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want two", got)
	}
	if got[0].program != "zenity" || got[1].program != "kdialog" {
		t.Fatalf("candidates = %v, want zenity then kdialog", got)
	}
}

func TestTheTimeoutIsReportedAsATimeout(t *testing.T) {
	// ★ The chooser waits for a human, so the bound is minutes. The test
	//   brings its own short deadline: the parent deadline wins over the
	//   package's, and that is what the production path does too.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	run := func(ctx context.Context, _ spec) (outcome, error) {
		<-ctx.Done()
		return outcome{}, ctx.Err()
	}
	_, err := pick(ctx, "windows", t.TempDir(), run, found)
	if err == nil || !strings.Contains(err.Error(), "given up on") {
		t.Fatalf("err = %v, want the timeout to be named", err)
	}
}

// contains reports whether the list holds the entry.
func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// decodeUTF16LE reads what PowerShell's -EncodedCommand carries.
func decodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		return ""
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(units))
}
