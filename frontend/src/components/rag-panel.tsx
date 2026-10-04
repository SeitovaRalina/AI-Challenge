import { useEffect, useState } from 'react'
import { AlertCircle, Layers, Loader2, RefreshCw } from 'lucide-react'

import { Alert, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  ApiError,
  getRagIndex,
  reindexRag,
  type ChunkStrategy,
  type IndexStatus,
} from '@/lib/api'

const STRATEGY_LABEL: Record<ChunkStrategy, string> = {
  fixed_size: 'По фиксированному размеру',
  structural: 'По структуре (поля сессии)',
}

const STRATEGY_HINT: Record<ChunkStrategy, string> = {
  fixed_size: 'Весь текст сессии окнами по ~500 символов, без учёта границ полей',
  structural: 'Один чанк на заголовок/сообщение/поле оценки (риск, допущение, подзадача…)',
}

// RagPanel is day 21's «Похожие задачи» screen: build/inspect the local RAG
// index over the chat agent's own historical task-estimation sessions, and
// compare the two chunking strategies side by side. Querying the index
// (day 22+) is a separate step added on top of this same screen later.
export function RagPanel() {
  const [status, setStatus] = useState<IndexStatus | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [reindexing, setReindexing] = useState(false)
  const [reindexError, setReindexError] = useState<string | null>(null)

  useEffect(() => {
    getRagIndex()
      .then(setStatus)
      .catch((err) =>
        setLoadError(err instanceof ApiError ? err.message : 'Не удалось загрузить состояние индекса'),
      )
  }, [])

  async function handleReindex() {
    setReindexing(true)
    setReindexError(null)
    try {
      const updated = await reindexRag()
      setStatus(updated)
    } catch (err) {
      setReindexError(
        err instanceof ApiError ? err.message : 'Не удалось перестроить индекс',
      )
    } finally {
      setReindexing(false)
    }
  }

  if (loadError && !status) {
    return (
      <Alert variant="destructive" className="max-w-3xl">
        <AlertCircle />
        <AlertTitle>{loadError}</AlertTitle>
      </Alert>
    )
  }
  if (!status) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Загрузка…
      </div>
    )
  }

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <section className="rounded-lg border border-border bg-card">
        <div className="flex items-start gap-3 p-4">
          <span className="mt-0.5 flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
            <Layers className="h-4 w-4" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-sm font-medium text-foreground">Локальный индекс</h2>
              {status.exists ? (
                <Badge className="bg-emerald-500/15 text-emerald-700 dark:text-emerald-400">
                  построен
                </Badge>
              ) : (
                <Badge variant="secondary">не построен</Badge>
              )}
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              Корпус — сессии оценки задач этого инстанса (реальные истории «описание задачи →
              оценка»), не сторонние документы.
            </p>
            {status.exists && (
              <dl className="mt-3 grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
                <Fact label="Сессий в индексе" value={String(status.source_count)} />
                <Fact label="Модель эмбеддингов" value={status.embed_model || '—'} />
                <Fact label="Построен" value={status.built_at ? formatTime(status.built_at) : '—'} />
              </dl>
            )}
          </div>
          <Button onClick={handleReindex} disabled={reindexing}>
            {reindexing ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            Переиндексировать
          </Button>
        </div>

        {reindexError && (
          <div className="border-t border-border p-4">
            <Alert variant="destructive">
              <AlertCircle />
              <AlertTitle>{reindexError}</AlertTitle>
            </Alert>
          </div>
        )}
      </section>

      {status.exists && status.strategies && status.strategies.length > 0 && (
        <section className="rounded-lg border border-border bg-card">
          <div className="border-b border-border p-4">
            <h2 className="text-sm font-medium text-foreground">
              Сравнение стратегий чанкинга
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Один и тот же корпус, две разных разбивки на чанки — день 22 использует лучшую из
              них для поиска.
            </p>
          </div>
          <table className="w-full text-left text-sm">
            <thead className="text-muted-foreground">
              <tr className="border-b border-border">
                <th className="px-4 py-2 font-medium">Стратегия</th>
                <th className="px-4 py-2 font-medium">Чанков</th>
                <th className="px-4 py-2 font-medium">Символов всего</th>
                <th className="px-4 py-2 font-medium">Средняя длина</th>
              </tr>
            </thead>
            <tbody>
              {status.strategies.map((s) => (
                <tr key={s.strategy} className="border-b border-border/60 align-top last:border-0">
                  <td className="px-4 py-3">
                    <div className="font-medium text-foreground">{STRATEGY_LABEL[s.strategy]}</div>
                    <div className="mt-0.5 text-xs text-muted-foreground">{STRATEGY_HINT[s.strategy]}</div>
                  </td>
                  <td className="px-4 py-3 text-foreground">{s.chunk_count}</td>
                  <td className="px-4 py-3 text-foreground">{s.total_chars.toLocaleString('ru-RU')}</td>
                  <td className="px-4 py-3 text-foreground">{Math.round(s.avg_chars)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </div>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate font-medium text-foreground" title={value}>
        {value}
      </dd>
    </div>
  )
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
