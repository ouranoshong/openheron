package spec

import (
	"strings"
	"testing"
)

// ★ The log destination is off unless a file is named. A server started from a
// terminal is already being watched, and a file nobody asked for is litter.
func TestTheLogDefaultsToStderrOnly(t *testing.T) {
	c := &Config{Server: defaultServer()}
	if err := c.loadServer("server/config.yaml", []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if c.Server.Log.File != "" {
		t.Fatalf("no file must be configured by default, got %q", c.Server.Log.File)
	}
	if !c.Server.Log.Console {
		t.Fatal("stderr must stay on by default")
	}
	if c.Server.Log.MaxSizeMB != 20 || c.Server.Log.MaxAgeDays != 14 || c.Server.Log.MaxBackups != 5 {
		t.Fatalf("rotation defaults changed: %+v", c.Server.Log)
	}
}

// ★ A negative cap is not a failed start: 0 already means "no cap" to the
//
//	rotator, so the loader normalises the value and says so once.
func TestANegativeLogCapIsReportedAndBecomesNoCap(t *testing.T) {
	c := &Config{Server: defaultServer()}
	data := []byte("log:\n  file: logs/openheron.log\n  max_size_mb: -5\n  max_age_days: -1\n")
	if err := c.loadServer("server/config.yaml", data); err != nil {
		t.Fatal(err)
	}
	if c.Server.Log.File != "logs/openheron.log" {
		t.Fatalf("the file was not kept: %q", c.Server.Log.File)
	}
	if c.Server.Log.MaxSizeMB != 0 || c.Server.Log.MaxAgeDays != 0 {
		t.Fatalf("negative caps must become 0, got %+v", c.Server.Log)
	}
	notices := strings.Join(c.Deferred(), "\n")
	for _, key := range []string{"log.max_size_mb", "log.max_age_days"} {
		if !strings.Contains(notices, key) {
			t.Fatalf("%s was clamped in silence:\n%s", key, notices)
		}
	}
}

func TestTheLogFileIsTrimmed(t *testing.T) {
	c := &Config{Server: defaultServer()}
	if err := c.loadServer("server/config.yaml", []byte("log:\n  file: \"  logs/a.log  \"\n")); err != nil {
		t.Fatal(err)
	}
	if c.Server.Log.File != "logs/a.log" {
		t.Fatalf("the path must be trimmed: %q", c.Server.Log.File)
	}
}
