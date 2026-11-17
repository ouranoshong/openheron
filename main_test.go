package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

func TestApplyLogLevelPrecedence(t *testing.T) {
	cases := []struct {
		name string
		env  string
		cfg  string
		want string
	}{
		{"the file alone", "", "warn", "warn"},
		{"the env wins over the file", "debug", "error", "debug"},
		{"both empty is info", "", "", "info"},
		{"the env alone", "error", "", "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(obs.LevelEnv, c.env)
			var buf bytes.Buffer
			lv := new(slog.LevelVar)
			log := obs.New(&buf, lv)

			applyLogLevel(log, lv, &spec.Config{Server: decl.Server{LogLevel: c.cfg}})

			if got := obs.LevelName(lv.Level()); got != c.want {
				t.Fatalf("level = %q, want %q", got, c.want)
			}
			if buf.Len() != 0 {
				t.Fatalf("a valid level warned: %s", buf.String())
			}
		})
	}
}

func TestApplyLogLevelFallsBackAndSaysSo(t *testing.T) {
	t.Setenv(obs.LevelEnv, "")
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	log := obs.New(&buf, lv)

	applyLogLevel(log, lv, &spec.Config{Server: decl.Server{LogLevel: "trace"}})

	if got := obs.LevelName(lv.Level()); got != "info" {
		t.Fatalf("level = %q, want info", got)
	}
	if !strings.Contains(buf.String(), "fell back") {
		t.Fatalf("the fallback was silent: %s", buf.String())
	}
}

// ★ The logger is built before the configuration is read and handed to hertz
//
//	and eino, so the file destination is installed *underneath* it. This is
//	the test that says the same logger ends up writing to the file.
func TestApplyLogFileWritesThroughTheSameLogger(t *testing.T) {
	t.Setenv(obs.FileEnv, "")
	root := t.TempDir()
	svc, err := spec.NewService(root, root)
	if err != nil {
		t.Fatal(err)
	}

	var fallback bytes.Buffer
	sink := obs.NewSink(&fallback)
	log := obs.New(sink, new(slog.LevelVar))

	cfg := &spec.Config{Server: decl.Server{Log: decl.LogCfg{
		File: "logs/openheron.log", Console: false, MaxSizeMB: 1,
	}}}
	closer := applyLogFile(log, sink, svc, cfg)
	if closer == nil {
		t.Fatal("no rotator: the file was configured but not opened")
	}
	defer closer.Close()

	log.Info("this line belongs in the file")

	data, err := os.ReadFile(filepath.Join(root, "logs", "openheron.log"))
	if err != nil {
		t.Fatalf("the log file was not written: %v", err)
	}
	if !strings.Contains(string(data), "this line belongs in the file") {
		t.Fatalf("the record never reached the file:/n%s", data)
	}
	if fallback.Len() != 0 {
		t.Fatalf("console was off, but the fallback still received: %q", fallback.String())
	}
}

func TestApplyLogFileEnvOverrideWins(t *testing.T) {
	root := t.TempDir()
	svc, err := spec.NewService(root, root)
	if err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(t.TempDir(), "from-env.log")
	t.Setenv(obs.FileEnv, envPath)

	var fallback bytes.Buffer
	sink := obs.NewSink(&fallback)
	log := obs.New(sink, new(slog.LevelVar))

	closer := applyLogFile(log, sink, svc, &spec.Config{Server: decl.Server{Log: decl.LogCfg{
		File: "logs/ignored.log", Console: false,
	}}})
	if closer == nil {
		t.Fatal("the env override did not open a file")
	}
	defer closer.Close()

	log.Info("the env path wins")

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("the env path was not used: %v", err)
	}
	if !strings.Contains(string(data), "the env path wins") {
		t.Fatalf("the record never reached the env path:/n%s", data)
	}
}

