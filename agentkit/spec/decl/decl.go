// Package decl 是**配置的声明**：`.openheron/` 里那些文件长什么样。
//
// ★ 为什么声明与「怎么读写」分成两个包：
//
//	声明是**词汇表**（Provider / Model / Agent / Policy / Limits / KB），
//	被装配层、模型层、接口层到处读；而读写（加载、校验、热更新、写盘）
//	只有进程自己用。把两者放一个包里，读者为了「有哪些配置项」就得
//	翻过加载与写盘那两千行。
//
// ★ 它是**叶子**：只依赖标准库与 yaml，不认识服务、不认识文件。
//
// ★ 包名不叫 schema —— eino 的 schema 与 ag-ui 的 types 在同一批文件里
//
//	已经占着这两个名字，再叫一遍就得到处起别名。decl（declaration）
//	说的是同一件事，且不撞车。
package decl

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// yamlNode 是 yaml.Node 的别名，让 Duration 的方法签名可读。
type yamlNode = yaml.Node

// DefaultDebounce 是热更新的防抖窗口，也是**写错时长时的兜底值**。
//
// ★ 它住在声明包：LenientDuration 的降级默认值要用它，
//
//	而 LenientDuration 属于声明 —— 机制（轮询）不该反过来被声明依赖。
const DefaultDebounce = 200 * time.Millisecond

// Provider 是一个厂商档案：连接属性 + 它声明的模型。
//
// ★ 拨号所需的四件事（地址、凭据、超时、头）都在这一层，**同一 provider 下
// 所有模型共享** —— 所以它们天然只建一份。
type Provider struct {
	// ID 是文件名（不含扩展名）。引用形式为 `<provider-id>/<model-id>`。
	ID string `yaml:"-"`

	// Type 只用于日志与显示 —— **代码里没有按它分支的地方**。
	Type string `yaml:"type"`

	// APIKey 支持 `${ENV}` / `@file:` 两种取值，见 expandValue。
	APIKey string `yaml:"api_key"`

	// BaseURL 是**主机（可带前缀）**。★ 不要写 `/chat/completions`，
	// 底层库自己会拼，写全了会得到 `…/chat/completions/chat/completions`。
	BaseURL string `yaml:"base_url"`

	// Header 是网关类端点常要求的归属头。
	Header map[string]string `yaml:"header"`

	// Timeout 是这个端点的超时。**不能依赖 SDK 的隐式默认**（各家差异极大）——
	// 排查 `context deadline exceeded` 时那种默认值极难归因。
	Timeout Duration `yaml:"timeout"`

	// Extra 是厂商私有请求体参数。★ **合并语义是顶层键覆盖**。
	Extra map[string]any `yaml:"extra"`

	// HintKeys 是**这个端点使用的字段名**（思考类参数等）。
	//
	// ★ 协议层不内置任何一家的字段名 —— 那是这里的事实。
	HintKeys []string `yaml:"hint_keys"`

	// Models 是它声明的模型。
	Models []*Model `yaml:"models"`
}

