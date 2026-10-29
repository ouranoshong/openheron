package spec

import (
	"fmt"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type yamlNode = yaml.Node

func (c *Config) loadProvider(file string, data []byte) error {
	id := strings.TrimSuffix(file, ".yaml")
	if id == "" {
		return fmt.Errorf("provider file name is empty")
	}

	var p decl.Provider
	if err := decodeStrict(data, &p); err != nil {
		return err
	}
	p.ID = id

	if p.Type == "" {
		return fmt.Errorf("missing type")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("missing base_url")
	}
	rawKey := p.APIKey
	key, resolved, err := expandValue(rawKey)
	if err != nil {
		return fmt.Errorf("api_key: %w", err)
	}
	if !resolved {
		p.APIKey = ""
		c.deferKey("llm/%s.yaml: api_key env %s is not set; calls to this endpoint will fail with 401 Unauthorized",
			id, envVarName(rawKey))
	} else {
		p.APIKey = key
	}

	for ref := range c.Models {
		if ProviderIDOf(ref) == id {
			delete(c.Models, ref)
		}
	}
	for i, m := range p.Models {
		if m == nil {
			continue
		}
		if m.ID == "" {
			return fmt.Errorf("models[%d]: missing id", i)
		}
		if m.SummarizeAt.Declared {
			c.deferKey("llm/%s.yaml: models[%s].summarize_at is no longer read — the compaction "+
				"threshold belongs to the agent; set summarize_at in the agent's own file "+
				"(agents/<id>.md), and use 0 to keep compaction off", id, m.ID)
		}
		c.Models[id+"/"+m.ID] = m
	}

	c.Providers[id] = &p
	return nil
}

func (c *Config) loadAgent(file string, data []byte) error {
	id := strings.TrimSuffix(file, ".md")
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}

	var a decl.Agent
	if err := decodeStrict(fm, &a); err != nil {
		return fmt.Errorf("%s: frontmatter: %w", id, err)
	}
	a.ID = id
	a.File = DirAgent + "/" + file
	a.Instruction = body

	if strings.TrimSpace(a.ModelRef) == "" {
		return fmt.Errorf("%s: must declare model (no inheritance, no fallback)", id)
	}
	if _, err := ParseRef(a.ModelRef); err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}

	c.Agents[id] = &a
	return nil
}

func (c *Config) loadServer(file string, data []byte) error {
	s := c.Server
	if err := decodeStrict(data, &s); err != nil {
		return err
	}
	c.Server = s
	return nil
}

// http.yaml        → decl.HTTPToolsCfg
// web_search.yaml  → decl.WebSearchCfg
// wikipedia.yaml   → decl.WikipediaCfg
// kb.yaml          → decl.KB
func (c *Config) loadTool(file string, data []byte) error {
	switch strings.TrimSuffix(file, ".yaml") {
	case "http":
		var t decl.HTTPToolsCfg
		if err := decodeStrict(data, &t); err != nil {
			return err
		}
		c.Tools.HTTP = &t
		return nil
	case "web_search":
		var t decl.WebSearchCfg
		if err := decodeStrict(data, &t); err != nil {
			return err
		}
		c.Tools.WebSearch = &t
		return nil
	case "wikipedia":
		var t decl.WikipediaCfg
		if err := decodeStrict(data, &t); err != nil {
			return err
		}
		c.Tools.Wikipedia = &t
		return nil
	case "kb":
		return c.loadKB(data)
	default:
		return fmt.Errorf("tools/ only accepts http.yaml, web_search.yaml, wikipedia.yaml and kb.yaml; unknown file %q", file)
	}
}

func (c *Config) loadKB(data []byte) error {
	k := c.KB
	var probe struct {
		Splitter *struct {
			Headers *map[string]string `yaml:"headers"`
		} `yaml:"splitter"`
	}
	if err := yaml.Unmarshal(data, &probe); err == nil &&
		probe.Splitter != nil && probe.Splitter.Headers != nil {
		k.Splitter.Headers = nil
	}
	if err := decodeStrict(data, &k); err != nil {
		return err
	}
	c.KB = k
	return nil
}

