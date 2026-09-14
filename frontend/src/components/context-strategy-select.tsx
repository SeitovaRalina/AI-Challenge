import { HelpCircle } from 'lucide-react'

import type { ContextStrategy } from '@/lib/api'

const STRATEGY_LABELS: Record<ContextStrategy, string> = {
  sliding_window: 'Sliding window',
  sticky_facts: 'Sticky facts',
  branching: 'Branching',
  rolling_summary: 'Rolling summary',
}

const STRATEGY_HINTS: Record<ContextStrategy, (n: number) => string> = {
  sliding_window: (n) =>
    `В модель уходят только последние ${n} сообщений — всё, что раньше, хранится в истории чата, но больше не отправляется.`,
  sticky_facts: (n) =>
    `После каждого сообщения отдельным запросом обновляется таблица фактов (цель, ограничения, договорённости). В модель уходят факты + последние ${n} сообщений.`,
  branching: () =>
    'Ставьте checkpoint в любой момент диалога и создавайте от него независимые ветки — переключайтесь между ними вкладками над перепиской.',
  rolling_summary: (n) =>
    `Когда несжатых сообщений становится больше ${2 * n}, всё, кроме последних ${n}, сворачивается в резюме — оно подставляется в запрос вместо полной истории. Команда /compress сжимает сразу.`,
}

interface ContextStrategySelectProps {
  value: ContextStrategy
  historyKeepLastN: number
  disabled?: boolean
  onChange: (strategy: ContextStrategy) => void
}

// ContextStrategySelect replaced day 9's compression on/off toggle — day 10
// adds two more strategies, so a single boolean no longer fits. disabled is
// true for a lab's own chats: their strategy is fixed for the comparison to
// mean anything, and is shown as plain text instead.
export function ContextStrategySelect({
  value,
  historyKeepLastN,
  disabled,
  onChange,
}: ContextStrategySelectProps) {
  return (
    <div className="flex items-center gap-1.5">
      {disabled ? (
        <span className="rounded-md border border-border px-2 py-1 text-xs text-muted-foreground">
          {STRATEGY_LABELS[value]}
        </span>
      ) : (
        <select
          value={value}
          onChange={(event) => onChange(event.target.value as ContextStrategy)}
          className="rounded-md border border-border bg-transparent px-2 py-1 text-xs text-foreground outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          {(Object.keys(STRATEGY_LABELS) as ContextStrategy[]).map((strategy) => (
            <option key={strategy} value={strategy}>
              {STRATEGY_LABELS[strategy]}
            </option>
          ))}
        </select>
      )}
      <span className="group relative inline-flex">
        <button
          type="button"
          aria-label="Как работает эта стратегия"
          className="text-muted-foreground hover:text-foreground"
        >
          <HelpCircle className="h-3.5 w-3.5" />
        </button>
        <span
          role="tooltip"
          className="pointer-events-none absolute bottom-full left-1/2 z-10 mb-2 w-64 -translate-x-1/2 rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground opacity-0 shadow-md transition-opacity duration-100 group-hover:opacity-100 group-focus-within:opacity-100"
        >
          {STRATEGY_HINTS[value](historyKeepLastN)}
        </span>
      </span>
    </div>
  )
}
