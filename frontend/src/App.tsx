import { useState } from 'react'

import { EstimateResult } from '@/components/estimate-result'
import { FormatComparison } from '@/components/format-comparison'
import { TaskForm } from '@/components/task-form'
import {
  ApiError,
  compareFormats,
  estimateTask,
  type Comparison,
  type Estimate,
} from '@/lib/api'
import { cn } from 'cn'

type Status = 'idle' | 'loading' | 'error' | 'success'
type Mode = 'estimate' | 'compare'

function App() {
  const [mode, setMode] = useState<Mode>('estimate')

  const [estimateStatus, setEstimateStatus] = useState<Status>('idle')
  const [estimate, setEstimate] = useState<Estimate | null>(null)
  const [estimateError, setEstimateError] = useState<string | null>(null)

  const [compareStatus, setCompareStatus] = useState<Status>('idle')
  const [comparison, setComparison] = useState<Comparison | null>(null)
  const [compareError, setCompareError] = useState<string | null>(null)

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
      const result = await compareFormats(task)
      setComparison(result)
      setCompareStatus('success')
    } catch (err) {
      setCompareError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setCompareStatus('error')
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
            {mode === 'estimate'
              ? 'Оценка задачи разработки'
              : 'Сравнение форматов ответа'}
          </h1>
          <p className="mt-1.5 text-sm text-muted-foreground">
            {mode === 'estimate'
              ? 'Получите предварительную AI-оценку задачи разработки. Это общая оценка — она пока ничего не знает о вашей личной истории работы.'
              : 'Один и тот же запрос уходит в LLM дважды: без ограничений формата и с явным форматом, лимитом длины и условием завершения.'}
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
        </div>

        {mode === 'estimate' ? (
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
        ) : (
          <div className="flex flex-col gap-8">
            <div className="max-w-2xl">
              <TaskForm
                onSubmit={handleCompareSubmit}
                isSubmitting={compareStatus === 'loading'}
                submitLabel="Сравнить"
                submittingLabel="Сравниваем…"
              />
            </div>
            <FormatComparison
              status={compareStatus}
              comparison={comparison}
              error={compareError}
            />
          </div>
        )}
      </main>
    </div>
  )
}

export default App
