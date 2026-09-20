import type { TaskStage } from '@/lib/api'

// One shared source of truth for how a task's stage (day 13) is labeled and
// colored — same colored-dot-plus-tinted-pill language as STRATEGY_META,
// a distinct hue per stage so it reads at a glance in the sidebar list.
export interface TaskStageMeta {
  label: string
  color: string
}

export const TASK_STAGE_META: Record<TaskStage, TaskStageMeta> = {
  intake: { label: 'Сбор описания', color: '#94a3b8' },
  clarifying: { label: 'Уточнение', color: '#d97706' },
  estimated: { label: 'Оценка сформирована', color: '#2563eb' },
  done: { label: 'Принято', color: '#16a34a' },
}
