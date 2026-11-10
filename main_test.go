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
