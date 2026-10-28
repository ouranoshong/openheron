import { useEffect, useRef, useState } from "react"

import { highlightToHtml } from "@/lib/highlight"
import { cn } from "@/lib/utils"

/**
 * 代码块：先纯文本，shiki 就绪后换成着色结果。
 *
 * ★ react-markdown 的组件是同步的，没法等一个 Promise。「先可读、再变好看」
 *   是唯一稳妥的顺序 —— 反过来会先白一片。
 *
 * ★ 高亮失败必须退回纯文本（`highlightToHtml` 返回 null）。代码块整个
 *   消失比没有颜色糟得多。
 *
 * ★ 流式下不高亮正在写的那一段。代码块逐段追加，每次变化都重新高亮会让
 *   整块内容闪，所以：文本变过就退回纯文本、安静 250ms 再着色，等待
 *   期间又变了就丢弃那次结果 —— 否则出现「旧内容的新着色」。
 *
 * ★ `className="shiki"` 不能省：`structure: "inline"` 只返回 span、不带
 *   wrapper class，而 index.css 里的规则是 `.shiki span` —— 没有这个
 *   class，颜色变量没人读，渲染出来全是默认文字色。
 */
export function CodeBlock({
  lang,
  text,
  className,
  testId = "code-block",
}: {
  lang?: string
  text: string
  className?: string
  testId?: string
}) {
  const supported = typeof IntersectionObserver !== "undefined"
  const [html, setHtml] = useState<string | null>(null)
  const [visible, setVisible] = useState(!supported)
  const preRef = useRef<HTMLPreElement>(null)
  const timer = useRef<number | undefined>(undefined)
  const current = useRef(text)

  /**
   * ★ 只有接近视口的代码块才排高亮任务。
   *
   *   历史一次挂载几十条消息时，每个 CodeBlock 都在 250ms 后调用 shiki；
   *   离屏块的高亮结果用户看不见，却会占满主线程。折叠的工具卡因为子内容
   *   不挂载本来就不会走到这里，这里再兜住正文里的长历史。
   */
  useEffect(() => {
    if (!supported) return
    const el = preRef.current
    if (!el) return
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true)
          io.disconnect()
        }
      },
      { rootMargin: "300px" },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [supported])

  useEffect(() => {
    if (!visible) return
    if (current.current !== text) {
      current.current = text
      setHtml(null)
    }

    clearTimeout(timer.current)
    timer.current = window.setTimeout(() => {
      void highlightToHtml(text, lang).then((h) => {
        if (current.current === text && h) setHtml(h)
      })
    }, 250)

    return () => window.clearTimeout(timer.current)
  }, [text, lang, visible])

  return (
    <pre ref={preRef} data-testid={testId} data-lang={lang ?? "text"} className={cn(BASE, className)}>
      {html ? (
        // 唯一往 DOM 注入 HTML 的地方，而内容是 shiki 自己的 span 树 ——
        // 它会转义代码文本里的 HTML，所以模型输出无法借此注入标签。
        <code className="shiki" dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <code>{text}</code>
      )}
    </pre>
  )
}

const BASE = "bg-muted overflow-x-auto rounded-lg p-3 text-xs"
