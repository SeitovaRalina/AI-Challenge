import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Check,
  ChevronDown,
  FlaskConical,
  PanelLeftClose,
  Pencil,
  Plus,
  Target,
  Trash2,
  X,
} from 'lucide-react'

import { StrategyBadge } from '@/components/strategy-badge'
import { cn } from 'cn'
import type { ChatSummary } from '@/lib/api'
import { isRealStrategy } from '@/lib/strategy'

export type DemoMode = 'estimate' | 'compare' | 'reasoning' | 'temperature' | 'models'

const DEMO_LINKS: { mode: DemoMode; label: string }[] = [
  { mode: 'estimate', label: 'День 1 — Оценка задачи' },
  { mode: 'compare', label: 'День 2 — Форматы ответа' },
  { mode: 'reasoning', label: 'День 3 — Способы рассуждения' },
  { mode: 'temperature', label: 'День 4 — Температура' },
  { mode: 'models', label: 'День 5 — Версии моделей' },
]

const MIN_WIDTH = 220
const MAX_WIDTH = 420
const DEFAULT_WIDTH = 264
const WIDTH_STORAGE_KEY = 'sidebar-width'

function loadStoredWidth(): number {
  try {
    const stored = Number(localStorage.getItem(WIDTH_STORAGE_KEY))
    if (Number.isFinite(stored) && stored >= MIN_WIDTH && stored <= MAX_WIDTH) {
      return stored
    }
  } catch {
    // localStorage unavailable — fall back to the default.
  }
  return DEFAULT_WIDTH
}

interface SidebarProps {
  chats: ChatSummary[]
  activeChatId: string | null
  activeDemo: DemoMode | null
  onNewChat: () => void
  onNewLab: (label: string) => void
  onSelectChat: (id: string) => void
  onSelectDemo: (mode: DemoMode) => void
  onCollapse: () => void
  onRenameChat: (id: string, title: string) => void
  onDeleteChat: (id: string) => void
  onDeleteLab: (labId: string) => void
}

interface ChatGroup {
  labId: string | null
  chats: ChatSummary[]
}

// groupChats clusters a lab's chats under one header, in the order each
// group first appeared — a lab's chats are always created together
// (coordinator first), so this naturally keeps them contiguous and the
// coordinator first without needing a separate sort.
function groupChats(chats: ChatSummary[]): ChatGroup[] {
  const groups: ChatGroup[] = []
  const groupIndexByLabId = new Map<string, number>()
  for (const chat of chats) {
    if (!chat.lab_id) {
      groups.push({ labId: null, chats: [chat] })
      continue
    }
    const existingIndex = groupIndexByLabId.get(chat.lab_id)
    if (existingIndex != null) {
      groups[existingIndex].chats.push(chat)
    } else {
      groupIndexByLabId.set(chat.lab_id, groups.length)
      groups.push({ labId: chat.lab_id, chats: [chat] })
    }
  }
  return groups
}

