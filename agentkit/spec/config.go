// Package config 读取**唯一一份** `.openheron/` 配置。
//
// # 一个配置根
//
//	内置默认 < <配置根> < 环境变量
//
// ★ 配置根由 [NewService] 解析，来源只有一个：显式指定（参数或环境变量）
//
//	或从工作目录上溯探测到的 `.openheron/`。**任一时刻只有一份配置生效** ——
//	没有「用户级 + 项目级」的继承链，也就没有「同名整体替换」「跨级取更严」
//	「跨级并集」这一整套需要解释的语义。
//
// ★ 为什么取消继承链：两份配置叠加时，「这份值到底从哪来」要靠额外的
//
//	机制（遮蔽标记、层级字段）才能说清，而那套机制的成本随分区数增长。
//	一份配置则只有「有」和「没有」两种状态。
//
// # 缺省值为什么靠「预填充结构体再 decode」
//
// yaml.v3 的 mappingStruct 只对文档中出现的键调用 unmarshal，未出现的字段
// 完全不触碰。所以「先填默认值，再 decode」天然实现「只覆盖出现的键」。
//
// # 依赖
//
// 本包是叶子：不 import openheron 的任何包，不用 hertz、eino。只用 yaml.v3 + os。
// 谁消费它由消费方决定 —— agentkit/run 尤其不能依赖它（否则单测要准备配置文件）。
package spec

import (
	"errors"
	"fmt"
	"openheron/agentkit/spec/decl"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// 目录名。**S0 只读四类**（agents / llm / server / policy），但名字是最终形态
// —— 目录名与覆盖规则一旦让用户写进 git 就不能再改。
const (
	DirAgent  = "agents"
	DirLLM    = "llm"
	DirServer = "server"
	DirPolicy = "policy"
	DirSkills = "skills"
	DirState  = "state"
	// DirTools 是**工具配置**目录：每个外部工具一个文件，外加知识库。
	//
	// ★ 它从 server/config.yaml 的 `tools:` 段与 kb/ 目录搬过来的。
	//
	//	搬的理由有两条：
	//	  - server/ **不进热更新**（它配的是进程自身）—— 而「换了 HTTP 工具
	//	    的超时」这种改动本该立刻生效，混在 server/ 里就只能重启。
	//	  - 一个目录一种东西：server/ 装进程自身的参数，tools/ 装「这个 agent
	//	    能连到外面去」的能力。两者的变更频率与风险面都不一样。
	//
	// ★ 它**进热更新**：改一个工具的参数，下一轮就用新的。
	DirTools = "tools"
	// DirKBIndex 是**运行数据**里知识库索引的子目录名
	// （`state/kb/<index>/`），**不是**配置目录名。
	//
	// ★ 曾经叫 DirKB，同时被当配置目录（kb/config.yaml）和状态子目录用 ——
	//	配置目录搬走之后，这个名字只剩状态那一个含义，所以改掉它，
	//	免得后来人以为 `.openheron/kb/` 还在。
	DirKBIndex = "kb"
)

// Category 是一个配置分区：目录名、扩展名、是否可编辑、进不进热更新。
//
// ★ 这张表是**加载门与写入门唯一共用的分区真相**。
//
//	两处各写一遍的后果不是「多几行代码」，而是改动只生效一边：新增一个分区
//	忘了改写门，用户在编辑器里看不到它，而接口说它不存在 —— 没有任何东西会响。
type Category struct {
	// ID 与目录名相同，作为 API 与界面的分区标识。
	ID string
	// Label 是界面文案（英文：按 AGENTS.md 的语言约定，界面文案属运行时输出）。
	Label string
	// Dir 是 `.openheron/` 下的子目录名。
	Dir string
	// Ext 是该分区的文件扩展名（含点）。
	Ext string
	// Editable 决定写入门放不放过它。server 不进去 —— 它不进热更新。
	Editable bool
	// hot 决定该分区是否进热更新。
	//
	// ★ 它曾经是一张「**在哪几级**热更新」的表，而每一行的答案都相同
	//	（全在，或全不在）—— 那张表只支撑着一个没人用的查询
	//	（HotAt），已连同它一起删掉。留一个布尔是因为「这个分区热不热」
	//	本身就是是/否问题，不必穿一层地图。
	//
	//	零值是 false：新增分区默认**不**热更新（而不是默认热更新、
	//	忘了再关）。
	hot bool
	// loader 是该分区的解析入口。
	loader func(c *Config, name string, data []byte) error
}

// Hot 报该分区是否进热更新。
//
// ★ 答案只有一个来源：[Categories] 里的 hot 字段 —— **不写死第二份清单**，
//
//	写死的话，某天把某个目录加进 [HotDirs] 而忘了同步，就会出现
//	「保存成功但不生效」且无人察觉。
func (c Category) Hot() bool { return c.hot }

// Categories 是全部分区，**同时**驱动加载与写入白名单。
//
// ★ 顺序不影响加载语义（各分区互不覆盖），但影响**报错先看到哪一条**，
//
//	所以显式排定，不依赖 map 遍历。
var Categories = []Category{
	{
		ID: DirLLM, Label: "Providers", Dir: DirLLM, Ext: ".yaml",
		Editable: true, hot: true,
		loader: (*Config).loadProvider,
	},
	{
		ID: DirAgent, Label: "Agents", Dir: DirAgent, Ext: ".md",
		Editable: true, hot: true,
		loader: (*Config).loadAgent,
	},
	{
		ID: DirPolicy, Label: "Approval policy", Dir: DirPolicy, Ext: ".yaml",
		Editable: true, hot: true,
		loader: (*Config).loadPolicy,
	},
	{
		ID: DirServer, Label: "decl.Server", Dir: DirServer, Ext: ".yaml",
		// ★ 不可编辑：它不进热更新。做成「保存成功但不生效」比拒绝更糟。
		Editable: false, hot: false,
		loader: (*Config).loadServer,
	},
	{
		ID: DirTools, Label: "Tools", Dir: DirTools, Ext: ".yaml",
		// ★ 进热更新：改一个工具的参数（超时、允许的方法、语料目录）
		//   下一轮就该生效，不必重启进程。
		Editable: true, hot: true,
		loader: (*Config).loadTool,
	},
}

// CategoryByDir 按目录名查一个分区。
//
// ★ 键是**目录名**（policy）而不是 ID —— 写入门拿到的正是路径，
//
//	让调用方先 `path.Dir` 再查，中间不会有机会把它换成别的东西。
func CategoryByDir(dir string) (Category, bool) {
	for _, c := range Categories {
		if c.Dir == dir {
			return c, true
		}
	}
	return Category{}, false
}

// hotDirList 从 [Categories] 推热更新**目录名**，已去重排序。
//
// ★ 它**不是**手写的第二份清单 —— 那会与 [Categories] 漂移，
//
//	而漂移的表现是「某个分区改了不生效」：没有报错，只有沉默。
//	（[Categories] 里的 hot 字段是「热不热」，把目录名收敛到一处
//	才是唯一能推出 HotDirs 的地方。）
func hotDirList() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(Categories))
	for _, c := range Categories {
		if !c.Hot() || seen[c.Dir] {
			continue
		}
		seen[c.Dir] = true
		out = append(out, c.Dir)
	}
	sort.Strings(out)
	return out
}

