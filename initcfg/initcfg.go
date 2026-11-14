package initcfg

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/templates"
	"github.com/ouranoshong/openheron/persist"
)

// initTmpSuffix is the temporary-file suffix of a direct write. It matches
// the one the configuration service uses, so a half-written file is never
// mistaken for a configuration file.
const initTmpSuffix = ".tmp"

type Options struct {
	Model    string
	Provider string
	BaseURL  string
	// NoTools leaves the tool configurations under tools/ unwritten.
	//
	// ★ The switch is **negative** on purpose: the zero value has to be the
	//   full tree, because the full tree is what a new configuration needs.
	//   A positive switch would make "the default" and "the complete
	//   configuration" two different things, and the first one is what a
	//   caller gets by forgetting to set a flag.
	//
	// ★ The tools are part of the tree, not an extra: an agent's `tools:`
	//   list names `request_get` / `duckduckgo_search` /
	//   `wikipedia_search`, and a name that no provider offers is reported
	//   as a typo.
	NoTools bool
	Force   bool
}

type Plan struct {
	Dir     string
	Files   []spec.File
	Skipped []string
	Notes   []string
	Model   string
	// EnvHint is the environment variable that holds the provider's key.
	//
	// ★ It is **derived** from the provider's name (see `spec.LLMEnvName`),
	//   not chosen: the file's name is the anchor of the whole secret
	//   naming scheme.
	EnvHint string
	// SecretHint is the keyring entry that holds the same key.
	//
	// ★ Both are named because the key has **two** places — the environment
	//   overrides the keyring, and the user needs to know which one is in
	//   force before they wonder why their export "did not work".
	SecretHint string
	force      bool

	// existing is the manifest's answer to "which files are there": the
	// same answer the settings editor gets, asked once for the whole plan.
	existing map[string]bool
}

