package kb

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

const (
	FieldContent = "content"
	FieldVector  = "vector_content"

	defaultBatchSize     = 10
	defaultTopK          = 5
	defaultMaxBatchChars = 1200
)

type Fields struct {
	Key         string
	Field2Value map[string]FieldValue
}

type FieldValue struct {
	Value     any
	EmbedKey  string
	Stringify func(val any) (string, error)
}

type DocumentToFields func(ctx context.Context, doc *schema.Document) (*Fields, error)
