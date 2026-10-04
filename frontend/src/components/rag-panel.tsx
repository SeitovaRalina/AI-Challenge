import { useEffect, useState } from 'react'
import { AlertCircle, Check, ChevronRight, Layers, Loader2, RefreshCw, X } from 'lucide-react'

import { Alert, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { cn } from 'cn'
import {
  ApiError,
  getEvalQuestions,
  getRagIndex,
  queryRag,
  reindexRag,
  runEval,
  runRetrievalEval,
  type ChunkStrategy,
  type EvalQuestion,
  type EvalRunResult,
  type IndexStatus,
  type RagAnswer,
  type RetrievalEvalResult,
} from '@/lib/api'

const STRATEGY_LABEL: Record<ChunkStrategy, string> = {
  fixed_size: 'По фиксированному размеру',
  structural: 'По структуре (поля сессии)',
}

const STRATEGY_HINT: Record<ChunkStrategy, string> = {
  fixed_size: 'Весь текст сессии окнами по ~500 символов, без учёта границ полей',
  structural: 'Один чанк на заголовок/сообщение/поле оценки (риск, допущение, подзадача…)',
}

// RagPanel: день 21 построил индекс поверх исторических сессий оценки
// задач; день 22 добавляет сам запрос — вопрос → поиск → ответ, и
// сравнение с ответом без RAG (главное в этом экране теперь), плюс
// отдельное retrieval-сравнение двух стратегий чанкинга (второстепенное,
// перенесено сюда из дня 21 — вопрос не чанкуется, он ищется среди уже
// готовых чанков, это механика дня 22).
export function RagPanel() {
  const [status, setStatus] = useState<IndexStatus | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [reindexing, setReindexing] = useState(false)
  const [reindexError, setReindexError] = useState<string | null>(null)
  const [strategy, setStrategy] = useState<ChunkStrategy>('structural')

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
      setReindexError(err instanceof ApiError ? err.message : 'Не удалось перестроить индекс')
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
      <CompareSection status={status} strategy={strategy} onStrategyChange={setStrategy} />
      <EvalSection status={status} strategy={strategy} />
      <RetrievalCompareSection status={status} />

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
              Источник — сессии оценки задач этого инстанса (реальные истории «описание задачи →
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
              Два способа нарезки — один и тот же корпус
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Одни и те же сессии разбиты двумя разными способами. Заметная разница в количестве
              и размере фрагментов.
            </p>
          </div>
          <table className="w-full text-left text-sm">
            <thead className="text-muted-foreground">
              <tr className="border-b border-border">
                <th className="px-4 py-2 font-medium">Способ</th>
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

// StrategySelect is the one control both the single-question comparison
// and the 10-question eval run read from — picking a strategy is a
// deliberate, visible choice, not hidden server-side magic.
function StrategySelect({
  value,
  onChange,
}: {
  value: ChunkStrategy
  onChange: (s: ChunkStrategy) => void
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value as ChunkStrategy)}
      className="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground"
    >
      <option value="structural">{STRATEGY_LABEL.structural}</option>
      <option value="fixed_size">{STRATEGY_LABEL.fixed_size}</option>
    </select>
  )
}

// CompareSection — день 22's основной инструмент: один вопрос, два ответа
// рядом. Не чат: ни истории, ни карточки оценки задачи — отдельный,
// минимальный путь в бэкенде (rag_query.go), не трогающий Agent/Estimate.
function CompareSection({
  status,
  strategy,
  onStrategyChange,
}: {
  status: IndexStatus
  strategy: ChunkStrategy
  onStrategyChange: (s: ChunkStrategy) => void
}) {
  const [question, setQuestion] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [noRag, setNoRag] = useState<RagAnswer | null>(null)
  const [rag, setRag] = useState<RagAnswer | null>(null)

  async function handleCompare() {
    const q = question.trim()
    if (!q) return
    setLoading(true)
    setError(null)
    setNoRag(null)
    setRag(null)
    try {
      const [noRagRes, ragRes] = await Promise.all([
        queryRag(q, 'no_rag'),
        queryRag(q, 'rag', strategy),
      ])
      setNoRag(noRagRes)
      setRag(ragRes)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Не удалось получить ответ')
    } finally {
      setLoading(false)
    }
  }

  return (
    <section id="rag-compare" className="rounded-lg border border-border bg-card p-4">
      <h2 className="text-sm font-medium text-foreground">RAG vs без RAG</h2>
      <p className="mt-1 text-xs text-muted-foreground">
        Один и тот же вопрос — модели без доступа к истории задач и модели, которой сначала нашли
        релевантные фрагменты из ваших прошлых оценок.
      </p>

      <div className="mt-3 flex flex-col gap-2 sm:flex-row sm:items-end">
        <textarea
          value={question}
          onChange={(e) => setQuestion(e.target.value)}
          placeholder="Например: сколько часов на экспорт списка заказов в CSV в админке?"
          rows={2}
          className="flex-1 resize-none rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground"
        />
        <div className="flex items-center gap-2">
          <StrategySelect value={strategy} onChange={onStrategyChange} />
          <Button onClick={handleCompare} disabled={loading || !status.exists || !question.trim()}>
            {loading && <Loader2 className="animate-spin" />}
            Сравнить
          </Button>
        </div>
      </div>
      {!status.exists && (
        <p className="mt-2 text-xs text-muted-foreground">
          Сначала постройте индекс — карточка «Локальный индекс» ниже.
        </p>
      )}

      {error && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{error}</AlertTitle>
        </Alert>
      )}

      {(noRag || rag) && (
        <div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <AnswerColumn title="Без RAG" answer={noRag} />
          <AnswerColumn title="С RAG" answer={rag} />
        </div>
      )}
    </section>
  )
}

