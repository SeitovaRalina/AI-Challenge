import { useEffect, useState } from 'react'
import { AlertCircle, ArrowRight, Check, ChevronRight, Loader2, X } from 'lucide-react'

import { Alert, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Markdown } from '@/components/markdown'
import { cn } from 'cn'
import {
  ApiError,
  getEvalQuestions,
  getRagIndex,
  queryRag,
  runEval,
  runRetrievalEval,
  type ChunkStrategy,
  type EvalQuestion,
  type EvalRunResult,
  type IndexStatus,
  type RagAnswer,
  type RetrievalEvalResult,
  type RetrievedChunk,
} from '@/lib/api'

const STRATEGY_LABEL: Record<ChunkStrategy, string> = {
  fixed_size: 'по фиксированному размеру',
  structural: 'по структуре',
}

// parseMessageIndex pulls the message index out of a structural chunk's
// section ("message[3].assistant" -> 3) so a source can link straight to
// that message in the real chat — null for sections that aren't tied to
// one specific message (title, estimate.*, task.*, or any fixed_size
// window).
function parseMessageIndex(section: string): number | null {
  const m = /^message\[(\d+)\]/.exec(section)
  return m ? Number(m[1]) : null
}

interface RagPanelProps {
  // Opens chatId as a real chat and (when not null) scrolls to/highlights
  // the exact message a source chunk came from.
  onOpenSource: (chatId: string, messageIndex: number | null) => void
}

// RagPanel is the day-22+ screen: question -> retrieval -> LLM, compared
// against the same question with no retrieval at all, plus the same
// comparison run over 10 hand-written control questions. Deliberately not
// the main chat (no history, no estimate card) and deliberately a
// separate sidebar screen from «Индексация» (day 21) — indexing is
// infrastructure you touch rarely, this is the feature itself, and by
// day 25 it grows into its own mini-chat.
export function RagPanel({ onOpenSource }: RagPanelProps) {
  const [status, setStatus] = useState<IndexStatus | null>(null)
  const [strategy, setStrategy] = useState<ChunkStrategy>('structural')

  useEffect(() => {
    getRagIndex()
      .then(setStatus)
      .catch(() => setStatus({ exists: false, source_count: 0 }))
  }, [])

  if (!status) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Загрузка…
      </div>
    )
  }

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      {!status.exists && (
        <Alert>
          <AlertCircle />
          <AlertTitle>
            Индекс ещё не построен — зайдите на экран «Индексация» и нажмите
            «Переиндексировать».
          </AlertTitle>
        </Alert>
      )}

      <CompareSection status={status} strategy={strategy} onStrategyChange={setStrategy} onOpenSource={onOpenSource} />
      <EvalSection status={status} strategy={strategy} onOpenSource={onOpenSource} />
      <RetrievalCompareSection status={status} />
    </div>
  )
}

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
      <option value="structural">Нарезка: {STRATEGY_LABEL.structural}</option>
      <option value="fixed_size">Нарезка: {STRATEGY_LABEL.fixed_size}</option>
    </select>
  )
}

// CompareSection — главный инструмент: один вопрос, два ответа рядом. Не
// чат: ни истории, ни карточки оценки задачи — отдельный, минимальный
// путь в бэкенде (rag_query.go), не трогающий Agent/Estimate.
function CompareSection({
  status,
  strategy,
  onStrategyChange,
  onOpenSource,
}: {
  status: IndexStatus
  strategy: ChunkStrategy
  onStrategyChange: (s: ChunkStrategy) => void
  onOpenSource: (chatId: string, messageIndex: number | null) => void
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
    <section className="rounded-lg border border-border bg-card p-4">
      <h2 className="text-sm font-medium text-foreground">RAG vs без RAG</h2>
      <p className="mt-1 text-xs text-muted-foreground">
        Один и тот же вопрос — модели без доступа к истории задач и модели, которой сначала нашли
        топ-5 ближайших по смыслу фрагментов из ваших прошлых оценок.
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

      {error && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{error}</AlertTitle>
        </Alert>
      )}

      {(noRag || rag) && (
        <div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <AnswerColumn title="Без RAG" answer={noRag} onOpenSource={onOpenSource} />
          <AnswerColumn title="С RAG" answer={rag} onOpenSource={onOpenSource} />
        </div>
      )}
    </section>
  )
}

