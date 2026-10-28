package build

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
	"openheron/llm/common"
)

// FromConfig 按配置装配一个厂商模型。
//
// 这是 [common] 与 `.openheron/llm/*.yaml` 之间那一层的正式实现 ——
// [ConfigFromEnv] 是它的过渡形态，待环境变量保留为覆盖项后即可下掉。
//
// ★ 一个 provider 只建**一个**实例：模型名走请求级 option 覆盖
//
//	（acl 的 GetCommonOptions，见 [common.Config.Model]），所以同一端点下的
//	多个模型共享同一个连接池。
func FromConfig(ctx context.Context, root string, p *decl.Provider, m *decl.Model) (
	model.ToolCallingChatModel, error) {

	if p == nil {
		return nil, fmt.Errorf("llm: provider is nil")
	}

	// ★ embedding 模型必须被 chat 装配路径**拒绝**。
	//
	//	config 的 validateKB 会检查 `kb.model` 指向的那个，但那是**一处**
	//	检查 —— 任何别的地方拿 embedding 模型来建 chat 实例都拦不住。
	//	而这种错不响：实例能构造成功，只有真去调 `/chat/completions` 才 404，
	//	那时索引已经建了一半（embedder 建索引，chat 建 agent）。
	if m != nil && m.IsEmbedding() {
		return nil, fmt.Errorf("llm: model %q is kind embedding and cannot be built as a chat model; "+
			"use llm.NewEmbedder", m.ID)
	}

	timeout := p.Timeout.Duration()
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	cfg := &common.Config{
		ProviderID:  p.ID,
		APIKey:      p.APIKey,
		BaseURL:     p.BaseURL,
		HTTPClient:  &http.Client{Timeout: timeout},
		HintKeys:    p.HintKeys,
		ExtraFields: p.Extra,
	}
	if m != nil {
		cfg.Model = m.ID
		if m.Timeout.Duration() > 0 {
			// ★ 模型级可覆盖：思考模型比普通模型慢一个数量级，
			//   而 provider 级只能容纳最慢的那个 —— 那会让快模型失去超时保护。
			//   但连接池仍共享 —— 换的是 client 的超时，不是 client 本身。
			cfg.HTTPClient = &http.Client{Timeout: m.Timeout.Duration()}
		}
	}

	cm, err := common.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("llm: build provider %q: %w", p.ID, err)
	}
	return cm, nil
}

// Resolve 在已加载的配置里解析一个 `<provider>/<model>` 引用。
//
// ★ 解析不到直接报错，**不静默退化** —— 那会让「为什么这么贵」变成查不出的
//
//	问题。调用方应当在本函数返回错误时终止启动。
func Resolve(c *spec.Config, ref string) (*decl.Provider, *decl.Model, error) {
	r, err := spec.ParseRef(ref)
	if err != nil {
		return nil, nil, err
	}
	p, ok := c.Providers[r.ProviderID]
	if !ok {
		return nil, nil, fmt.Errorf("llm: provider %q is not declared under .openheron/llm/ (declared: %s)",
			r.ProviderID, joinProviderIDs(c))
	}
	m, ok := c.Models[ref]
	if !ok {
		return nil, nil, fmt.Errorf("llm: provider %q does not declare model %q", r.ProviderID, r.ModelID)
	}
	return p, m, nil
}

// CallIntent 是一次调用要的采样意图：思考档位 + 这个 agent 的采样参数。
//
// ★ 做成结构体而不是继续加参数：调用点只会搬值（agent 规格 → 这里），
//
//	而三个同源字段散成三个实参时，漏传一个既不会编译失败也不会有提示。
type CallIntent struct {
	// Reasoning 是档位名；空 = 退到模型的 reasoning_default。
	Reasoning string
	// Temperature / MaxTokens 来自 agents/*.md。
	Temperature *float32
	MaxTokens   *int
}

// BuildModel 按 `<provider>/<model>` 引用建一个**带请求级参数**的模型实例。
//
// ★ 三处装配（入口 agent、子 agent、skill 的 model 覆盖）都走这里：拆成三份
// 各写一遍的话，「思考档位 / 采样参数有没有装上」会变成三处各自的自洽 ——
// 而漏装的那一处不报错，只是模型忽然不思考了。
func BuildModel(ctx context.Context, c *spec.Config, ref string, in CallIntent) (
	model.ToolCallingChatModel, error) {

	p, m, err := Resolve(c, ref)
	if err != nil {
		return nil, err
	}
	cm, err := FromConfig(ctx, c.ConfigRoot, p, m)
	if err != nil {
		return nil, err
	}
	field, from := ReasoningFields(m)
	cm = common.AttachRequestOptions(cm, RequestOptionsFor(p, m, in), field, from)
	// ★ 盖上「这条是哪个模型、哪个档位产的」。
	//
	//	放在**最外面**：`AttachRequestOptions` 在没配任何参数时原样返回
	//	底层模型，而盖戳不能被它跳过。
	//
	//	★ 记的是**实际服务那次请求**的模型与**实际注入**的档位 ——
	//	  skill 可以覆盖 model（agentkit/build/skill.go），记配置里写的那个会与
	//	  实际不符；档位则正是装配那一刻配置里的那个（模型实例带着它）。
	return stampModel(cm, ref, effectiveTier(m, in)), nil
}

