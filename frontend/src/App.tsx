import { useState } from 'react'

import { CompareOptionsForm } from '@/components/compare-options'
import { EstimateResult } from '@/components/estimate-result'
import { FormatComparison } from '@/components/format-comparison'
import {
  ReasoningComparison,
  type ReasoningReaction,
} from '@/components/reasoning-comparison'
import { ReasoningStatusPanel } from '@/components/reasoning-status-panel'
import { TaskForm } from '@/components/task-form'
import {
  ApiError,
  compareControlled,
  compareFormats,
  compareReasoning,
  estimateTask,
  type Comparison,
  type CompareOptions,
  type Estimate,
  type RawResult,
  type ReasoningComparison as ReasoningComparisonData,
} from '@/lib/api'
import { cn } from 'cn'

type Status = 'idle' | 'loading' | 'error' | 'success'
type Mode = 'estimate' | 'compare' | 'reasoning'

const DEFAULT_COMPARE_OPTIONS: CompareOptions = {
  maxTokens: 1000,
  maxItems: 3,
  temperature: 0.2,
  useStopInstruction: true,
}

const MODE_COPY: Record<Mode, { title: string; description: string }> = {
  estimate: {
    title: 'Оценка задачи разработки',
    description:
      'Получите предварительную AI-оценку задачи разработки. Это общая оценка — она пока ничего не знает о вашей личной истории работы.',
  },
  compare: {
    title: 'Сравнение форматов ответа',
    description:
      'Один и тот же запрос уходит в LLM дважды: без ограничений формата и с явным форматом, лимитом длины и условием завершения.',
  },
  reasoning: {
    title: 'Способы рассуждения',
    description:
      'Одна и та же задача решается через LLM четырьмя способами: прямой ответ, пошаговое рассуждение, мета-промпт (модель сама составляет промпт) и группа экспертов.',
  },
}

