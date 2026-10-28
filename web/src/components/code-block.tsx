import { useEffect, useRef, useState } from "react"

import { highlightToHtml } from "@/lib/highlight"
import { cn } from "@/lib/utils"

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
        <code className="shiki" dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <code>{text}</code>
      )}
    </pre>
  )
}

const BASE = "bg-muted overflow-x-auto rounded-lg p-3 text-xs"
