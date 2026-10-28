package common

import (
	"github.com/cloudwego/eino/schema"
)

func attributingStream(sr *schema.StreamReader[*schema.Message], hintKeys []string) *schema.StreamReader[*schema.Message] {
	out, sw := schema.Pipe[*schema.Message](1)

	go func() {
		defer sw.Close()
		for {
			msg, err := sr.Recv()
			if err != nil {
				_ = sw.Send(nil, wrap(err, hintKeys))
				return
			}
			if closed := sw.Send(msg, nil); closed {
				return
			}
		}
	}()

	return out
}
