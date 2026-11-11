package build

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	fsapi "github.com/cloudwego/eino/adk/filesystem"
	fsmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/shell"
)

// executeArgs is the execute tool's argument shape.
type executeArgs struct {
	Command string `json:"command"`
}

// newExecuteTool builds the execute tool.
//
// ★ It is **ours**, not the framework's. The framework mounts one too
//
//	(eino's filesystem middleware), but its description says nothing about
//	the shell on this machine — and the model is the one who writes the
//	command, so the description is the only place the dialect can be
//	stated. The framework's middleware is therefore told not to mount its
//	own (`cfg.StreamingShell` stays nil): two tools with one name is a
//	model that calls the wrong one.
//
// ★ The streaming body is the framework's, copied: the model reads the
//
//	output, so the sentences it ends with ("[Command failed with exit
//	code 2]", "[Command executed successfully with no output]") are part
//	of the contract. Changing them is a change the model can see.
func newExecuteTool(sb fsapi.StreamingShell, desc string) (tool.BaseTool, error) {
	return utils.InferStreamTool("execute", desc,
		func(ctx context.Context, input executeArgs) (*schema.StreamReader[string], error) {
			result, err := sb.ExecuteStreaming(ctx, &fsapi.ExecuteRequest{
				Command: input.Command,
			})
			if err != nil {
				return nil, err
			}
			sr, sw := schema.Pipe[string](10)
			go func() {
				defer func() {
					if e := recover(); e != nil {
						sw.Send("", fmt.Errorf("panic: %v,\n stack: %s", e, string(debug.Stack())))
					}
					sw.Close()
				}()

				var hasSentContent bool
				var exitCode *int

				for {
					chunk, recvErr := result.Recv()
					if recvErr == io.EOF {
						break
					}
					if recvErr != nil {
						sw.Send("", recvErr)
						return
					}
					if chunk == nil {
						continue
					}
					if chunk.ExitCode != nil {
						exitCode = chunk.ExitCode
					}

					parts := make([]string, 0, 2)
					if chunk.Output != "" {
						parts = append(parts, chunk.Output)
					}
					if chunk.Truncated {
						parts = append(parts, "[Output was truncated due to size limits]")
					}
					if len(parts) > 0 {
						sw.Send(strings.Join(parts, "\n"), nil)
						hasSentContent = true
					}
				}

				if exitCode != nil && *exitCode != 0 {
					sw.Send(fmt.Sprintf("\n[Command failed with exit code %d]", *exitCode), nil)
				} else if !hasSentContent {
					sw.Send("[Command executed successfully with no output]", nil)
				}
			}()

			return sr, nil
		})
}

// executeToolDesc is the description the model reads.
//
// ★ The framework's description is the base — it carries the quoting rules
//
//	— and the dialect is appended: the model writes the command, so it has
//	to know which shell is going to run it. A description that says
//	"sandbox environment" and nothing else is how a Windows agent ends up
//	running `ls -la` in cmd.exe.
func executeToolDesc(sh shell.Shell) string {
	return strings.TrimSpace(fsmw.ExecuteToolDesc) + "\n\n" + sh.Note()
}
