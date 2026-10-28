// Package initcfg 算出「一个目录成为可运行的 openheron 项目」还缺哪些配置文件。
//
// ★ 它**只算**（Plan），不落盘；落盘走 [Plan.Apply]，而落盘本身走
//
//	config 的写入门（试合并 → 落盘 → 重算指纹）。绕开写入门就等于
//	把「写完能跑」这个保证扔了。
//
// ★ 头号原则：**只补确实缺的东西**。同名文件是**整体替换**的，
//
//	所以「顺手写一份」等于把配置根里那份悄悄换掉 ——
//	密钥被占位符替掉、收紧过的策略被出厂默认放宽，而且没有任何东西会响。
//	见 docs/init-design.md。
package initcfg

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"openheron/agentkit/spec"
)

// templates 是出厂模板集。
//
// ★ 用 `all:` 是因为通用模板的文件名以 `_` 开头（`_generic.yaml`），
//
//	而 go:embed 默认**跳过**以 `_` 或 `.` 开头的文件 —— 漏掉它就是
//	「自定义 provider 找不到模板」这种看不懂的报错。
//
//go:embed all:templates
var templates embed.FS

// templatesRoot 是模板在 embed FS 里的根目录。
const templatesRoot = "templates"

// Options 是 init 的参数。
type Options struct {
	// Model 是 "<provider>/<model>"；空 = 自动挑（已有的就复用，没有用模板默认）。
	Model string
	// Provider 是 provider 模板名；只在没给 Model 时用。
	Provider string
	// BaseURL 覆盖模板里的端点。
	BaseURL string
	// Env 覆盖模板里的 api_key 环境变量名。
	Env string
	// NoPolicy = 不写 policy/permissions.yaml。
	NoPolicy bool
	// Force = 覆盖已存在的目标文件（默认只补缺的）。
	Force bool
}

// Plan 是一次 init 的结果：打算写什么、为什么。
type Plan struct {
	// Dir 是配置根目录（<项目根>/.openheron）。
	Dir string
	// Files 是要写的文件。
	Files []spec.File
	// Skipped 是因为已存在而跳过的（相对路径）。
	Skipped []string
	// Notes 是要报给用户的话。
	Notes []string
	// Model 是这份配置最终用的模型引用。
	Model string
	// EnvHint 是 api_key 的环境变量名（空 = 复用了已有的模型，不用设）。
	EnvHint string
	// force 是「覆盖已存在的目标文件」。
	force bool
}

// BuildPlan 算出「要写什么」。
//
// ★ 它**只读**：不写任何文件。落盘是 [Plan.Apply] 的事。
//
// ★ 合并配置加载失败时**不立刻报错**：那正是「配置坏着」的情形，
//
//	而 init 能做的事（补一个缺的 provider）也许正好能修好它。
//	真正的判断交给写入门那一次试合并 —— 它报出来的才是真原因。
func BuildPlan(svc *spec.Service, opts Options) (*Plan, error) {
	// ★ 落点是**生效的那个配置根**（[spec.Service.Root]），不是「项目根 +
	//	.openheron」—— init 写完就该被下一轮运行读到，写到别处等于白写。
	p := &Plan{Dir: svc.Root(), force: opts.Force}

	// ★ 读「磁盘上现在有什么」要用**不校验**的那条路：新项目没有 main agent
	//	是正常的，而那会让校验整个失败 —— 于是「磁盘上已经有什么」
	//	（已经声明了哪些模型）就再也看不见了。
	merged, readErr := svc.LoadUnchecked()
	if readErr != nil {
		return nil, fmt.Errorf("init: read the configuration: %w", readErr)
	}
	// 当前配置能不能跑 —— 跑不了要说一声（但不妨碍 init 补文件）。
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
	//
	// ★ 配置根里已有 main ⇒ 不写：已经有了，写一份会把它的内容整体替换掉。
	//
	// ★ 出厂的两个 agent 是**一套**，不是两个独立的文件：main 的
	//
	//	`subagents: [task]` 引用 task，而引用一个不存在的 agent 是
	//	**启动期硬错误**（config/load.go：references unknown agent）。
	//	所以要么两个都写，要么两个都不写 —— 补一半会得到起不来的配置。
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
		// ★ 配置根里已有同名 provider ⇒ **不能写**：那份文件里是用户自己的
		//	端点与密钥，写下去等于把它们悄悄换掉。
		//
		//	走到这一步说明「用户点名的模型」那份 provider 并没声明 ——
		//	写模板是唯一的「满足」办法，而它会毁掉用户那份。所以这是**错误**，
		//	不是「跳过」：跳过的话，用户要的模型压根不在配置里。
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
	// ★ 配置根里已有 policy ⇒ 不写：ask 是**整体替换**语义，写一份出厂默认
	//	会把用户收紧过的东西放宽。
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

// Apply 把这份计划落盘。
func (p *Plan) Apply(svc *spec.Service) error {
	if len(p.Files) == 0 {
		return nil
	}
	_, _, err := svc.WriteMany(p.Files, "")
	return err
}

// add 记下一个待写文件；若目标已存在则跳过（除非 Force）。
func (p *Plan) add(rel string, content []byte) {
	dst := filepath.Join(p.Dir, filepath.FromSlash(rel))
	if _, err := os.Stat(dst); err == nil && !p.force {
		p.Skipped = append(p.Skipped, rel)
		return
	}
	p.Files = append(p.Files, spec.File{Rel: rel, Content: content})
}

// choice 是选模型的结果。
type choice struct {
	// Ref 是 "<provider>/<model>"。
	Ref string
	// Provider 是 provider id（文件名，不含扩展名）。
	Provider string
	// Model 是模型 id。
	Model string
	// Template 是要用的模板（相对 templates 根）。
	Template string
	// NeedProvider 为 true 表示要新建 provider 文件。
	NeedProvider bool
	// EnvName 是 api_key 的环境变量名。
	EnvName string
}

// chooseModel 决定用哪个模型，以及要不要新建 provider 文件。
//
// ★ 判据是**按引用解析**，不是按文件名 —— 「文件同名」与「需要的东西相同」
//
//	不是一回事：配置根里有 llm/openai.yaml，但它声明的模型可能是另一个。
func chooseModel(merged *spec.Config, opts Options) (choice, error) {
	// ── 显式给了模型 ────────────────────────────────────────────────────
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
				return c, nil // 已声明 ⇒ 复用，不写 provider
			}
		}
		c.NeedProvider = true
		if err := c.fillTemplate(opts); err != nil {
			return choice{}, err
		}
		return c, nil
	}

	// ── 自动：已有能用的模型就复用 ──────────────────────────────────────
	if merged != nil {
		if names := usableModels(merged); len(names) > 0 {
			r, err := spec.ParseRef(names[0])
			if err != nil {
				return choice{}, fmt.Errorf("init: parse model reference %q: %w", names[0], err)
			}
			return choice{Ref: names[0], Provider: r.ProviderID, Model: r.ModelID}, nil
		}
	}

	// ── 什么都没有 ⇒ 用模板集的默认 ──────────────────────────────────────
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

