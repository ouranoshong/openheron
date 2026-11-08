package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ★ A skill is a **folder**: `skills/<name>/SKILL.md`, and the folder name is
// what an agent writes in `skills:`. These cases pin the three things that
// make that work — the path is two levels deep and only for this partition,
// the folder is created with the file and removed with it, and a tree the
// process does not read is refused rather than written into.

func seedSkill(t *testing.T, f *fixture, name, body string) {
	t.Helper()
	dir := filepath.Join(f.root, DirSkills, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestASkillIsWrittenIntoItsOwnFolder(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	rel := DirSkills + "/code-review/" + SkillFileName
	if _, _, err := s.Write(rel, []byte("---\ndescription: reviews\n---\nbody\n"), ""); err != nil {
		t.Fatalf("writing a skill must work: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, DirSkills, "code-review", SkillFileName)); err != nil {
		t.Fatalf("★ the folder must be created with the file: %v", err)
	}

	names, err := s.ListFiles(Category{ID: DirSkills, Dir: DirSkills, Ext: ".md", Nested: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "code-review/"+SkillFileName {
		t.Fatalf("listed = %v, want code-review/SKILL.md", names)
	}
}

func TestDeletingASkillRemovesItsFolder(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	seedSkill(t, f, "code-review", "---\ndescription: d\n---\nbody\n")
	s := f.service(t)

	rel := DirSkills + "/code-review/" + SkillFileName
	if _, err := s.Delete(rel); err != nil {
		t.Fatalf("deleting a skill must work: %v", err)
	}
	// ★ An empty folder left behind is a skill that no list shows and no
	//   editor can delete.
	if _, err := os.Stat(filepath.Join(f.root, DirSkills, "code-review")); !os.IsNotExist(err) {
		t.Fatalf("★ the skill's folder must go with the file, stat err = %v", err)
	}
}

func TestASkillNameThatCannotBeReadBackIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	for _, bad := range []string{
		DirSkills + "/Code Review/" + SkillFileName, // spaces, capitals
		DirSkills + "/a__b/" + SkillFileName,        // a run of underscores
		DirSkills + "/-lead/" + SkillFileName,
	} {
		_, _, err := s.Write(bad, []byte("---\ndescription: d\n---\nbody\n"), "")
		if err == nil {
			t.Fatalf("%q must be refused: the name is the folder and the agent cannot point at it", bad)
		}
		if !strings.Contains(err.Error(), "skill name") {
			t.Fatalf("%q: the message must say it is the name, got %v", bad, err)
		}
	}
}

func TestOnlySkillsIsTwoLevelsDeep(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	// ★ The loader reads one level of every other partition, so a file two
	//   levels down would be saved and never loaded — refused, not written.
	_, _, err := s.Write(DirAgent+"/sub/main.md", []byte("---\nmodel: openai/fast\n---\n"), "")
	if err == nil {
		t.Fatal("a nested file outside skills/ must be refused")
	}
	if !strings.Contains(err.Error(), "holds") {
		t.Fatalf("the message must say what the partition holds, got %v", err)
	}
}

// ★ The silent failure this guards: the process reads skills from somewhere
// else, the editor writes into the configuration root, and the save succeeds
// while nothing changes.
func TestSkillsWrittenWhereTheProcessDoesNotReadThemAreRefused(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)

	elsewhere := t.TempDir()
	s.SetSkillsDir(elsewhere)

	rel := DirSkills + "/code-review/" + SkillFileName
	_, _, err := s.Write(rel, []byte("---\ndescription: d\n---\nbody\n"), "")
	if err == nil {
		t.Fatal("★ a skill written into a tree the process does not read must be refused")
	}
	if !strings.Contains(err.Error(), elsewhere) {
		t.Fatalf("the refusal must name where the process reads skills, got %v", err)
	}

	m, err := s.Manifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Categories {
		if c.ID != DirSkills {
			continue
		}
		if c.Editable {
			t.Fatal("★ the section must not claim to be editable when the write is refused")
		}
		if !strings.Contains(c.Note, elsewhere) {
			t.Fatalf("the note must name the directory in force, got %q", c.Note)
		}
	}
}

// The other side of the same rule: when the process resolved no skills
// directory at all, writing here is how one gets created.
func TestSkillsAreWritableWhenTheProcessResolvedNone(t *testing.T) {
	f := newFixture(t)
	f.seedMinimal(t)
	s := f.service(t)
	s.SetSkillsDir("")

	if _, _, err := s.Write(DirSkills+"/code-review/"+SkillFileName,
		[]byte("---\ndescription: d\n---\nbody\n"), ""); err != nil {
		t.Fatalf("a tree with no skills directory is where one starts: %v", err)
	}
}
