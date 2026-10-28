package helpers

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/schema"
)

// ApplyModelRetry enables model-call retries for transient rate-limit errors.
func ApplyModelRetry(cfg *deep.Config, maxRetries int) {
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}
	cfg.ModelRetryConfig = &adk.TypedModelRetryConfig[*schema.Message]{
		MaxRetries: maxRetries,
		IsRetryAble: func(_ context.Context, err error) bool {
			if err == nil {
				return false
			}
			msg := err.Error()
			return strings.Contains(msg, "429") ||
				strings.Contains(msg, "Too Many Requests") ||
				strings.Contains(msg, "qpm limit")
		},
	}
}

const DefaultMaxRetries = 5

// IsModelRetryInProgress reports whether err is a retryable model-call failure
// that the ADK will retry on its own.
func IsModelRetryInProgress(err error) bool {
	var willRetry *adk.WillRetryError
	return errors.As(err, &willRetry)
}
