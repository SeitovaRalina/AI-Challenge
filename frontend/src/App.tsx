import { useState } from 'react'

import { EstimateResult } from '@/components/estimate-result'
import { TaskForm } from '@/components/task-form'
import { ApiError, estimateTask, type Estimate } from '@/lib/api'

type Status = 'idle' | 'loading' | 'error' | 'success'

function App() {
  const [status, setStatus] = useState<Status>('idle')
  const [estimate, setEstimate] = useState<Estimate | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function handleSubmit(task: string) {
    setStatus('loading')
    setError(null)
    try {
      const result = await estimateTask(task)
      setEstimate(result)
      setStatus('success')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
      setStatus('error')
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
        <div className="mb-8 max-w-2xl">
          <h1 className="text-2xl font-medium text-foreground">
            Оценка задачи разработки
          </h1>
          <p className="mt-1.5 text-sm text-muted-foreground">
            Получите предварительную AI-оценку задачи разработки. Это общая
            оценка — она пока ничего не знает о вашей личной истории работы.
          </p>
        </div>

        <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
          <TaskForm onSubmit={handleSubmit} isSubmitting={status === 'loading'} />
          <EstimateResult status={status} estimate={estimate} error={error} />
        </div>
      </main>
    </div>
  )
}

export default App
