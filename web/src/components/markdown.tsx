import { createContext, memo, useContext, type ComponentPropsWithoutRef, type ReactNode } from "react"
import Markdown from "react-markdown"
import remarkGfm from "remark-gfm"

import { CodeBlock } from "@/components/code-block"
import { langOf } from "@/lib/highlight"
import { cn } from "@/lib/utils"

const InPreContext = createContext(false)

// ★ The prose width. The chat column is wide so that a table or a code block
//   fits whole — but a paragraph of sixty characters is a hard read, so the
//   **text** blocks stop at the prose column and the block elements do not.
//   One class, applied to the text elements only: a table inside a paragraph
//   is not a thing, and the ceiling lives on the cells.
const PROSE = "max-w-[var(--prose-column)]"

function Pre({ children }: { children: ReactNode }) {
  return <InPreContext.Provider value>{children}</InPreContext.Provider>
}

function InlineCode({ className, children, ...rest }: ComponentPropsWithoutRef<"code">) {
  return (
    <code className={cn("bg-muted rounded px-1 py-0.5 font-mono text-[0.9em]", className)} {...rest}>
      {children}
    </code>
  )
}

function CodeAdapter({
  className,
  children,
  ...rest
}: ComponentPropsWithoutRef<"code"> & { node?: unknown }) {
  const inPre = useContext(InPreContext)
  const raw = String(children ?? "").replace(/\n$/, "")
  const lang = langOf(className)

  if (inPre || lang) return <CodeBlock lang={lang} text={raw} />
  return <InlineCode className={className} {...rest}>{children}</InlineCode>
}

export const MarkdownText = memo(function MarkdownText({ text }: { text: string }) {
  return (
    <Markdown
      remarkPlugins={[remarkGfm]}
      components={{
        pre: ({ children }) => <Pre>{children}</Pre>,
        code: (props) => <CodeAdapter {...props} />,
        table: ({ children }) => (
          <div data-testid="markdown-table" className="my-3 w-full max-w-full overflow-x-auto">
            <table
              className={cn(
                "border-border w-full border-collapse text-sm tabular-nums",
                "[&_td]:border [&_td]:border-border [&_td]:px-3 [&_td]:py-2 [&_td]:align-top",
                // ★ A table has two limits, and they answer different questions.
                //   The **floor** (7rem per column) is what makes a table of
                //   many columns overflow and scroll, instead of squeezing
                //   every column into a sliver.
                //   The **ceiling** (28rem per cell) is what makes a long
                //   sentence wrap inside its cell instead of stretching the
                //   table past the column.
                "[&_td]:min-w-[7rem] [&_td]:max-w-[28rem] [&_td]:break-words",
                "[&_th]:border [&_th]:border-border [&_th]:bg-muted/50 [&_th]:px-3 [&_th]:py-2 [&_th]:text-left [&_th]:font-medium [&_th]:min-w-[7rem] [&_th]:whitespace-nowrap",
              )}
            >
              {children}
            </table>
          </div>
        ),
        p: ({ children }) => <p className={cn("my-2", PROSE)}>{children}</p>,
        h1: ({ children }) => (
          <h1 className={cn("mt-6 mb-2 text-xl leading-snug font-semibold", PROSE)}>{children}</h1>
        ),
        h2: ({ children }) => (
          <h2 className={cn("mt-5 mb-2 text-lg leading-snug font-semibold", PROSE)}>{children}</h2>
        ),
        h3: ({ children }) => (
          <h3 className={cn("mt-4 mb-1.5 text-base leading-snug font-semibold", PROSE)}>{children}</h3>
        ),
        h4: ({ children }) => (
          <h4 className={cn("mt-4 mb-1.5 text-sm font-semibold", PROSE)}>{children}</h4>
        ),
        h5: ({ children }) => (
          <h5 className={cn("mt-4 mb-1.5 text-sm font-semibold", PROSE)}>{children}</h5>
        ),
        h6: ({ children }) => (
          <h6 className={cn("text-muted-foreground mt-4 mb-1.5 text-sm font-semibold", PROSE)}>
            {children}
          </h6>
        ),
        strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
        em: ({ children }) => <em className="italic">{children}</em>,
        del: ({ children }) => <del className="opacity-70 line-through">{children}</del>,
        blockquote: ({ children }) => (
          <blockquote
            className={cn("border-border text-muted-foreground my-3 border-l-2 pl-3", PROSE)}
          >
            {children}
          </blockquote>
        ),
        hr: () => <hr className="border-border my-5 border-t" />,
        img: ({ src, alt, title }) => (
          <img src={src} alt={alt} title={title} className="my-3 max-w-full rounded-lg" />
        ),
        ul: ({ children, className }) => {
          const taskList = className?.includes("contains-task-list")
          return (
            <ul
              className={cn(
                "my-2 space-y-1 pl-5",
                taskList ? "list-none pl-1" : "list-disc",
                PROSE,
                className,
              )}
            >
              {children}
            </ul>
          )
        },
        ol: ({ children, start }) => (
          <ol start={start} className={cn("my-2 list-decimal space-y-1 pl-5", PROSE)}>
            {children}
          </ol>
        ),
        li: ({ children, value }) => (
          <li
            value={value}
            className="pl-1 [&>p]:my-0 [&>p+p]:mt-2 [&>input]:mr-2 [&>input]:size-3.5 [&>input]:translate-y-0.5"
          >
            {children}
          </li>
        ),
        a: ({ children, ...rest }) => (
          <a className="underline underline-offset-4" target="_blank" rel="noreferrer" {...rest}>
            {children}
          </a>
        ),
      }}
    >
      {text}
    </Markdown>
  )
})

export function MessageBody({ children }: { children: ReactNode }) {
  return (
    <div
      data-testid="message-body"
      className="space-y-2 break-words text-[15px] leading-[1.75] empty:hidden [&>p]:my-2 [text-autospace:normal]"
    >
      {children}
    </div>
  )
}