// Model 是一个模型的固有属性。
type Model struct {
	ID string `yaml:"id"`

	// Kind 区分这个模型是聊天模型还是向量化模型。
	//
	// ★ 空 = chat。缺省成 chat 而不是必填，是为了让已入库的
	//
	//	`llm/*.yaml` 继续按原样加载 —— 绝大多数模型就是 chat。
	//
	// ★ **embedding 模型必须被 chat 装配路径跳过。** `llm/common` 打的是
	//
	//	`/chat/completions`，拿它装配一个 embedding 模型会得到一个**能
	//	构造成功、每次调用都 404** 的对象 —— 那种错不响，只在真用时炸。
	Kind string `yaml:"kind"`

	// Dimensions 是这个模型产出的向量维度。只对 kind: embedding 有意义。
	//
	// ★ 为 0 表示**未知**（不是「不限维度」）—— 与 ContextWindow 同一套
	//
	//	约定。未知时由首次建索引时从响应里观测到，并写进索引 manifest。
	//	它真正的价值是**提前拦截**：换了 embedding 模型之后，旧向量还在
	//	库里，而跨模型的检索给出的是看起来正常的错结果。
	//
	// ★ **绝不转发给端点。** eino-ext 的 EmbeddingConfig.Dimensions 是
	//
	//	「让服务端把输出截断到 N 维」的**请求参数**（只有
	//	text-embedding-3 支持）。两个字段同名同类型而语义正交：实测
	//	llama-server 收下 `dimensions: 1024` 并静默忽略，换到
	//	text-embedding-3 就是静默截断。
	Dimensions int `yaml:"dimensions"`

	// ContextWindow 为 0 表示**未知**（不是「无限制」）。
	// 当前 openheron 没有 summarization，所以它是纯元数据 ——
	// 但字段位置现在就要留对，否则将来加裁剪必须改已入库的 schema。
	ContextWindow int `yaml:"context_window"`

	// Timeout 允许覆盖 provider 级：思考模型比普通模型慢一个数量级。
	Timeout Duration `yaml:"timeout"`

	// ReasoningField 是**这个端点收发思维链用的字段名**。
	// 空 = 用底层库的原生字段名。
	ReasoningField string `yaml:"reasoning_field"`

	// ReasoningFieldFrom 是回放历史时思维链的**源**字段名，默认同上。
	ReasoningFieldFrom string `yaml:"reasoning_field_from"`

	// Extra 覆盖 provider 级同名键。
	Extra map[string]any `yaml:"extra"`

	// Reasoning 是**档位词汇表**：档位名 → 这个端点把它发成什么字段。
	//
	// ★ 为什么它在 model 层：它描述的是**端点的方言**而非常用偏好 ——
	//   同一个 `high`，有的端点要 `{reasoning_effort: high}`、有的要
	//   `{thinking: {type: enabled}}`。档位名不写进代码，端点的知识是数据。
	//
	//   为空 = 这个模型不支持思考。
	Reasoning map[string]map[string]any `yaml:"reasoning"`

	// ReasoningDefault 是**建议档位**：agent 没写 `reasoning` 时用它。
	// 留空 = 不注入任何思考字段。
	//
	// ★ 名字与 agent 侧的 `reasoning` 同族：provider 定义「有哪些档位」，
	//   agent 选「用哪一档」。两个 `reasoning` 一个给词汇表、一个给选择，
	//   读起来是同一件事的两面。
	ReasoningDefault string `yaml:"reasoning_default"`
}

// LevelFields 解析一个档位对应的请求体字段。
//
// ★ agent 写了模型没有的档位**不报错**，但一定出声 —— 档位集变得比本程序
//
//	的发布快，维护一份硬编码清单会在它过期时「拒掉能用的配置」。
//	调用方负责把 ok=false 这件事打印出来。
func (m *Model) LevelFields(level string) (map[string]any, bool) {
	if m == nil || level == "" {
		return nil, false
	}
	f, ok := m.Reasoning[level]
	return f, ok
}

// ── agents/<agent-id>.md ─────────────────────────────────────────────────

