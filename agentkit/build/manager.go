package build

import (
	"context"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

type ConfigManager interface {
	// Current returns the configuration the process runs on.
	//
	// ★ It is the **whole** answer, and that is the point: ★ the tree is
	//   built from the configuration the manager holds **now**, and a
	//   reload publishes a new one. ★ A counter used to say "the tree
	//   moved"; the object says it too, and it says which tree.
	Current() *spec.Config
	// Publish accepts a configuration the builder read itself.
	//
	// ★ The builder reads the configuration again when the write path
	//   marked a change, so that "the next turn uses it" is a promise and
	//   not a race with the watcher's poll. ★ The configuration is
	//   **published** rather than kept privately: one tree, one answer to
	//   "what is running" — the same rule every other answer follows.
	Publish(cfg *spec.Config)
}

type modelFactory func(ctx context.Context, cfg *spec.Config, ref string,
	in CallIntent, log *slog.Logger) (model.BaseModel[*schema.Message], error)

func defaultModelFactory(ctx context.Context, cfg *spec.Config, ref string,
	in CallIntent, log *slog.Logger) (model.BaseModel[*schema.Message], error) {
	return BuildModel(ctx, cfg, ref, in, log)
}
