package spec

import (
	"github.com/ouranoshong/openheron/agentkit/templates"
)

// Templates returns the files one partition holds, keyed by the path they
// are written to.
//
// ★ The **shape** of the answer is the same for every partition: a caller
//
//	that adds a template file must not have to learn a new function.
//
// ★ The content behind it is the spec's business: the files live in the
//
//	templates package, and the spec is the only thing that knows which
//	partition a template belongs to.
//
// ★ The llm/ partition is not here: its files are alternatives — the
//
//	provider's own template and the generic fallback — and a partition of
//	alternatives is not a partition to copy.
func Templates(dir string) map[string]string {
	switch dir {
	case DirAgent, DirTools, DirMCP, DirServer:
		files, err := templates.Dir(dir)
		if err != nil {
			return map[string]string{}
		}
		return files
	default:
		return map[string]string{}
	}
}

// Has reports whether one file of a partition is already declared.
//
// ★ It is the question "is this file there?", asked of the configuration
//
//	root — and the answer belongs to the configuration layer, so that the
//	command that writes files and the interface that edits them ask it in
//	the same words.
func (s *Service) Has(dir, name string) bool {
	cat, ok := CategoryByDir(dir)
	if !ok {
		return false
	}
	names, err := s.ListFiles(cat)
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
