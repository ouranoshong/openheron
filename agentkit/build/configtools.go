package build

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// configTool is the shape every config_* tool has: a declared name, a
// declared schema, and one function that answers.
//
// ★ It exists so the six tools read as six rows instead of six types. What
// makes them worth having is that they all answer from the **same** place —
// `spec.Service` and the tier table — and that is easier to see when the
// boilerplate is shared.
type configTool struct {
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
	run    func(ctx context.Context, args string) (any, error)
}

func (t *configTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        t.name,
		Desc:        t.desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(t.params),
	}, nil
}

func (t *configTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	if args == "" {
		args = "{}"
	}
	out, err := t.run(ctx, args)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("%s: marshal the answer: %w", t.name, err)
	}
	return string(b), nil
}

type configToolProvider struct{}

func (configToolProvider) ID() string { return "config" }

// Optional is false: a configuration surface that silently disappears is
// worse than one that fails to build. The provider returns no tools when
// the service was not wired, which is a different thing from failing.
func (configToolProvider) Optional() bool { return false }

func (configToolProvider) Provide(_ context.Context, env toolEnv) ([]tool.BaseTool, error) {
	// ★ No service, no tools. The registry is built in tests without one,
	//   and a tool that panics on first use is worse than an absent tool.
	if env.cfg == nil || env.svc == nil {
		return nil, nil
	}
	svc := env.svc
	cfg := env.cfg
	policy := policyOf(cfg)

	get := &configTool{
		name: decl.ToolNameConfigGet,
		desc: "Read the configuration. With a path, return that file's text and revision. " +
			"Without one, return the partition table, the files that exist, and a summary " +
			"of what the running process holds. Paths are relative to the config root " +
			"(agents/main.md, tools/http.yaml).",
		params: map[string]*schema.ParameterInfo{
			"path": {Type: schema.String, Desc: "relative path, e.g. agents/main.md; omit for the overview"},
		},
		run: func(_ context.Context, args string) (any, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return nil, fmt.Errorf("%s: %w", decl.ToolNameConfigGet, err)
			}
			if in.Path == "" {
				return configOverview(svc, cfg)
			}
			// ★ The read gate, not the write gate: a partition the agent may
			//   not edit is exactly the one it needs to read — the answer to
			//   "what am I actually running with" lives in server/.
			cat, err := spec.ValidateReadPath(in.Path)
			if err != nil {
				return nil, err
			}
			content, rev, err := svc.ReadFile(in.Path)
			if err != nil {
				return nil, err
			}
			if len(content) > maxReadableConfigBytes {
				return nil, fmt.Errorf("%s is %d bytes; this tool hands back at most %d. "+
					"Edit it in the settings editor, or read the part you need with read_file",
					in.Path, len(content), maxReadableConfigBytes)
			}
			return struct {
				Path     string `json:"path"`
				Exists   bool   `json:"exists"`
				Hot      bool   `json:"hot"`
				Revision string `json:"revision,omitempty"`
				Content  string `json:"content"`
			}{in.Path, rev != "", cat.Hot(), rev, string(content)}, nil
		},
	}

	validate := &configTool{
		name: decl.ToolNameConfigValidate,
		desc: "Check a file against the tree without writing it: pass path and content, and get " +
			"back the issues the loader would report, each with the line it is on. " +
			"Call it with no arguments to check the tree as it is on disk.",
		params: map[string]*schema.ParameterInfo{
			"path":    {Type: schema.String, Desc: "relative path of the file the content would be written to"},
			"content": {Type: schema.String, Desc: "the file's text; omit to check the tree on disk"},
		},
		run: func(_ context.Context, args string) (any, error) {
			var in struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return nil, fmt.Errorf("%s: %w", decl.ToolNameConfigValidate, err)
			}
			if in.Path == "" {
				return svc.Check(), nil
			}
			return svc.Diagnostics(in.Path, []byte(in.Content))
		},
	}

	template := &configTool{
		name: decl.ToolNameConfigTemplate,
		desc: "Return the skeleton a new configuration file starts from. With a path, the " +
			"skeleton for that one file. With a section, that partition's templates. With " +
			"neither, what can be asked for.",
		params: map[string]*schema.ParameterInfo{
			"path":    {Type: schema.String, Desc: `relative path, e.g. agents/researcher.md`},
			"section": {Type: schema.String, Desc: `a partition, e.g. tools, mcps, agents`},
		},
		run: func(_ context.Context, args string) (any, error) {
			var in struct {
				Path    string `json:"path"`
				Section string `json:"section"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return nil, fmt.Errorf("%s: %w", decl.ToolNameConfigTemplate, err)
			}
			switch {
			case in.Path != "":
				content, err := spec.TemplateFor(in.Path, cfg.ModelRefs())
				if err != nil {
					return nil, err
				}
				return struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				}{in.Path, content}, nil
			case in.Section != "":
				cat, ok := spec.CategoryByDir(in.Section)
				if !ok {
					return nil, fmt.Errorf("unknown config section %q", in.Section)
				}
				templates := spec.Templates(cat.Dir)
				// ★ The section's own skeleton is in the answer under its
				//   directory key, exactly as the manifest carries it: a
				//   caller that has read one has read the other.
				templates[cat.Dir] = spec.Skeleton(cat.Dir, cfg.ModelRefs())
				return struct {
					Section   string            `json:"section"`
					Templates map[string]string `json:"templates"`
				}{in.Section, templates}, nil
			default:
				// ★ Not every template at once. The whole set is tens of
				//   kilobytes, and a model handed all of it has to find the
				//   one it wanted anyway — an index is the answer to "what
				//   can I ask for".
				type section struct {
					ID    string   `json:"id"`
					Paths []string `json:"paths"`
				}
				out := make([]section, 0, len(spec.Categories))
				for _, cat := range spec.Categories {
					names := make([]string, 0, len(spec.Templates(cat.Dir)))
					for rel := range spec.Templates(cat.Dir) {
						names = append(names, rel)
					}
					sort.Strings(names)
					out = append(out, section{cat.Dir, names})
				}
				return struct {
					Sections []section `json:"sections"`
				}{out}, nil
			}
		},
	}

	notices := &configTool{
		name: decl.ToolNameConfigNotices,
		desc: "Return what the loader noticed but did not refuse: renamed keys, retired " +
			"directories, values that fell back to a default. These are the answers to " +
			"\"did my change do what I meant\".",
		params: nil,
		run: func(_ context.Context, _ string) (any, error) {
			return struct {
				Deferred      []string `json:"deferred"`
				Version       string   `json:"version"`
				ConfigVersion string   `json:"configVersion"`
				Watch         bool     `json:"watch"`
			}{orEmpty(cfg.Deferred()), cfg.Version, svc.Fingerprint(), watchEnabled(cfg)}, nil
		},
	}

	tier := &configTool{
		name: decl.ToolNameConfigTier,
		desc: "Explain approval tiers. With a tool name, its tier (allow or ask). With an " +
			"agent id, that agent's tools and the tier of each. With neither, the default " +
			"tier and the whole exception list.",
		params: map[string]*schema.ParameterInfo{
			"tool":  {Type: schema.String, Desc: "a tool name, e.g. write_file"},
			"agent": {Type: schema.String, Desc: "an agent id, e.g. main"},
		},
		run: func(_ context.Context, args string) (any, error) {
			var in struct {
				Tool  string `json:"tool"`
				Agent string `json:"agent"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return nil, fmt.Errorf("%s: %w", decl.ToolNameConfigTier, err)
			}
			switch {
			case in.Tool != "":
				return struct {
					Tool string `json:"tool"`
					Tier Tier   `json:"tier"`
				}{in.Tool, policy.TierOf(in.Tool)}, nil
			case in.Agent != "":
				a, ok := cfg.Agents[in.Agent]
				if !ok {
					return nil, fmt.Errorf("no agent %q under agents/", in.Agent)
				}
				type row struct {
					Name string `json:"name"`
					Tier Tier   `json:"tier"`
				}
				rows := make([]row, 0, len(a.Tools))
				for _, name := range a.Tools {
					rows = append(rows, row{name, policy.TierOf(name)})
				}
				return struct {
					Agent string `json:"agent"`
					Tools []row  `json:"tools"`
				}{in.Agent, rows}, nil
			default:
				allow := append([]string(nil), policy.Allow...)
				sort.Strings(allow)
				return struct {
					DefaultTier Tier     `json:"defaultTier"`
					Allow       []string `json:"allow"`
				}{TierAsk, allow}, nil
			}
		},
	}

	write := &configTool{
		name: decl.ToolNameConfigWrite,
		desc: "Write a configuration file. The write is checked before it lands: it must parse, " +
			"the tree must still hold together, and the base revision must match what is on " +
			"disk. A change takes effect on the next turn. Always asks for approval.",
		params: map[string]*schema.ParameterInfo{
			"path":         {Type: schema.String, Desc: "relative path, e.g. agents/main.md", Required: true},
			"content":      {Type: schema.String, Desc: "the file's full new text", Required: true},
			"baseRevision": {Type: schema.String, Desc: "the revision config_get returned; omit to overwrite whatever is there"},
		},
		run: func(_ context.Context, args string) (any, error) {
			var in struct {
				Path         string `json:"path"`
				Content      string `json:"content"`
				BaseRevision string `json:"baseRevision"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return nil, fmt.Errorf("%s: %w", decl.ToolNameConfigWrite, err)
			}
			if in.Path == "" {
				return nil, fmt.Errorf("%s: path is required", decl.ToolNameConfigWrite)
			}
			rev, _, err := svc.Write(in.Path, []byte(in.Content), in.BaseRevision)
			if err != nil {
				return nil, err
			}
			out := struct {
				Path            string `json:"path"`
				Revision        string `json:"revision"`
				ConfigVersion   string `json:"configVersion"`
				Applied         bool   `json:"applied"`
				RequiresRestart bool   `json:"requiresRestart,omitempty"`
				Note            string `json:"note,omitempty"`
			}{Path: in.Path, Revision: rev, ConfigVersion: svc.Fingerprint()}
			hot := true
			if cat, err := spec.ValidatePath(in.Path); err == nil {
				hot = cat.Hot()
			}
			out.Applied = hot && watchEnabled(cfg)
			if !out.Applied {
				out.RequiresRestart = true
				if hot {
					out.Note = "hot reload is disabled; restart openheron to apply"
				} else {
					out.Note = "this section is not hot-reloaded; restart openheron to apply"
				}
			}
			return out, nil
		},
	}

	// ★ Not wrapped in capTool: that cap truncates the **string**, which for a
	//   JSON answer leaves a document that does not parse. The tools bound
	//   their own answers instead — config_get refuses a file it cannot hand
	//   back whole, and config_template hands out one section at a time.
	var out []tool.BaseTool
	for _, t := range []*configTool{get, validate, template, notices, tier, write} {
		out = append(out, t)
	}
	return out, nil
}

// maxReadableConfigBytes is the largest file config_get hands back.
//
// ★ It **refuses** rather than truncates. A tool that returns half a file is
//
//	a tool that invites the model to edit the half it got and write it back:
//	the base revision would still match, because the revision it was given
//	was the revision of the truncated text.
const maxReadableConfigBytes = 64 * 1024

// configOverview is `config_get` with no path: what exists, and what the
// running process holds.
//
// ★ The templates are **not** here. They are the largest part of the
// manifest and the least often needed — a file's skeleton matters when one
// is being created, which is config_template's question.
func configOverview(svc *spec.Service, cfg *spec.Config) (any, error) {
	m, err := svc.Manifest(cfg)
	if err != nil {
		return nil, err
	}

	out := configOverviewAnswer{ConfigVersion: m.ConfigVersion}
	out.Categories = make([]configOverviewCategory, 0, len(m.Categories))
	for _, c := range m.Categories {
		out.Categories = append(out.Categories, configOverviewCategory{c.ID, c.Editable, c.Hot})
	}
	out.Items = make([]configOverviewItem, 0, len(m.Items))
	for _, it := range m.Items {
		out.Items = append(out.Items, configOverviewItem{it.Path, it.Hot, it.Revision})
	}

	mcps := make([]string, 0, len(cfg.MCPs))
	for name := range cfg.MCPs {
		mcps = append(mcps, name)
	}
	sort.Strings(mcps)

	// ★ The summary is the **running** configuration, not the tree on disk:
	//   the two differ for as long as a hot change has not been picked up,
	//   and "what the process holds" is the question a caller can act on.
	out.Effective = configOverviewEffective{
		DefaultAgent: cfg.DefaultAgent(),
		Agents:       cfg.AgentNames(),
		Models:       cfg.ModelRefs(),
		ProviderTool: cfg.EnabledToolNames(),
		MCPs:         mcps,
		Listen:       cfg.Server.ListenAddr,
		Watch:        watchEnabled(cfg),
	}
	return out, nil
}

type configOverviewAnswer struct {
	ConfigVersion string                   `json:"configVersion"`
	Categories    []configOverviewCategory `json:"categories"`
	Items         []configOverviewItem     `json:"items"`
	Effective     configOverviewEffective  `json:"effective"`
}

type configOverviewCategory struct {
	ID       string `json:"id"`
	Editable bool   `json:"editable"`
	Hot      bool   `json:"hot"`
}

type configOverviewItem struct {
	Path     string `json:"path"`
	Hot      bool   `json:"hot"`
	Revision string `json:"revision,omitempty"`
}

type configOverviewEffective struct {
	DefaultAgent string   `json:"defaultAgent"`
	Agents       []string `json:"agents"`
	Models       []string `json:"models"`
	ProviderTool []string `json:"providerTools"`
	MCPs         []string `json:"mcps"`
	Listen       string   `json:"listen"`
	Watch        bool     `json:"watch"`
}

func watchEnabled(cfg *spec.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.WatchEnabled()
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