// fillTemplate 决定用哪个模板，并把模型 id 定下来。
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
	// ★ 模板里的默认值就在**文件里** —— 厂商知识不进 Go 代码。
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

// usableModels 列出能当聊天模型用的模型引用（排除 embedding）。
//
// ★ 排除 embedding 是必须的：拿一个 embedding 模型去聊天会在几百次调用后
//
//	才炸，而它**根本不是**聊天模型。
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

// fileExists 报文件在不在。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// rootHas 报**生效的配置根**里有没有这个文件。
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

// defaultEnvName 给 provider 名的环境变量约定。
func defaultEnvName(provider string) string {
	return strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"
}

// defaultProvider 是模板集里的默认 provider。
//
// ★ 为什么是 deepseek 而不是 openai：**出厂的 agent 模板写着思考档位**
//
//	（main 是 `high`、task 是 `low`），而端点没声明的档位会被**静默忽略**
//	—— 不报错、不进启动日志，请求时不带任何思考字段。
//	deepseek 那份模板带档位表（off/low/high/max），openai 那份不带；
//	所以默认给 deepseek，agent 写的档位才真的生效。
//
//	两个模板都留着：想要 openai 就显式 `--provider openai`。
const defaultProvider = "deepseek"

// providerDefaultModel 报模板里那个 provider 的默认模型 id。
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

// templateFor 选 provider 的模板：先找同名模板，没有就用通用模板。
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

// templateDefault 报模板里 `{{key|默认值}}` 的那个默认值。
func templateDefault(content, key string) string {
	for _, g := range phRe.FindAllStringSubmatch(content, -1) {
		if g[1] == key && g[2] != "" {
			return g[2]
		}
	}
	return ""
}

// phRe 是模板占位符：`{{key}}` 或 `{{key|默认值}}`。
var phRe = regexp.MustCompile(`\{\{([a-z_]+)(?:\|([^}]*))?\}\}`)

// renderers 是各占位符的渲染方式。
//
// ★ `api_key_env` 渲染成 `${NAME}` —— 密钥**永远**是环境变量引用，
//
//	明文密钥根本进不来这条路（config 的写入门也会拒）。
var renderers = map[string]func(string) string{
	"model":       func(v string) string { return v },
	"base_url":    func(v string) string { return v },
	"api_key_env": func(v string) string { return "${" + v + "}" },
}

// renderTemplate 渲染一个模板文件。
//
// ★ values 是 flag 给的覆盖；fallbacks 是调用方的兜底。
//
//	占位符既没有值也没有默认值时**报错**，不猜 —— 猜出来的端点会让用户
//	拿到 404，比直接报错贵得多。
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
			v = string(g[2]) // 模板里的默认值
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