// DefaultAgentID 是**默认 agent**的 id（也是它的文件名，不含扩展名）。
//
// ★ agent 与 agent 之间**没有角色差别** —— 一个 agent 声明了 `subagents:`
//
//	就能带队伍，没声明就是独一个。「默认 agent」不是一种角色，只是
//	「新会话没指定时用哪个」的那个约定。
const DefaultAgentID = "main"

// ConfigDirEnv 指定**配置根**（里面直接有 llm/ agents/ policy/ …）。
//
// ★ 它以前是「用户级基目录」—— 叠加在项目级之上的那一层。取消继承链之后，
//
//	它是**唯一**的那一层：设了它，就不再看项目里的 `.openheron/`。
//	值语义没变（一直就是「配置根本身」，不自动拼 .openheron）。
const ConfigDirEnv = "OPENHERON_CONFIG_DIR"

// DefaultBaseDirName 是配置目录名（`.openheron`）。
const DefaultBaseDirName = ".openheron"

// Config 是合并后的配置视图。
type Config struct {
	// Server 是 server/config.yaml。
	Server decl.Server `yaml:"server"`
	// Policy 是 policy/permissions.yaml。★ **deny 与出厂默认取并集**，
	// 见 loadPolicy 的说明 —— 安全边界只往严的方向合。
	Policy decl.Policy `yaml:"-"`
	// Limits 是 policy/limits.yaml。★ **逐字段取更严的**（min）——
	// 与 Policy.Deny 同思路：限制是安全边界，只能收紧不能放松。
	Limits decl.Limits `yaml:"-"`
	// KB 是 tools/kb.yaml。语料与索引路径见 [decl.KB]。
	KB decl.KB `yaml:"-"`
	// Tools 是 tools/ 目录下的工具配置（http / web_search / wikipedia）。
	//
	// ★ 它**不在** Server 里：那些是「连到外面去」的能力，变更频率与
	//	风险面都跟进程自身参数不同，而且**要能热更新**。
	Tools decl.ToolsCfg `yaml:"-"`

	// Providers 按 provider-id 索引（llm/<id>.yaml 的文件名）。
	Providers map[string]*decl.Provider `yaml:"-"`
	// Models 按 `<provider-id>/<model-id>` 索引。
	Models map[string]*decl.Model `yaml:"-"`
	// Agents 按 agent-id 索引（agents/<id>.md 的文件名）。
	Agents map[string]*decl.Agent `yaml:"-"`

	// Version 是这份配置的指纹。
	//
	// ★ 它的用途是「这一轮跑的是哪份配置」的归因（日志 / 事件 / 排障）。
	//
	//	它**不是** decl.Agent Pool 的代际键：代际由 agent 包的 ConfigManager 给，
	//	因为池需要可比大小的单调计数，而指纹是字符串。
	Version string `yaml:"-"`

	// ConfigRoot 记下这一轮读的是**哪个配置根**，便于日志与事件归因。
	//
	// ★ 它**不是** decl.Agent Pool 的代际键：代际由 agent 包的 ConfigManager 给。
	ConfigRoot string `yaml:"-"`

	// deferred 收集「配了但 S0 不兑现」的键，逐条带原因。
	//
	// 它存在的唯一目的是让降级**可见**。空就是空 —— 不做假动作。
	deferred []string
}

