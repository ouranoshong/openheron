package build

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

type ConfigManager interface {
	Current() (*spec.Config, uint64)
}

type modelFactory func(ctx context.Context, cfg *spec.Config, ref string,
	in CallIntent) (model.BaseModel[*schema.Message], error)

func defaultModelFactory(ctx context.Context, cfg *spec.Config, ref string,
	in CallIntent) (model.BaseModel[*schema.Message], error) {
	return BuildModel(ctx, cfg, ref, in)
}