func (c *Config) loadPolicy(file string, data []byte) error {
	switch strings.TrimSuffix(file, ".yaml") {
	case "permissions":
		var p decl.Policy
		if err := decodeStrict(data, &p); err != nil {
			return err
		}
		c.Policy.Deny = union(c.Policy.Deny, p.Deny)
		c.Policy.Ask = p.Ask
		return nil
	case "limits":
		var l decl.Limits
		if err := decodeStrict(data, &l); err != nil {
			return err
		}
		c.Limits = mergeLimits(c.Limits, l)
		return nil
	default:
		return fmt.Errorf("policy/ only accepts permissions.yaml and limits.yaml; unknown file %q", file)
	}
}

func mergeLimits(a, b decl.Limits) decl.Limits {
	return decl.Limits{
		Agent: decl.AgentLimit{MaxDepth: tighter(a.Agent.MaxDepth, b.Agent.MaxDepth)},
		Retry: decl.RetryLimit{MaxRetries: tighter(a.Retry.MaxRetries, b.Retry.MaxRetries)},
	}
}

func tighter(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, v := range append(append([]string{}, a...), b...) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

type Ref struct {
	ProviderID string
	ModelID    string
}

func (r Ref) String() string { return r.ProviderID + "/" + r.ModelID }

func ParseRef(s string) (Ref, error) {
	i := strings.Index(s, "/")
	if i < 0 {
		return Ref{}, fmt.Errorf("model ref %q is missing the provider prefix; expected <provider>/<model>", s)
	}
	if i == 0 || i == len(s)-1 {
		return Ref{}, fmt.Errorf("model ref %q has an empty provider or model", s)
	}
	return Ref{ProviderID: s[:i], ModelID: s[i+1:]}, nil
}

func ProviderIDOf(ref string) string {
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i]
	}
	return ref
}

func (c *Config) validate() error {
	if _, ok := c.Agents[DefaultAgentID]; !ok {
		return fmt.Errorf("agents/%s.md does not exist or could not be parsed", DefaultAgentID)
	}

	ids := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		a := c.Agents[id]
		ref, _ := ParseRef(a.ModelRef)
		if _, ok := c.Models[ref.String()]; !ok {
			where := a.File
			if where == "" {
				where = DirAgent + "/" + id + ".md"
			}
			return fmt.Errorf("agent %s (%s) references model %s which is not declared under llm/",
				id, where, a.ModelRef)
		}
	}

	if err := c.validateAgentGraph(ids); err != nil {
		return err
	}
	if err := c.validateKB(); err != nil {
		return err
	}
	return c.validateDepth(ids)
}

func (c *Config) validateKB() error {
	if c.KB.Corpus == "" {
		return nil
	}
	if filepath.IsAbs(c.KB.Corpus) {
		return fmt.Errorf("kb.corpus must be relative to the project root, got an absolute path %q; "+
			"the index and its corpus must have the same lifetime", c.KB.Corpus)
	}
	if c.KB.Model == "" {
		return fmt.Errorf("kb.model is required when kb.corpus is set; "+
			"it must reference a model declared with kind: embedding under %s/", DirLLM)
	}
	ref, _ := ParseRef(c.KB.Model)
	m, ok := c.Models[ref.String()]
	if !ok {
		return fmt.Errorf("kb.model %s is not declared under %s/", c.KB.Model, DirLLM)
	}
	if !m.IsEmbedding() {
		return fmt.Errorf("kb.model %s is not an embedding model; add `kind: embedding` to it "+
			"(a chat endpoint has no %s endpoint)", c.KB.Model, "/embeddings")
	}
	if m.Dimensions < 0 {
		return fmt.Errorf("kb.model %s declares dimensions: %d; it must be a positive count or "+
			"omitted (0 = unknown, learned at index time)", c.KB.Model, m.Dimensions)
	}
	return nil
}

