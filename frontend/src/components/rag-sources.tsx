import { useState } from 'react'
import { ArrowRight, Check, ChevronRight, X } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { cn } from 'cn'
import type { Citation, RetrievedChunk } from '@/lib/api'

// Shared by the standalone «Похожие задачи» screen (rag-panel.tsx, days
// 22-24) and the main chat's own RAG grounding (day 25, chat-panel.tsx) —
// both show the same thing, a historical fragment plus the verbatim quotes
// an answer drew from it, so this lives in one place instead of two.

// parseMessageIndex pulls the message index out of a structural chunk's
// section ("message[3].assistant" -> 3) so a source can link straight to
// that message in the real chat — null for sections that aren't tied to
// one specific message (title, estimate.*, task.*, or any fixed_size
// window).
export function parseMessageIndex(section: string): number | null {
  const m = /^message\[(\d+)\]/.exec(section)
  return m ? Number(m[1]) : null
}

// SourceList renders every retrieved chunk an answer grounded in.
export function SourceList({
  sources,
  onOpenSource,
  expectedSources,
}: {
  sources: RetrievedChunk[]
  onOpenSource: (chatId: string, messageIndex: number | null) => void
  expectedSources?: string[]
}) {
  return (
    <ul className="mt-1 flex flex-col gap-1">
      {sources.map((r) => (
        <SourceItem
          key={r.chunk_id}
          chunk={r}
          onOpenSource={onOpenSource}
          isExpected={expectedSources?.includes(r.session_id) ?? false}
        />
      ))}
    </ul>
  )
}

// SourceItem — свёрнутая строка (заголовок сессии, где внутри неё, score);
// разворачивается в полный текст найденного чанка и кнопку перехода в
// реальный чат, к конкретному сообщению, если секция к нему привязана.
// isExpected (только в «10 контрольных вопросах») подсвечивает чанк,
// из-за которого вопросу засчитан бейдж «сессия найдена» — иначе непонятно,
// почему бейдж зелёный, если сам текст ответа этот факт не нашёл.
export function SourceItem({
  chunk,
  onOpenSource,
  isExpected,
}: {
  chunk: RetrievedChunk
  onOpenSource: (chatId: string, messageIndex: number | null) => void
  isExpected?: boolean
}) {
  const [open, setOpen] = useState(false)
  const messageIndex = parseMessageIndex(chunk.section)

  return (
    <li className={cn('rounded border border-border/60', isExpected && 'border-emerald-500/50 bg-emerald-500/5')}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-start gap-1.5 px-2 py-1.5 text-left"
      >
        <ChevronRight
          className={cn('mt-0.5 h-3 w-3 flex-shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')}
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
            <span className="text-xs text-foreground">{chunk.title}</span>
            <span className="font-mono text-xs text-muted-foreground">{chunk.section}</span>
          </div>
          {isExpected && (
            <span className="text-xs font-medium text-emerald-700 dark:text-emerald-400">ожидаемая сессия</span>
          )}
        </div>
        <span className="flex-shrink-0 text-xs text-muted-foreground">{chunk.score.toFixed(2)}</span>
      </button>
      {open && (
        <div className="border-t border-border/60 px-2 py-1.5">
          <p className="whitespace-pre-line text-xs text-foreground">{chunk.text}</p>
          <Button
            size="xs"
            variant="outline"
            className="mt-1.5"
            onClick={() => onOpenSource(chunk.session_id, messageIndex)}
          >
            Перейти в чат
            {messageIndex != null && ' → к сообщению'}
            <ArrowRight />
          </Button>
        </div>
      )}
    </li>
  )
}

// CitationList renders every citation an answer gave, each with its
// server-side verification result.
export function CitationList({ citations }: { citations: Citation[] }) {
  return (
    <ul className="mt-1 flex flex-col gap-1">
      {citations.map((c, i) => (
        <CitationItem key={i} citation={c} />
      ))}
    </ul>
  )
}

// CitationItem — one verbatim quote the model claims backs the answer,
// with the server-side verification result (never the model's own word)
// made visible rather than hidden — an unverified citation is itself a
// finding, not noise to suppress.
export function CitationItem({ citation }: { citation: Citation }) {
  return (
    <li
      className={cn(
        'flex items-start gap-1.5 rounded border border-border/60 px-2 py-1.5',
        citation.verified ? 'border-emerald-500/40 bg-emerald-500/5' : 'border-red-500/40 bg-red-500/5',
      )}
    >
      {citation.verified ? (
        <Check className="mt-0.5 h-3 w-3 flex-shrink-0 text-emerald-600" />
      ) : (
        <X className="mt-0.5 h-3 w-3 flex-shrink-0 text-red-600" />
      )}
      <div className="min-w-0 flex-1">
        <p className="text-xs italic text-foreground">«{citation.text}»</p>
        <p className="font-mono text-[11px] text-muted-foreground">{citation.chunk_id}</p>
      </div>
    </li>
  )
}
