import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { EstimateDetails } from '@/components/estimate-details'
import { Skeleton } from '@/components/ui/skeleton'
import { complexityLabels, complexityStyles } from '@/lib/complexity'
import type { ReasoningComparison as ReasoningComparisonData, ReasoningStep } from '@/lib/api'

interface ReasoningComparisonProps {
  status: 'idle' | 'loading' | 'error' | 'success'
  comparison: ReasoningComparisonData | null
  error: string | null
}

function ColumnSkeleton() {
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-border p-5">
      <Skeleton className="h-5 w-32" />
      <Skeleton className="h-4 w-full" />
      <Skeleton className="h-4 w-full" />
      <Skeleton className="h-4 w-2/3" />
    </div>
  )
}

interface StrategyCardProps {
  label: string
  step: ReasoningStep
  reasoningTitle?: string
}

function StrategyCard({ label, step, reasoningTitle = 'Ход рассуждения' }: StrategyCardProps) {
  const { estimate } = step

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div>
          <p className="text-xs font-medium tracking-wide text-muted-foreground">
            {label}
          </p>
          <CardTitle className="mt-1">{estimate.category}</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">{estimate.summary}</p>
        </div>
        <Badge className={complexityStyles[estimate.complexity]}>
          {complexityLabels[estimate.complexity]}
        </Badge>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <EstimateDetails estimate={estimate} />

        {step.reasoning && (
          <details className="rounded-md border border-border">
            <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium tracking-wide text-muted-foreground">
              {reasoningTitle}
            </summary>
            <p className="whitespace-pre-wrap border-t border-border px-3 py-2 text-sm text-foreground">
              {step.reasoning}
            </p>
          </details>
        )}

        {step.generated_prompt && (
          <details className="rounded-md border border-border">
            <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium tracking-wide text-muted-foreground">
              Промпт, составленный моделью
            </summary>
            <p className="whitespace-pre-wrap border-t border-border px-3 py-2 font-mono text-xs text-foreground">
              {step.generated_prompt}
            </p>
          </details>
        )}

        <div className="flex gap-4 border-t border-border pt-3 font-mono text-xs text-muted-foreground">
          <span>{step.length_chars} симв.</span>
          <span>{step.latency_ms} мс</span>
        </div>
      </CardContent>
    </Card>
  )
}

export function ReasoningComparison({
  status,
  comparison,
  error,
}: ReasoningComparisonProps) {
  if (status === 'idle') {
    return (
      <div className="flex h-full min-h-[24rem] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border px-6 text-center">
        <p className="text-sm font-medium text-foreground">Пока нет решений</p>
        <p className="max-w-sm text-sm text-muted-foreground">
          Опишите задачу выше и отправьте её — один и тот же запрос уйдёт в LLM
          четырьмя способами рассуждения, результаты появятся рядом.
        </p>
      </div>
    )
  }

  if (status === 'loading') {
    return (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <ColumnSkeleton />
        <ColumnSkeleton />
        <ColumnSkeleton />
        <ColumnSkeleton />
      </div>
    )
  }

  if (status === 'error') {
    return (
      <Alert variant="destructive">
        <AlertTitle>Не удалось получить решения</AlertTitle>
        <AlertDescription>
          {error ?? 'Что-то пошло не так при обращении к сервису рассуждений.'}
        </AlertDescription>
      </Alert>
    )
  }

  if (!comparison) return null

  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <StrategyCard label="Прямой ответ" step={comparison.direct} />
      <StrategyCard
        label="Пошагово"
        step={comparison.step_by_step}
        reasoningTitle="Пошаговое рассуждение"
      />
      <StrategyCard label="Мета-промпт" step={comparison.meta_prompt} />
      <StrategyCard
        label="Экспертная группа"
        step={comparison.expert_panel}
        reasoningTitle="Обсуждение экспертов"
      />
    </div>
  )
}
