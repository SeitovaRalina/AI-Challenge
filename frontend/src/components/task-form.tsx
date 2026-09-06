import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

const EXAMPLE_TASK =
  'Обновить устаревшее Flutter-приложение до новой версии Flutter, обновить зависимости, исправить проблемы сборки под iOS и Android и подготовить новые билды.'

interface TaskFormProps {
  onSubmit: (task: string) => void
  isSubmitting: boolean
}

export function TaskForm({ onSubmit, isSubmitting }: TaskFormProps) {
  const [task, setTask] = useState('')

  function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    const trimmed = task.trim()
    if (!trimmed || isSubmitting) return
    onSubmit(trimmed)
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <label htmlFor="task" className="text-sm font-medium text-foreground">
          Опишите задачу
        </label>
        <Textarea
          id="task"
          value={task}
          onChange={(event) => setTask(event.target.value)}
          placeholder={EXAMPLE_TASK}
          rows={10}
          className="resize-none font-mono text-sm"
        />
        <p className="text-xs text-muted-foreground">
          Опишите реальную задачу разработки своими словами. Чем конкретнее,
          тем полезнее будет оценка.
        </p>
      </div>
      <Button
        type="submit"
        disabled={!task.trim() || isSubmitting}
        className="self-start"
      >
        {isSubmitting ? 'Оцениваем…' : 'Оценить задачу'}
      </Button>
    </form>
  )
}