// Agent 是一个 agent 的声明。
//
// ★ agent 之间**没有角色差别**：每个 agent 都是同一格式的同一个东西，
//
//	声明了 `subagents:` 的那个能带队伍，没声明的就是独一个。会话选哪个
//	agent，就按哪个的声明装配。
type Agent struct {
	ID string `yaml:"-"`

	// File 是这个 agent 来自哪个文件，形如 `agents/main.md`。
	//
	// ★ 它只为**报错文案**存在，而那正是它值得占一个字段的理由：
	//
	//	跨文件错误（引用的模型没声明）出在**别处**，不带路径的话用户只知道
	//	「agent broken」，得自己去 agents/ 里逐个翻才知道该改哪个文件。
	//	`yaml:"-"` —— 它不是配置项，不该出现在解析面里。
	File string `yaml:"-"`

	// Description 进 `task` 工具描述，**直接决定模型选不选它**。
	// 子 agent 必填。
	Description string `yaml:"description"`

	// ModelRef 形如 `<provider-id>/<model-id>`。
	//
	// ★ **必填，且不继承不回退** —— 「子 agent 静默继承主模型」是**成本事故**。
	ModelRef string `yaml:"model"`

	// Instruction 是正文（markdown 去掉 frontmatter 后剩下的全部）。
	// 空 = 用 deep.New 的内置提示。
	Instruction string `yaml:"-"`

	MaxIterations int `yaml:"max_iterations"`

	// SubAgents 声明拓扑。**顺序 = task 描述里的顺序**。
	SubAgents []string `yaml:"subagents"`

	// DisabledTools 只影响本 agent（内层）。**与全局 deny 分开传，永不合并** ——
	// 合并就丢掉了「谁在外层」，而那是子 agent 不能越权加回被禁工具的唯一保证。
	DisabledTools []string `yaml:"disabled_tools"`

	// 推理参数：这次调用的意图。同一个模型在不同 agent 里就该用不同参数。
	Temperature *float32 `yaml:"temperature"`
	MaxTokens   *int     `yaml:"max_tokens"`

	// Reasoning 从 ModelSpec 的**词汇表里选一个档位名**，不写数字预算。
	Reasoning string `yaml:"reasoning"`
}

// ── policy/permissions.yaml ──────────────────────────────────────────────

// Policy 是审批策略。
//
// ★ **只有 ask 与 deny** —— 未点名的工具一律放行。
//
//	「直接放行」不需要一列来声明：没点名就是放行 ✅。留着 allow 只会让人
//	以为「没列在 allow 里就是不让用」✅ —— 而事实相反 ✅ ✅。
type Policy struct {
	// Ask 可见，但调用需人批准。
	Ask []string `yaml:"ask"`
	// Deny 直接拒绝。★ 与出厂默认**取并集** —— 安全边界只往严的方向合。
	Deny []string `yaml:"deny"`
}

// Limits 是限制规则（数量上的上限），来自 policy/limits.yaml。
//
// ★ 它与 [Policy] 的分工是**正交**的：
//
//	Policy 回答「这个工具要不要人点头」（审批决策）
//	Limits 回答「最多能做多少」（额度上限）
//
//	一个是「允许吗」，一个是「到这就停」。两份文件可以同时存在而互不干扰。
//
// ★ 合并语义：**取更严的**（逐字段 min）—— 不是覆盖。
//
//	两层是 server/config.yaml 的全局基线与 policy/limits.yaml 的收紧，
//	方向与 [Policy.Deny] 取并集一致：限制是**安全边界**，
//	只能收紧，不能放松。若允许写个更大的值就能解开限制，
//	那「放一份 limits.yaml」就成了一条提权路径。
//
// ★ 0 = 不设限，沿用全局值（server/config.yaml）。要收紧就写一个正数。
type Limits struct {
	Agent AgentLimit `yaml:"agent"`
	Retry RetryLimit `yaml:"retry"`
}

// AgentLimit 是 agent 侧的额度上限。
type AgentLimit struct {
	// MaxDepth 是嵌套深度上限。**入口 agent 是第 0 层**；深度 d 的 agent
	// 允许再生子 agent 当且仅当 d < MaxDepth。
	//
	// 与 server/config.yaml 的 agent.max_depth 取**更严的**那个。
	MaxDepth int `yaml:"max_depth"`
}

// RetryLimit 是重试侧的额度上限。
type RetryLimit struct {
	// MaxRetries 是模型调用失败后的重试次数上限。
	//
	// 与 server/config.yaml 的 retry.max_retries 取更严的。
	MaxRetries int `yaml:"max_retries"`
}

// DefaultMaxDepth 是未声明嵌套深度上限时的取值。
const DefaultMaxDepth = 1

// DefaultMaxRetries 是未声明重试上限时的取值。
//
// ★ 与 helpers.DefaultMaxRetries 是同一个数，但**不能共用常量** ——
//
//	helpers 不 import config（它是叶子包），config 也不 import helpers
//	（配置不该知道中间件）。两边各留一份，由 TestMaxRetriesDefaultsMatch
//	钉住一致性 —— 与 Policy 的工具名常量同一套路（见 [Policy] 的注释）。
const DefaultMaxRetries = 5

