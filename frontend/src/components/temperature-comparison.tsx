import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Markdown } from '@/components/markdown'
import { Skeleton } from '@/components/ui/skeleton'
import { TemperatureGauge } from '@/components/temperature-gauge'
import type {
  TemperatureAnalysis,
  TemperatureComparison as TemperatureComparisonData,
  TemperatureResult,
} from '@/lib/api'

interface TemperatureComparisonProps {
  status: 'idle' | 'loading' | 'error' | 'success'
  comparison: TemperatureComparisonData | null
  error: string | null
}

const TEMPERATURE_NAMES: Record<string, string> = {
  '0': 'Точный',
  '0.7': 'Сбалансированный',
  '1.2': 'Творческий',
}

function temperatureLabel(value: number): string {
  const name = TEMPERATURE_NAMES[String(value)]
  return name ? `${name} (t=${value})` : `t=${value}`
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

function ResultCard({ result }: { result: TemperatureResult }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium text-muted-foreground">
          {temperatureLabel(result.temperature)}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <Markdown>{result.text}</Markdown>
        <div className="flex gap-4 border-t border-border pt-3 font-mono text-xs text-muted-foreground">
          <span>{result.length_chars} симв.</span>
          <span>{result.latency_ms} мс</span>
        </div>
      </CardContent>
    </Card>
  )
}

function AnalysisCard({ analysis }: { analysis: TemperatureAnalysis }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium text-muted-foreground">
          {temperatureLabel(analysis.temperature)}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <div>
          <p className="text-xs font-medium tracking-wide text-muted-foreground">
            Точность
          </p>
          <p className="text-foreground">{analysis.accuracy}</p>
        </div>
        <div>
          <p className="text-xs font-medium tracking-wide text-muted-foreground">
            Креативность
          </p>
          <p className="text-foreground">{analysis.creativity}</p>
        </div>
        <div>
          <p className="text-xs font-medium tracking-wide text-muted-foreground">
            Разнообразие
          </p>
          <p className="text-foreground">{analysis.diversity}</p>
        </div>
        <div className="flex flex-col gap-1.5 border-t border-border pt-3">
          <p className="text-xs font-medium tracking-wide text-muted-foreground">
            Подходит для
          </p>
          <div className="flex flex-wrap gap-1.5">
            {analysis.best_for.map((item) => (
              <Badge key={item} variant="outline">
                {item}
              </Badge>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

export function TemperatureComparison({
  status,
  comparison,
  error,
}: TemperatureComparisonProps) {
  if (status === 'idle') {
    return (
      <div className="flex h-full min-h-[24rem] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border px-6 text-center">
        <p className="text-sm font-medium text-foreground">Пока нет ответов</p>
        <p className="max-w-sm text-sm text-muted-foreground">
          Опишите задачу выше и отправьте её — один и тот же запрос уйдёт в LLM
          трижды, с температурой 0, 0.7 и 1.2, результаты появятся рядом.
        </p>
      </div>
    )
  }

  if (status === 'loading') {
    return (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <ColumnSkeleton />
        <ColumnSkeleton />
        <ColumnSkeleton />
      </div>
    )
  }

  if (status === 'error') {
    return (
      <Alert variant="destructive">
        <AlertTitle>Не удалось получить ответы</AlertTitle>
        <AlertDescription>
          {error ?? 'Что-то пошло не так при обращении к сервису сравнения температур.'}
        </AlertDescription>
      </Alert>
    )
  }

  if (!comparison) return null

  return (
    <div className="flex flex-col gap-6">
      <TemperatureGauge values={comparison.results.map((r) => r.temperature)} />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {comparison.results.map((result) => (
          <ResultCard key={result.temperature} result={result} />
        ))}
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {comparison.verdict.analysis.map((analysis) => (
          <AnalysisCard key={analysis.temperature} analysis={analysis} />
        ))}
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium text-muted-foreground">
            Общий вывод
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Markdown>{comparison.verdict.summary}</Markdown>
        </CardContent>
      </Card>
    </div>
  )
}
