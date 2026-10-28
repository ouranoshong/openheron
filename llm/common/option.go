package common

import (
	"context"

	acl "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type PayloadModifier func(ctx context.Context, rawBody []byte) ([]byte, error)

func WithPayloadModifier(fn PayloadModifier) model.Option {
	return acl.WithRequestPayloadModifier(
		func(ctx context.Context, _ []*schema.Message, rawBody []byte) ([]byte, error) {
			return fn(ctx, rawBody)
		})
}

func WithExtraFields(fields map[string]any) model.Option {
	return acl.WithExtraFields(fields)
}

func WithExtraHeader(header map[string]string) model.Option {
	return acl.WithExtraHeader(header)
}
