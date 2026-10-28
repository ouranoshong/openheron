package message

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// VariantIsToolResult reports whether a message variant carries tool output.
func VariantIsToolResult(mv *adk.TypedMessageVariant[*schema.Message]) bool {
	if mv == nil {
		return false
	}
	if mv.Role == schema.Tool {
		return true
	}
	if mv.Message != nil {
		return len(toolResults(mv.Message)) > 0
	}
	return false
}

// DrainToolResult consumes a tool-result variant and returns its text and call ID.
func DrainToolResult(mv *adk.TypedMessageVariant[*schema.Message]) (content, id, name string) {
	if mv == nil {
		return "", "", ""
	}
	if mv.IsStreaming && mv.MessageStream != nil {
		var buf strings.Builder
		for {
			chunk, err := mv.MessageStream.Recv()
			if errors.Is(err, io.EOF) || err != nil {
				break
			}
			for _, result := range toolResults(chunk) {
				if id == "" {
					id = result.ID
				}
				if name == "" {
					name = result.Name
				}
				buf.WriteString(result.Content)
			}
			if text := Text(chunk); text != "" && len(toolResults(chunk)) == 0 {
				buf.WriteString(text)
			}
		}
		return buf.String(), id, name
	}
	if mv.Message == nil {
		return "", "", ""
	}
	results := toolResults(mv.Message)
	if len(results) == 0 {
		return Text(mv.Message), "", ""
	}
	for _, result := range results {
		if id == "" {
			id = result.ID
		}
		if name == "" {
			name = result.Name
		}
		content += result.Content
	}
	return content, id, name
}

// ConcatChunks merges streaming message chunks.
func ConcatChunks(chunks []*schema.Message) (*schema.Message, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks to concat")
	}
	if len(chunks) == 1 {
		return chunks[0], nil
	}
	return schema.ConcatMessages(chunks)
}
