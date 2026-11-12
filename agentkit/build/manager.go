package build

import (
	"context"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

type ConfigManager interface {
	Current() (*spec.Config, uint64)
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