// Deferred 返回本次加载里「配了但未生效」的项目，启动时应当全部打印出来。
//
// 调用方必须真的把它们打出来。**不打印就等于静默忽略**。
func (c *Config) Deferred() []string { return c.deferred }

// DefaultAgent returns the id of the agent a new session runs as.
//
// Named with a call because the package already exports the DefaultAgentID
// constant, and an identifier cannot be both.
//
// ★ **默认 agent 恒为 main** —— 不需要配置来指定：「谁是默认」不是配置项，
//
//	只是「新会话没指定时用哪个」的约定。别的 agent 同样能当会话入口
//	（见 [Config.AgentSpecs]），默认只是其中之一。
//
//	★ main.md 不存在时回退到第一个 agent 名 —— 这一条与
//	  [Config.AgentSpecs] 曾经硬编码 DefaultAgentID 对不上：那时这里给出了
//	  一个根，那里却报「agent "main" is not declared」。按 agent 展开后那个
//	  硬编码点消失了。
func (c *Config) DefaultAgent() string {
	if c == nil {
		return DefaultAgentID
	}
	if _, ok := c.Agents[DefaultAgentID]; ok {
		return DefaultAgentID
	}
	names := c.AgentNames()
	if len(names) == 0 {
		return DefaultAgentID
	}
	return names[0]
}

func (c *Config) deferKey(format string, args ...any) {
	c.deferred = append(c.deferred, fmt.Sprintf(format, args...))
}

// Service 负责加载配置。
//
// ★ 它只认**一个**配置根 —— 没有第二层可叠加。
type Service struct {
	// root 是**配置根**：里面直接躺着 llm/ agents/ policy/ server/ tools/。
	root string
	// projectRoot 是**项目根**：agent 的工作目录与相对路径（static_dir）的基准。
	//
	// ★ 它与 root **解耦**：配置可以放在别处，而 agent 仍在项目目录里工作。
	//   两者默认重合（上溯探测时 root 是 <项目根>/.openheron）。
	projectRoot string
}

// NewService 定位**唯一**的配置根。
//
// 顺序：
//
//  1. 显式 root（命令行 --config-dir 传进来的）
//  2. 环境变量 [ConfigDirEnv]
//  3. 从 projectRoot 向上找第一个含 .openheron/ 的祖先
//  4. projectRoot 自身（`<projectRoot>/.openheron`）
//
// ★ 显式路径指向的**就是配置根本身**（里面直接有 llm/ agents/），
//
//	不再自动拼 .openheron —— 与 [ConfigDirEnv] 的值语义一致。
func NewService(root, projectRoot string) (*Service, error) {
	if projectRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("config: resolve working directory: %w", err)
		}
		projectRoot = wd
	}
	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("config: resolve project root %q: %w", projectRoot, err)
	}

	if root == "" {
		root = os.Getenv(ConfigDirEnv)
	}
	if root == "" {
		// ★ 上溯：cd 到子目录时仍命中项目里的配置。找不到就用项目根本身。
		//
		//   probeUpwards 返回的是**含 .openheron 的目录**（即配置目录本身），
		//   所以不能把它塞回 projectRoot 再拼一次 —— 那样会得到 .openheron/.openheron。
		root = filepath.Join(projectRoot, DefaultBaseDirName)
		if _, err := os.Stat(root); err != nil {
			if found, ok := probeUpwards(projectRoot); ok {
				root = found
			}
		}
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("config: resolve config root %q: %w", root, err)
	}
	return &Service{root: abs, projectRoot: projectRoot}, nil
}

