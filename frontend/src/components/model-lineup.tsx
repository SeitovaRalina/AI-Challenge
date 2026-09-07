import type { ModelTier } from '@/lib/api'

// Static description of the three compared models — mirrors backend/types.go's
// comparedModels. Shown as a legend next to the task form regardless of
// whether a comparison has run yet, so the reader knows what's being tested
// before results arrive.
interface ModelLineupEntry {
  tier: ModelTier
  name: string
  note: string
  color: string
}

// Same cold-to-hot palette used across the app (temperature-gauge.tsx,
// model-comparison.tsx) for weak/medium/strong.
const MODEL_LINEUP: ModelLineupEntry[] = [
  { tier: 'weak', name: 'GLM-4.7-Flash', note: 'слабая · 3B активных', color: '#3b82f6' },
  { tier: 'medium', name: 'DeepSeek-V4-Flash', note: 'средняя · 13B активных', color: '#f59e0b' },
  { tier: 'strong', name: 'DeepSeek-V4-Pro', note: 'сильная · 49B активных', color: '#ef4444' },
]

export function ModelLineup() {
  return (
    <div className="flex h-full flex-col gap-3 rounded-xl border border-border p-5">
      <p className="text-xs font-medium tracking-wide text-muted-foreground">
        Сравниваемые модели
      </p>
      <div className="flex flex-col gap-2">
        {MODEL_LINEUP.map((entry) => (
          <span
            key={entry.tier}
            className="inline-flex w-fit items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium"
            style={{ background: `${entry.color}1a`, color: entry.color }}
          >
            <span className="size-1.5 shrink-0 rounded-full" style={{ background: entry.color }} />
            {entry.name}
            <span className="font-normal opacity-80">— {entry.note}</span>
          </span>
        ))}
      </div>
    </div>
  )
}
