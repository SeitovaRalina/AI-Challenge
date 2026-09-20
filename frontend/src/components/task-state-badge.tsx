import { TASK_STAGE_META } from '@/lib/task-state'
import type { TaskStage } from '@/lib/api'

// Same colored-pill language as StrategyBadge — a dot plus a background
// tinted to 10% of the stage's own color.
export function TaskStateBadge({ stage }: { stage: TaskStage }) {
  const meta = TASK_STAGE_META[stage]
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
