package main

import (
	"bytes"
	"log/slog"
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
