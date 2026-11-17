package build

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

// configGate hands the writes that land inside the configuration root to the
// configuration service.
//
// ★ The filesystem does not know what a configuration file is — it asks the
//
//	interface it defines, and the answer comes from here, where both the
//	service and the config root are known. That keeps `agentkit/fsx` a leaf
//	package: it depends on nothing of openheron's.
type configGate struct {
	svc *spec.Service
	// root is the configuration root, absolute and symlink-resolved — the
	// same shape the filesystem's own roots have, so the two can be
	// compared.
	root string
}

// newConfigGate builds the gate. It returns nil when there is no service or
// no root: a backend without a configuration tree writes everything itself,
// which is the behaviour outside openheron.
func newConfigGate(svc *spec.Service) *configGate {
	if svc == nil {
		return nil
	}
	root := strings.TrimSpace(svc.Root())
	if root == "" {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return &configGate{svc: svc, root: abs}
}

// OwnsPath reports whether this absolute path is inside the configuration
// root.
//
// ★ **The whole root**, not just the partitions: the root holds the session
//
//	store next to the configuration, and a file written there by the
//	filesystem would land wherever it was told to. The service is the one
//	that decides what may be written — this only decides what has to go
//	through it.
func (g *configGate) OwnsPath(absPath string) bool {
	if g == nil || g.svc == nil || g.root == "" {
		return false
	}
	rel, err := g.rel(absPath)
	if err != nil {
		return false
	}
	return rel != ""
}

// rel turns an absolute path into one relative to the configuration root.
func (g *configGate) rel(absPath string) (string, error) {
	rel, err := filepath.Rel(g.root, absPath)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("path %q is outside the configuration root %q", absPath, g.root)
	}
	return rel, nil
}

// ReadConfig returns a configuration file's content and revision.
//
// ★ A file that is not there is an **error** here, although the service
//
//	answers "not found" with an empty revision and no error. The caller is
//	the filesystem's Edit, and that one has always failed on a missing
//	file.
func (g *configGate) ReadConfig(absPath string) ([]byte, string, error) {
	rel, err := g.rel(absPath)
	if err != nil {
		return nil, "", err
	}
	content, rev, err := g.svc.ReadFile(rel)
	if err != nil {
		return nil, "", err
	}
	if rev == "" {
		return nil, "", fmt.Errorf("failed to read file: %s does not exist", rel)
	}
	return content, rev, nil
}

// WriteConfig writes a configuration file through the service.
//
// ★ This is the whole point of the gate: the service's checks run — the
//
//	tree must still load, no secret may land in it, a working agent may
//	not disappear from it, and the write is atomic — and a refusal comes
//	with the service's reason instead of a silent success.
func (g *configGate) WriteConfig(absPath string, content []byte, baseRev string) error {
	rel, err := g.rel(absPath)
	if err != nil {
		return err
	}
	_, _, err = g.svc.Write(rel, content, baseRev)
	return err
}
