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
import type { Comparison } from '@/lib/api'

interface FormatComparisonProps {
  status: 'idle' | 'loading' | 'error' | 'success'
  comparison: Comparison | null
  error: string | null
  uncontrolledReused: boolean
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

export function FormatComparison({
  status,
  comparison,
  error,
  uncontrolledReused,
}: FormatComparisonProps) {
  if (status === 'idle') {
    return (
      <div className="flex h-full min-h-[24rem] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border px-6 text-center">
        <p className="text-sm font-medium text-foreground">Пока нет сравнения</p>
        <p className="max-w-sm text-sm text-muted-foreground">
          Опишите задачу выше и отправьте её — один и тот же запрос уйдёт в LLM
          без ограничений формата и с ними, результаты появятся рядом.
        </p>
      </div>
    )
  }

  if (status === 'loading') {
    return (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <ColumnSkeleton />
        <ColumnSkeleton />
      </div>
    )
  }

  if (status === 'error') {
    return (
      <Alert variant="destructive">
        <AlertTitle>Не удалось сравнить форматы</AlertTitle>
        <AlertDescription>
          {error ?? 'Что-то пошло не так при обращении к сервису сравнения.'}
        </AlertDescription>
      </Alert>
    )
  }

  if (!comparison) return null

  const { uncontrolled, controlled } = comparison

  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3">
          <CardTitle className="text-sm font-medium text-muted-foreground">
            Без ограничений
          </CardTitle>
          {uncontrolledReused && (
            <Badge variant="outline" className="text-xs font-normal">
              результат переиспользован
            </Badge>
          )}
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <p className="whitespace-pre-wrap text-sm text-foreground">
            {uncontrolled.text}
          </p>
          <div className="flex gap-4 border-t border-border pt-3 font-mono text-xs text-muted-foreground">
            <span>{uncontrolled.length_chars} симв.</span>
            <span>{uncontrolled.latency_ms} мс</span>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-3">
          <div>
            <p className="text-xs font-medium tracking-wide text-muted-foreground">
              С ограничениями
            </p>
            <CardTitle className="mt-1">{controlled.estimate.category}</CardTitle>
            <p className="mt-1 text-sm text-muted-foreground">
              {controlled.estimate.summary}
            </p>
          </div>
          <Badge className={complexityStyles[controlled.estimate.complexity]}>
            {complexityLabels[controlled.estimate.complexity]}
          </Badge>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          <EstimateDetails estimate={controlled.estimate} />

          <div className="flex gap-4 border-t border-border pt-3 font-mono text-xs text-muted-foreground">
            <span>{controlled.length_chars} симв.</span>
            <span>{controlled.latency_ms} мс</span>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
