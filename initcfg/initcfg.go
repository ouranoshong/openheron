package initcfg

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

//go:embed all:templates
var templates embed.FS

const templatesRoot = "templates"

type Options struct {
	Model    string
	Provider string
	BaseURL  string
	Env      string
	NoPolicy bool
	Force    bool
}

type Plan struct {
	Dir     string
	Files   []spec.File
	Skipped []string
	Notes   []string
	Model   string
	EnvHint string
	force   bool
}

func BuildPlan(svc *spec.Service, opts Options) (*Plan, error) {
	p := &Plan{Dir: svc.Root(), force: opts.Force}

	merged, readErr := svc.LoadUnchecked()
	if readErr != nil {
		return nil, fmt.Errorf("init: read the configuration: %w", readErr)
	}
	if _, err := svc.Load(); err != nil {
		if merged.Agents[spec.DefaultAgentID] != nil {
			p.Notes = append(p.Notes,
				fmt.Sprintf("the current configuration does not load (%v); the files below are the ones that are clearly missing", err))
		}
	}

	choice, err := chooseModel(merged, opts)
	if err != nil {
		return nil, err
	}
	p.Model = choice.Ref

	// ── agent ────────────────────────────────────────────────────────────
	if merged != nil && merged.Agents[spec.DefaultAgentID] != nil {
		p.Notes = append(p.Notes, fmt.Sprintf(
			"agent %q is already declared; it is kept as is", spec.DefaultAgentID))
	} else {
		for _, rel := range []string{"agents/main.md", "agents/task.md"} {
			content, err := renderTemplate(rel, map[string]string{
				"model": choice.Ref,
			}, nil)
			if err != nil {
				return nil, err
			}
			p.add(rel, content)
		}
	}

	// ── provider ────────────────────────────────────────────────────────
	if choice.NeedProvider {
		exists, err := rootHas(svc, spec.DirLLM, choice.Provider+".yaml")
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf(
				"init: llm/%s.yaml already exists and does not declare %s; writing it here would replace it wholesale — "+
					"pick a model that file already declares, or rename that file",
				choice.Provider+".yaml", choice.Model)
		}
		content, err := renderTemplate(choice.Template, map[string]string{
			"model":       choice.Model,
			"base_url":    opts.BaseURL,
			"api_key_env": opts.Env,
		}, map[string]string{
			"api_key_env": defaultEnvName(choice.Provider),
		})
		if err != nil {
			return nil, err
		}
		p.add("llm/"+choice.Provider+".yaml", content)
		p.EnvHint = choice.EnvName
	} else {
		p.Notes = append(p.Notes, fmt.Sprintf(
			"model %q is already declared; it is reused as is", choice.Ref))
	}

	// ── policy ──────────────────────────────────────────────────────────
	if opts.NoPolicy {
		p.Notes = append(p.Notes, "policy/permissions.yaml was skipped (--no-policy)")
	} else {
		has, err := rootHas(svc, spec.DirPolicy, "permissions.yaml")
		if err != nil {
			return nil, err
		}
		if has {
			p.Notes = append(p.Notes,
				"policy/permissions.yaml is already declared; it is kept as is")
		} else {
			content, err := renderTemplate("policy/permissions.yaml", nil, nil)
			if err != nil {
				return nil, err
			}
			p.add("policy/permissions.yaml", content)
		}
	}

	return p, nil
}

func (p *Plan) Apply(svc *spec.Service) error {
	if len(p.Files) == 0 {
		return nil
	}
	_, _, err := svc.WriteMany(p.Files, "")
	return err
}

func (p *Plan) add(rel string, content []byte) {
	dst := filepath.Join(p.Dir, filepath.FromSlash(rel))
	if _, err := os.Stat(dst); err == nil && !p.force {
		p.Skipped = append(p.Skipped, rel)
		return
	}
	p.Files = append(p.Files, spec.File{Rel: rel, Content: content})
}

type choice struct {
	Ref          string
	Provider     string
	Model        string
	Template     string
	NeedProvider bool
	EnvName      string
}

