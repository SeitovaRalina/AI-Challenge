import { useState } from 'react'
import { Pencil, X } from 'lucide-react'

import { updateProjectInvariants, updateProjectMemory, type Project } from '@/lib/api'

interface ProjectMemoryPopupProps {
  project: Project
  onClose: () => void
  onUpdate: (project: Project) => void
}

// linesToList / listToLines: the edit UI is a plain textarea, one entry per
// line — the simplest control that still gives full add/edit/delete over a
// list (type a new line to add, edit a line in place, delete a line to
// remove it), without a per-tag add/remove widget.
function listToLines(items: string[]): string {
  return items.join('\n')
}
function linesToList(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

// ProjectMemoryPopup shows day 11's long-term memory layer — known_stack and
// notes accumulated across every chat in this project, not just the one
// currently open. Same backdrop + centered-modal shell as ContextPopup,
// props-driven (the parent already holds the Project, no fetch here) except
// for the edit save, which calls the API directly and reports the result up
// via onUpdate — this is day 11's explicit "you choose what's saved" path,
// alongside the automatic per-turn extraction in memory_project.go.
export function ProjectMemoryPopup({ project, onClose, onUpdate }: ProjectMemoryPopupProps) {
  const knownStack = project.known_stack ?? []
  const notes = project.notes ?? []
  const invariants = project.invariants ?? []

  const [editing, setEditing] = useState(false)
  const [stackDraft, setStackDraft] = useState(() => listToLines(knownStack))
  const [notesDraft, setNotesDraft] = useState(() => listToLines(notes))
  const [invariantsDraft, setInvariantsDraft] = useState(() => listToLines(invariants))
  const [saving, setSaving] = useState(false)

  function startEditing() {
    setStackDraft(listToLines(knownStack))
    setNotesDraft(listToLines(notes))
    setInvariantsDraft(listToLines(invariants))
    setEditing(true)
  }

  async function handleSave() {
    setSaving(true)
    try {
      const [updated] = await Promise.all([
        updateProjectMemory(project.id, linesToList(stackDraft), linesToList(notesDraft)),
        updateProjectInvariants(project.id, linesToList(invariantsDraft)),
      ])
      onUpdate({ ...updated, invariants: linesToList(invariantsDraft) })
      setEditing(false)
    } finally {
      setSaving(false)
    }
  }

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
              Память проекта · {project.name}
            </p>
            <div className="flex items-center gap-0.5">
              {!editing && (
                <button
                  type="button"
                  onClick={startEditing}
                  title="Редактировать"
                  className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  <Pencil className="h-3.5 w-3.5" />
                </button>
              )}
              <button
                type="button"
                onClick={onClose}
                className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          <p className="mt-1 text-[11px] text-muted-foreground">
            Долговременная память — известна во всех чатах этого проекта, не только в текущем.
          </p>

          {editing ? (
            <div className="mt-3.5 flex flex-col gap-3.5">
              <div>
                <p className="text-xs font-medium text-muted-foreground">
                  Известный стек (по одному пункту на строку)
                </p>
                <textarea
                  autoFocus
                  value={stackDraft}
                  onChange={(event) => setStackDraft(event.target.value)}
                  rows={4}
                  className="mt-1.5 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
                />
              </div>
              <div>
                <p className="text-xs font-medium text-muted-foreground">
                  Заметки (по одной на строку)
                </p>
                <textarea
                  value={notesDraft}
                  onChange={(event) => setNotesDraft(event.target.value)}
                  rows={4}
                  className="mt-1.5 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
                />
              </div>
              <div>
                <p className="text-xs font-medium text-amber-600 dark:text-amber-500">
                  Инварианты — жёсткие ограничения (по одному на строку)
                </p>
                <p className="mt-0.5 text-[11px] text-muted-foreground">
                  Агент добавляет их сам, когда в диалоге явно зафиксировано решение; убрать
                  можно только здесь.
                </p>
                <textarea
                  value={invariantsDraft}
                  onChange={(event) => setInvariantsDraft(event.target.value)}
                  rows={3}
                  className="mt-1.5 w-full rounded-md border border-amber-600/40 bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
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
          ) : (
            <>
              <div className="mt-3.5">
                <p className="text-xs font-medium text-muted-foreground">Известный стек</p>
                {knownStack.length === 0 ? (
                  <p className="mt-1.5 text-sm text-muted-foreground">Пока ничего не зафиксировано</p>
                ) : (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {knownStack.map((item) => (
                      <span
                        key={item}
                        className="rounded-full border border-border px-2.5 py-1 text-xs text-foreground"
                      >
                        {item}
                      </span>
                    ))}
                  </div>
                )}
              </div>

              <div className="mt-4 border-t border-border pt-3.5">
                <p className="text-xs font-medium text-muted-foreground">Заметки</p>
                {notes.length === 0 ? (
                  <p className="mt-1.5 text-sm text-muted-foreground">Пока ничего не зафиксировано</p>
                ) : (
                  <ul className="mt-2 flex flex-col gap-1.5">
                    {notes.map((note, index) => (
                      <li key={index} className="rounded-md border border-border px-2.5 py-1.5 text-xs text-foreground">
                        {note}
                      </li>
                    ))}
                  </ul>
                )}
              </div>

              <div className="mt-4 border-t border-border pt-3.5">
                <p className="text-xs font-medium text-amber-600 dark:text-amber-500">
                  Инварианты — жёсткие ограничения
                </p>
                {invariants.length === 0 ? (
                  <p className="mt-1.5 text-sm text-muted-foreground">Пока не зафиксировано ни одного</p>
                ) : (
                  <ul className="mt-2 flex flex-col gap-1.5">
                    {invariants.map((invariant, index) => (
                      <li
                        key={index}
                        className="rounded-md border border-amber-600/30 bg-amber-600/5 px-2.5 py-1.5 text-xs text-foreground"
                      >
                        {invariant}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </>
          )}
        </div>
      </div>
    </>
  )
}
