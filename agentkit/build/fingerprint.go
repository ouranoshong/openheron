package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// agentDeps is what one agent depends on.
//
// ★ The fingerprint is the agent's identity: two builds of the same agent
//
//	with the same fingerprint are the same agent, whatever the rest of the
//	configuration did. ★ That is what lets the pool's key be a fingerprint
//	instead of a generation — a generation says "everything changed", and a
//	fingerprint says "this one changed".
type agentDeps struct {
	// fingerprint identifies the agent's built form.
	fingerprint string
	// files maps a configuration file to the contribution that file makes
	// to this agent.
	//
	// ★ An **empty** contribution means the file says nothing about this
	//	agent: it is still listed, because a file that starts naming one of
	//	the agent's tools changes the answer, but nothing the file says
	//	today reaches the agent.
	files map[string]string
	// live are the contributions that are **not files**: the tool list a
	// mounted MCP server offers right now.
	//
	// ★ They are in the fingerprint and **not** in the files map: a server
	//	that comes up changes what the agent can do, but it changed no
	//	file — and a list of changed files that named it would be a lie.
	live []string
}

// mcpTools reports the live tool list of a mounted server.
//
// ★ It is an interface because the dependency walk must not require the MCP
//
//	manager to exist: a builder without one has no servers, and a nil
//	interface says exactly that.
type mcpTools interface {
	ToolsOf(server string) []string
}

// computeDeps returns what every agent in the configuration depends on.
//
// ★ The truth is the **content** of the files, not the moment they were
//
//	parsed: a file that changed on disk changes what the next turn runs,
//	whatever the running tree still holds. ★ So a file's contribution is
//	its content hash — the same rule the reload uses (see spec/reload.go).
//
// ★ The dependencies are **per agent**, not per partition: a skill only
//
//	concerns the agents that name it, and a model file only the agents
//	that run on it. ★ That is what makes "change one file, rebuild what
//	depends on it" possible at all — a partition-wide dependency would
//	rebuild the whole tree for every change, which is the behaviour this
//	replaces.
func computeDeps(cfg *spec.Config, svc *spec.Service, skillsDir string, mcp mcpTools) map[string]agentDeps {
	if cfg == nil {
		return nil
	}
	w := &depWalker{cfg: cfg, svc: svc, skillsDir: skillsDir, mcp: mcp, out: map[string]agentDeps{}}
	for id := range cfg.Agents {
		w.of(id, map[string]bool{})
	}
	return w.out
}

type depWalker struct {
	cfg       *spec.Config
	svc       *spec.Service
	skillsDir string
	mcp       mcpTools
	out       map[string]agentDeps
}

// of computes the dependencies of one agent and of its subtree.
//
// ★ The children's fingerprints are part of the parent's: the parent's
//
//	`task` tool description carries the child's name and description, so a
//	child that changed changes the parent too. ★ That is why the walk is
//	post-order — the children are resolved first.
func (w *depWalker) of(id string, path map[string]bool) agentDeps {
	if d, ok := w.out[id]; ok {
		return d
	}
	a, ok := w.cfg.Agents[id]
	if !ok {
		return agentDeps{files: map[string]string{}}
	}
	if path[id] {
		// ★ A cycle would hang the walk. The loader refuses one, but a
		//   fingerprint must not depend on that to terminate.
		return agentDeps{fingerprint: "cycle:" + id, files: map[string]string{}}
	}
	path[id] = true
	defer delete(path, id)

	files := map[string]string{}
	w.addOwnFile(files, a)
	w.addModelFile(files, a)
	w.addToolFiles(files, a)
	live := w.addMCPFiles(files, a)
	w.addSkillFiles(files, a)

	children := make([]string, 0, len(a.SubAgents))
	for _, child := range a.SubAgents {
		children = append(children, child+"="+w.of(child, path).fingerprint)
	}

	d := agentDeps{fingerprint: fingerprintOf(id, files, live, children), files: files, live: live}
	w.out[id] = d
	return d
}

