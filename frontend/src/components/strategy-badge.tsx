import { STRATEGY_META } from '@/lib/strategy'
import type { ContextStrategy } from '@/lib/api'

// Same colored-pill language as model-lineup.tsx's weak/medium/strong badges
// — a dot plus a background tinted to 10% of the strategy's own color.
export function StrategyBadge({ strategy }: { strategy: Exclude<ContextStrategy, 'coordinator'> }) {
  const meta = STRATEGY_META[strategy]
  return (
    <span
      className="inline-flex w-fit items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium"
      style={{ background: `${meta.color}1a`, color: meta.color }}
    >
      <span className="size-1.5 shrink-0 rounded-full" style={{ background: meta.color }} />
      {meta.label}
    </span>
  )
}
