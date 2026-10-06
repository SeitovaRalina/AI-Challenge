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
  runRetrievalEval,
  streamRunEval,
  type ChunkStrategy,
  type EvalQuestion,
  type IndexStatus,
  type RagAnswer,
  type RetrievedChunk,
} from '@/lib/api'
import { useRagPanelState } from '@/lib/rag-panel-state'

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
// separate sidebar screen from «Индексация» (day 21). All results live in
// useRagPanelState's module-level cache, not plain useState — navigating
// to a source chat and back must not lose a run that took minutes.
export function RagPanel({ onOpenSource }: RagPanelProps) {
  const [status, setStatus] = useState<IndexStatus | null>(null)
  const [state, update] = useRagPanelState()

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

      <CompareSection status={status} state={state} update={update} onOpenSource={onOpenSource} />
      <EvalSection status={status} state={state} update={update} onOpenSource={onOpenSource} />
      <RetrievalCompareSection status={status} state={state} update={update} />
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

type RagState = ReturnType<typeof useRagPanelState>[0]
type RagUpdate = ReturnType<typeof useRagPanelState>[1]

// CompareSection — главный инструмент: один вопрос, два ответа рядом. Не
// чат: ни истории, ни карточки оценки задачи — отдельный, минимальный
// путь в бэкенде (rag_query.go), не трогающий Agent/Estimate.
function CompareSection({
  status,
  state,
  update,
  onOpenSource,
}: {
  status: IndexStatus
  state: RagState
  update: RagUpdate
  onOpenSource: (chatId: string, messageIndex: number | null) => void
}) {
  async function handleCompare() {
    const q = state.compareQuestion.trim()
    if (!q) return
    update({ compareLoading: true, compareError: null, compareNoRag: null, compareRag: null })
    try {
      const [noRagRes, ragRes] = await Promise.all([
        queryRag(q, 'no_rag'),
        queryRag(q, 'rag', state.strategy),
      ])
      update({ compareNoRag: noRagRes, compareRag: ragRes, compareLoading: false })
    } catch (err) {
      update({
        compareError: err instanceof ApiError ? err.message : 'Не удалось получить ответ',
        compareLoading: false,
      })
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
          value={state.compareQuestion}
          onChange={(e) => update({ compareQuestion: e.target.value })}
          placeholder="Например: сколько часов на экспорт списка заказов в CSV в админке?"
          rows={2}
          className="flex-1 resize-none rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground"
        />
        <div className="flex items-center gap-2">
          <StrategySelect value={state.strategy} onChange={(strategy) => update({ strategy })} />
          <Button
            onClick={handleCompare}
            disabled={state.compareLoading || !status.exists || !state.compareQuestion.trim()}
          >
            {state.compareLoading && <Loader2 className="animate-spin" />}
            Сравнить
          </Button>
        </div>
      </div>

      {state.compareError && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{state.compareError}</AlertTitle>
        </Alert>
      )}

      {(state.compareNoRag || state.compareRag) && (
        <div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <AnswerColumn title="Без RAG" answer={state.compareNoRag} onOpenSource={onOpenSource} />
          <AnswerColumn title="С RAG" answer={state.compareRag} onOpenSource={onOpenSource} />
        </div>
      )}
    </section>
  )
}

function AnswerColumn({
  title,
  answer,
  onOpenSource,
  expectedSources,
}: {
  title: string
  answer: RagAnswer | null
  onOpenSource: (chatId: string, messageIndex: number | null) => void
  expectedSources?: string[]
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
              <SourceItem
                key={r.chunk_id}
                chunk={r}
                onOpenSource={onOpenSource}
                isExpected={expectedSources?.includes(r.session_id) ?? false}
              />
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
// isExpected (только в «10 контрольных вопросах») подсвечивает чанк,
// из-за которого вопросу засчитан бейдж «сессия найдена» — иначе непонятно,
// почему бейдж зелёный, если сам текст ответа этот факт не нашёл.
function SourceItem({
  chunk,
  onOpenSource,
  isExpected,
}: {
  chunk: RetrievedChunk
  onOpenSource: (chatId: string, messageIndex: number | null) => void
  isExpected?: boolean
}) {
  const [open, setOpen] = useState(false)
  const messageIndex = parseMessageIndex(chunk.section)

  return (
    <li className={cn('rounded border border-border/60', isExpected && 'border-emerald-500/50 bg-emerald-500/5')}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-start gap-1.5 px-2 py-1.5 text-left"
      >
        <ChevronRight
          className={cn('mt-0.5 h-3 w-3 flex-shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')}
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
            <span className="text-xs text-foreground">{chunk.title}</span>
            <span className="font-mono text-xs text-muted-foreground">{chunk.section}</span>
          </div>
          {isExpected && (
            <span className="text-xs font-medium text-emerald-700 dark:text-emerald-400">ожидаемая сессия</span>
          )}
        </div>
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
// expected source. Единственная автоматическая метрика — попадание
// ожидаемой сессии в топ-5 (бейдж «сессия найдена»); качество самого
// текста ответа сравнивается на глаз.
function EvalSection({
  status,
  state,
  update,
  onOpenSource,
}: {
  status: IndexStatus
  state: RagState
  update: RagUpdate
  onOpenSource: (chatId: string, messageIndex: number | null) => void
}) {
  const [questions, setQuestions] = useState<EvalQuestion[] | null>(null)
  const [questionsError, setQuestionsError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<number>>(new Set())

  useEffect(() => {
    getEvalQuestions()
      .then(setQuestions)
      .catch((err) =>
        setQuestionsError(err instanceof ApiError ? err.message : 'Не удалось загрузить контрольные вопросы'),
      )
  }, [])

  function toggle(i: number) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(i)) next.delete(i)
      else next.add(i)
      return next
    })
  }

  async function handleRun() {
    update({ evalRunning: true, evalError: null, evalProgress: null, evalResult: null })
    try {
      const result = await streamRunEval(state.strategy, (progress) => update({ evalProgress: progress }))
      update({ evalResult: result, evalRunning: false })
    } catch (err) {
      update({
        evalError: err instanceof ApiError ? err.message : 'Не удалось прогнать контрольные вопросы',
        evalRunning: false,
      })
    }
  }

  const progressPct = state.evalProgress ? Math.round((state.evalProgress.step / state.evalProgress.total) * 100) : 0

  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-sm font-medium text-foreground">10 контрольных вопросов</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            Для каждого — ожидание и (если применимо) ожидаемый источник.
          </p>
        </div>
        <Button onClick={handleRun} disabled={state.evalRunning || !status.exists || !questions?.length}>
          {state.evalRunning && <Loader2 className="animate-spin" />}
          Прогнать все 10
        </Button>
      </div>
      <p className="mt-2 rounded-md bg-muted/50 p-2 text-xs text-muted-foreground">
        Как это оценивается: единственная автоматическая метрика — попал ли среди топ-5
        найденных фрагментов чанк из ожидаемой сессии (бейдж «сессия найдена» ✓/✗ у вопроса; это
        проверка retrieval, не проверка того, что модель правильно использовала найденный факт).
        Качество самого текста ответа автоматически не оценивается — сравнивайте два столбца
        глазами после разворота вопроса.
      </p>

      {state.evalRunning && state.evalProgress && (
        <div className="mt-3">
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full bg-primary transition-all"
              style={{ width: `${progressPct}%` }}
            />
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {state.evalProgress.step}/{state.evalProgress.total} —{' '}
            {state.evalProgress.stage === 'rag' ? 'с RAG' : 'без RAG'}: {state.evalProgress.question}
          </p>
        </div>
      )}

      {questionsError && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{questionsError}</AlertTitle>
        </Alert>
      )}
      {state.evalError && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{state.evalError}</AlertTitle>
        </Alert>
      )}

      <ul className="mt-3 divide-y divide-border border-t border-border">
        {(state.evalResult?.results ?? questions)?.map((q, i) => {
          const r = state.evalResult?.results[i]
          const open = expanded.has(i)
          return (
            <li key={i}>
              <button
                type="button"
                onClick={() => toggle(i)}
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
                    {r &&
                      (r.expected_source_check ? (
                        <Badge
                          title="Хотя бы один чанк из ожидаемой сессии попал в топ-5 retrieval. Это проверка поиска, а не проверка того, что модель правильно использовала найденный факт в ответе — см. подсветку источника ниже."
                          className={cn(
                            r.expected_source_hit
                              ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400'
                              : 'bg-amber-500/15 text-amber-700 dark:text-amber-400',
                          )}
                        >
                          {r.expected_source_hit ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
                          сессия найдена
                        </Badge>
                      ) : (
                        <Badge
                          variant="secondary"
                          title="У вопроса нет одной конкретной ожидаемой сессии (агрегатный вопрос или вне базы) — метрика здесь неприменима."
                        >
                          нет привязки к сессии
                        </Badge>
                      ))}
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
                        expectedSources={r.expected_sources}
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
function RetrievalCompareSection({
  status,
  state,
  update,
}: {
  status: IndexStatus
  state: RagState
  update: RagUpdate
}) {
  async function handleRun() {
    update({ retrievalLoading: true, retrievalError: null })
    try {
      const result = await runRetrievalEval()
      update({ retrievalResult: result, retrievalLoading: false })
    } catch (err) {
      update({
        retrievalError: err instanceof ApiError ? err.message : 'Не удалось сравнить стратегии',
        retrievalLoading: false,
      })
    }
  }

  const result = state.retrievalResult

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
        <Button variant="outline" size="sm" onClick={handleRun} disabled={state.retrievalLoading || !status.exists}>
          {state.retrievalLoading && <Loader2 className="animate-spin" />}
          Сравнить
        </Button>
      </div>

      {state.retrievalError && (
        <Alert variant="destructive" className="mt-3">
          <AlertCircle />
          <AlertTitle>{state.retrievalError}</AlertTitle>
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
              onClick={() => update({ retrievalDetailsOpen: !state.retrievalDetailsOpen })}
              className="text-muted-foreground underline underline-offset-2"
            >
              {state.retrievalDetailsOpen ? 'скрыть по вопросам' : 'показать по вопросам'}
            </button>
          </div>

          {state.retrievalDetailsOpen && (
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