// addOwnFile adds the agent's own file.
//
// ★ It is the agent's whole definition: its instruction, its description,
//
//	its model, its lists. ★ Nothing else can change what the agent is.
func (w *depWalker) addOwnFile(files map[string]string, a *decl.Agent) {
	if a.File == "" {
		return
	}
	files[a.File] = w.configHash(a.File)
}

// addModelFile adds the file that declares the agent's model.
//
// ★ The whole provider file is the dependency, not just the model the
//
//	agent names: the provider carries what the model needs — the base URL,
//	the credential reference — and a change to any of them changes the
//	agent. ★ A second model in the same file does not, and that is the
//	price of the coarse rule: the file is one unit.
func (w *depWalker) addModelFile(files map[string]string, a *decl.Agent) {
	ref, err := spec.ParseRef(a.ModelRef)
	if err != nil {
		return
	}
	rel := spec.DirLLM + "/" + ref.ProviderID + ".yaml"
	files[rel] = w.configHash(rel)
}

// addToolFiles adds the `tools/` files this agent's tools come from.
//
// ★ Two files can matter for one tool: the one that **configures** it (the
//
//	HTTP tool's timeout lives in tools/http.yaml), and the one that grants
//	it its exception (the `allows:` list that decides whether it runs
//	without a nod). ★ Both are dependencies: a change to either changes
//	what the agent does.
//
// ★ Every file of the partition is listed, even the ones that say nothing
//
//	about this agent: a file that starts naming one of the agent's tools
//	changes the answer, and a file that is not watched cannot be seen to
//	change. ★ The contribution of a file that says nothing is empty, so
//	the agent is not rebuilt for it.
func (w *depWalker) addToolFiles(files map[string]string, a *decl.Agent) {
	allows := w.cfg.ToolAllows()
	uses := map[string]bool{}
	for _, entry := range a.Tools {
		uses[entry] = true
	}
	for _, rel := range spec.ToolConfigFiles() {
		files[rel] = w.toolsContribution(rel, allows[rel], uses)
	}
	// ★ A file that **configures** a tool the agent uses carries that tool's
	//	whole configuration, so its content is the contribution.
	for _, entry := range a.Tools {
		rel := spec.ToolConfigFile(entry)
		if rel == "" {
			continue
		}
		files[rel] = w.configHash(rel)
	}
}

