package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearRootEnv empties every environment variable that names a root, so a
// test that resolves one starts from a known state.
//
// ★ The machine the tests run on may have any of them set — a developer
//
//	who exported $OPENHERON_CONFIG_ROOT once would otherwise have every
//	test in this file answer about their own home directory.
func clearRootEnv(t *testing.T) {
	t.Helper()
	t.Setenv(RootEnv, "")
	t.Setenv(ConfigRootEnv, "")
	t.Setenv(ConfigDirEnv, "")
	t.Setenv(DefaultProjectRootEnv, "")
	t.Setenv(ProjectRootEnv, "")
}

// TestTheOpenheronRootHoldsTheConfigRoot pins the new layout: the config
// root lives inside the openheron root, not inside a project.
func TestTheOpenheronRootHoldsTheConfigRoot(t *testing.T) {
	clearRootEnv(t)
	home := filepath.Join(t.TempDir(), "base")

	s, err := NewService("", "", WithHome(home))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, DefaultConfigDirName)
	if s.Root() != want {
		t.Fatalf("the config root must be <openheron root>/configs, got %q (want %q)", s.Root(), want)
	}
	if !s.RootIsDefaulted() {
		t.Fatal("a root nobody named must be reported as defaulted")
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("the missing config root must be created at %q: %v", want, err)
	}
}

// TestTheStateLivesInTheOpenheronRoot pins the split: the configuration can
// move, the state and the log file cannot.
func TestTheStateLivesInTheOpenheronRoot(t *testing.T) {
	clearRootEnv(t)
	home := filepath.Join(t.TempDir(), "base")
	configRoot := filepath.Join(t.TempDir(), "somewhere-else")

	s, err := NewService(configRoot, "", WithHome(home))
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.DataDir("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, DirState); data != want {
		t.Fatalf("the data dir must be <openheron root>/state, got %q (want %q)", data, want)
	}
	logFile, err := s.LogFile("logs/openheron.log")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "logs", "openheron.log"); logFile != want {
		t.Fatalf("the log file must be under the openheron root, got %q (want %q)", logFile, want)
	}
}

// TestTheDefaultProjectRootLivesInTheOpenheronRoot pins where a session that
// belongs to no project does its work.
func TestTheDefaultProjectRootLivesInTheOpenheronRoot(t *testing.T) {
	clearRootEnv(t)
	home := filepath.Join(t.TempDir(), "base")

	s, err := NewService(filepath.Join(t.TempDir(), "cfg"), "", WithHome(home))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, DefaultProjectsDirName, "default")
	if s.DefaultProjectRoot() != want {
		t.Fatalf("the default project root must be <openheron root>/projects/default, got %q (want %q)",
			s.DefaultProjectRoot(), want)
	}
}

// ★ The default project root has to **exist**: it is where a session that
//
//	belongs to no project does its work, and a command that cannot chdir
//	into it never starts. ★ Only the defaulted one is created — a named root
//	is the user's directory, and making it for them would hide a typo.
func TestTheDefaultedProjectRootIsCreated(t *testing.T) {
	clearRootEnv(t)
	home := filepath.Join(t.TempDir(), "base")

	if _, err := NewService(filepath.Join(t.TempDir(), "cfg"), "", WithHome(home)); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, DefaultProjectsDirName, "default")
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("the default project root must exist: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("the default project root must be a directory: %s", want)
	}
}

// ★ A named project root is the user's own directory: the server does not
//
//	create it for them, the same rule the config root follows.
func TestANamedProjectRootIsNotCreated(t *testing.T) {
	clearRootEnv(t)
	home := filepath.Join(t.TempDir(), "base")
	named := filepath.Join(t.TempDir(), "mine")

	if _, err := NewService(filepath.Join(t.TempDir(), "cfg"), named, WithHome(home)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(named); !os.IsNotExist(err) {
		t.Fatalf("a named project root must not be created for the user: %v", err)
	}
}

// TestAnUnnamedOpenheronRootKeepsTheOldLayout pins the compatibility rule: a
// caller that never heard of the openheron root must not find a directory
// appearing in the user's home.
func TestAnUnnamedOpenheronRootKeepsTheOldLayout(t *testing.T) {
	clearRootEnv(t)
	root := filepath.Join(t.TempDir(), "project", ".openheron")
	project := filepath.Dir(root)

	s, err := NewService(root, project)
	if err != nil {
		t.Fatal(err)
	}
	if s.Home() != "" {
		t.Fatalf("an unnamed openheron root must stay empty, got %q", s.Home())
	}
	data, err := s.DataDir("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, DirState); data != want {
		t.Fatalf("without an openheron root the config root stays the base, got %q (want %q)", data, want)
	}
}

// TestTheOldEnvironmentNamesStillWork pins the compatibility rule for the
// environment: the old name is read, and it is reported.
func TestTheOldEnvironmentNamesStillWork(t *testing.T) {
	clearRootEnv(t)
	fromEnv := filepath.Join(t.TempDir(), "from-env")
	t.Setenv(ConfigDirEnv, fromEnv)

	s, err := NewService("", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != fromEnv {
		t.Fatalf("the old environment name must still work, got %q (want %q)", s.Root(), fromEnv)
	}
	if len(s.Notices()) == 0 {
		t.Fatal("the old environment name must be reported")
	}
	if !strings.Contains(s.Notices()[0], ConfigRootEnv) {
		t.Fatalf("the notice must name the new variable, got %q", s.Notices()[0])
	}
}

// TestTheNewEnvironmentNamesWinOverTheOldOnes pins the precedence: the new
// name is the one the documentation says, and it is the one that wins.
func TestTheNewEnvironmentNamesWinOverTheOldOnes(t *testing.T) {
	clearRootEnv(t)
	newRoot := filepath.Join(t.TempDir(), "new")
	oldRoot := filepath.Join(t.TempDir(), "old")
	t.Setenv(ConfigRootEnv, newRoot)
	t.Setenv(ConfigDirEnv, oldRoot)

	s, err := NewService("", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != newRoot {
		t.Fatalf("the new environment name must win, got %q (want %q)", s.Root(), newRoot)
	}
	if len(s.Notices()) != 0 {
		t.Fatalf("the old name was not used, so it must not be reported: %v", s.Notices())
	}
}

// TestTheOldProjectRootVariableStillWorks pins the same rule for the project
// root: the old name is read, and it is reported.
func TestTheOldProjectRootVariableStillWorks(t *testing.T) {
	clearRootEnv(t)
	fromEnv := filepath.Join(t.TempDir(), "project")
	t.Setenv(ProjectRootEnv, fromEnv)

	s, err := NewService(filepath.Join(t.TempDir(), "cfg"), "")
	if err != nil {
		t.Fatal(err)
	}
	if s.DefaultProjectRoot() != fromEnv {
		t.Fatalf("the old environment name must still work, got %q (want %q)",
			s.DefaultProjectRoot(), fromEnv)
	}
	if len(s.Notices()) == 0 {
		t.Fatal("the old environment name must be reported")
	}
	if !strings.Contains(s.Notices()[0], DefaultProjectRootEnv) {
		t.Fatalf("the notice must name the new variable, got %q", s.Notices()[0])
	}
}
