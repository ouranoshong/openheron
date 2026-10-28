package common

import (
	"github.com/cloudwego/eino/schema"
)

// attributingStream 给流套一层错误归因。
//
// ★ **为什么必须有它**：端点错误（429 / 401 / …）在流式下**不从 Stream 返回**，
//
//	而是 go-openai 的 processLines 在**读取流的途中**认出 `data: {"error":`
//	开头，累积正文后由 Recv 抛出。只归因 Stream 的返回 error 等于一条都抓不到 ——
//	症状是「流到一半静默停住」。
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
			// 下游提前放弃（openheron 的 Abort / 抢占会走到这里）：关掉上游流，
			// 别让 goroutine 继续空转收数据。
			if closed := sw.Send(msg, nil); closed {
				return
			}
		}
	}()

	return out
}
