import type { ContextStrategy } from '@/lib/api'

// One shared source of truth for how a strategy is labeled and colored,
// used by the sidebar's badges and the in-chat strategy select/hint alike.
// Same visual language as model-lineup.tsx's weak/medium/strong pills
// (colored dot + tinted background), a new hue per strategy so it never
// reads as "another weak/medium/strong".
export interface StrategyMeta {
  label: string
  color: string
}

export const STRATEGY_META: Record<Exclude<ContextStrategy, 'coordinator'>, StrategyMeta> = {
  sliding_window: { label: 'Sliding Window', color: '#3b82f6' },
  sticky_facts: { label: 'Sticky Facts', color: '#8b5cf6' },
  branching: { label: 'Branching', color: '#10b981' },
  rolling_summary: { label: 'Rolling Summary', color: '#f59e0b' },
}

export function isRealStrategy(
  strategy: ContextStrategy,
): strategy is Exclude<ContextStrategy, 'coordinator'> {
  return strategy !== 'coordinator'
}