function AnswerColumn({ title, answer }: { title: string; answer: RagAnswer | null }) {
  return (
    <div className="rounded-md border border-border bg-muted/30 p-3">
      <div className="text-xs font-medium text-muted-foreground">{title}</div>
      <p className="mt-1.5 whitespace-pre-line text-sm text-foreground">
        {answer ? answer.answer : '—'}
      </p>
      {answer?.retrieved && answer.retrieved.length > 0 && (
        <div className="mt-2 border-t border-border/60 pt-2">
          <div className="text-xs text-muted-foreground">Источники:</div>
          <ul className="mt-1 flex flex-col gap-0.5">
            {answer.retrieved.map((r) => (
              <li key={r.chunk_id} className="truncate text-xs text-muted-foreground" title={r.title}>
                {r.title} — <span className="font-mono">{r.section}</span> (
                {r.score.toFixed(2)})
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

// EvalSection — day-22's deliverable over the full control set: all 10
// questions, both modes, with whether retrieval actually found the
// expected source. This is the real "сравнение качества", not the
// retrieval-only section below.
function EvalSection({ status, strategy }: { status: IndexStatus; strategy: ChunkStrategy }) {
  const [questions, setQuestions] = useState<EvalQuestion[] | null>(null)
  const [questionsError, setQuestionsError] = useState<string | null>(null)
  const [running, setRunning] = useState(false)
  const [runError, setRunError] = useState<string | null>(null)
  const [result, setResult] = useState<EvalRunResult | null>(null)
  const [expanded, setExpanded] = useState<number | null>(null)

  useEffect(() => {
    getEvalQuestions()
      .then(setQuestions)
      .catch((err) =>
        setQuestionsError(err instanceof ApiError ? err.message : 'Не удалось загрузить контрольные вопросы'),
      )
  }, [])

  async function handleRun() {
    setRunning(true)
    setRunError(null)
    try {
      setResult(await runEval(strategy))
    } catch (err) {
      setRunError(err instanceof ApiError ? err.message : 'Не удалось прогнать контрольные вопросы')
    } finally {
      setRunning(false)
    }
  }

  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-sm font-medium text-foreground">10 контрольных вопросов</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            Для каждого — ожидание и (если применимо) ожидаемый источник. «Прогнать все 10»
            отвечает на каждый без RAG и с RAG ({STRATEGY_LABEL[strategy].toLowerCase()}) — это
            20 запросов к модели, может занять пару минут.
          </p>
        </div>
        <Button onClick={handleRun} disabled={running || !status.exists || !questions?.length}>
          {running && <Loader2 className="animate-spin" />}
          Прогнать все 10
        </Button>
      </div>

      {questionsError && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{questionsError}</AlertTitle>
        </Alert>
      )}
      {runError && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{runError}</AlertTitle>
        </Alert>
      )}

      <ul className="mt-3 divide-y divide-border border-t border-border">
        {(result?.results ?? questions)?.map((q, i) => {
          const r = result?.results[i]
          const open = expanded === i
          return (
            <li key={i}>
              <button
                type="button"
                onClick={() => setExpanded(open ? null : i)}
                className="flex w-full items-start gap-2 py-2.5 text-left transition-colors hover:bg-accent/50"
              >
                <ChevronRight
                  className={cn(
                    'mt-0.5 h-4 w-4 flex-shrink-0 text-muted-foreground transition-transform',
                    open && 'rotate-90',
                  )}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm text-foreground">{q.question}</span>
                    {r?.expected_source_check && (
                      <Badge
                        className={cn(
                          r.expected_source_hit
                            ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400'
                            : 'bg-amber-500/15 text-amber-700 dark:text-amber-400',
                        )}
                      >
                        {r.expected_source_hit ? (
                          <Check className="h-3 w-3" />
                        ) : (
                          <X className="h-3 w-3" />
                        )}
                        источник
                      </Badge>
                    )}
                  </div>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">{q.expectation}</p>
                </div>
              </button>
              {open && (
                <div className="px-6 pb-3">
                  {r ? (
                    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                      <AnswerColumn title="Без RAG" answer={{ mode: 'no_rag', answer: r.no_rag_answer }} />
                      <AnswerColumn
                        title="С RAG"
                        answer={{ mode: 'rag', answer: r.rag_answer, retrieved: r.retrieved }}
                      />
                    </div>
                  ) : (
                    <p className="text-xs text-muted-foreground">
                      Ещё не прогнано — нажмите «Прогнать все 10» выше.
                    </p>
                  )}
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}

// RetrievalCompareSection — второстепенное: чисто retrieval, без LLM,
// hit-rate по 2 стратегиям на тех же 10 вопросах. Перенесено из дня 21
// (см. правку плана) — визуально ниже и скромнее главной секции выше.
function RetrievalCompareSection({ status }: { status: IndexStatus }) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<RetrievalEvalResult | null>(null)

  async function handleRun() {
    setLoading(true)
    setError(null)
    try {
      setResult(await runRetrievalEval())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Не удалось сравнить стратегии')
    } finally {
      setLoading(false)
    }
  }

  return (
    <section className="rounded-lg border border-border bg-muted/20 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-xs font-medium text-foreground">
            Доп.: какая стратегия чанкинга точнее находит источник
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            Без LLM — только retrieval: попадает ли ожидаемая сессия в топ-5 для каждой стратегии.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={handleRun} disabled={loading || !status.exists}>
          {loading && <Loader2 className="animate-spin" />}
          Сравнить стратегии
        </Button>
      </div>

      {error && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{error}</AlertTitle>
        </Alert>
      )}

      {result && (
        <div className="mt-3">
          <div className="flex gap-4 text-xs">
            {result.strategies.map((s) => (
              <div key={s.strategy}>
                <span className="text-muted-foreground">{STRATEGY_LABEL[s.strategy]}: </span>
                <span className="font-medium text-foreground">
                  {s.hits}/{s.total} ({Math.round(s.hit_rate * 100)}%)
                </span>
              </div>
            ))}
          </div>
          <table className="mt-2 w-full text-left text-xs">
            <thead className="text-muted-foreground">
              <tr className="border-b border-border">
                <th className="py-1.5 pr-2 font-medium">Вопрос</th>
                <th className="py-1.5 pr-2 font-medium">fixed_size</th>
                <th className="py-1.5 font-medium">structural</th>
              </tr>
            </thead>
            <tbody>
              {result.questions
                .filter((q) => q.checked)
                .map((q) => (
                  <tr key={q.question} className="border-b border-border/60 last:border-0">
                    <td className="py-1.5 pr-2 text-foreground">{q.question}</td>
                    <td className="py-1.5 pr-2">
                      {q.hits.fixed_size ? (
                        <Check className="h-3.5 w-3.5 text-emerald-600" />
                      ) : (
                        <X className="h-3.5 w-3.5 text-muted-foreground" />
                      )}
                    </td>
                    <td className="py-1.5">
                      {q.hits.structural ? (
                        <Check className="h-3.5 w-3.5 text-emerald-600" />
                      ) : (
                        <X className="h-3.5 w-3.5 text-muted-foreground" />
                      )}
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
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
