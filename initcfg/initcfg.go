package initcfg

import (
	"fmt"
	"maps"
	"os"
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
	Env      string
	// Tools writes the tool configurations under tools/.
	//
	// ★ A provider whose configuration is absent mounts nothing, so writing
	//   the file is what turns the tool on — which makes this opt-in: a
	//   minimal runnable configuration should not open an outbound channel
	//   on its own.
	Tools bool
	Force bool
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
		// ★ The whole partition, not a list of files: the templates package
		//   holds the files, and a new one comes along on its own.
		if err := copySection(p, spec.DirAgent, map[string]string{
			"model": choice.Ref,
		}); err != nil {
			return nil, err
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
	//
	// ★ init does not write the policy/ section at all, and the reason is
	//   that both of its files would say nothing: `permissions.yaml` lists
	//   exactly the tiers the code already defaults to, and `limits.yaml`
	//   writes 0, which means "no limit — fall back to the global value".
	//   ★ A file that says what the code already does is noise, and noise
	//   in a configuration tree is read as meaning.
	//
	//   The partition stays: a user who wants a tier of their own writes
	//   policy/permissions.yaml by hand, and the configuration editor
	//   scaffolds it.

	// ── tools ───────────────────────────────────────────────────────────
	//
	// ★ A provider whose configuration is absent mounts nothing, so init does
	//   not write them on its own — which tools exist is the user's call.
	if !opts.Tools {
		p.Notes = append(p.Notes, "tools/ was skipped (--tools writes the tool configurations)")
	} else {
		if err := copySection(p, spec.DirTools, nil); err != nil {
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
	if err := copySection(p, spec.DirMCP, nil); err != nil {
		return nil, err
	}

	// ── server ──────────────────────────────────────────────────────────
	//
	// ★ server/ is not hot-reloaded and the config editor may not write it,
	//   so init writes it directly (see Apply). The values are the code's own
	//   defaults: writing them changes nothing, it only makes them visible.
	if err := copySection(p, spec.DirServer, nil); err != nil {
		return nil, err
	}
	p.Notes = append(p.Notes,
		"server/ is not hot-reloaded — it configures the process, so it takes effect on the next start")

	return p, nil
}

// copySection copies one partition's templates into the plan.
//
// ★ The **templates package** decides which files a partition holds; this
//
//	function decides what to do with them. ★ A file name is the template's
//	business: adding a template to the package must not mean adding a
//	name here.
//
// ★ The existence check is p.add's: a file that is already there is
//
//	skipped and reported, never overwritten.
//
// ★ The llm/ partition is the one that does not come here: its files are
//
//	alternatives — the provider's own template and the generic fallback —
//	and a partition of alternatives is not a partition to copy.
func copySection(p *Plan, dir string, placeholders map[string]string) error {
	files, err := templates.Dir(dir)
	if err != nil {
		return err
	}
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		content, err := renderTemplate(rel, placeholders, nil)
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
	raw, err := templates.File(name)
	if err != nil {
		return fmt.Errorf("init: read template %s: %w", name, err)
	}
	if c.Model == "" {
		c.Model = templates.Default(string(raw), "model")
	}
	c.EnvName = templates.Default(string(raw), "api_key_env")
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
	out, err := templates.Render(raw, values, fallbacks)
	if err != nil {
		return nil, fmt.Errorf("init: template %s: %w", rel, err)
	}
	return []byte(out), nil
}