func chooseModel(merged *spec.Config, opts Options) (choice, error) {
	if opts.Model != "" {
		r, err := spec.ParseRef(opts.Model)
		if err != nil {
			return choice{}, fmt.Errorf("init: invalid --model %q: %w", opts.Model, err)
		}
		if opts.Provider != "" && opts.Provider != r.ProviderID {
			return choice{}, fmt.Errorf(
				"init: --model %q and --provider %q disagree; give one or the other", opts.Model, opts.Provider)
		}
		c := choice{Ref: r.String(), Provider: r.ProviderID, Model: r.ModelID}
		if merged != nil {
			if _, ok := merged.Models[c.Ref]; ok {
				return c, nil
			}
		}
		c.NeedProvider = true
		if err := c.fillTemplate(opts); err != nil {
			return choice{}, err
		}
		return c, nil
	}

	if merged != nil {
		if names := usableModels(merged); len(names) > 0 {
			r, err := spec.ParseRef(names[0])
			if err != nil {
				return choice{}, fmt.Errorf("init: parse model reference %q: %w", names[0], err)
			}
			return choice{Ref: names[0], Provider: r.ProviderID, Model: r.ModelID}, nil
		}
	}

	provider := opts.Provider
	if provider == "" {
		provider = defaultProvider
	}
	c := choice{Ref: provider + "/" + providerDefaultModel(provider), Provider: provider}
	c.NeedProvider = true
	if err := c.fillTemplate(opts); err != nil {
		return choice{}, err
	}
	if c.Model == "" {
		return choice{}, fmt.Errorf("init: --model is required for provider %q: it has no built-in default model", provider)
	}
	c.Ref = provider + "/" + c.Model
	return c, nil
}

func (c *choice) fillTemplate(opts Options) error {
	name, err := templateFor(c.Provider)
	if err != nil {
		return err
	}
	c.Template = name
	raw, err := templates.ReadFile(templatesRoot + "/" + name)
	if err != nil {
		return fmt.Errorf("init: read template %s: %w", name, err)
	}
	if c.Model == "" {
		c.Model = templateDefault(string(raw), "model")
	}
	c.EnvName = templateDefault(string(raw), "api_key_env")
	if c.EnvName == "" {
		c.EnvName = defaultEnvName(c.Provider)
	}
	if opts.Env != "" {
		c.EnvName = opts.Env
	}
	return nil
}

func usableModels(merged *spec.Config) []string {
	out := make([]string, 0, len(merged.Models))
	for name, m := range merged.Models {
		if m == nil || m.IsEmbedding() {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func rootHas(svc *spec.Service, dir, name string) (bool, error) {
	cat, ok := spec.CategoryByDir(dir)
	if !ok {
		return false, fmt.Errorf("init: unknown config section %q", dir)
	}
	names, err := svc.ListFiles(cat)
	if err != nil {
		return false, fmt.Errorf("init: list %s: %w", dir, err)
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

func defaultEnvName(provider string) string {
	return strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"
}

const defaultProvider = "deepseek"

func providerDefaultModel(provider string) string {
	name, err := templateFor(provider)
	if err != nil {
		return ""
	}
	raw, err := templates.ReadFile(templatesRoot + "/" + name)
	if err != nil {
		return ""
	}
	return templateDefault(string(raw), "model")
}

func templateFor(provider string) (string, error) {
	name := "llm/" + provider + ".yaml"
	if _, err := templates.ReadFile(templatesRoot + "/" + name); err == nil {
		return name, nil
	}
	if _, err := templates.ReadFile(templatesRoot + "/llm/_generic.yaml"); err == nil {
		return "llm/_generic.yaml", nil
	}
	return "", fmt.Errorf("init: no provider template for %q", provider)
}

func templateDefault(content, key string) string {
	for _, g := range phRe.FindAllStringSubmatch(content, -1) {
		if g[1] == key && g[2] != "" {
			return g[2]
		}
	}
	return ""
}

var phRe = regexp.MustCompile(`\{\{([a-z_]+)(?:\|([^}]*))?\}\}`)

var renderers = map[string]func(string) string{
	"model":       func(v string) string { return v },
	"base_url":    func(v string) string { return v },
	"api_key_env": func(v string) string { return "${" + v + "}" },
}

func renderTemplate(rel string, values, fallbacks map[string]string) ([]byte, error) {
	raw, err := templates.ReadFile(templatesRoot + "/" + rel)
	if err != nil {
		return nil, fmt.Errorf("init: read template %s: %w", rel, err)
	}
	if values == nil {
		values = map[string]string{}
	}
	if fallbacks == nil {
		fallbacks = map[string]string{}
	}
	var missing []string
	out := phRe.ReplaceAllFunc(raw, func(m []byte) []byte {
		g := phRe.FindSubmatch(m)
		key := string(g[1])
		v := values[key]
		if v == "" {
			v = string(g[2])
		}
		if v == "" {
			v = fallbacks[key]
		}
		render, ok := renderers[key]
		if v == "" || !ok {
			missing = append(missing, key)
			return m
		}
		return []byte(render(v))
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("init: template %s needs a value for %q; pass the matching flag", rel, missing[0])
	}
	return out, nil
}