export function Sidebar({
  chats,
  activeChatId,
  activeDemo,
  onNewChat,
  onNewLab,
  onSelectChat,
  onSelectDemo,
  onCollapse,
  onRenameChat,
  onDeleteChat,
  onDeleteLab,
}: SidebarProps) {
  const [demosOpen, setDemosOpen] = useState(false)
  const [labFormOpen, setLabFormOpen] = useState(false)
  const [width, setWidth] = useState(loadStoredWidth)
  const resizing = useRef(false)
  const groups = groupChats(chats)

  const handlePointerMove = useCallback((event: PointerEvent) => {
    if (!resizing.current) return
    const next = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, event.clientX))
    setWidth(next)
  }, [])

  const handlePointerUp = useCallback(() => {
    if (!resizing.current) return
    resizing.current = false
    document.body.style.cursor = ''
    setWidth((current) => {
      try {
        localStorage.setItem(WIDTH_STORAGE_KEY, String(current))
      } catch {
        // ignore — width just won't persist across reloads.
      }
      return current
    })
    window.removeEventListener('pointermove', handlePointerMove)
    window.removeEventListener('pointerup', handlePointerUp)
  }, [handlePointerMove])

  function startResize(event: React.PointerEvent) {
    event.preventDefault()
    resizing.current = true
    document.body.style.cursor = 'col-resize'
    window.addEventListener('pointermove', handlePointerMove)
    window.addEventListener('pointerup', handlePointerUp)
  }

  useEffect(() => {
    return () => {
      window.removeEventListener('pointermove', handlePointerMove)
      window.removeEventListener('pointerup', handlePointerUp)
    }
  }, [handlePointerMove, handlePointerUp])

  return (
    <aside
      style={{ width }}
      className="relative flex h-full flex-shrink-0 flex-col border-r border-border"
    >
      <div className="flex flex-col gap-1.5 p-3">
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={onNewChat}
            className="flex flex-1 items-center justify-center gap-2 rounded-md border border-border px-3 py-2 text-sm font-medium text-foreground transition-colors hover:bg-accent"
          >
            <Plus className="h-4 w-4" />
            Новый чат
          </button>
          <button
            type="button"
            onClick={onCollapse}
            title="Скрыть панель"
            className="flex-shrink-0 rounded-md p-2 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <PanelLeftClose className="h-4 w-4" />
          </button>
        </div>

        {labFormOpen ? (
          <NewLabForm
            onCreate={(label) => {
              onNewLab(label)
              setLabFormOpen(false)
            }}
            onCancel={() => setLabFormOpen(false)}
          />
        ) : (
          <button
            type="button"
            onClick={() => setLabFormOpen(true)}
            className="flex items-center justify-center gap-2 rounded-md border border-dashed border-border px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <FlaskConical className="h-4 w-4" />
            Лаборатория
          </button>
        )}
      </div>

      <nav className="flex-1 overflow-y-auto px-2">
        <ul className="flex flex-col gap-0.5">
          {groups.map((group) => {
            if (!group.labId) {
              const chat = group.chats[0]
              return (
                <ChatListItem
                  key={chat.id}
                  chat={chat}
                  active={activeChatId === chat.id}
                  onSelect={() => onSelectChat(chat.id)}
                  onRename={(title) => onRenameChat(chat.id, title)}
                  onDelete={() => onDeleteChat(chat.id)}
                />
              )
            }

            const coordinator = group.chats.find((c) => c.is_lab_coordinator)
            const strategyChats = group.chats.filter((c) => !c.is_lab_coordinator)
            const label = coordinator?.title ?? group.chats[0].title

            return (
              <li key={group.labId} className="flex flex-col gap-0.5">
                <LabGroupHeader label={label} onDelete={() => onDeleteLab(group.labId!)} />
                {coordinator && (
                  <ChatListItem
                    chat={coordinator}
                    active={activeChatId === coordinator.id}
                    onSelect={() => onSelectChat(coordinator.id)}
                    onRename={(title) => onRenameChat(coordinator.id, title)}
                    icon={<Target className="h-3.5 w-3.5 text-primary" />}
                    highlighted
                  />
                )}
                <ul className="flex flex-col gap-0.5 border-l border-border pl-2">
                  {strategyChats.map((chat) => (
                    <li key={chat.id}>
                      <button
                        type="button"
                        onClick={() => onSelectChat(chat.id)}
                        className={cn(
                          'flex w-full items-center rounded-md px-3 py-1.5 text-left transition-colors',
                          activeChatId === chat.id ? 'bg-accent' : 'hover:bg-accent',
                        )}
                      >
                        {isRealStrategy(chat.context_strategy) && (
                          <StrategyBadge strategy={chat.context_strategy} />
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </li>
            )
          })}
        </ul>
      </nav>

      <div className="border-t border-border px-2 py-2">
        <button
          type="button"
          onClick={() => setDemosOpen((open) => !open)}
          className="flex w-full items-center justify-between rounded-md px-3 py-2 text-xs font-medium tracking-wide text-muted-foreground hover:text-foreground"
        >
          День 1–5 (демо)
          <ChevronDown
            className={cn('h-3.5 w-3.5 transition-transform', demosOpen && 'rotate-180')}
          />
        </button>
        {demosOpen && (
          <ul className="flex flex-col gap-0.5 pb-1">
            {DEMO_LINKS.map((link) => (
              <li key={link.mode}>
                <button
                  type="button"
                  onClick={() => onSelectDemo(link.mode)}
                  className={cn(
                    'block w-full truncate rounded-md px-3 py-1.5 text-left text-sm transition-colors',
                    activeDemo === link.mode
                      ? 'bg-accent text-foreground'
                      : 'text-muted-foreground hover:bg-accent hover:text-foreground',
                  )}
                >
                  {link.label}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div
        onPointerDown={startResize}
        className="absolute top-0 right-0 h-full w-1 cursor-col-resize hover:bg-primary/30"
      />
    </aside>
  )
}

// LabGroupHeader names the scenario (the coordinator's own title) and is the
// only place a lab can be deleted from — its member chats don't get their
// own delete button, since removing one alone would leave the comparison
// incomplete.
function LabGroupHeader({ label, onDelete }: { label: string; onDelete: () => void }) {
  const [confirming, setConfirming] = useState(false)

  if (confirming) {
    return (
      <div className="mt-1.5 flex items-center gap-1 rounded-md bg-destructive/10 px-3 py-1">
        <span className="flex-1 truncate text-[11px] text-foreground">Удалить лабораторию?</span>
        <button
          type="button"
          onClick={onDelete}
          title="Да, удалить"
          className="rounded p-1 text-destructive hover:bg-destructive/20"
        >
          <Check className="h-3 w-3" />
        </button>
        <button
          type="button"
          onClick={() => setConfirming(false)}
          title="Отмена"
          className="rounded p-1 text-muted-foreground hover:bg-accent"
        >
          <X className="h-3 w-3" />
        </button>
      </div>
    )
  }

  return (
    <div className="group mt-1.5 flex items-center gap-1.5 px-3 py-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
      <FlaskConical className="h-3 w-3 shrink-0" />
      <span className="flex-1 truncate normal-case">{label}</span>
      <button
        type="button"
        onClick={() => setConfirming(true)}
        title="Удалить лабораторию"
        className="rounded p-1 opacity-0 transition-opacity hover:bg-background hover:text-destructive group-hover:opacity-100"
      >
        <Trash2 className="h-3 w-3" />
      </button>
    </div>
  )
}

// NewLabForm asks for the scenario's label, then creates the coordinator
// chat plus one chat per required day-10 strategy (sliding_window/
// sticky_facts/branching) — see backend/lab.go.
function NewLabForm({
  onCreate,
  onCancel,
}: {
  onCreate: (label: string) => void
  onCancel: () => void
}) {
  const [label, setLabel] = useState('')

  function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    const trimmed = label.trim()
    if (!trimmed) return
    onCreate(trimmed)
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-1.5 rounded-md border border-border p-2">
      <input
        autoFocus
        value={label}
        onChange={(event) => setLabel(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape') onCancel()
        }}
        placeholder="Название сценария"
        className="w-full rounded-md border border-border bg-transparent px-2 py-1.5 text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring"
      />
      <div className="flex items-center gap-1.5">
        <button
          type="submit"
          disabled={!label.trim()}
          className="flex-1 rounded-md bg-primary px-2 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-40"
        >
          Создать лабораторию
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          Отмена
        </button>
      </div>
    </form>
  )
}

interface ChatListItemProps {
  chat: ChatSummary
  active: boolean
  onSelect: () => void
  onRename?: (title: string) => void
  onDelete?: () => void
  icon?: React.ReactNode
  highlighted?: boolean
}

function ChatListItem({ chat, active, onSelect, onRename, onDelete, icon, highlighted }: ChatListItemProps) {
  const [editing, setEditing] = useState(false)
  const [draftTitle, setDraftTitle] = useState(chat.title)
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing) inputRef.current?.select()
  }, [editing])

  function startEditing() {
    setDraftTitle(chat.title)
    setEditing(true)
  }

  function commitRename() {
    const trimmed = draftTitle.trim()
    setEditing(false)
    if (trimmed && trimmed !== chat.title) onRename?.(trimmed)
  }

  if (editing) {
    return (
      <li>
        <input
          ref={inputRef}
          value={draftTitle}
          onChange={(event) => setDraftTitle(event.target.value)}
          onBlur={commitRename}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              commitRename()
            }
            if (event.key === 'Escape') setEditing(false)
          }}
          autoFocus
          className="w-full rounded-md border border-ring bg-background px-3 py-2 text-sm outline-none ring-3 ring-ring/50"
        />
      </li>
    )
  }

  if (confirmingDelete) {
    return (
      <li className="flex items-center gap-1 rounded-md bg-destructive/10 px-3 py-2">
        <span className="flex-1 truncate text-sm text-foreground">Удалить чат?</span>
        <button
          type="button"
          onClick={onDelete}
          title="Да, удалить"
          className="rounded p-1 text-destructive hover:bg-destructive/20"
        >
          <Check className="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          onClick={() => setConfirmingDelete(false)}
          title="Отмена"
          className="rounded p-1 text-muted-foreground hover:bg-accent"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </li>
    )
  }

  return (
    <li className="group relative">
      <button
        type="button"
        onClick={onSelect}
        className={cn(
          'flex w-full items-center gap-1.5 truncate rounded-md py-2 pr-14 pl-3 text-left text-sm transition-colors',
          active
            ? 'bg-accent text-foreground'
            : highlighted
              ? 'bg-primary/5 text-foreground hover:bg-accent'
              : 'text-muted-foreground hover:bg-accent hover:text-foreground',
        )}
        title={chat.title}
      >
        {icon}
        <span className="truncate">{chat.title}</span>
      </button>
      {(onRename || onDelete) && (
        <div className="absolute top-1/2 right-1 flex -translate-y-1/2 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          {onRename && (
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation()
                startEditing()
              }}
              title="Переименовать"
              className="rounded p-1.5 text-muted-foreground hover:bg-background hover:text-foreground"
            >
              <Pencil className="h-3.5 w-3.5" />
            </button>
          )}
          {onDelete && (
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation()
                setConfirmingDelete(true)
              }}
              title="Удалить"
              className="rounded p-1.5 text-muted-foreground hover:bg-background hover:text-destructive"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      )}
    </li>
  )
}
