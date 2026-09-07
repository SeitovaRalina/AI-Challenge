import { cn } from 'cn'
import {
  ClipboardList,
  ShieldAlert,
  ThumbsDown,
  ThumbsUp,
  User,
  Wrench,
} from 'lucide-react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { EstimateDetails } from '@/components/estimate-details'
import { InfoTooltip } from '@/components/info-tooltip'
import { Markdown } from '@/components/markdown'
import { Skeleton } from '@/components/ui/skeleton'
import { complexityLabels, complexityStyles } from '@/lib/complexity'
import type {
  ExpertTurn,
  ReasoningComparison as ReasoningComparisonData,
  ReasoningStep,
  ReasoningStrategy,
} from '@/lib/api'

export type ReasoningReaction = 'like' | 'dislike' | null

interface ReasoningComparisonProps {
  status: 'idle' | 'loading' | 'error' | 'success'
  comparison: ReasoningComparisonData | null
  error: string | null
  reaction: ReasoningReaction
  onReactionChange: (reaction: ReasoningReaction) => void
}

const STRATEGY_LABELS: Record<ReasoningStrategy, string> = {
  direct: 'Прямой ответ',
  step_by_step: 'Пошагово',
  meta_prompt: 'Мета-промпт',
  expert_panel: 'Экспертная группа',
}

const STRATEGY_EXPLANATIONS: Record<ReasoningStrategy, string> = {
  direct:
    'Модель отвечает сразу, без дополнительных инструкций о том, как рассуждать — первый ответ без подсказок.',
  step_by_step:
    'Модели явно велено сначала порассуждать пошагово, и только затем дать финальный ответ.',
  meta_prompt:
    'Сначала модель сама составляет промпт, который, по её мнению, лучше всего решит задачу, а затем этот промпт используется, чтобы получить финальную оценку.',
  expert_panel:
    'Модель разыгрывает обсуждение трёх ролей — аналитика, инженера и критика — в одном ответе, а затем выдаёт итоговую оценку по итогам этого обсуждения.',
}

const EXPERT_ICONS: Record<string, typeof User> = {
  Аналитик: ClipboardList,
  Инженер: Wrench,
  Критик: ShieldAlert,
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

function ExpertPanelTranscript({ turns }: { turns: ExpertTurn[] }) {
  return (
    <div className="flex flex-col gap-3">
      {turns.map((turn, index) => {
        const Icon = EXPERT_ICONS[turn.role] ?? User
        return (
          <div key={index} className="flex gap-2.5">
            <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
              <Icon className="size-3.5" />
            </span>
            <div className="min-w-0">
              <p className="text-sm font-medium text-foreground">{turn.role}</p>
              <Markdown>{turn.text}</Markdown>
            </div>
          </div>
        )
      })}
    </div>
  )
}

interface StrategyCardProps {
  strategy: ReasoningStrategy
  step: ReasoningStep
}

function StrategyCard({ strategy, step }: StrategyCardProps) {
  const { estimate } = step

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-1.5">
            <p className="text-xs font-medium tracking-wide text-muted-foreground">
              {STRATEGY_LABELS[strategy]}
            </p>
            <InfoTooltip label={`Что такое «${STRATEGY_LABELS[strategy]}»`}>
              {STRATEGY_EXPLANATIONS[strategy]}
            </InfoTooltip>
          </div>
          <CardTitle className="mt-1">{estimate.category}</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">{estimate.summary}</p>
        </div>
        <Badge className={complexityStyles[estimate.complexity]}>
          {complexityLabels[estimate.complexity]}
        </Badge>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <EstimateDetails estimate={estimate} />

        {step.panel && step.panel.length > 0 && (
          <details className="rounded-md border border-border">
            <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium tracking-wide text-muted-foreground">
              Обсуждение экспертов
            </summary>
            <div className="border-t border-border px-3 py-3">
              <ExpertPanelTranscript turns={step.panel} />
            </div>
          </details>
        )}

        {step.reasoning && (
          <details className="rounded-md border border-border">
            <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium tracking-wide text-muted-foreground">
              Ход рассуждения
            </summary>
            <div className="border-t border-border px-3 py-2">
              <Markdown>{step.reasoning}</Markdown>
            </div>
          </details>
        )}

        {step.generated_prompt && (
          <details className="rounded-md border border-border">
            <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium tracking-wide text-muted-foreground">
              Промпт, составленный моделью
            </summary>
            <div className="border-t border-border px-3 py-2">
              <Markdown>{step.generated_prompt}</Markdown>
            </div>
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

function VerdictCard({
  comparison,
  reaction,
  onReactionChange,
}: {
  comparison: ReasoningComparisonData
  reaction: ReasoningReaction
  onReactionChange: (reaction: ReasoningReaction) => void
}) {
  const { verdict } = comparison

  const verdictToneClass =
    reaction === 'like'
      ? 'ring-primary/60'
      : reaction === 'dislike'
        ? 'ring-destructive/60'
        : 'ring-warning/60'

  return (
    <Card className={cn('ring-2 transition-colors', verdictToneClass)}>
      <CardHeader>
        <CardTitle className="text-sm font-medium text-muted-foreground">
          Сравнение результатов
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="outline">
            {verdict.differs ? 'Ответы заметно отличаются' : 'Ответы в целом совпадают'}
          </Badge>
          <Badge className={complexityStyles.low}>
            Точнее всего: {STRATEGY_LABELS[verdict.most_accurate]}
          </Badge>
        </div>

        <Markdown>{verdict.rationale}</Markdown>

        <div className="flex items-center gap-2 border-t border-border pt-3">
          <span className="text-xs text-muted-foreground">Согласны с оценкой?</span>
          <Button
            type="button"
            variant={reaction === 'like' ? 'default' : 'outline'}
            size="icon-sm"
            onClick={() => onReactionChange(reaction === 'like' ? null : 'like')}
            aria-pressed={reaction === 'like'}
            aria-label="Согласен с оценкой"
          >
            <ThumbsUp className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant={reaction === 'dislike' ? 'destructive' : 'outline'}
            size="icon-sm"
            onClick={() => onReactionChange(reaction === 'dislike' ? null : 'dislike')}
            aria-pressed={reaction === 'dislike'}
            aria-label="Не согласен с оценкой"
          >
            <ThumbsDown className="size-3.5" />
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

export function ReasoningComparison({
  status,
  comparison,
  error,
  reaction,
  onReactionChange,
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
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <StrategyCard strategy="direct" step={comparison.direct} />
        <StrategyCard strategy="step_by_step" step={comparison.step_by_step} />
        <StrategyCard strategy="meta_prompt" step={comparison.meta_prompt} />
        <StrategyCard strategy="expert_panel" step={comparison.expert_panel} />
      </div>

      <VerdictCard
        comparison={comparison}
        reaction={reaction}
        onReactionChange={onReactionChange}
      />
    </div>
  )
}

