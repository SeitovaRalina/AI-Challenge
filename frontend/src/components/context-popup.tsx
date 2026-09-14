import { X } from 'lucide-react'

import { cn } from 'cn'
import type {
  AgentMessage,
  BranchSummary,
  CompressionEvent,
  ContextStrategy,
} from '@/lib/api'

interface ContextPopupProps {
  strategy: ContextStrategy
  historyKeepLastN: number
  messages: AgentMessage[]
  facts?: Record<string, string>
  branches?: BranchSummary[]
  activeBranchId?: string
  compressionEvents: CompressionEvent[]
  summarizedMessageCount: number
  rawMessageCount: number
  onClose: () => void
}

const STRATEGY_TITLES: Record<ContextStrategy, string> = {
  sliding_window: 'Sliding window',
  sticky_facts: 'Sticky facts',
  branching: 'Branching',
  rolling_summary: 'Rolling summary',
}

// ContextPopup is the /context command's output: unlike /tokens (a small
// panel anchored above the composer), this is a centered modal — the user
// asked for it to "float in the center over the chat" — since its content
// (a full branch tree, or a facts table) can be taller than a corner popup
// comfortably holds.
export function ContextPopup({
  strategy,
  historyKeepLastN,
  messages,
  facts,
  branches,
  activeBranchId,
  compressionEvents,
  summarizedMessageCount,
  rawMessageCount,
  onClose,
}: ContextPopupProps) {
  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/40" onClick={onClose} />
      <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
        <div
          onClick={(event) => event.stopPropagation()}
          className="max-h-[80vh] w-full max-w-md overflow-y-auto rounded-xl border border-border bg-card p-4 shadow-lg"
        >
          <div className="flex items-center justify-between">
            <p className="text-sm font-medium text-foreground">
              Контекст сейчас · {STRATEGY_TITLES[strategy]}
            </p>
            <button
              type="button"
              onClick={onClose}
              className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>

          <div className="mt-3.5">
            {strategy === 'sliding_window' && (
              <SlidingWindowSection messages={messages} historyKeepLastN={historyKeepLastN} />
            )}
            {strategy === 'sticky_facts' && (
              <>
                <FactsSection facts={facts} />
                <div className="mt-4 border-t border-border pt-3.5">
                  <SlidingWindowSection messages={messages} historyKeepLastN={historyKeepLastN} />
                </div>
              </>
            )}
            {strategy === 'branching' && (
              <BranchingSection branches={branches} activeBranchId={activeBranchId} />
            )}
            {strategy === 'rolling_summary' && (
              <RollingSummarySection
                compressionEvents={compressionEvents}
                summarizedMessageCount={summarizedMessageCount}
                rawMessageCount={rawMessageCount}
              />
            )}
          </div>
        </div>
      </div>
    </>
  )
}

function SlidingWindowSection({
  messages,
  historyKeepLastN,
}: {
  messages: AgentMessage[]
  historyKeepLastN: number
}) {
  const windowMessages =
    historyKeepLastN > 0 ? messages.slice(-historyKeepLastN) : messages
  const droppedCount = messages.length - windowMessages.length

  return (
    <div>
      <p className="text-xs font-medium text-muted-foreground">
        В промпт уходят последние {historyKeepLastN || messages.length} сообщений
      </p>
      <ul className="mt-2 flex flex-col gap-1.5">
        {windowMessages.map((message, index) => (
          <li key={index} className="rounded-md border border-border px-2.5 py-1.5 text-xs">
            <span className="font-mono text-[10px] tracking-wide text-muted-foreground uppercase">
              {message.role === 'user' ? 'user' : 'assistant'}
            </span>
            <p className="mt-0.5 line-clamp-2 text-foreground">{message.content}</p>
          </li>
        ))}
      </ul>
      {droppedCount > 0 && (
        <p className="mt-2 text-[11px] text-muted-foreground">
          Ещё {droppedCount} сообщ. хранится в истории чата, но больше не отправляется модели.
        </p>
      )}
    </div>
  )
}

function FactsSection({ facts }: { facts?: Record<string, string> }) {
  const entries = Object.entries(facts ?? {})
  return (
    <div>
      <p className="text-xs font-medium text-muted-foreground">
        Факты (обновляются после каждого сообщения)
      </p>
      {entries.length === 0 ? (
        <p className="mt-1.5 text-sm text-muted-foreground">Пока ничего не зафиксировано</p>
      ) : (
        <dl className="mt-2 flex flex-col gap-1.5">
          {entries.map(([key, value]) => (
            <div key={key} className="rounded-md border border-border px-2.5 py-1.5 text-xs">
              <dt className="font-mono text-[10px] tracking-wide text-muted-foreground">{key}</dt>
              <dd className="mt-0.5 text-foreground">{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}

function BranchingSection({
  branches,
  activeBranchId,
}: {
  branches?: BranchSummary[]
  activeBranchId?: string
}) {
  if (!branches || branches.length === 0) {
    return <p className="text-sm text-muted-foreground">Веток пока нет</p>
  }
  return (
    <div>
      <p className="text-xs font-medium text-muted-foreground">Дерево веток</p>
      <ul className="mt-2 flex flex-col gap-1.5">
        {branches.map((branch) => (
          <li
            key={branch.id}
            className={cn(
              'rounded-md border px-2.5 py-1.5 text-xs',
              branch.id === activeBranchId
                ? 'border-primary bg-primary/5'
                : 'border-border',
            )}
          >
            <div className="flex items-center justify-between">
              <span className="font-medium text-foreground">{branch.label}</span>
              {branch.id === activeBranchId && (
                <span className="text-[10px] font-medium tracking-wide text-primary uppercase">
                  активна
                </span>
              )}
            </div>
            <p className="mt-0.5 text-[11px] text-muted-foreground">
              {branch.parent_id
                ? `от «${branch.parent_id === 'main' ? 'Основная' : branch.parent_id}», сообщ. ${branch.fork_index}`
                : 'корневая ветка'}{' '}
              · {branch.message_count} сообщ.
            </p>
          </li>
        ))}
      </ul>
    </div>
  )
}

function RollingSummarySection({
  compressionEvents,
  summarizedMessageCount,
  rawMessageCount,
}: {
  compressionEvents: CompressionEvent[]
  summarizedMessageCount: number
  rawMessageCount: number
}) {
  const latest = compressionEvents[compressionEvents.length - 1]
  return (
    <div>
      {summarizedMessageCount > 0 ? (
        <p className="text-xs text-muted-foreground">
          Резюме {summarizedMessageCount} сообщений + {rawMessageCount} последних как есть
        </p>
      ) : (
        <p className="text-xs text-muted-foreground">
          Пока ничего не сжато ({rawMessageCount} сообщений в истории)
        </p>
      )}
      {latest && (
        <p className="mt-2 rounded-md border border-border px-2.5 py-1.5 text-xs text-foreground italic">
          {latest.summary}
        </p>
      )}
    </div>
  )
}
