import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  ApiError,
  listSolidtimeTasks,
  listTimesheetDrafts,
  prepareTimesheet,
  reconcileTimesheetDraft,
  reviewTimesheetDraft,
  syncTimesheetDraft,
  type SolidtimeProject,
  type SolidtimeTask,
  type TimesheetDraft,
} from '@/lib/api'

const statusLabel: Record<TimesheetDraft['status'], string> = {
  draft: 'Черновик',
  confirmed: 'Подтверждено',
  rejected: 'Отклонено',
  syncing: 'Результат записи неизвестен',
  synced: 'Записано в Solidtime',
}

function errorText(error: unknown): string {
  return error instanceof ApiError ? error.message : 'Не удалось выполнить действие. Повторите попытку.'
}

function formatTime(value: string): string {
  return new Date(value).toLocaleString('ru-RU', { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' })
}

export function TimesheetSyncCard({ projects }: { projects: SolidtimeProject[] }) {
  const [days, setDays] = useState(7)
  const [drafts, setDrafts] = useState<TimesheetDraft[]>([])
  const [tasks, setTasks] = useState<SolidtimeTask[]>([])
  const [warnings, setWarnings] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    const [stored, taskList] = await Promise.all([listTimesheetDrafts(), listSolidtimeTasks()])
    setDrafts(stored.drafts)
    setTasks(taskList.tasks)
  }, [])

  useEffect(() => {
    Promise.all([listTimesheetDrafts(), listSolidtimeTasks()])
      .then(([stored, taskList]) => { setDrafts(stored.drafts); setTasks(taskList.tasks) })
      .catch((err) => setError(errorText(err)))
  }, [])

  async function prepare() {
    setBusy(true)
    setError(null)
    try {
      const result = await prepareTimesheet(days)
      setWarnings(result.warnings ?? [])
      await reload()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  function updateDraft(updated: TimesheetDraft) {
    setDrafts((previous) => previous.map((draft) => draft.id === updated.id ? updated : draft))
  }

  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="font-semibold">Запись времени в Solidtime</h2>
          <p className="text-sm text-muted-foreground">Проверьте сессии, выберите задачу проекта и подтвердите каждую запись. Отправка — отдельное действие.</p>
        </div>
        <div className="flex items-center gap-2">
          <label htmlFor="timesheet-days" className="text-sm">За последние дни</label>
          <input id="timesheet-days" type="number" min={1} max={30} value={days} onChange={(event) => setDays(Number(event.target.value))} className="w-16 rounded-md border border-input bg-background px-2 py-1 text-sm" />
          <Button onClick={prepare} disabled={busy || days < 1 || days > 30}>{busy ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Подготовить'}</Button>
        </div>
      </div>
      {error && <p role="alert" className="mt-3 text-sm text-destructive">{error}</p>}
      {warnings.map((warning, index) => <p key={index} className="mt-2 text-xs text-amber-700 dark:text-amber-400">{warning}</p>)}
      {drafts.length === 0 && <p className="mt-4 text-sm text-muted-foreground">Черновиков пока нет. Нажмите «Подготовить».</p>}
      <div className="mt-4 space-y-3">
        {drafts.map((draft) => <DraftRow key={`${draft.id}:${draft.status}:${draft.description}:${draft.task_id}:${draft.billable}`} draft={draft} projects={projects} tasks={tasks} onUpdated={updateDraft} />)}
      </div>
    </section>
  )
}

function DraftRow({ draft, projects, tasks, onUpdated }: { draft: TimesheetDraft; projects: SolidtimeProject[]; tasks: SolidtimeTask[]; onUpdated: (draft: TimesheetDraft) => void }) {
  const [taskId, setTaskId] = useState(draft.task_id ?? '')
  const [description, setDescription] = useState(draft.description)
  const [billable, setBillable] = useState(draft.billable)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const project = projects.find((item) => item.id === draft.project_id)
  const availableTasks = tasks.filter((item) => item.project_id === draft.project_id && !item.is_done)
  const flagged = (draft.session.flags?.length ?? 0) > 0
  const editable = draft.status === 'draft' || draft.status === 'rejected' || draft.status === 'confirmed'

  async function act(action: () => Promise<TimesheetDraft>) {
    setBusy(true)
    setError(null)
    try {
      onUpdated(await action())
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <article className="rounded-md border border-border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
        <strong>{draft.session.repo || 'Без репозитория'}</strong>
        <span>{formatTime(draft.session.start)} — {formatTime(draft.session.end)}</span>
        <span className="text-muted-foreground">{statusLabel[draft.status]}</span>
      </div>
      <p className="mt-1 text-xs text-muted-foreground">Проект: {project?.name ?? 'не выбран'} · Событий: {draft.session.event_count}</p>
      {draft.session.remark && <p className="mt-2 text-sm">{draft.session.remark}</p>}
      {flagged && <p className="mt-2 text-sm text-amber-700 dark:text-amber-400">Требует проверки: {draft.session.flags?.join(', ')}. Подтверждение заблокировано.</p>}
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <label className="text-sm">Задача в проекте
          <select value={taskId} onChange={(event) => setTaskId(event.target.value)} disabled={!editable || busy} className="mt-1 w-full rounded-md border border-input bg-background px-2 py-2">
            <option value="">Выберите задачу</option>
            {availableTasks.map((task) => <option key={task.id} value={task.id}>{task.name}</option>)}
          </select>
        </label>
        <label className="text-sm">Описание трека
          <input value={description} onChange={(event) => setDescription(event.target.value)} disabled={!editable || busy} maxLength={5000} className="mt-1 w-full rounded-md border border-input bg-background px-2 py-2" />
        </label>
      </div>
      <label className="mt-3 flex items-center gap-2 text-sm"><input type="checkbox" checked={billable} onChange={(event) => setBillable(event.target.checked)} disabled={!editable || busy} />Оплачиваемое время</label>
      {error && <p role="alert" className="mt-2 text-sm text-destructive">{error}</p>}
      <div className="mt-3 flex flex-wrap gap-2">
        {(draft.status === 'draft' || draft.status === 'rejected' || draft.status === 'confirmed') && <>
          <Button variant="outline" disabled={busy || flagged || !taskId || !description.trim()} onClick={() => act(() => reviewTimesheetDraft(draft.id, 'confirm', taskId, description, billable))}>Подтвердить</Button>
          <Button variant="ghost" disabled={busy} onClick={() => act(() => reviewTimesheetDraft(draft.id, 'reject', taskId, description, billable))}>Отклонить</Button>
        </>}
        {draft.status === 'confirmed' && <Button disabled={busy} onClick={() => act(() => syncTimesheetDraft(draft.id))}>Записать в Solidtime</Button>}
        {draft.status === 'syncing' && <Button variant="outline" disabled={busy} onClick={() => act(() => reconcileTimesheetDraft(draft.id))}>Сверить с Solidtime</Button>}
        {draft.solidtime_entry_id && <span className="self-center text-xs text-muted-foreground">ID: {draft.solidtime_entry_id}</span>}
      </div>
    </article>
  )
}
