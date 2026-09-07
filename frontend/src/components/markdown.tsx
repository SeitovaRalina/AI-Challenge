import ReactMarkdown, { type Components } from 'react-markdown'

const components: Components = {
  p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
  ul: ({ children }) => (
    <ul className="mb-2 list-disc space-y-1 pl-4 last:mb-0">{children}</ul>
  ),
  ol: ({ children }) => (
    <ol className="mb-2 list-decimal space-y-1 pl-4 last:mb-0">{children}</ol>
  ),
  li: ({ children }) => <li>{children}</li>,
  strong: ({ children }) => (
    <strong className="font-semibold text-foreground">{children}</strong>
  ),
  code: ({ className, children }) => {
    // Fenced code blocks with a language tag (```js) get a "language-js"
    // className from remark; inline `code` never does. Only the inline case
    // gets the small pill styling — block code is styled by `pre` below, so
    // it isn't double-boxed.
    if (className) {
      return <code className={className}>{children}</code>
    }
    return (
      <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
        {children}
      </code>
    )
  },
  pre: ({ children }) => (
    <pre className="mb-2 overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs leading-relaxed last:mb-0">
      {children}
    </pre>
  ),
  a: ({ children, href }) => (
    <a href={href} className="underline underline-offset-2" target="_blank" rel="noreferrer">
      {children}
    </a>
  ),
}

interface MarkdownProps {
  children: string
}

export function Markdown({ children }: MarkdownProps) {
  return (
    <div className="text-sm leading-relaxed text-foreground">
      <ReactMarkdown components={components}>{children}</ReactMarkdown>
    </div>
  )
}
