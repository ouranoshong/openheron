package build

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

// impactAnswer answers "what changes next turn".
//
// ★ It **recomputes**: the configuration on disk is read again, the
//
//	dependencies are computed from it, and the result is compared with the
//	running tree's. ★ The mark set is not read — it is a cache that dies
//	with the process, and an answer that lies after a restart is worse
//	than no answer at all.
//
// ★ The answer is about the **running** tree: the dependency graph is the
//
//	running tree's, and the comparison says what the disk would change
//	about it. ⇒ `source` is `running`, while `config_get {path}` answers
//	`disk`: two questions, two sides.
func impactAnswer(svc *spec.Service, running *spec.Config, deps map[string]agentDeps,
	mcp mcpTools, skillsDir, rel string) (any, error) {

	if svc == nil {
		return nil, fmt.Errorf("config_get: no configuration service is wired")
	}
	current, err := svc.Load()
	if err != nil {
		return nil, fmt.Errorf("the configuration on disk does not load: %w", err)
	}
	view := newImpactView(deps, computeDeps(current, svc, skillsDir, mcp))

	if rel == "" {
		return struct {
			Source       string   `json:"source"`
			ChangedFiles []string `json:"changedFiles"`
			Rebuilds     []string `json:"rebuilds"`
		}{sourceRunning, view.changedFiles(), view.rebuilds()}, nil
	}

	cat, err := spec.ValidateReadPath(rel)
	if err != nil {
		return nil, err
	}
	// ★ The paths the dependency map holds are the configuration's own
	//   shape — forward slashes, relative to the root — so the question is
	//   asked in that shape too.
	key := path.Clean(filepath.ToSlash(rel))
	return struct {
		Path            string   `json:"path"`
		Source          string   `json:"source"`
		Dependents      []string `json:"dependents"`
		Rebuilds        []string `json:"rebuilds"`
		Changed         bool     `json:"changed"`
		RequiresRestart bool     `json:"requiresRestart,omitempty"`
	}{
		Path:       key,
		Source:     sourceRunning,
		Dependents: view.dependents(key),
		Rebuilds:   view.rebuilds(),
		Changed:    view.changed(key),
		// ★ A file in a partition that is not hot-reloaded does not reach
		//   any agent: it configures the **process**, and only a restart
		//   picks it up. ⇒ The honest answer is "nothing changes, and you
		//   have to restart" — not "nothing happens".
		RequiresRestart: !cat.Hot() || (running != nil && !running.WatchEnabled()),
	}, nil
}

// impactView compares the running tree's dependencies with the current ones.
//
// ★ It is a **comparison of two trees**, not a lookup: "what changes next
//
//	turn" is the difference between the tree this turn runs and the tree the
//	disk would build now. ★ That is why it takes both — an answer about
//	one tree alone can only say what that tree depends on, not what moved.
type impactView struct {
	running map[string]agentDeps
	current map[string]agentDeps
}

func newImpactView(running, current map[string]agentDeps) impactView {
	return impactView{running: running, current: current}
}

// rebuilds are the agents the next turn rebuilds.
//
// ★ An agent is rebuilt when its **fingerprint** moved — not when a file it
//
//	depends on moved: a file that says nothing about the agent does not
//	reach it, and rebuilding it anyway would be the global swap this
//	replaced.
//
// ★ An agent that is **gone** is not in the list: there is nothing left to
//
//	rebuild, and the tree that loses it is a fact the loader reports.
func (v impactView) rebuilds() []string {
	out := []string{}
	for id, cur := range v.current {
		old, ok := v.running[id]
		if !ok || old.fingerprint != cur.fingerprint {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// changedFiles are the files whose contribution to some agent moved.
//
// ★ A file that no agent depends on is not in the list: nothing it says
//
//	reaches an agent, so its change changes nothing.
func (v impactView) changedFiles() []string {
	seen := map[string]bool{}
	for id, cur := range v.current {
		old, ok := v.running[id]
		if !ok {
			for path, contribution := range cur.files {
				if contribution != "" {
					seen[path] = true
				}
			}
			continue
		}
		for path, contribution := range cur.files {
			if old.files[path] != contribution {
				seen[path] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// dependents are the agents the file says something about.
//
// ★ The dependency is **what the file contributes**, not the file's
//
//	presence: a `tools/` file that configures no tool the agent uses says
//	nothing about it, so it is not a dependent. ★ The running tree is read
//	as well, because an agent that used to depend on the file and does not
//	any more is exactly the one the reader wants to see.
func (v impactView) dependents(path string) []string {
	out := []string{}
	seen := map[string]bool{}
	for id, d := range v.current {
		if d.files[path] != "" {
			out = append(out, id)
			seen[id] = true
		}
	}
	for id, d := range v.running {
		if !seen[id] && d.files[path] != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// changed says whether the file's contribution to some agent moved.
//
// ★ A file **nobody depends on** has not changed in the sense that matters:
//
//	it is not in any agent's dependency list, so there is no contribution
//	to compare and nothing it says reaches a tree.
func (v impactView) changed(path string) bool {
	for id, cur := range v.current {
		old, ok := v.running[id]
		if !ok {
			if cur.files[path] != "" {
				return true
			}
			continue
		}
		if old.files[path] != cur.files[path] {
			return true
		}
	}
	return false
}