// NewServiceAt 建一个指向**指定**配置根的服务，**不做上溯探测**。
//
// ★ 为什么要有它：上溯（[probeUpwards]）是**运行期**的正确规则 ——
//
//	「cd 到子目录时仍命中项目里的配置」。但写配置的命令（openheron init）落点
//	必须由调用方指定：不能因为某个祖先恰好有 `.openheron/` 就写到那里去。
func NewServiceAt(root, projectRoot string) (*Service, error) {
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("config: resolve config root %q: %w", root, err)
		}
		root = abs
	}
	if projectRoot != "" {
		abs, err := filepath.Abs(projectRoot)
		if err != nil {
			return nil, fmt.Errorf("config: resolve project root %q: %w", projectRoot, err)
		}
		projectRoot = abs
	}
	return &Service{root: root, projectRoot: projectRoot}, nil
}

// probeUpwards 从 from 向上找第一个含 .openheron/ 的祖先。
func probeUpwards(from string) (string, bool) {
	dir := from
	for {
		cand := filepath.Join(dir, DefaultBaseDirName)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Root 返回**配置根**（里面直接有 llm/ agents/ policy/ server/ tools/）。
//
// ★ 它就是「这一轮跑的是哪份配置」的答案 —— 只有一个，没有第二层。
func (s *Service) Root() string { return s.root }

// ProjectRoot 返回**项目根**（静态资源、agent 的工作目录所在）。
//
// ★ 与 [Service.Root] 区分：Root 是配置目录（配置与 state/ 所在），
//
//	ProjectRoot 是项目目录（`static_dir` 等相对路径按它解析，
//	否则会去 `.openheron/static` 找前端）。
//	两者**解耦**：配置可以放在别处，而 agent 仍在项目目录里工作。
func (s *Service) ProjectRoot() string { return s.projectRoot }

// DataDir 解析并创建运行数据目录。
//
// 空串 = `<配置根>/state`（即 `.openheron/state`）；相对路径按**配置根**解析；
// 绝对路径原样。
//
// ★ 基准与 [ResolveDir] 不同，是有意的：`static_dir` 指向项目根（前端资源
//
//	与 agent 的工作目录同层），而 `state/` 历来与配置同根。若把 `state`
//	按项目根解析，默认值会从 `.openheron/state` 悄悄变成 `<root>/state` ——
//	`.gitignore` 与测试隔离（`.openheron-test/project/.openheron/`）同时失效，
//	而且新目录根本没人创建，表现为「启动就报错」。见
//	docs/config-design.md §6.4 末段。
func (s *Service) DataDir(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = DirState
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(s.root, v)
	}
	if err := os.MkdirAll(v, 0o755); err != nil {
		return "", fmt.Errorf("config: create data directory: %w", err)
	}
	return v, nil
}

// ResolveDir 把一个配置里的目录值解析成路径：相对路径按**项目根**算
// （见 [ProjectRoot]），绝对路径原样返回，空串原样返回（调用方据此禁用该功能）。
//
// ★ 相对路径按项目根而不是进程 cwd，也不是配置目录：否则 `cd /tmp && openheron`
// 会跑到 /tmp 下找 `static/`，或去 `.openheron/static` 找一个不存在的前端。
func (s *Service) ResolveDir(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(s.ProjectRoot(), v)
}

// Load 读配置并校验。
func (s *Service) Load() (*Config, error) {
	return s.loadWith(nil)
}

// loadWith 读配置，可带一个**试算用的改动**。
//
// ★ m 为 nil 时行为与 [Service.Load] 完全一致。
//
// ★ 它是 [Service.loadWithMany] 的单文件包装 —— 写一个文件时用它，
//
//	写一批互相依赖的文件时用那个（见 [Service.WriteMany]）。
func (s *Service) loadWith(m *mutation) (*Config, error) {
	if m == nil {
		return s.loadWithMany(nil)
	}
	return s.loadWithMany(map[string]*mutation{m.Rel: m})
}

// loadWithMany 读配置，可带一批**试算用的改动**。
//
// ★ 为什么不直接复用 [Service.Load]：写配置必须先验证「**合并之后**」还成立，
//
//	办法就是真加载一次、只在内存里替换掉那一个文件。单文件语法正确远不够 ——
//	删掉一个被 agents/*.md 引用的 provider，单文件毫无问题，合并后却是坏的。
//
// ★ ms 是一批改动（键是相对路径），不是一个个来 —— 互相依赖的文件
//
//	（agent 的 model 引用 provider）逐个试合并会永远过不了校验：
//	先改 agent 则 provider 还没声明，先改 provider 则 agent 还不存在。
//	见 [Service.WriteMany] 与 docs/init-design.md §6。
func (s *Service) loadWithMany(ms map[string]*mutation) (*Config, error) {
	cfg, err := s.loadOnly(ms)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.Version = s.Fingerprint()
	return cfg, nil
}

// LoadUnchecked 读当前配置根，但**跳过校验**。
//
// ★ 它回答的是「磁盘上现在有什么」，不是「这份配置能不能跑」——
//
//	后者是 [Service.Load] 的事。
//	为什么需要前者：「没有 main agent」这一条会让 validate 整个失败，
//	于是「磁盘上已经有什么」就再也看不见了 —— 而那正是 openheron init
//	要问的问题（它得在配置坏着的时候也能用）。
//
// ★ 返回的配置**不能**直接拿去跑。跑之前必须过一次 [Service.Load]。
func (s *Service) LoadUnchecked() (*Config, error) {
	return s.loadOnly(nil)
}

// loadOnly 读配置（可带一批试算用的改动），**不校验**。
func (s *Service) loadOnly(ms map[string]*mutation) (*Config, error) {
	cfg := &Config{
		Providers: map[string]*decl.Provider{},
		Models:    map[string]*decl.Model{},
		Agents:    map[string]*decl.Agent{},
		// ★ 记下来，便于日志与事件归因（「这一轮跑的是哪份配置」）。
		ConfigRoot: s.root,
		// ★ 默认值必须**在加载前**预填：loadServer 是往这个结构体上逐层
		//   decode 的，不预填就等于「缺失即零值」—— listen_addr 会变成
		//   空串而不是 :8080。
		Server: defaultServer(),
		Policy: defaultPolicy(),
		KB:     defaultKB(),
	}

	// ★ 只有一个配置根，所以没有「谁覆盖谁」的问题。
	for _, cat := range Categories {
		cat := cat
		if err := s.loadDir(filepath.Join(s.root, cat.Dir), cat.Ext, cat.Dir, ms,
			cfg.loaderFor(cat)); err != nil {
			return nil, err
		}
	}

	cfg.collectBadDurations()
	cfg.collectNoopLimits()
	return cfg, nil
}

// loaderFor 给出某分区的解析入口。
//
// ★ 它挂在 [Config] 上而不是直接用 [Category].loader：加载器是 Config 的方法，
//
//	而 Categories 是包级变量、没有 Config 可用。包一层换来的是
//	「加载循环只认这一个入口」，测试也只需验它非 nil。
func (c *Config) loaderFor(cat Category) func(name string, data []byte) error {
	if cat.loader == nil {
		return nil
	}
	return func(name string, data []byte) error { return cat.loader(c, name, data) }
}

// loadDir 读一个目录下的全部实体文件。
//
// **目录不存在不是错误**（新项目就是空的），只是这个分区没有配置。
//
// cat 是**目录名**而不是 [Category]：加载循环要知道「当前这个文件是不是
// 本轮要改的那一个」，而那只需要目录名。试合并阶段不关心可编辑性与
// 热更新 —— 那是写入门的事，在它之前就拦掉了。
func (s *Service) loadDir(dir, ext, cat string, ms map[string]*mutation,
	loader func(name string, data []byte) error) error {

	// ★ 先算出「本轮要改的文件是不是落在这个分区这一级」。
	//
	//	放在最前面是因为它决定后面两条路：改动可能是在**创建**一个新文件，
	//	而那个文件既不在磁盘上、所在目录也可能压根不存在（用户的第一个
	//	policy/permissions.yaml 就是这种形状）。把判断留在读目录之后，
	//	`os.ReadDir` 的「目录不存在不是错误」早退就会绕过它。
	//
	//	★ 删除**也要**进这个判断：待删的文件必须被**跳过**，否则试合并
	//	看到的还是旧内容 —— 于是「删掉一个被引用的 provider」明明该失败，
	//	却通过了校验并把文件删了，坏配置就此落盘。
	//
	// ★ 键是**文件名**（不是相对路径）：加载循环手上只有这一段名字，
	//	而相对路径带分区前缀（policy/permissions.yaml）。写成按相对路径
	//	比对的话这条分支永远不进 —— 症状是「试合并没报错却也没生效」。
	pending := map[string]*mutation{}
	for _, m := range ms {
		if path.Dir(filepath.ToSlash(m.Rel)) != cat {
			continue
		}
		pending[path.Base(filepath.ToSlash(m.Rel))] = m
	}
	// applyNew 应用「磁盘上没有」的那些改动（创建场景）。
	//
	// ★ 按文件名排序：同一分区里可能有好几个新文件，先建哪个决定了
	//	**报错时先看到哪一条** —— 那必须是可复现的。
	applyNew := func(names []string) error {
		for _, name := range names {
			m := pending[name]
			if m.Delete {
				continue
			}
			if err := loader(name, m.Content); err != nil {
				return fmt.Errorf("config: parse %s: %w", m.Rel, err)
			}
		}
		return nil
	}
	// pendingNames 按文件名排好序的待改文件名。
	pendingNames := func() []string {
		out := make([]string, 0, len(pending))
		for name := range pending {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		// 目录不存在 ⇒ 磁盘上什么都没有 ⇒ 这一批全是新建。
		return applyNew(pendingNames())
	}
	if err != nil {
		return fmt.Errorf("config: read directory %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		names = append(names, e.Name())
	}
	// ★ 显式排序：让「加载顺序」不依赖 ReadDir 已经有序这件事 ——
	// 报错时哪条先被看到必须是可复现的。
	sort.Strings(names)

	// consumed 记录本轮改动里哪些已在下面的循环里被处理。
	consumed := map[string]bool{}
	for _, name := range names {
		// ★ 先问「这个文件是不是本轮要改的那一个」，是就用候选内容
		//   （或直接跳过，那是删除）—— **绝不落盘**。
		//   试合并必须纯在内存里发生。
		//
		//   比的是**文件名**：m.Rel 带分区前缀（policy/permissions.yaml），
		//   而这里的 name 只有一段。写成 m.Rel == name 的话这条分支永远
		//   不进，而症状是「试合并没报错却也没生效」—— 静默失效。
		if m, ok := pending[name]; ok {
			consumed[name] = true
			if m.Delete {
				continue
			}
			if err := loader(name, m.Content); err != nil {
				return fmt.Errorf("config: parse %s: %w", name, err)
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("config: read %s: %w", filepath.Join(dir, name), err)
		}
		if err := loader(name, data); err != nil {
			return fmt.Errorf("config: parse %s: %w", filepath.Join(dir, name), err)
		}
	}

	// ★ 循环没命中 = 这个文件在磁盘上不存在，也就是这次改动在**新建**它。
	//
	//	漏掉这一步的症状是「新建 policy/permissions.yaml 写 ask: [execute]，
	//	试合并说没事，实际配置里还是默认值」：不报错、不生效，且看不出
	//	是哪里没接上。
	//
	//	★ 一批改动里可能**有的**命中、**有的**没命中 —— 所以按名字逐个判，
	//	不是一个布尔量。
	var newNames []string
	for _, name := range pendingNames() {
		if consumed[name] {
			continue
		}
		newNames = append(newNames, name)
	}
	return applyNew(newNames)
}

// decodeStrict 严格解码：**未知字段报错，且带行号**。
//
// 为什么严格：宽松模式下拼错字段名会静默用默认值，而「为什么我的配置没生效」
// 是排障成本最高的一类问题。
func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// mergeExtra 按**顶层键**覆盖。
//
// 不做深合并 —— 深合并在「改 thinking.type 而保留 thinking.other」这件事上
// 语义不可预测。
func mergeExtra(outer, inner map[string]any) map[string]any {
	if len(outer) == 0 {
		return inner
	}
	out := make(map[string]any, len(outer)+len(inner))
	for k, v := range outer {
		out[k] = v
	}
	for k, v := range inner {
		out[k] = v
	}
	return out
}

// resolvePath 把相对路径按**配置根**解析。
//
// ★ 统一基准是配置根而不是 cwd —— 否则换个目录启动就换了数据目录。
func (s *Service) resolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.root, p)
}
