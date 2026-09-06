import type { CompareOptions } from '@/lib/api'

interface CompareOptionsFormProps {
  value: CompareOptions
  onChange: (value: CompareOptions) => void
}

export function CompareOptionsForm({ value, onChange }: CompareOptionsFormProps) {
  return (
    <div className="flex flex-col gap-4 rounded-lg border border-border p-4 text-sm">
      <p className="text-xs font-medium tracking-wide text-muted-foreground">
        Параметры «С ограничениями»
      </p>

      <div className="flex flex-col gap-1.5">
        <label htmlFor="max-tokens" className="text-xs text-muted-foreground">
          Лимит длины (max_tokens)
        </label>
        <input
          id="max-tokens"
          type="number"
          min={200}
          max={4000}
          step={100}
          value={value.maxTokens}
          onChange={(event) =>
            onChange({ ...value, maxTokens: Number(event.target.value) })
          }
          className="w-full rounded-md border border-border bg-transparent px-2 py-1 font-mono text-sm text-foreground"
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <label htmlFor="max-items" className="text-xs text-muted-foreground">
          Макс. пунктов в списках
        </label>
        <input
          id="max-items"
          type="number"
          min={1}
          max={10}
          value={value.maxItems}
          onChange={(event) =>
            onChange({ ...value, maxItems: Number(event.target.value) })
          }
          className="w-full rounded-md border border-border bg-transparent px-2 py-1 font-mono text-sm text-foreground"
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <label htmlFor="temperature" className="text-xs text-muted-foreground">
          Temperature
        </label>
        <input
          id="temperature"
          type="number"
          min={0}
          max={1}
          step={0.1}
          value={value.temperature}
          onChange={(event) =>
            onChange({ ...value, temperature: Number(event.target.value) })
          }
          className="w-full rounded-md border border-border bg-transparent px-2 py-1 font-mono text-sm text-foreground"
        />
      </div>

      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        <input
          type="checkbox"
          checked={value.useStopInstruction}
          onChange={(event) =>
            onChange({ ...value, useStopInstruction: event.target.checked })
          }
          className="size-3.5 rounded border-border accent-primary"
        />
        Инструкция завершения
      </label>
    </div>
  )
}
