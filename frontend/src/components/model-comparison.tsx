import { useRef, useState } from 'react'
import { cn } from 'cn'
import { ChevronDown, ExternalLink, Lightbulb } from 'lucide-react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Markdown } from '@/components/markdown'
import { Skeleton } from '@/components/ui/skeleton'
import type {
  ModelComparison as ModelComparisonData,
  ModelResult,
  ModelTier,
} from '@/lib/api'

interface ModelComparisonProps {
  status: 'idle' | 'loading' | 'error' | 'success'
  comparison: ModelComparisonData | null
  error: string | null
}

// Same cold-to-hot palette temperature-gauge.tsx and model-lineup.tsx use,
// for the same "small/cheap" to "large/expensive" metaphor.
const MODEL_TIER_COLORS: Record<ModelTier, string> = {
  weak: '#3b82f6',
  medium: '#f59e0b',
  strong: '#ef4444',
}

function formatCost(cost: number | undefined): string {
  if (cost == null) return 'нет данных о стоимости'
  return `$${cost.toFixed(6)}`
}

function RowSkeleton() {
  return (
    <div className="flex flex-col gap-4 rounded-xl border border-border p-5 md:flex-row">
      <div className="flex flex-col gap-2 md:w-64 md:shrink-0">
        <Skeleton className="h-5 w-32" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-2/3" />
      </div>
      <div className="flex flex-1 flex-col gap-2">
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-3/4" />
      </div>
    </div>
  )
}

// A collapsed answer shows a partial preview (fading into the card's
// background) instead of disappearing entirely, so there's still something
// to read and a visual cue that more is hidden below.
function AnswerPreview({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(true)
  const wrapperRef = useRef<HTMLDivElement>(null)

  return (
    <div ref={wrapperRef}>
      <div
        className={cn(
          'relative overflow-hidden transition-[max-height] duration-300 ease-in-out',
          expanded ? 'max-h-[10000px]' : 'max-h-36',
        )}
        onTransitionEnd={(event) => {
          // Collapsing can shrink the page by a lot; without this the
          // viewport stays put and the reader ends up mid-way into the next
          // row's unrelated content. Wait for the height transition to
          // finish (not fire immediately on click) so the scroll target is
          // measured at its final, collapsed position. Only nudges the
          // scroll if this row actually ended up out of view.
          if (event.target === event.currentTarget && !expanded) {
            wrapperRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
          }
        }}
      >
        <Markdown>{text}</Markdown>
        {!expanded && (
          <div className="pointer-events-none absolute inset-x-0 bottom-0 h-14 bg-gradient-to-t from-card to-transparent" />
        )}
      </div>
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
      >
        {expanded ? 'Свернуть ответ' : 'Показать полностью'}
        <ChevronDown
          className={cn(
            'size-3.5 transition-transform duration-200',
            expanded && 'rotate-180',
          )}
        />
      </button>
    </div>
  )
}

function ResultRow({ result }: { result: ModelResult }) {
  const color = MODEL_TIER_COLORS[result.tier]

  return (
    <Card>
      <CardContent className="flex flex-col gap-4 md:flex-row md:items-start">
        <div className="flex flex-col gap-3 border-b border-border pb-4 md:w-64 md:shrink-0 md:border-b-0 md:border-r md:border-border md:pb-0 md:pr-5">
          <div>
            <a
              href={result.docs_url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1.5 text-sm font-medium text-foreground hover:underline"
            >
              <span
                className="size-2 shrink-0 rounded-full"
                style={{ background: color }}
              />
              {result.name}
              <ExternalLink className="size-3 text-muted-foreground" />
            </a>
            <p className="mt-1 text-xs text-muted-foreground">{result.description}</p>
          </div>
          <div className="flex flex-col gap-1 font-mono text-xs text-muted-foreground">
            <span>{result.latency_ms} мс</span>
            <span>
              {result.total_tokens} токенов ({result.prompt_tokens}+{result.completion_tokens})
            </span>
            <span>{formatCost(result.cost_usd)}</span>
          </div>
        </div>

        <div className="min-w-0 flex-1">
          <AnswerPreview text={result.text} />
        </div>
      </CardContent>
    </Card>
  )
}

export function ModelComparison({ status, comparison, error }: ModelComparisonProps) {
  if (status === 'idle') {
    return (
      <div className="flex h-full min-h-[24rem] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border px-6 text-center">
        <p className="text-sm font-medium text-foreground">Пока нет ответов</p>
        <p className="max-w-sm text-sm text-muted-foreground">
          Опишите задачу выше и отправьте её — один и тот же запрос уйдёт трём
          моделям возрастающей мощности, результаты появятся рядом.
        </p>
      </div>
    )
  }

  if (status === 'loading') {
    return (
      <div className="flex flex-col gap-4">
        <RowSkeleton />
        <RowSkeleton />
        <RowSkeleton />
      </div>
    )
  }

  if (status === 'error') {
    return (
      <Alert variant="destructive">
        <AlertTitle>Не удалось получить ответы</AlertTitle>
        <AlertDescription>
          {error ?? 'Что-то пошло не так при обращении к сервису сравнения моделей.'}
        </AlertDescription>
      </Alert>
    )
  }

  if (!comparison) return null

  const qualityByTier = new Map(
    comparison.verdict.quality.map((q) => [q.tier, q.quality]),
  )

  return (
    <div className="flex flex-col gap-4">
      {comparison.results.map((result) => (
        <ResultRow key={result.tier} result={result} />
      ))}

      <Card className="ring-2 ring-primary/40">
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lightbulb className="size-4 text-primary" />
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Анализ результатов и вывод
            </CardTitle>
          </div>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            {comparison.results.map((result) => (
              <div key={result.tier} className="flex flex-col gap-1">
                <p className="flex items-center gap-1.5 text-xs font-medium tracking-wide text-muted-foreground">
                  <span
                    className="size-2 shrink-0 rounded-full"
                    style={{ background: MODEL_TIER_COLORS[result.tier] }}
                  />
                  {result.name}
                </p>
                <p className="text-sm text-foreground">
                  {qualityByTier.get(result.tier)}
                </p>
              </div>
            ))}
          </div>

          <div className="border-t border-border pt-4">
            <Markdown>{comparison.verdict.summary}</Markdown>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