// effectiveTier 算出这次请求**实际注入**的思考档位名。
//
// ★ 与 [RequestOptionsFor] 里的算法**必须一致** —— 两处各算一遍的话，
//
//	「记录的档位」与「注入的档位」会漂移，而那正是这个字段的全部意义。
//	所以这里只算一次，那边调它。
//
// ★ 空 = 没注入思考字段：agent 没写档位、模型也没有 reasoning_default，
//
//	或者档位不在模型的词汇表里（那种情况 config 侧已记进 deferred）。
func effectiveTier(m *decl.Model, in CallIntent) string {
	level := in.Reasoning
	if level == "" && m != nil {
		level = m.ReasoningDefault
	}
	return level
}

// RequestOptionsFor 把 agent 的调用意图折成请求级注入。
//
// ★ 合并链只有一条，且顺序固定：
//
//		provider.extra ← model.extra ← reasoning[level] ← agent 采样参数
//
//	  三层合完的**完整一份**必须整份递下去 —— WithExtraFields 是**替换**
//	  不是合并（`go-openai/chat.go:415`），只传其中一层会把其余的丢掉。
//
//	  档位名由调用方给出：优先用 agent 显式的 `reasoning`，为空时退到
//	  模型的 `reasoning_default`。档位不在词汇表里时**不注入也不报错** ——
//	  config 侧已记进 deferred，启动时会打出来（「配了思考参数不报错也不
//	  生效」是这类问题唯一可见的线索）。
//
// ★ agent 的 temperature / max_tokens 走 eino 的通用 option，但**同时要把
//
//	extra 里同名的键删掉**：acl 在设置完 common options 之后才
//	`req.SetExtraFields(...)`（chat_model.go:591 / :624），extra 会覆盖
//	typed 值。不删的话「agent 写了 temperature 却被 extra 盖掉」—— 而两边
//	都配了时用户预期的是更具体的那一层赢。
func RequestOptionsFor(p *decl.Provider, m *decl.Model, in CallIntent) common.RequestOptions {
	level := effectiveTier(m, in)
	fields := shallowMerge(p.Extra, nil)
	if m != nil {
		fields = shallowMerge(fields, m.Extra)
		if lvl, ok := m.LevelFields(level); ok {
			fields = shallowMerge(fields, lvl)
		}
	}
	// 后者赢：先把低优先级的键去掉，typed 值才真的生效。
	if in.Temperature != nil {
		delete(fields, "temperature")
	}
	if in.MaxTokens != nil {
		delete(fields, "max_tokens")
	}

	ro := common.RequestOptions{
		Fields:      fields,
		Temperature: in.Temperature,
		MaxTokens:   in.MaxTokens,
	}
	if m != nil {
		ro.Model = m.ID
	}
	return ro
}

// shallowMerge 顶层键覆盖（后者赢）。
//
// 不做深合并 —— 深合并在「改 thinking.type 而保留 thinking.other」这件事上
// 语义不可预测。
func shallowMerge(a, b map[string]any) map[string]any {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// ReasoningFields 返回这个模型的思维链字段名（发送侧改名用）。
//
// ★ 空串表示「用底层库的原生字段名」—— 不要在代码里猜厂商的字段名，
//
//	那正是 TestNoVendorKnowledgeInCode 守着的事。
func ReasoningFields(m *decl.Model) (field, from string) {
	if m == nil {
		return "", ""
	}
	from = m.ReasoningFieldFrom
	if from == "" {
		from = m.ReasoningField
	}
	return m.ReasoningField, from
}

func joinProviderIDs(c *spec.Config) string {
	if len(c.Providers) == 0 {
		return "none"
	}
	names := make([]string, 0, len(c.Providers))
	for id := range c.Providers {
		names = append(names, id)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// SkillsDir 返回配置根下的 skills 目录（未声明时给默认相对路径）。
func SkillsDir(root string) string { return filepath.Join(root, spec.DirSkills) }