// ★ A destination that cannot be opened must not take the process with it: the
//
//	log is where the reason would be read.
func TestApplyLogFileKeepsStderrWhenThePathIsUnusable(t *testing.T) {
	root := t.TempDir()
	svc, err := spec.NewService(root, root)
	if err != nil {
		t.Fatal(err)
	}
	// A file where the directory has to be.
	if err := os.WriteFile(filepath.Join(root, "logs"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	var fallback bytes.Buffer
	sink := obs.NewSink(&fallback)
	log := obs.New(sink, new(slog.LevelVar))

	closer := applyLogFile(log, sink, svc, &spec.Config{Server: decl.Server{Log: decl.LogCfg{
		File: "logs/openheron.log",
	}}})
	if closer != nil {
		closer.Close()
		t.Fatal("an unusable path must not leave a file destination installed")
	}

	log.Info("still on stderr")
	got := fallback.String()
	if !strings.Contains(got, "still on stderr") {
		t.Fatalf("the fallback stopped receiving records: %q", got)
	}
	if !strings.Contains(got, "stays on stderr") {
		t.Fatalf("the failure was not reported: %q", got)
	}
}

// ★ The address is a notice: it reaches the terminal even when the log has
//
//	moved to a file, and it is said once.
func TestAnnounceServingReachesTheTerminal(t *testing.T) {
	var console, file bytes.Buffer
	sink := obs.NewSink(&console)
	sink.Install(&file, false)
	notice := obs.NewNotice(sink.NoticeWriter())

	announceServing(&startup{addr: "127.0.0.1:8080", notice: notice})

	if !strings.Contains(console.String(), "http://127.0.0.1:8080") {
		t.Fatalf("the address never reached the terminal:\n%s", console.String())
	}
	if n := strings.Count(console.String(), "the server serves on"); n != 1 {
		t.Fatalf("the address was said %d times:\n%s", n, console.String())
	}
	if !strings.Contains(file.String(), "http://127.0.0.1:8080") {
		t.Fatalf("the address never reached the log file:\n%s", file.String())
	}
}

// ★ The exposure is a notice too: the log's destination is configuration,
// and a line that says "anyone who can reach this port can drive the
// agent" must not be the thing the configuration hides.
//
// ★ The log is in the file and the console is off — the configuration that
//
//	hid this line before.
func TestAnnounceExposureReachesTheTerminal(t *testing.T) {
	var console, file bytes.Buffer
	sink := obs.NewSink(&console)
	sink.Install(&file, false)
	notice := obs.NewNotice(sink.NoticeWriter())

	announceExposure(&startup{addr: "0.0.0.0:8080", notice: notice})

	if !strings.Contains(console.String(), "listens beyond the loopback") {
		t.Fatalf("the exposure never reached the terminal:\n%s", console.String())
	}
	if !strings.Contains(file.String(), "listens beyond the loopback") {
		t.Fatalf("the exposure never reached the log file:\n%s", file.String())
	}
	if !strings.Contains(console.String(), "[Warn]") {
		t.Fatalf("the exposure lost its level:\n%s", console.String())
	}
}

// ★ A listener on the loopback is not an exposure, and a warning that
//
//	always appears is a warning nobody reads.
func TestAnnounceExposureIsSilentOnTheLoopback(t *testing.T) {
	var console bytes.Buffer
	sink := obs.NewSink(&console)
	notice := obs.NewNotice(sink.NoticeWriter())

	announceExposure(&startup{addr: "127.0.0.1:8080", notice: notice})

	if console.Len() != 0 {
		t.Fatalf("the loopback was reported as an exposure:\n%s", console.String())
	}
}

// ★ A bare port is not a loopback address: it binds every interface, and
//
//	it is the shape a port number takes.
func TestAnnounceExposureNamesABarePort(t *testing.T) {
	var console bytes.Buffer
	sink := obs.NewSink(&console)
	notice := obs.NewNotice(sink.NoticeWriter())

	announceExposure(&startup{addr: ":8080", notice: notice})

	if !strings.Contains(console.String(), "listens beyond the loopback") {
		t.Fatalf("a bare port is every interface, and it was not reported:\n%s", console.String())
	}
}

func TestServerURL(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{":8080", "http://localhost:8080"},
		{"0.0.0.0:8080", "http://localhost:8080"},
		{"[::1]:8080", "http://[::1]:8080"},
		{"not-an-address", "http://not-an-address"},
	}
	for _, c := range cases {
		if got := serverURL(c.addr); got != c.want {
			t.Fatalf("serverURL(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

// ── the roots on the command line ───────────────────────────────────────────

// ★ The positional argument is the project this process focuses on. It is
//
//	also the default project root, because a person who says "work here"
//	means it for the sessions that belong to no project too.
func TestTheServerAcceptsOneProjectDirectory(t *testing.T) {
	f, err := parseServerFlags([]string{"--config-root", "cfg", "/work/api"})
	if err != nil {
		t.Fatalf("parseServerFlags: %v", err)
	}
	if f.scopeDir != "/work/api" {
		t.Fatalf("scopeDir = %q, want /work/api", f.scopeDir)
	}
	if f.configRoot != "cfg" {
		t.Fatalf("configRoot = %q, want cfg", f.configRoot)
	}
}

func TestTheServerRejectsTwoProjectDirectories(t *testing.T) {
	_, err := parseServerFlags([]string{"a", "b"})
	if err == nil {
		t.Fatal("two directories must be an error")
	}
	if !strings.Contains(err.Error(), "one directory") {
		t.Fatalf("the error must say what is wrong: %v", err)
	}
}

// ★ The old flag still works: a flag that stops working is a flag that
//
//	breaks somebody's script.
func TestTheServerAcceptsTheRetiredProjectRootFlag(t *testing.T) {
	f, err := parseServerFlags([]string{"--project-root", "/work/legacy"})
	if err != nil {
		t.Fatalf("parseServerFlags: %v", err)
	}
	if f.legacyProjectRoot != "/work/legacy" {
		t.Fatalf("legacyProjectRoot = %q, want /work/legacy", f.legacyProjectRoot)
	}
}

// ★ The openheron root is the flag, else the environment, else the user's
//
//	home directory.
func TestTheOpenheronRootResolution(t *testing.T) {
	t.Setenv(spec.RootEnv, "")
	home, err := resolveOpenheronRoot("")
	if err != nil {
		t.Fatalf("resolveOpenheronRoot: %v", err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory on this machine: %v", err)
	}
	if want := filepath.Join(userHome, ".openheron"); home != want {
		t.Fatalf("the openheron root must default to %q, got %q", want, home)
	}

	t.Setenv(spec.RootEnv, filepath.Join(t.TempDir(), "from-env"))
	home, err = resolveOpenheronRoot("")
	if err != nil {
		t.Fatalf("resolveOpenheronRoot: %v", err)
	}
	if !strings.HasSuffix(home, "from-env") {
		t.Fatalf("the environment must win, got %q", home)
	}

	named := filepath.Join(t.TempDir(), "named")
	home, err = resolveOpenheronRoot(named)
	if err != nil {
		t.Fatalf("resolveOpenheronRoot: %v", err)
	}
	if home != named {
		t.Fatalf("the flag must win, got %q (want %q)", home, named)
	}
}

// ★ The old environment variable names still work, and the new one is
//
//	reported — a variable that quietly stops working is the hardest kind of
//	mistake to find.
func TestTheOldEnvironmentNamesAreReported(t *testing.T) {
	t.Setenv(spec.RootEnv, "")
	t.Setenv(spec.ConfigRootEnv, "")
	t.Setenv(spec.ConfigDirEnv, filepath.Join(t.TempDir(), "from-env"))
	t.Setenv(spec.DefaultProjectRootEnv, "")
	t.Setenv(spec.ProjectRootEnv, "")

	svc, err := spec.NewService("", "", spec.WithHome(filepath.Join(t.TempDir(), "base")))
	if err != nil {
		t.Fatal(err)
	}
	if len(svc.Notices()) == 0 {
		t.Fatal("the old environment name must be reported")
	}
	if !strings.Contains(svc.Notices()[0], spec.ConfigRootEnv) {
		t.Fatalf("the notice must name the new variable, got %q", svc.Notices()[0])
	}
}
