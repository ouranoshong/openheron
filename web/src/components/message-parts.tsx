import { BrainIcon, ChevronRightIcon } from "lucide-react"
import { useState } from "react"

import { MarkdownText, MessageBody } from "@/components/markdown"
import { ToolCard } from "@/components/tool-card"
import { thinkingLabel } from "@/lib/thinking"
import type { Message, Part } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * 一条消息里的各个 part。
 *
 * ★ reasoning 默认折叠。
 *   思考过程通常很长，默认展开会把正文挤到屏幕外；而它本来就是「想看才看」
 *   的内容。用户想看推理时可以展开，且展开状态按 part 记住。
 */

/**
 * ReasoningPart 渲染。
 *
 * 单独的组件而不是内联三元：它有自己的展开状态，而 MessageParts 是
 * 每次流式增量都会重渲染的 —— 把 useState 提到上层会让整条消息
 * 在每次 token 到达时都跟着重建。
 *
 * ★ **用时**在思维链结束时才定下来：期间只显示「Thinking…」，
 *   结束后标题变成「Thought for 3.2s」—— 结束的那一刻用户才知道
 *   「刚才那一下想了多久」。
 *
 *   ★ 计时用的是**服务端事件的时间戳**（part 的 startedAt/endedAt），
 *     不是组件里的 Date.now()：服务端知道模型**真的**在想的那段时间，
 *     而客户端只知道自己收到事件的时间；而且这样组件的渲染里没有
 *     「读时钟」这种 impure call（归约器也因此保持纯函数）。
 */
export function Reasoning({
  text,
  complete,
  interrupted,
  elapsedMs,
}: {
  text: string
  complete: boolean
  interrupted?: boolean
  /** 用时（毫秒）；null = 不知道（还没结束，或历史回放没有计时）。 */
  elapsedMs: number | null
}) {
  const [open, setOpen] = useState(false)

  if (!text) return null

  return (
    <div data-testid="reasoning" className="border-muted/60 my-1.5 rounded-lg border border-dashed">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="text-muted-foreground hover:text-foreground flex w-full items-center gap-1.5 px-3 py-1.5 text-xs"
      >
        <ChevronRightIcon className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        <BrainIcon className="size-3.5 shrink-0" />
        <span className="font-medium" data-testid="reasoning-label">
          {thinkingLabel({ complete, elapsedMs, interrupted })}
        </span>
        {open && <span className="ml-auto opacity-70">collapse</span>}
      </button>
      {open && (
        <div className="text-muted-foreground border-t px-3 py-2 text-xs whitespace-pre-wrap">
          {text}
        </div>
      )}
    </div>
  )
}

export function MessageParts({ parts, role }: { parts: Part[]; role: Message["role"] }) {
  return (
    <>
      {parts.map((p) => {
        switch (p.kind) {
          case "text":
            if (!p.text) return null
            // ★ 用户输入按字面呈现：react-markdown 默认把软换行折叠成空格，
            //   多行指令在历史里会变形；用户写下的 `#` / `**` 也不该被当成格式。
            if (role === "user") {
              return (
                <MessageBody key={p.id}>
                  <div data-testid="user-text" className="whitespace-pre-wrap break-words">
                    {p.text}
                  </div>
                </MessageBody>
              )
            }
            return (
              <MessageBody key={p.id}>
                <MarkdownText text={p.text} />
              </MessageBody>
            )

          case "reasoning":
            return (
              <Reasoning
                key={p.id}
                text={p.text}
                complete={p.complete}
                interrupted={p.interrupted}
                elapsedMs={p.elapsedMs ?? null}
              />
            )

          case "tool":
            return <ToolCard key={p.id} part={p} />
        }
      })}
    </>
  )
}
