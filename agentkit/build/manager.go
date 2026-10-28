package build

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec"
)

// ConfigManager 提供「当前这一代配置」。
//
// ★ 它是 agent 包与「配置从哪来」之间**唯一**的接缝：agent 包只问
//
//	「现在这一代是什么」，而这份配置**原样**交出去 —— 派生（规格、模型、
//	工具）都在 agent 包里做。
//
// ★ 代际号必须**单调递增**：池按 (名字, 代际) 复用实例，而池里有一条
//
//	「期间已经换代了 ⇒ 用更新的那份」的判断（pool.go:115），它依赖代际
//	**可比大小**。所以这里不能用配置指纹（字符串）—— 指纹留给「这一轮跑的是
//	哪份配置」的归因（config/spec.go:250）。
//
// ★ 发布出去的 *spec.Config **视为只读**：Reloader 每次 Load 都造新对象，
//
//	没有任何一方在发布后原地改它。这是「配置与代际同锁取出」之外的第二道保证。
type ConfigManager interface {
	// Current 返回当前配置与它的代际号（首次加载后从 1 开始）。
	//
	// ★ 配置与代际号必须在**同一把锁内成对返回**（§6.4）：分两次取会在
	//	配置变更夹在中间时拿到「新配置 + 旧代际」。
	Current() (*spec.Config, uint64)
}

// modelFactory 是「引用 + 调用意图 → 模型实例」的注入点。
//
// ★ 生产永远用 [BuildModel]；只有**包内测试**会换成桩模型。
//
//	它不是 Option —— 公开面不该出现「怎么造模型」。
type modelFactory func(ctx context.Context, cfg *spec.Config, ref string,
	in CallIntent) (model.BaseModel[*schema.Message], error)

// defaultModelFactory 是生产用的工厂。
//
// ★ 必须包一层：BuildModel 的返回类型是 model.ToolCallingChatModel，
//
//	而函数类型之间没有协变 —— 不能直接把它赋给 modelFactory。
func defaultModelFactory(ctx context.Context, cfg *spec.Config, ref string,
	in CallIntent) (model.BaseModel[*schema.Message], error) {
	return BuildModel(ctx, cfg, ref, in)
}