func BuildPlan(svc *spec.Service, opts Options) (*Plan, error) {
	p := &Plan{Dir: svc.Root(), force: opts.Force}

	merged, readErr := svc.LoadUnchecked()
	if readErr != nil {
		return nil, fmt.Errorf("init: read the configuration: %w", readErr)
	}
	// ★ One manifest for the whole plan. It answers the two questions init
	//   asks — which files are already there (the skip list) and what a new
	//   file starts from (the content) — and the settings editor asks it the
	//   same ones. A second traversal of the templates package here is how
	//   `init` and "New file" drift apart.
	manifest, err := svc.Manifest(merged)
	if err != nil {
		return nil, fmt.Errorf("init: read the configuration tree: %w", err)
	}
	p.existing = make(map[string]bool, len(manifest.Items))
	for _, it := range manifest.Items {
		p.existing[it.Path] = it.Exists
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
		// ★ The whole partition, not a list of files: the templates package
		//   holds the files, and a new one comes along on its own.
		if err := copySection(p, manifest.Templates, spec.DirAgent, map[string]string{
			"model": choice.Ref,
		}); err != nil {
			return nil, err
		}
	}

	// ── skills ──────────────────────────────────────────────────────────
	//
	// ★ The skills are a **nested** partition — `skills/<name>/SKILL.md` —
	//   and they are written like the rest: an agent's `skills:` list names
	//   them, and a name that is not on disk is a skill nobody can load.
	if err := copySection(p, manifest.Templates, spec.DirSkills, nil); err != nil {
		return nil, err
	}

	// ── provider ────────────────────────────────────────────────────────
	if choice.NeedProvider {
		if svc.Has(spec.DirLLM, choice.Provider+".yaml") {
			return nil, fmt.Errorf(
				"init: llm/%s.yaml already exists and does not declare %s; writing it here would replace it wholesale — "+
					"pick a model that file already declares, or rename that file",
				choice.Provider+".yaml", choice.Model)
		}
		content, err := renderTemplate(choice.Template, map[string]string{
			"model":    choice.Model,
			"base_url": opts.BaseURL,
		}, nil)
		if err != nil {
			return nil, err
		}
		p.add("llm/"+choice.Provider+".yaml", content)
		p.EnvHint = choice.EnvName
		p.SecretHint = spec.LLMSecretUser(choice.Provider)
	} else {
		p.Notes = append(p.Notes, fmt.Sprintf(
			"model %q is already declared; it is reused as is", choice.Ref))
	}

	// ── tools ───────────────────────────────────────────────────────────
	//
	// ★ The tool configurations are part of the tree: the agents name
	//   `request_get` / `duckduckgo_search` / `wikipedia_search`, and a name
	//   that no provider offers is reported as a typo. So they are written
	//   unless the caller asks for them not to be.
	if opts.NoTools {
		p.Notes = append(p.Notes,
			"tools/ was skipped (--no-tools leaves the tool configurations unwritten)")
	} else {
		if err := copySection(p, manifest.Templates, spec.DirTools, nil); err != nil {
			return nil, err
		}
	}

	// ── mcps ────────────────────────────────────────────────────────────
	//
	// ★ The example is a **loadable** file: a file under mcps/ defines a
	//   server, so a file that cannot be parsed is not a server that is
	//   off — it is a configuration that does not load. Its command is a
	//   placeholder that is not installed: the server does not come up,
	//   the log says so, and the agent carries on.
	//
	// ★ The content is this package's own template file. The configuration
	//   editor hands out its own copy (the editor is served by the API,
	//   and the API cannot import this package — it would be a cycle),
	//   and a test pins the two against each other.
	if err := copySection(p, manifest.Templates, spec.DirMCP, nil); err != nil {
		return nil, err
	}

	// ── server ──────────────────────────────────────────────────────────
	//
	// ★ server/ is not hot-reloaded and the config editor may not write it,
	//   so init writes it directly (see Apply). The values are the code's own
	//   defaults: writing them changes nothing, it only makes them visible.
	//
	// ★ The exceptions are log.file and log.console: the code's defaults are
	//   an empty path and a copy on the standard error, and the template
	//   names a file and keeps stderr quiet — a new project's log lands on
	//   disk and nowhere else, instead of dying with the terminal it was
	//   started in.
	if err := copySection(p, manifest.Templates, spec.DirServer, nil); err != nil {
		return nil, err
	}
	p.Notes = append(p.Notes,
		"server/ is not hot-reloaded — it configures the process, so it takes effect on the next start")

	return p, nil
}

// copySection copies one partition's templates into the plan.
//
// ★ The **manifest** decides which files a partition holds; this function
//
//	decides what to do with them. Adding a template to the templates
//	package must not mean adding a name here.
//
// ★ The existence check is p.add's: a file that is already there is
//
//	skipped and reported, never overwritten.
//
// ★ The llm/ partition is the one that does not come here: its files are
//
//	alternatives — the provider's own template and the generic fallback —
//	and a partition of alternatives is not a partition to copy, which is
//	why the manifest carries no llm/ templates at all.
func copySection(p *Plan, files map[string]string, dir string,
	placeholders map[string]string) error {

	prefix := dir + "/"
	rels := make([]string, 0, len(files))
	for rel := range files {
		if strings.HasPrefix(rel, prefix) {
			rels = append(rels, rel)
		}
	}
	if len(rels) == 0 {
		// ★ Loud, not silent: a partition that ships no template would
		//   otherwise be a directory init quietly does not create, and the
		//   first symptom would be a configuration that does not load.
		return fmt.Errorf("init: the manifest ships no template for %s/", dir)
	}
	slices.Sort(rels)
	for _, rel := range rels {
		content, err := renderOne(rel, files[rel], placeholders, nil)
		if err != nil {
			return err
		}
		p.add(rel, content)
	}
	return nil
}

