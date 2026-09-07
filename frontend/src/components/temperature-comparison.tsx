import { Lightbulb } from 'lucide-react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Markdown } from '@/components/markdown'
import { Skeleton } from '@/components/ui/skeleton'
import { TEMPERATURE_COLORS, TemperatureGauge } from '@/components/temperature-gauge'
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

function SectionHeading({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="text-sm font-semibold text-foreground">{children}</h3>
  )
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

interface AnalysisRow {
  label: string
  render: (analysis: TemperatureAnalysis) => React.ReactNode
}

const ANALYSIS_ROWS: AnalysisRow[] = [
  { label: 'Точность', render: (a) => a.accuracy },
  { label: 'Креативность', render: (a) => a.creativity },
  { label: 'Разнообразие', render: (a) => a.diversity },
  {
    label: 'Подходит для',
    render: (a) => (
      <ul className="flex flex-col gap-1">
        {a.best_for.map((item) => (
          <li key={item} className="border-l-2 border-primary/40 pl-2">
            {item}
          </li>
        ))}
      </ul>
    ),
  },
]

function AnalysisTable({ analysis }: { analysis: TemperatureAnalysis[] }) {
  return (
    <div className="overflow-x-auto rounded-xl ring-1 ring-foreground/10">
      <table className="w-full min-w-[640px] border-collapse text-sm">
        <thead>
          <tr className="bg-muted/50">
            <th className="w-36 border-b border-border px-4 py-3 text-left text-xs font-medium tracking-wide text-muted-foreground">
              Критерий
            </th>
            {analysis.map((a) => (
              <th
                key={a.temperature}
                className="border-b border-border px-4 py-3 text-left text-xs font-medium tracking-wide text-muted-foreground"
              >
                <span className="inline-flex items-center gap-1.5">
                  <span
                    className="size-2 shrink-0 rounded-full"
                    style={{ background: TEMPERATURE_COLORS[String(a.temperature)] }}
                  />
                  {temperatureLabel(a.temperature)}
                </span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {ANALYSIS_ROWS.map((row) => (
            <tr key={row.label} className="align-top odd:bg-muted/20">
              <td className="px-4 py-3 text-xs font-medium tracking-wide text-muted-foreground">
                {row.label}
              </td>
              {analysis.map((a) => (
                <td key={a.temperature} className="px-4 py-3 text-foreground">
                  {row.render(a)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
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

      <div className="flex flex-col gap-3">
        <SectionHeading>Ответы модели</SectionHeading>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
          {comparison.results.map((result) => (
            <ResultCard key={result.temperature} result={result} />
          ))}
        </div>
      </div>

      <div className="flex flex-col gap-3">
        <SectionHeading>Сравнение по критериям</SectionHeading>
        <AnalysisTable analysis={comparison.verdict.analysis} />
      </div>

      <Card className="ring-2 ring-primary/40">
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lightbulb className="size-4 text-primary" />
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Общий вывод
            </CardTitle>
          </div>
        </CardHeader>
        <CardContent>
          <Markdown>{comparison.verdict.summary}</Markdown>
        </CardContent>
      </Card>
    </div>
  )
}