// Tier 是审批档位。
type Tier string

const (
	TierAllow Tier = "allow"
	TierAsk   Tier = "ask"
	TierDeny  Tier = "deny"
)

// TierOf 判定一个工具的档位。
//
// ★ **兜底是 allow** —— 只有明确写进 ask / deny 的工具才被拦。
//
//	（与 agent 包的 [agent.Policy.TierOf] 同一套语义 ✅ —— 两处必须一致 ✅。）
func (p Policy) TierOf(tool string) Tier {
	if contains(p.Deny, tool) {
		return TierDeny
	}
	if contains(p.Ask, tool) {
		return TierAsk
	}
	return TierAllow
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ── server/config.yaml ───────────────────────────────────────────────────

// Server 是进程自身与 agent 侧工具的配置。
type Server struct {
	ListenAddr string `yaml:"listen_addr"`
	// StaticDir 是 frontend_mode: dir 时服务的目录。
	//
	// ★ 默认指向 web/dist（Vite 的构建产物）：`dir` 是**开发**形态，
	//   而开发形态下界面就是那份产物。v1 时代这里指的是旧 A2UI 静态页，
	//   那个目录已随 v1 下线删除。
	StaticDir string `yaml:"static_dir"`
	DataDir   string `yaml:"data_dir"`
	// FrontendMode 决定界面从哪来：
	//
	//	embed —— go:embed 的 web/dist（发布形态，单文件）
	//	dir   —— StaticDir 指向磁盘目录（开发过渡用）
	//
	// 留空等同 embed：发布二进制里的默认值就该是「开箱即用」，
	// 而空值出现在 yaml 里通常是「还没想好」，退回最常用的那个。
	FrontendMode string `yaml:"frontend_mode"`
	// StartTimeout 是「一轮从提交到进入运行态」的上限。
	// 用 LenientDuration：写错退回默认即可 —— 它只影响「等多久算启动失败」，
	// 而启动失败本身会由 ErrStartTimeout 明确报出来。
	StartTimeout LenientDuration `yaml:"start_timeout"`
	Watch        Watch           `yaml:"watch"`
	Retry        RetryCfg        `yaml:"retry"`
	Agent        AgentCfg        `yaml:"agent"`
	// ★ 这里**没有** `tools:` 段了 —— 外部工具的配置搬到 tools/ 目录。
	//
	//	理由见 [DirTools]：server/ 不进热更新，而「改一个工具的超时」
	//	这种改动本该下一轮就生效。留在 server/ 里只能重启。
}

// 工具名（配置层与装配层共用一份 —— 两处各写一遍，迟早分叉）。
//
// ★ 它们住在 config 包，是因为配置层要拿它们算「这份配置启用了哪些
//
//	工具」；装配层再从同一个常量取名字去建工具。
const (
	ToolNameHTTPGet    = "request_get"
	ToolNameHTTPPost   = "request_post"
	ToolNameHTTPPut    = "request_put"
	ToolNameHTTPDelete = "request_delete"
	ToolNameWebSearch  = "duckduckgo_search"
	ToolNameWikipedia  = "wikipedia_search"
)

// ToolsCfg 是三类外部工具的配置。
//
// ★ **没有 enabled 字段** —— 「那个工具的配置在不在」本身就是开关：
//
//	tools/http.yaml 在 = 开，不在 = 关（要关就删掉那个文件）。
//
//	★ 为什么不用布尔字段：多一个字段就多一处能写错的地方 ——
//
//	「enabled: false 但下面配了 provider」到底算开还是关？而「文件在不在」
//	没有第二种读法。与知识库的空 corpus 同一条规矩（空 = 关）。
//
//	★ 三类**各自独立** —— 写了哪个就开哪个。
//
//	★ 它们共同的性质是「把外部内容带进上下文」：网页、HTTP 响应。策略层
//
//	（allow/ask/deny）管的是「能不能调」，管不了「结果里带恶意指令」——
//	那一面要靠「默认关 + 用的人自己知道风险」。
type ToolsCfg struct {
	// HTTP 是通用 HTTP 请求工具（GET / POST / PUT / DELETE）。
	//
	// ★ 指针：nil = 这一段没写 = 关。
	HTTP *HTTPToolsCfg `yaml:"http"`
	// WebSearch 是网页搜索。
	WebSearch *WebSearchCfg `yaml:"web_search"`
	// Wikipedia 是维基百科检索。
	Wikipedia *WikipediaCfg `yaml:"wikipedia"`
}

// HTTPToolsCfg 是通用 HTTP 请求工具的配置。
type HTTPToolsCfg struct {
	// Methods 是允许的方法。
	//
	// ★ **默认只有 get**：POST/PUT/DELETE 能改外部世界的状态，得显式
	//   点名才给 —— 而模型不需要理由就能编出一个 POST。
	Methods []string `yaml:"methods"`
	// Headers 是每个请求都带的头。
	Headers map[string]string `yaml:"headers"`
	// Timeout 是单个请求的上限。默认 30s。
	Timeout LenientDuration `yaml:"timeout"`
}

// WebSearchCfg 是网页搜索的配置。
type WebSearchCfg struct {
	// Provider 是搜索后端：duckduckgo / searxng。
	//
	// ★ 默认 duckduckgo：零配置、零 key。searxng 要自己起一个实例。
	Provider string `yaml:"provider"`
	// MaxResults 是一次搜索最多返回几条。默认 10。
	MaxResults int `yaml:"max_results"`
	// Timeout 是单个请求的上限。默认 30s。
	Timeout LenientDuration `yaml:"timeout"`
	// Region 是搜索地区（duckduckgo 的 kl 参数）。空 = 全球。
	Region string `yaml:"region"`
}

// WikipediaCfg 是维基百科检索的配置。
type WikipediaCfg struct {
	// TopK 是返回几条。默认 3。
	TopK int `yaml:"top_k"`
	// Language 是维基的语言（zh / en …）。默认 zh。
	Language string `yaml:"language"`
	// Timeout 是单个请求的上限。默认 15s。
	Timeout LenientDuration `yaml:"timeout"`
	// BaseURL 覆盖 API 端点（自建镜像用）。空 = 官方。
	BaseURL string `yaml:"base_url"`
}

// AgentCfg 是多 agent 的运行时约束。
type AgentCfg struct {
	// MaxDepth 是嵌套深度上限。**入口 agent 是第 0 层**；深度 d 的 agent
	// 允许再生子 agent 当且仅当 d < MaxDepth。
	//
	// 0 = 用 DefaultMaxDepth(=1)，即「只有根能委派」。
	MaxDepth int `yaml:"max_depth"`
}

// Watch 是热更新参数。★ server/ **不进热更新** —— 改进程自身要重启。
type Watch struct {
	Enabled *bool `yaml:"enabled"`
	// Debounce 用**宽松**类型：写错只退回默认并记一条日志，不让配置加载失败。
	// 理由与 timeout 相反 —— debounce 只影响「多久算保存完了」，
	// 一个写错的值不值得让整个程序起不来；而 timeout 写错会让请求超时行为
	// 变得不可预测，必须当场报错。
	Debounce LenientDuration `yaml:"debounce"`
}

// LenientDuration 是写错就退回默认的时长。
type LenientDuration time.Duration

// UnmarshalYAML 解析失败时退回默认并记录。
func (d *LenientDuration) UnmarshalYAML(n *yamlNode) error {
	var strict Duration
	if err := strict.UnmarshalYAML(n); err != nil {
		recordBadDuration(err.Error())
		*d = LenientDuration(DefaultDebounce)
		return nil
	}
	*d = LenientDuration(strict)
	return nil
}

// Duration 返回底层值。
func (d LenientDuration) Duration() time.Duration { return time.Duration(d) }

// badDurations 收集「写错但已降级」的项。启动时必须打出来 ——
// 降级如果不出声，就等于静默忽略用户的配置意图。
var badDurations []string

func recordBadDuration(msg string) { badDurations = append(badDurations, msg) }

// BadDurations 返回并清空已降级的时长项。
func BadDurations() []string {
	out := append([]string(nil), badDurations...)
	badDurations = nil
	return out
}

// RetryCfg 是模型重试。
type RetryCfg struct {
	MaxRetries int `yaml:"max_retries"`
}

// KB 是知识库配置（tools/kb.yaml）。
//
// ★ 设计见 docs/kb-design.md。语料与索引的**作用域**是这一节最要紧的
//
//	一条：Corpus 拒绝绝对路径，因为它必须跟着 projectDir 走。索引与语料
//	一旦分家，换个目录跑就会检索到另一个项目的文档，而那**不报错** ——
//	只是自信地返回无关的答案。
type KB struct {
	// Corpus 是语料目录，**相对项目根**（见 Service.ResolveDir）。
	//
	// ★ 拒绝绝对路径 —— 这是「索引与语料同生共死」的结构保证，不是约定。
	//   空串 = 关闭知识库；那让「没配」与「配了但没索引」两种状态不会混淆：
	//   没配就干脆没有 kb_search，而不是有一个永远召回不到东西的工具。
	Corpus string `yaml:"corpus"`

	// Index 是索引目录名，落在 stateDir/kb/<index>/。
	//
	// 它与 Corpus 同生命周期。留成两个字段是为了目录名可读 —— 出问题时
	// `ls .openheron/state/kb/` 能看出是哪个语料，不用猜哪个 hash 是哪个。
	Index string `yaml:"index"`

	// Model 是向量化模型，`<provider-id>/<model-id>` 形式。
	//
	// ★ 它必须指向一个 `kind: embedding` 的模型。指错会在建索引前报错，
	//   而不是拿 chat 端点去调 /embeddings 得到 404。
	Model string `yaml:"model"`

	// QueryInstruction 是**只加在查询侧**的指令前缀。
	//
	// Qwen3-Embedding 这类模型是非对称训练的：建索引用原文，查询才加
	// `Instruct: …\nQuery: …`。加错的后果不是报错，是召回质量悄悄变差。
	QueryInstruction string `yaml:"query_instruction"`

	// BatchSize 是一次向量化请求里的**条数**上限。
	//
	// ★ 它单独用**不够**：条数说不了体积。v1 按块切时实测 docs/ 有 405 块，
	//   p50 是 426 字符、p95 是 1505、最大 9500 —— `batch_size: 8` 的一批最多
	//   能到 12000 字符，远超 llama-server 的 `-c`。那不会报错，而是服务端被
	//   一个放不下的请求拖垮（实测 8 分钟后 EOF、进程退出）。
	//   它与 MaxBatchChars 是**两个都要满足**的上限。
	//
	// ★ v2 的单元是**源文件**，一份 `EmbedText` 是 973–2653 字符（§8.1），
	//   于是这两个上限都不再咬人（一组最多 10 份）。字段保留不动 ——
	//   它与 eino-ext 逐字段对齐的承诺不因此改。
	BatchSize int `yaml:"batch_size"`

	// MaxBatchChars 是一批向量化文本的字符数上限。
	//
	// ★ 为什么用字符不是 token：openheron 没有分词器，为它引入 tiktoken 之类是
	//   一笔大账，而中英混排下「1 字符 ≈ 1 token」是**偏保守**的估计 ——
	//   宁可贵卡同好过，也不能把请求撑爆。
	//
	// ★ 缺省 `1200` 是**按块级粒度**定的，文档级下每一份都超过它，于是分批
	//   退化成「一份一批」。不影响正确性（单条超限时它单独成批、不报错），
	//   但它已经失去意义：现在挡的不是「一批太多」，而是任何一批。M4 之后
	//   仍待按文档级实测重定（粗估 4000–6000），字段本身不动（§8.1.1）。
	MaxBatchChars int `yaml:"max_batch_chars"`

	// TopK 是 kb_search 默认召回条数。
	TopK int `yaml:"top_k"`

	// Splitter 是**遗留**字段 —— v2 起它不参与索引（§9.1）。
	Splitter KBSplitter `yaml:"splitter"`
}

// KBSplitter 是 v1 的 markdown 切分参数，v2 起整体失效。
//
// ★ 保留结构而不是删掉，是因为 `decodeStrict`：存量配置里写着它，当未知字段
// 处理会让那些配置起不来。v3 删除（§9.1）。
type KBSplitter struct {
	// Headers 曾是「标题符号 → 元数据字段名」。
	//
	// ★ **v2 起没有任何东西读它**：章节与行号由按行扫描产出（§6.2），切分器
	//   已经不在了。写了的配置会在 `kb status` 里看到一句「该配置项已不生效」，
	//   没写的不会 —— 好让那句话只对真正受影响的人出现。
	//
	// ★ 它也**不再校验非空**：一个被忽略的字段去校验内容，只会让「按文档说的
	//   把它清空」变成一次启动失败。
	Headers map[string]string `yaml:"headers"`
}

// ModelKind 区分模型是聊天用还是向量化用。
type ModelKind string

const (
	// ModelKindChat 是缺省 —— 绝大多数模型就是聊天模型。
	ModelKindChat ModelKind = "chat"
	// ModelKindEmbedding 是向量化模型。装配时走 eino-ext 的 embedder，
	// **不经过 llm/common**（那边打的是 /chat/completions）。
	ModelKindEmbedding ModelKind = "embedding"
)

// IsEmbedding 报这个模型是否用于向量化。
func (m *Model) IsEmbedding() bool { return m != nil && ModelKind(m.Kind) == ModelKindEmbedding }

// Duration 是强制字符串的时长类型。
//
// ★ 为什么不让 yaml.v3 直接解 `time.Duration`：那样 `timeout: 60`（漏了单位）
// 会被当成 60 纳秒 —— 一个几乎没人会写、但写出来就极难发现的错。
type Duration time.Duration

// UnmarshalYAML 要求**带单位**的字符串。
func (d *Duration) UnmarshalYAML(n *yamlNode) error {
	// ★ 先看类型再解码：yaml 能把 !!int 节点"成功"解进 string（得到 "60"），
	//   只靠 Decode 的 error 会漏掉 `timeout: 300` 这种写法。
	if n.Tag != "!!str" {
		return fmt.Errorf("duration must be a string with a unit (e.g. 60s / 5m); bare numbers are not allowed")
	}
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("cannot parse duration: %w", err)
	}
	if strings.TrimSpace(s) == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("cannot parse duration %q: %w", s, err)
	}
	if v < 0 {
		return fmt.Errorf("duration %q must not be negative", s)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML 输出带单位的字符串。
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Duration 返回底层值。
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// SubAgentSpec 是**脱离 config 自己的**一个 agent 声明快照。
//
// ★ 之所以另立一个类型：它是**值**，不是活配置的一部分。
//
//	[Config.AgentSpecs] 每次调用都造一份新的（连 DisabledTools 都拷贝一份），
//	于是装配层拿到的是「某一刻的声明」，而不是会被热更新换掉的那个对象。
//
// ★ 类型上也与 config 自己的结构解耦：规格树里装的是**已经解析好的**
//
//	子树，而不是一串待查的 agent id —— 装配层不必再回头查配置。
type SubAgentSpec struct {
	ID          string
	Description string
	Instruction string
	MaxIter     int
	ModelRef    string
	// DisabledTools 只属于这一个 agent —— 与全局 deny 分层保存，绝不合并。
	DisabledTools []string
	// Reasoning 是这个 agent 要的**档位名**（不是字段形状 —— 形状由模型的
	// 词汇表决定）。空 = 退到模型的 `reasoning_default`，再空则不注入思考。
	Reasoning string
	// Temperature / MaxTokens 是这个 agent 的采样参数。
	//
	// ★ 与 config.Agent 上同名字段逐字对应：装配层只搬值，不做换算。
	//   它们最后落到 llm.RequestOptionsFor 的合并链末端 —— 见那里的顺序说明。
	Temperature *float32
	MaxTokens   *int
	// Sub 声明它的下一层。
	Sub []SubAgentSpec
}
