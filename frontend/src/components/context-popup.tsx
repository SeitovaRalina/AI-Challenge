import { useState } from 'react'
import { Pencil, X } from 'lucide-react'

import { cn } from 'cn'
import { updateChatTask, type AgentMessage, type BranchSummary, type CompressionEvent, type ContextStrategy, type TaskMemory } from '@/lib/api'

interface ContextPopupProps {
  chatId: string
  strategy: ContextStrategy
  historyKeepLastN: number
  messages: AgentMessage[]
  facts?: Record<string, string>
  branches?: BranchSummary[]
  activeBranchId?: string
  compressionEvents: CompressionEvent[]
  summarizedMessageCount: number
  rawMessageCount: number
  task?: TaskMemory
  onUpdateTask: (task: TaskMemory) => void
  onClose: () => void
}

const STRATEGY_TITLES: Record<ContextStrategy, string> = {
  sliding_window: 'Sliding window',
  sticky_facts: 'Sticky facts',
  branching: 'Branching',
  rolling_summary: 'Rolling summary',
  coordinator: 'Координатор',
}

// ContextPopup is the /context command's output: unlike /tokens (a small
// panel anchored above the composer), this is a centered modal — the user
// asked for it to "float in the center over the chat" — since its content
// (a full branch tree, or a facts table) can be taller than a corner popup
// comfortably holds.
export function ContextPopup({
  chatId,
  strategy,
  historyKeepLastN,
  messages,
  facts,
  branches,
  activeBranchId,
  compressionEvents,
  summarizedMessageCount,
  rawMessageCount,
  task,
  onUpdateTask,
  onClose,
}: ContextPopupProps) {
  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/40" onClick={onClose} />
      <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
        <div
          onClick={(event) => event.stopPropagation()}
          className="max-h-[80vh] w-full max-w-md overflow-y-auto rounded-xl border border-border bg-card p-4 shadow-lg"
        >
          <div className="flex items-center justify-between">
            <p className="text-sm font-medium text-foreground">
              Контекст сейчас · {STRATEGY_TITLES[strategy]}
            </p>
            <button
              type="button"
              onClick={onClose}
              className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>

          <div className="mt-3.5 border-b border-border pb-3.5">
            <TaskSection chatId={chatId} task={task} onUpdate={onUpdateTask} />
          </div>

          <div className="mt-3.5">
            <p className="text-xs font-medium text-muted-foreground">
              Краткосрочная память
            </p>
            <div className="mt-2">
              {strategy === 'sliding_window' && (
                <SlidingWindowSection messages={messages} historyKeepLastN={historyKeepLastN} />
              )}
              {strategy === 'sticky_facts' && (
                <>
                  <FactsSection facts={facts} />
                  <div className="mt-4 border-t border-border pt-3.5">
                    <SlidingWindowSection messages={messages} historyKeepLastN={historyKeepLastN} />
                  </div>
                </>
              )}
              {strategy === 'branching' && (
                <BranchingSection branches={branches} activeBranchId={activeBranchId} />
              )}
              {strategy === 'rolling_summary' && (
                <RollingSummarySection
                  compressionEvents={compressionEvents}
                  summarizedMessageCount={summarizedMessageCount}
                  rawMessageCount={rawMessageCount}
                />
              )}
            </div>
          </div>
        </div>
      </div>
    </>
  )
}

function SlidingWindowSection({
  messages,
  historyKeepLastN,
}: {
  messages: AgentMessage[]
  historyKeepLastN: number
}) {
  const windowMessages =
    historyKeepLastN > 0 ? messages.slice(-historyKeepLastN) : messages
  const droppedCount = messages.length - windowMessages.length

  return (
    <div>
      <p className="text-xs font-medium text-muted-foreground">
        В промпт уходят последние {historyKeepLastN || messages.length} сообщений
      </p>
      <ul className="mt-2 flex flex-col gap-1.5">
        {windowMessages.map((message, index) => (
          <li key={index} className="rounded-md border border-border px-2.5 py-1.5 text-xs">
            <span className="font-mono text-[10px] tracking-wide text-muted-foreground uppercase">
              {message.role === 'user' ? 'user' : 'assistant'}
            </span>
            <p className="mt-0.5 line-clamp-2 text-foreground">{message.content}</p>
          </li>
        ))}
      </ul>
      {droppedCount > 0 && (
        <p className="mt-2 text-[11px] text-muted-foreground">
          Ещё {droppedCount} сообщ. хранится в истории чата, но больше не отправляется модели.
        </p>
      )}
    </div>
  )
}

