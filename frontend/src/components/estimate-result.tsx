import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import type { Estimate } from '@/lib/api'

const complexityStyles: Record<Estimate['complexity'], string> = {
  low: 'bg-primary/10 text-primary',
  medium: 'bg-warning/20 text-warning-foreground',
  high: 'bg-destructive/10 text-destructive',
}

const complexityLabels: Record<Estimate['complexity'], string> = {
  low: 'низкая',
  medium: 'средняя',
  high: 'высокая',
}

interface EstimateResultProps {
  status: 'idle' | 'loading' | 'error' | 'success'
  estimate: Estimate | null
  error: string | null
}

export function EstimateResult({ status, estimate, error }: EstimateResultProps) {
  if (status === 'idle') {
    return (
      <div className="flex h-full min-h-[24rem] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border px-6 text-center">
        <p className="text-sm font-medium text-foreground">Пока нет оценки</p>
        <p className="max-w-xs text-sm text-muted-foreground">
          Опишите задачу слева и отправьте её, чтобы увидеть здесь
          предварительную AI-оценку.
        </p>
      </div>
    )
  }

  if (status === 'loading') {
    return (
      <div className="flex flex-col gap-4 rounded-xl border border-border p-5">
        <div className="flex items-center justify-between">
          <Skeleton className="h-5 w-24" />
          <Skeleton className="h-5 w-16" />
        </div>
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-5/6" />
        <Skeleton className="h-10 w-40" />
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-16 w-full" />
      </div>
    )
  }

  if (status === 'error') {
    return (
      <Alert variant="destructive">
        <AlertTitle>Не удалось получить оценку</AlertTitle>
        <AlertDescription>
          {error ?? 'Что-то пошло не так при обращении к сервису оценки.'}
        </AlertDescription>
      </Alert>
    )
  }

  if (!estimate) return null

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div>
          <CardTitle>{estimate.category}</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            {estimate.summary}
          </p>
        </div>
        <Badge className={complexityStyles[estimate.complexity]}>
          {complexityLabels[estimate.complexity]}
        </Badge>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <div>
          <p className="text-xs font-medium tracking-wide text-muted-foreground">
            Оценка трудозатрат
          </p>
          <p className="font-mono text-3xl font-medium tabular-nums text-foreground">
            {estimate.estimated_hours_min}–{estimate.estimated_hours_max}{' '}
            <span className="text-lg font-normal text-muted-foreground">
              часов
            </span>
          </p>
        </div>

        {estimate.risks.length > 0 && (
          <div>
            <p className="mb-1.5 text-xs font-medium tracking-wide text-muted-foreground">
              Риски
            </p>
            <ul className="flex flex-col gap-1 text-sm text-foreground">
              {estimate.risks.map((risk) => (
                <li key={risk} className="border-l-2 border-destructive/40 pl-2">
                  {risk}
                </li>
              ))}
            </ul>
          </div>
        )}

        {estimate.assumptions.length > 0 && (
          <div>
            <p className="mb-1.5 text-xs font-medium tracking-wide text-muted-foreground">
              Допущения
            </p>
            <ul className="flex flex-col gap-1 text-sm text-foreground">
              {estimate.assumptions.map((assumption) => (
                <li key={assumption} className="border-l-2 border-border pl-2">
                  {assumption}
                </li>
              ))}
            </ul>
          </div>
        )}

        <p className="text-xs text-muted-foreground">
          Только предварительная AI-оценка — не основана на вашей личной
          истории работы.
        </p>
      </CardContent>
    </Card>
  )
}
