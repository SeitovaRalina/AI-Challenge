import { X } from 'lucide-react'

import type { AgentMessage, LocalModelPerformance, LLMProvider } from '@/lib/api'

const label: Record<LLMProvider, string> = { ollama: 'Ollama', litellm: 'LiteLLM' }

function median(numbers: number[]): number {
  const values = [...numbers].sort((a, b) => a - b)
  const middle = Math.floor(values.length / 2)
  return values.length % 2 ? values[middle] : (values[middle - 1] + values[middle]) / 2
}

function duration(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} с` : `${ms} мс`
}

function bytes(value: number): string {
  return `${(value / 1024 ** 3).toFixed(2)} ГБ`
}

export function PerformancePopup({
  messages,
  runtime,
  runtimeError,
  onClose,
}: {
  messages: AgentMessage[]
  runtime: LocalModelPerformance | null
  runtimeError: boolean
  onClose: () => void
}) {
  const turns = messages.filter((item) => item.role === 'assistant' && item.performance)
  const latest = turns.at(-1)
  const recent = turns.slice(-10)
  return (
    <>
      <div className="fixed inset-0 z-40" onClick={onClose} />
      <div role="dialog" aria-label="Производительность моделей" className="absolute right-6 bottom-full z-50 mb-3 w-80 max-w-[calc(100vw-3rem)] rounded-xl border border-border bg-card p-4 shadow-lg">
        <div className="flex items-center justify-between">
          <p className="text-sm font-medium">Производительность</p>
          <button type="button" aria-label="Закрыть" onClick={onClose} className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"><X className="h-3.5 w-3.5" /></button>
        </div>

        {latest?.performance ? (
          <div className="mt-3 space-y-1 text-xs">
            <p className="font-medium">Последний ответ · {label[latest.performance.provider]}</p>
            <p className="font-mono text-muted-foreground">{latest.performance.model}</p>
            <PerfRow name="Весь ответ" value={duration(latest.performance.duration_ms)} />
            {latest.usage && <>
              <PerfRow name="Запрос / ответ" value={`${latest.usage.prompt_tokens} / ${latest.usage.completion_tokens} токенов`} />
              <PerfRow name="Скорость всего ответа" value={`${(latest.usage.completion_tokens / Math.max(latest.performance.duration_ms / 1000, 0.001)).toFixed(1)} токенов/с`} />
            </>}
          </div>
        ) : <p className="mt-3 text-xs text-muted-foreground">Пока нет измеренных ответов.</p>}

        {recent.length > 0 && <div className="mt-4 space-y-1 border-t border-border pt-3 text-xs">
          <p className="font-medium">Последние {recent.length} ответов</p>
          {(['ollama', 'litellm'] as const).map((provider) => {
            const values = recent.filter((item) => item.performance?.provider === provider).map((item) => item.performance!.duration_ms)
            return values.length ? <PerfRow key={provider} name={`${label[provider]} · медиана (${values.length})`} value={duration(median(values))} /> : null
          })}
          <p className="text-[11px] text-muted-foreground">Запросы могут различаться; для точного сравнения нужен одинаковый набор задач.</p>
        </div>}

        <div className="mt-4 space-y-1 border-t border-border pt-3 text-xs">
          <p className="font-medium">Локальная модель сейчас</p>
          {runtime ? <>
            <PerfRow name="Модель" value={runtime.model} />
            <PerfRow name="Температура / ответ" value={`${runtime.temperature} / ${runtime.max_tokens} токенов`} />
            <PerfRow name="Бюджет контекста" value={`${runtime.app_context_budget} токенов`} />
            {runtime.loaded ? <>
              <PerfRow name="Квантование" value={runtime.quantization || 'неизвестно'} />
              <PerfRow name="Фактический контекст" value={`${runtime.context_length ?? '—'} токенов`} />
              <PerfRow name="Загруженный размер" value={bytes(runtime.loaded_size_bytes ?? 0)} />
              <PerfRow name="Видеопамять" value={bytes(runtime.vram_bytes ?? 0)} />
            </> : <p className="text-muted-foreground">Модель сейчас не загружена.</p>}
          </> : <p className="text-muted-foreground">{runtimeError ? 'Нет связи с диагностикой Ollama.' : 'Загрузка…'}</p>}
          <p className="text-[11px] text-muted-foreground">Размер модели и видеопамять — данные Ollama; это не полный расход RAM или загрузка CPU.</p>
        </div>
      </div>
    </>
  )
}

function PerfRow({ name, value }: { name: string; value: string }) {
  return <div className="flex justify-between gap-3"><span className="text-muted-foreground">{name}</span><span className="text-right tabular-nums">{value}</span></div>
}
