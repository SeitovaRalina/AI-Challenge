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
import type { Estimate } from '@/lib/api'

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
        <EstimateDetails estimate={estimate} />

        <p className="text-xs text-muted-foreground">
          Только предварительная AI-оценка — не основана на вашей личной
          истории работы.
        </p>
      </CardContent>
    </Card>
  )
}