function AnswerColumn({
  title,
  answer,
  onOpenSource,
}: {
  title: string
  answer: RagAnswer | null
  onOpenSource: (chatId: string, messageIndex: number | null) => void
}) {
  return (
    <div className="rounded-md border border-border bg-muted/30 p-3">
      <div className="text-xs font-medium text-muted-foreground">{title}</div>
      <div className="mt-1.5 text-sm text-foreground">
        {answer ? <Markdown>{answer.answer}</Markdown> : <span className="text-muted-foreground">—</span>}
      </div>
      {answer?.retrieved && answer.retrieved.length > 0 && (
        <div className="mt-2 border-t border-border/60 pt-2">
          <div className="text-xs text-muted-foreground">Источники:</div>
          <ul className="mt-1 flex flex-col gap-1">
            {answer.retrieved.map((r) => (
              <SourceItem key={r.chunk_id} chunk={r} onOpenSource={onOpenSource} />
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

// SourceItem — свёрнутая строка (заголовок сессии, где внутри неё, score);
// разворачивается в полный текст найденного чанка и кнопку перехода в
// реальный чат, к конкретному сообщению, если секция к нему привязана.
function SourceItem({
  chunk,
  onOpenSource,
}: {
  chunk: RetrievedChunk
  onOpenSource: (chatId: string, messageIndex: number | null) => void
}) {
  const [open, setOpen] = useState(false)
  const messageIndex = parseMessageIndex(chunk.section)

  return (
    <li className="rounded border border-border/60">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 px-2 py-1 text-left"
      >
        <ChevronRight className={cn('h-3 w-3 flex-shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')} />
        <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={chunk.title}>
          {chunk.title} — <span className="font-mono">{chunk.section}</span>
        </span>
        <span className="flex-shrink-0 text-xs text-muted-foreground">{chunk.score.toFixed(2)}</span>
      </button>
      {open && (
        <div className="border-t border-border/60 px-2 py-1.5">
          <p className="whitespace-pre-line text-xs text-foreground">{chunk.text}</p>
          <Button
            size="xs"
            variant="outline"
            className="mt-1.5"
            onClick={() => onOpenSource(chunk.session_id, messageIndex)}
          >
            Перейти в чат
            {messageIndex != null && ' → к сообщению'}
            <ArrowRight />
          </Button>
        </div>
      )}
    </li>
  )
}

// EvalSection — day-22's deliverable over the full control set: all 10
// questions, both modes, with whether retrieval actually found the
// expected source. Нет автоматической оценки СМЫСЛА ответа (это потребовало
// бы отдельной LLM-judge модели — вне рамок дня 22) — единственная
// автоматическая метрика здесь: нашёлся ли среди источников ожидаемый
// (бейдж «источник»); качество самого текста сравнивается на глаз, читая
// два столбца.
function EvalSection({
  status,
  strategy,
  onOpenSource,
}: {
  status: IndexStatus
  strategy: ChunkStrategy
  onOpenSource: (chatId: string, messageIndex: number | null) => void
}) {
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
            Для каждого — ожидание и (если применимо) ожидаемый источник.
          </p>
        </div>
        <Button onClick={handleRun} disabled={running || !status.exists || !questions?.length}>
          {running && <Loader2 className="animate-spin" />}
          Прогнать все 10
        </Button>
      </div>
      <p className="mt-2 rounded-md bg-muted/50 p-2 text-xs text-muted-foreground">
        Как это оценивается: единственная автоматическая метрика — попал ли среди топ-5
        найденных фрагментов чанк из ожидаемой сессии (бейдж «источник» ✓/✗ у вопроса). Качество
        самого текста ответа автоматически не оценивается — сравнивайте два столбца глазами
        после разворота вопроса.
      </p>

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
                        {r.expected_source_hit ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
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
                      <AnswerColumn
                        title="Без RAG"
                        answer={{ mode: 'no_rag', answer: r.no_rag_answer }}
                        onOpenSource={onOpenSource}
                      />
                      <AnswerColumn
                        title="С RAG"
                        answer={{ mode: 'rag', answer: r.rag_answer, retrieved: r.retrieved }}
                        onOpenSource={onOpenSource}
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

// RetrievalCompareSection — второстепенное: чисто retrieval, без LLM.
// Перенесено из дня 21 (вопрос не чанкуется, он ищется среди готовых
// чанков — это механика дня 22), визуально свёрнуто по умолчанию.
function RetrievalCompareSection({ status }: { status: IndexStatus }) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<RetrievalEvalResult | null>(null)
  const [detailsOpen, setDetailsOpen] = useState(false)

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
            Доп.: какая нарезка на чанки точнее находит источник
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            Метрика: для каждого из применимых контрольных вопросов — эмбеддинг вопроса ищет
            топ-5 ближайших чанков в индексе; попал ли среди них чанк из ожидаемой сессии. Только
            сам поиск, без обращения к LLM — не оценка ответа, а оценка того, нашёл ли поиск
            вообще правильный источник.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={handleRun} disabled={loading || !status.exists}>
          {loading && <Loader2 className="animate-spin" />}
          Сравнить
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
          <div className="flex items-center gap-4 text-xs">
            {result.strategies.map((s) => (
              <div key={s.strategy}>
                <span className="text-muted-foreground">Нарезка {STRATEGY_LABEL[s.strategy]}: </span>
                <span className="font-medium text-foreground">
                  {s.hits}/{s.total} вопросов ({Math.round(s.hit_rate * 100)}%)
                </span>
              </div>
            ))}
            <button
              type="button"
              onClick={() => setDetailsOpen((v) => !v)}
              className="text-muted-foreground underline underline-offset-2"
            >
              {detailsOpen ? 'скрыть по вопросам' : 'показать по вопросам'}
            </button>
          </div>

          {detailsOpen && (
            <table className="mt-2 w-full text-left text-xs">
              <thead className="text-muted-foreground">
                <tr className="border-b border-border">
                  <th className="py-1.5 pr-2 font-medium">Вопрос</th>
                  <th className="py-1.5 pr-2 font-medium">{STRATEGY_LABEL.fixed_size}</th>
                  <th className="py-1.5 font-medium">{STRATEGY_LABEL.structural}</th>
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
          )}
        </div>
      )}
    </section>
  )
}