func (c *Config) validateDepth(sorted []string) error {
	max := c.MaxDepth()
	for _, id := range sorted {
		d, ok := c.teamDepth(id)
		if !ok {
			continue
		}
		if d > max {
			return fmt.Errorf(
				"agent %s has a team nested to depth %d, hitting the cap of %d; "+
					"raise server/config.yaml agent.max_depth (or policy/limits.yaml agent.max_depth) "+
					"or reduce the subagents nesting",
				id, d, max)
		}
	}
	return nil
}

func (c *Config) teamDepth(id string) (int, bool) {
	if _, ok := c.Agents[id]; !ok {
		return 0, false
	}
	visited := map[string]bool{}
	queue := []struct {
		name  string
		depth int
	}{{id, 0}}
	deepest := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth > deepest {
			deepest = cur.depth
		}
		if visited[cur.name] {
			continue
		}
		visited[cur.name] = true
		if sub, ok := c.Agents[cur.name]; ok {
			for _, s := range sub.SubAgents {
				queue = append(queue, struct {
					name  string
					depth int
				}{s, cur.depth + 1})
			}
		}
	}
	return deepest, true
}

func (c *Config) MaxDepth() int {
	if c == nil {
		return decl.DefaultMaxDepth
	}
	return tighter(c.globalMaxDepth(), c.Limits.Agent.MaxDepth)
}

func (c *Config) MaxRetries() int {
	if c == nil {
		return decl.DefaultMaxRetries
	}
	return tighter(c.globalMaxRetries(), c.Limits.Retry.MaxRetries)
}

func (c *Config) globalMaxDepth() int {
	if c == nil || c.Server.Agent.MaxDepth <= 0 {
		return decl.DefaultMaxDepth
	}
	return c.Server.Agent.MaxDepth
}

func (c *Config) globalMaxRetries() int {
	if c == nil || c.Server.Retry.MaxRetries <= 0 {
		return decl.DefaultMaxRetries
	}
	return c.Server.Retry.MaxRetries
}

func (c *Config) collectNoopLimits() {
	if c == nil {
		return
	}
	if v := c.Limits.Agent.MaxDepth; v > c.globalMaxDepth() {
		c.deferKey("policy/limits.yaml agent.max_depth: %d is looser than the global baseline (%d); "+
			"it has no effect — raise server/config.yaml agent.max_depth to widen the limit",
			v, c.globalMaxDepth())
	}
	if v := c.Limits.Retry.MaxRetries; v > c.globalMaxRetries() {
		c.deferKey("policy/limits.yaml retry.max_retries: %d is looser than the global baseline (%d); "+
			"it has no effect — raise server/config.yaml retry.max_retries to widen the limit",
			v, c.globalMaxRetries())
	}
}

func (c *Config) validateAgentGraph(sorted []string) error {
	for _, id := range sorted {
		for _, sub := range c.Agents[id].SubAgents {
			if _, ok := c.Agents[sub]; !ok {
				known := make([]string, 0, len(c.Agents))
				for k := range c.Agents {
					known = append(known, k)
				}
				sort.Strings(known)
				return fmt.Errorf("agent %s subagents references unknown %q (declared: %s)",
					id, sub, strings.Join(known, ", "))
			}
		}
	}
	return c.detectCycle(sorted)
}

func (c *Config) detectCycle(sorted []string) error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string

	var visit func(string) error
	visit = func(id string) error {
		color[id] = grey
		path = append(path, id)
		for _, sub := range c.Agents[id].SubAgents {
			switch color[sub] {
			case grey:
				at := 0
				for i, p := range path {
					if p == sub {
						at = i
						break
					}
				}
				return fmt.Errorf("subagents contains a cycle: %s", strings.Join(append(append([]string{}, path[at:]...), sub), " → "))
			case white:
				if err := visit(sub); err != nil {
					return err
				}
			}
		}
		path = path[:len(path)-1]
		color[id] = black
		return nil
	}

	for _, id := range sorted {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

type mutation struct {
	Rel     string
	Delete  bool
	Content []byte
}