// linesToList / listToLines / pairsToLines / linesToPairs: the edit UI is
// plain textareas, one entry per line ("key: value" for the answers map) —
// the simplest control that still gives full add/edit/delete over a list or
// map, without a per-item add/remove widget.
function listToLines(items: string[]): string {
  return items.join('\n')
}
function linesToList(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}
function pairsToLines(pairs: Record<string, string>): string {
  return Object.entries(pairs)
    .map(([key, value]) => `${key}: ${value}`)
    .join('\n')
}
function linesToPairs(text: string): Record<string, string> {
  const result: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const separatorIndex = trimmed.indexOf(':')
    if (separatorIndex === -1) continue
    const key = trimmed.slice(0, separatorIndex).trim()
    const value = trimmed.slice(separatorIndex + 1).trim()
    if (key) result[key] = value
  }
  return result
}

// TaskSection is day 11's working-memory layer — always shown, independent
// of the strategy switch below it, since Chat.Task is updated regardless of
// which ContextStrategy the chat is on (unlike Facts, which only exists for
// sticky_facts). Kept visually distinct from the sections below so it's
// obvious this is a different kind of memory, not another strategy view.
// Editable — day 11's "явно выбирали, что и куда сохраняется" means the
// user can add, edit, or delete this directly, not just watch the model
// fill it in automatically after each turn.
function TaskSection({
  chatId,
  task,
  onUpdate,
}: {
  chatId: string
  task?: TaskMemory
  onUpdate: (task: TaskMemory) => void
}) {
  const hasContent =
    task && (task.goal || task.constraints.length > 0 || Object.keys(task.clarifying_answers).length > 0)

  const [editing, setEditing] = useState(false)
  const [goalDraft, setGoalDraft] = useState(task?.goal ?? '')
  const [constraintsDraft, setConstraintsDraft] = useState(() => listToLines(task?.constraints ?? []))
  const [answersDraft, setAnswersDraft] = useState(() => pairsToLines(task?.clarifying_answers ?? {}))
  const [saving, setSaving] = useState(false)

  function startEditing() {
    setGoalDraft(task?.goal ?? '')
    setConstraintsDraft(listToLines(task?.constraints ?? []))
    setAnswersDraft(pairsToLines(task?.clarifying_answers ?? {}))
    setEditing(true)
  }

  async function handleSave() {
    setSaving(true)
    try {
      const updated = await updateChatTask(chatId, {
        goal: goalDraft.trim(),
        constraints: linesToList(constraintsDraft),
        clarifying_answers: linesToPairs(answersDraft),
      })
      onUpdate(updated)
      setEditing(false)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between">
        <p className="text-xs font-medium text-muted-foreground">
          Рабочая память задачи
        </p>
        {!editing && (
          <button
            type="button"
            onClick={startEditing}
            title="Редактировать"
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <Pencil className="h-3 w-3" />
          </button>
        )}
      </div>

      {editing ? (
        <div className="mt-2 flex flex-col gap-3">
          <div>
            <p className="text-[10px] font-mono tracking-wide text-muted-foreground uppercase">цель</p>
            <input
              autoFocus
              value={goalDraft}
              onChange={(event) => setGoalDraft(event.target.value)}
              className="mt-1 w-full rounded-md border border-border bg-transparent px-2 py-1.5 text-xs outline-none focus-visible:border-ring"
            />
          </div>
          <div>
            <p className="text-[10px] font-mono tracking-wide text-muted-foreground uppercase">
              ограничения (по одному на строку)
            </p>
            <textarea
              value={constraintsDraft}
              onChange={(event) => setConstraintsDraft(event.target.value)}
              rows={3}
              className="mt-1 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
            />
          </div>
          <div>
            <p className="text-[10px] font-mono tracking-wide text-muted-foreground uppercase">
              ответы (формат «ключ: значение», по одному на строку)
            </p>
            <textarea
              value={answersDraft}
              onChange={(event) => setAnswersDraft(event.target.value)}
              rows={3}
              className="mt-1 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
            />
          </div>
          <div className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={handleSave}
              disabled={saving}
              className="flex-1 rounded-md bg-primary px-2 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-40"
            >
              {saving ? 'Сохраняем…' : 'Сохранить'}
            </button>
            <button
              type="button"
              onClick={() => setEditing(false)}
              disabled={saving}
              className="rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground"
            >
              Отмена
            </button>
          </div>
        </div>
      ) : !hasContent ? (
        <p className="mt-1.5 text-sm text-muted-foreground">Пока ничего не зафиксировано</p>
      ) : (
        <div className="mt-2 flex flex-col gap-1.5">
          {task.goal && (
            <p className="rounded-md border border-border px-2.5 py-1.5 text-xs text-foreground">
              {task.goal}
            </p>
          )}
          {task.constraints.length > 0 && (
            <div className="rounded-md border border-dashed border-border px-2.5 py-1.5 text-xs">
              <p className="font-mono text-[10px] tracking-wide text-muted-foreground uppercase">
                ограничения
              </p>
              <ul className="mt-1 list-disc pl-4 text-foreground">
                {task.constraints.map((constraint, index) => (
                  <li key={index}>{constraint}</li>
                ))}
              </ul>
            </div>
          )}
          {Object.entries(task.clarifying_answers).map(([key, value]) => (
            <div key={key} className="rounded-md border border-border px-2.5 py-1.5 text-xs">
              <dt className="font-mono text-[10px] tracking-wide text-muted-foreground">{key}</dt>
              <dd className="mt-0.5 text-foreground">{value}</dd>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function FactsSection({ facts }: { facts?: Record<string, string> }) {
  const entries = Object.entries(facts ?? {})
  return (
    <div>
      <p className="text-xs font-medium text-muted-foreground">
        Факты (обновляются после каждого сообщения)
      </p>
      {entries.length === 0 ? (
        <p className="mt-1.5 text-sm text-muted-foreground">Пока ничего не зафиксировано</p>
      ) : (
        <dl className="mt-2 flex flex-col gap-1.5">
          {entries.map(([key, value]) => (
            <div key={key} className="rounded-md border border-border px-2.5 py-1.5 text-xs">
              <dt className="font-mono text-[10px] tracking-wide text-muted-foreground">{key}</dt>
              <dd className="mt-0.5 text-foreground">{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}

function BranchingSection({
  branches,
  activeBranchId,
}: {
  branches?: BranchSummary[]
  activeBranchId?: string
}) {
  if (!branches || branches.length === 0) {
    return <p className="text-sm text-muted-foreground">Веток пока нет</p>
  }
  return (
    <div>
      <p className="text-xs font-medium text-muted-foreground">Дерево веток</p>
      <ul className="mt-2 flex flex-col gap-1.5">
        {branches.map((branch) => (
          <li
            key={branch.id}
            className={cn(
              'rounded-md border px-2.5 py-1.5 text-xs',
              branch.id === activeBranchId
                ? 'border-primary bg-primary/5'
                : 'border-border',
            )}
          >
            <div className="flex items-center justify-between">
              <span className="font-medium text-foreground">{branch.label}</span>
              {branch.id === activeBranchId && (
                <span className="text-[10px] font-medium tracking-wide text-primary uppercase">
                  активна
                </span>
              )}
            </div>
            <p className="mt-0.5 text-[11px] text-muted-foreground">
              {branch.parent_id
                ? `от «${branch.parent_id === 'main' ? 'Основная' : branch.parent_id}», сообщ. ${branch.fork_index}`
                : 'корневая ветка'}{' '}
              · {branch.message_count} сообщ.
            </p>
          </li>
        ))}
      </ul>
    </div>
  )
}

function RollingSummarySection({
  compressionEvents,
  summarizedMessageCount,
  rawMessageCount,
}: {
  compressionEvents: CompressionEvent[]
  summarizedMessageCount: number
  rawMessageCount: number
}) {
  const latest = compressionEvents[compressionEvents.length - 1]
  return (
    <div>
      {summarizedMessageCount > 0 ? (
        <p className="text-xs text-muted-foreground">
          Резюме {summarizedMessageCount} сообщений + {rawMessageCount} последних как есть
        </p>
      ) : (
        <p className="text-xs text-muted-foreground">
          Пока ничего не сжато ({rawMessageCount} сообщений в истории)
        </p>
      )}
      {latest && (
        <p className="mt-2 rounded-md border border-border px-2.5 py-1.5 text-xs text-foreground italic">
          {latest.summary}
        </p>
      )}
    </div>
  )
}