func (p *Plan) Apply(svc *spec.Service) error {
	if len(p.Files) == 0 {
		return nil
	}
	// ★ server/ is not hot-reloaded, so the config editor refuses to write it
	//   (spec.ValidatePath rejects the whole section). init is not the editor:
	//   it creates the file before the process starts, so it writes it
	//   directly. The direct write happens **after** the merge of everything
	//   else has been checked, so a rejected configuration leaves nothing
	//   behind.
	//
	// ★ The MCP example is direct for the same kind of reason: its name
	//   says it is not a configuration yet, and the editor only accepts
	//   <server>.yaml under mcps/.
	var editable, direct []spec.File
	for _, f := range p.Files {
		if isDirectWrite(f) {
			direct = append(direct, f)
			continue
		}
		editable = append(editable, f)
	}
	if len(editable) > 0 {
		if _, _, err := svc.WriteMany(editable, ""); err != nil {
			return err
		}
	}
	for _, f := range direct {
		dst := filepath.Join(svc.Root(), filepath.FromSlash(f.Rel))
		if err := persist.AtomicWrite(dst, f.Content, initTmpSuffix); err != nil {
			return err
		}
	}
	return nil
}

// isDirectWrite reports whether a file bypasses the configuration editor.
//
// ★ Only the files under server/ do: that section is not hot-reloaded,
//
//	and the editor refuses to write it.
//
// ★ Everything else goes through the editor — the MCP example included.
//
//	A file under mcps/ has to **load**, and the editor's trial load is
//	what proves it does.
func isDirectWrite(f spec.File) bool {
	return path.Dir(f.Rel) == spec.DirServer
}

func (p *Plan) add(rel string, content []byte) {
	if p.existing[rel] && !p.force {
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
		if err := c.fillTemplate(); err != nil {
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
	if err := c.fillTemplate(); err != nil {
		return choice{}, err
	}
	if c.Model == "" {
		return choice{}, fmt.Errorf("init: --model is required for provider %q: it has no built-in default model", provider)
	}
	c.Ref = provider + "/" + c.Model
	return c, nil
}

func (c *choice) fillTemplate() error {
	name, err := templateFor(c.Provider)
	if err != nil {
		return err
	}
	c.Template = name
	raw, err := templates.File(name)
	if err != nil {
		return fmt.Errorf("init: read template %s: %w", name, err)
	}
	if c.Model == "" {
		c.Model = templates.Default(string(raw), "model")
	}
	// ★ The variable name is **derived**, not chosen: the provider's name is
	//   the file's name, and the name says where the key comes from. There is
	//   no flag for a different one — a second spelling of the same fact is
	//   a second thing to get wrong.
	c.EnvName = spec.LLMEnvName(c.Provider)
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

const defaultProvider = "deepseek"

func providerDefaultModel(provider string) string {
	name, err := templateFor(provider)
	if err != nil {
		return ""
	}
	raw, err := templates.File(name)
	if err != nil {
		return ""
	}
	return templates.Default(string(raw), "model")
}

func templateFor(provider string) (string, error) {
	name := "llm/" + provider + ".yaml"
	if templates.Has(name) {
		return name, nil
	}
	if templates.Has("llm/_generic.yaml") {
		return "llm/_generic.yaml", nil
	}
	return "", fmt.Errorf("init: no provider template for %q", provider)
}

// renderTemplate renders one template file.
//
// ★ The rendering itself lives in the templates package — the placeholders
//
//	and the way a value is written into a file are the package's business,
//	and the editor's scaffolds go through the same rules.
func renderTemplate(rel string, values, fallbacks map[string]string) ([]byte, error) {
	raw, err := templates.File(rel)
	if err != nil {
		return nil, fmt.Errorf("init: read template %s: %w", rel, err)
	}
	return renderOne(rel, raw, values, fallbacks)
}

// renderOne substitutes the placeholders in one template already in hand.
//
// ★ The content comes from the caller — the manifest for a partition, the
//
//	templates package for an llm/ provider — and only the substitution rules
//	are shared. Those are the ones that have to agree: how a value is
//	written into a file is not a decision two callers may take separately.
func renderOne(rel, raw string, values, fallbacks map[string]string) ([]byte, error) {
	out, err := templates.Render(raw, values, fallbacks)
	if err != nil {
		return nil, fmt.Errorf("init: template %s: %w", rel, err)
	}
	return []byte(out), nil
}