function App() {
  const [mode, setMode] = useState<Mode>('estimate')

  const [estimateStatus, setEstimateStatus] = useState<Status>('idle')
  const [estimate, setEstimate] = useState<Estimate | null>(null)
  const [estimateError, setEstimateError] = useState<string | null>(null)

  const [compareStatus, setCompareStatus] = useState<Status>('idle')
  const [comparison, setComparison] = useState<Comparison | null>(null)
  const [compareError, setCompareError] = useState<string | null>(null)
  const [compareOptions, setCompareOptions] = useState<CompareOptions>(
    DEFAULT_COMPARE_OPTIONS,
  )
  const [uncontrolledCache, setUncontrolledCache] = useState<{
    task: string
    result: RawResult
  } | null>(null)
  const [uncontrolledReused, setUncontrolledReused] = useState(false)

  const [reasoningStatus, setReasoningStatus] = useState<Status>('idle')
  const [reasoningComparison, setReasoningComparison] =
    useState<ReasoningComparisonData | null>(null)
  const [reasoningError, setReasoningError] = useState<string | null>(null)
  const [reasoningReaction, setReasoningReaction] =
    useState<ReasoningReaction>(null)

  async function handleEstimateSubmit(task: string) {
    setEstimateStatus('loading')
    setEstimateError(null)
    try {
      const result = await estimateTask(task)
      setEstimate(result)
      setEstimateStatus('success')
    } catch (err) {
      setEstimateError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setEstimateStatus('error')
    }
  }

  async function handleCompareSubmit(task: string) {
    setCompareStatus('loading')
    setCompareError(null)
    try {
      if (uncontrolledCache && uncontrolledCache.task === task) {
        const controlled = await compareControlled(task, compareOptions)
        setComparison({ task, uncontrolled: uncontrolledCache.result, controlled })
        setUncontrolledReused(true)
      } else {
        const result = await compareFormats(task, compareOptions)
        setComparison(result)
        setUncontrolledCache({ task, result: result.uncontrolled })
        setUncontrolledReused(false)
      }
      setCompareStatus('success')
    } catch (err) {
      setCompareError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setCompareStatus('error')
    }
  }

  async function handleReasoningSubmit(task: string) {
    setReasoningStatus('loading')
    setReasoningError(null)
    setReasoningReaction(null)
    try {
      const result = await compareReasoning(task)
      setReasoningComparison(result)
      setReasoningStatus('success')
    } catch (err) {
      setReasoningError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setReasoningStatus('error')
    }
  }

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b border-border">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4">
          <div className="flex items-center gap-2">
            <span className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-sm font-semibold text-primary-foreground">
              W
            </span>
            <span className="text-sm font-medium text-foreground">
              Work Intelligence
            </span>
          </div>
          <nav className="flex items-center gap-4 text-sm text-muted-foreground">
            <span className="text-foreground">Оценка</span>
            <span className="cursor-not-allowed opacity-50">Таймшит</span>
            <span className="cursor-not-allowed opacity-50">Аналитика</span>
          </nav>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-6 py-10">
        <div className="mb-6 max-w-2xl">
          <h1 className="text-2xl font-medium text-foreground">
            {MODE_COPY[mode].title}
          </h1>
          <p className="mt-1.5 text-sm text-muted-foreground">
            {MODE_COPY[mode].description}
          </p>
        </div>

        <div className="mb-8 inline-flex rounded-lg border border-border p-1 text-sm">
          <button
            type="button"
            onClick={() => setMode('estimate')}
            className={cn(
              'rounded-md px-3 py-1.5 font-medium transition-colors',
              mode === 'estimate'
                ? 'bg-primary text-primary-foreground'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            Оценка
          </button>
          <button
            type="button"
            onClick={() => setMode('compare')}
            className={cn(
              'rounded-md px-3 py-1.5 font-medium transition-colors',
              mode === 'compare'
                ? 'bg-primary text-primary-foreground'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            Сравнение форматов
          </button>
          <button
            type="button"
            onClick={() => setMode('reasoning')}
            className={cn(
              'rounded-md px-3 py-1.5 font-medium transition-colors',
              mode === 'reasoning'
                ? 'bg-primary text-primary-foreground'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            Способы рассуждения
          </button>
        </div>

        {mode === 'estimate' && (
          <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
            <TaskForm
              onSubmit={handleEstimateSubmit}
              isSubmitting={estimateStatus === 'loading'}
            />
            <EstimateResult
              status={estimateStatus}
              estimate={estimate}
              error={estimateError}
            />
          </div>
        )}

        {mode === 'compare' && (
          <div className="flex flex-col gap-8">
            <div className="grid grid-cols-1 gap-8 lg:grid-cols-[2fr_1fr]">
              <TaskForm
                onSubmit={handleCompareSubmit}
                isSubmitting={compareStatus === 'loading'}
                submitLabel="Сравнить"
                submittingLabel="Сравниваем…"
                noteBeforeSubmit={
                  <p className="font-mono text-xs text-primary">
                    Запрос «с ограничениями» отправится с параметрами:
                    max_tokens={compareOptions.maxTokens}, max_items=
                    {compareOptions.maxItems}, temperature=
                    {compareOptions.temperature}, stop_instruction=
                    {compareOptions.useStopInstruction ? 'да' : 'нет'}
                  </p>
                }
              />
              <CompareOptionsForm
                value={compareOptions}
                onChange={setCompareOptions}
              />
            </div>
            <FormatComparison
              status={compareStatus}
              comparison={comparison}
              error={compareError}
              uncontrolledReused={uncontrolledReused}
            />
          </div>
        )}

        {mode === 'reasoning' && (
          <div className="flex flex-col gap-8">
            <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
              <TaskForm
                onSubmit={handleReasoningSubmit}
                isSubmitting={reasoningStatus === 'loading'}
                submitLabel="Решить"
                submittingLabel="Решаем…"
              />
              <ReasoningStatusPanel
                status={reasoningStatus}
                comparison={reasoningComparison}
                reaction={reasoningReaction}
              />
            </div>
            <ReasoningComparison
              status={reasoningStatus}
              comparison={reasoningComparison}
              error={reasoningError}
              reaction={reasoningReaction}
              onReactionChange={setReasoningReaction}
            />
          </div>
        )}
      </main>
    </div>
  )
}

export default App
