import { TASK_STAGE_META, taskStageOrdinal } from '@/lib/task-state'
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

// TaskStageHeader marks what stage the task was at for one specific
// assistant reply (or, while a reply is still in flight, the stage as of
// right before it): "Этап N/4" plus the stage's own step description, in
// the stage's color, with a thin tinted rule underneath separating it from
// the message/typing-indicator bubble that follows.
export function TaskStageHeader({ stage, step }: { stage: TaskStage; step: string }) {
  const meta = TASK_STAGE_META[stage]
  return (
    <div className="flex w-full flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
        <span className="font-semibold" style={{ color: meta.color }}>
          Этап {taskStageOrdinal(stage)}/4
        </span>
        <span className="text-muted-foreground">· {step}</span>
      </div>
      <div className="h-px w-full" style={{ background: `${meta.color}33` }} />
    </div>
  )
}
