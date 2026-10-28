package kb

import (
	"os"
	"path/filepath"
	"testing"
)

func repoCorpusDir(t *testing.T) string {
	t.Helper()

	dir := os.Getenv("OPENHERON_KB_CORPUS")
	if dir == "" {
		dir = filepath.Join("..", "docs")
	}

	_, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		t.Skipf("corpus directory %s does not exist: the openheron distribution ships without docs/, "+
			"so this absence is by design and not a corpus that was moved away. "+
			"Point OPENHERON_KB_CORPUS at a corpus root to make this case run.",
			dir)
	case err != nil:
		t.Fatalf("stat corpus %s: %v", dir, err)
	}

	return dir
}