// toolsContribution is what one `tools/` file says about one agent.
//
// ★ Two parts, and both matter: the file's **configuration**, when it
//
//	configures a tool the agent uses, and the **exceptions** it names that
//	the agent's tools are covered by. ★ The second one is why the file is
//	watched even when it configures nothing the agent uses: an exception
//	is a fact about the tier, and the tier is what the agent runs with.
func (w *depWalker) toolsContribution(rel string, allows []string, uses map[string]bool) string {
	var parts []string
	if rel != "" && w.configures(rel, uses) {
		parts = append(parts, "config "+w.configHash(rel))
	}
	for _, name := range allows {
		if w.usesTool(uses, name) {
			parts = append(parts, "allow "+name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return hashString(strings.Join(parts, "\n"))
}

// configures reports whether the file configures a tool the agent uses.
//
// ★ The tool names in an agent's list may be **patterns** (`request_*`), so
//
//	the answer is a match against the pattern, not an equality.
func (w *depWalker) configures(rel string, uses map[string]bool) bool {
	for entry := range uses {
		prefix, isPattern, err := decl.ParseToolNamePattern(entry)
		if err != nil {
			continue
		}
		if !isPattern && spec.ToolConfigFile(entry) != rel {
			continue
		}
		if isPattern {
			// ★ A pattern covers a whole family of tools, and the file
			//   that configures one of them is the file that configures
			//   the family: the names are known, so the match is exact.
			// ★ The configuration tools have a file of their own now, so
			//	they belong in the walk too — a pattern that covers them
			//	is the same fact as a name that names them.
			for _, name := range decl.KnownToolNames {
				if strings.HasPrefix(name, prefix) && spec.ToolConfigFile(name) == rel {
					return true
				}
			}
			continue
		}
		return true
	}
	return false
}

// usesTool reports whether one of the agent's tool entries covers a name.
//
// ★ The agent's list is the allow-list, and an entry may be a pattern: the
//
//	name is covered when it equals the entry or when it starts with the
//	entry's prefix.
func (w *depWalker) usesTool(uses map[string]bool, name string) bool {
	for entry := range uses {
		prefix, isPattern, err := decl.ParseToolNamePattern(entry)
		if err != nil {
			continue
		}
		if isPattern {
			if strings.HasPrefix(name, prefix) {
				return true
			}
			continue
		}
		if entry == name {
			return true
		}
	}
	return false
}

// addMCPFiles adds the servers' own files, and the tool lists they offer.
//
// ★ Mounting a server grants its tools, so the server's file is the agent's
//
//	dependency — and so is the **live** tool list: a server that comes up
//	changes what the agent can do, and the agent has to be rebuilt for it.
//
// ★ The live list is not a file, so it is not in the files map: it is in
//
//	the fingerprint, and it is reported apart.
func (w *depWalker) addMCPFiles(files map[string]string, a *decl.Agent) []string {
	var live []string
	for _, server := range a.MCPServers {
		rel := spec.DirMCP + "/" + server + ".yaml"
		files[rel] = w.configHash(rel)
		if w.mcp == nil {
			continue
		}
		names := w.mcp.ToolsOf(server)
		if len(names) == 0 {
			continue
		}
		sorted := append([]string(nil), names...)
		sort.Strings(sorted)
		live = append(live, "mcp "+server+" "+strings.Join(sorted, ","))
	}
	return live
}

// addSkillFiles adds the skills the agent names.
//
// ★ A skill is a **file**, and the agent's `skills:` list says which ones
//
//	it may load — so the skill's own file is the dependency, and a skill
//	that is not named is not this agent's business.
func (w *depWalker) addSkillFiles(files map[string]string, a *decl.Agent) {
	for _, name := range a.Skills {
		rel := spec.DirSkills + "/" + name + "/" + spec.SkillFileName
		files[rel] = w.skillHash(name)
	}
}

// configHash returns the content hash of a configuration file.
//
// ★ A file that cannot be read still takes part, with an empty hash:
//
//	dropping it would make a read error look like a deletion.
func (w *depWalker) configHash(rel string) string {
	if w.svc == nil || rel == "" {
		return ""
	}
	content, _, err := w.svc.ReadFile(rel)
	if err != nil || content == nil {
		return ""
	}
	return hashBytes(content)
}

// skillHash returns the content hash of a skill's file.
//
// ★ The skills directory is the runtime's own, and it may live outside the
//
//	configuration root — so the file is read where the runtime reads it, not
//	where the editor writes it.
func (w *depWalker) skillHash(name string) string {
	if w.skillsDir == "" || name == "" {
		return ""
	}
	content, err := os.ReadFile(filepath.Join(w.skillsDir, name, spec.SkillFileName))
	if err != nil {
		return ""
	}
	return hashBytes(content)
}

// fingerprintOf hashes an agent's dependencies.
//
// ★ The paths are sorted, so the order the configuration happened to list
//
//	things in does not change the answer: two configurations that differ
//	only in the order of two allow-lists are the same agent.
func fingerprintOf(id string, files map[string]string, live []string, children []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "agent %s\n", id)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(h, "file %s %s\n", p, files[p])
	}
	sorted := append([]string(nil), live...)
	sort.Strings(sorted)
	for _, l := range sorted {
		fmt.Fprintf(h, "live %s\n", l)
	}
	sort.Strings(children)
	for _, c := range children {
		fmt.Fprintf(h, "child %s\n", c)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashString(s string) string {
	return hashBytes([]byte(s))
}
