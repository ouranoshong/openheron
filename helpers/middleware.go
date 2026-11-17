package helpers

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// NewSafeToolMiddleware converts tool errors into error-message strings so that
// a non-zero exit code or mid-stream failure reaches the model as a readable
// tool result instead of aborting the agent pipeline. Interrupt reruns are
// passed through untouched — swallowing them would strand the approval gate.
func NewSafeToolMiddleware() adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &safeToolMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
	}
}

type safeToolMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
}

func (m *safeToolMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		result, err := endpoint(ctx, args, opts...)
		if err != nil {
			if _, ok := compose.IsInterruptRerunError(err); ok {
				return "", err
			}
			// ★ The failure is recorded **here**: this is the only layer
			//   that sees the error. Everything above it — and the model
			//   itself — sees a string.
			recordToolFailure(ctx, tCtx, err)
			return fmt.Sprintf("[tool error] %v", err), nil
		}
		return result, nil
	}, nil
}

func (m *safeToolMiddleware) WrapStreamableToolCall(
	_ context.Context,
	endpoint adk.StreamableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		sr, err := endpoint(ctx, args, opts...)
		if err != nil {
			if _, ok := compose.IsInterruptRerunError(err); ok {
				return nil, err
			}
			recordToolFailure(ctx, tCtx, err)
			return SingleChunkReader(fmt.Sprintf("[tool error] %v", err)), nil
		}
		return SafeWrapReader(ctx, tCtx, sr), nil
	}, nil
}

// SingleChunkReader returns a StreamReader that emits one string then EOF.
func SingleChunkReader(msg string) *schema.StreamReader[string] {
	r, w := schema.Pipe[string](1)
	_ = w.Send(msg, nil)
	w.Close()
	return r
}

// SafeWrapReader proxies chunks from sr; on a stream error it emits the error
// as a final chunk instead of propagating it, so the model sees a complete
// (if error-annotated) tool result rather than a pipeline failure.
//
// ★ The failure is recorded too: the stream failed **after** it started, so
//
//	there was no error to catch at the call — and the call is the thing the
//	interface has to mark as failed.
func SafeWrapReader(ctx context.Context, tCtx *adk.ToolContext, sr *schema.StreamReader[string]) *schema.StreamReader[string] {
	r, w := schema.Pipe[string](64)
	go func() {
		defer w.Close()
		for {
			chunk, err := sr.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				recordToolFailure(ctx, tCtx, err)
				_ = w.Send(fmt.Sprintf("\n[tool error] %v", err), nil)
				return
			}
			_ = w.Send(chunk, nil)
		}
	}()
	return r
}
