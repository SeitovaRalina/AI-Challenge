import { useState } from 'react'
import { Pencil, X } from 'lucide-react'

import { updateProfile, type UserProfile } from '@/lib/api'

interface ProfilePopupProps {
  profile: UserProfile
  initialEditing?: boolean
  onClose: () => void
  onUpdate: (profile: UserProfile) => void
}

function listToLines(items: string[]): string {
  return items.join('\n')
}
function linesToList(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

// ProfilePopup shows day 12's personalization layer — the single global
// profile applied to every non-lab chat. Same backdrop + centered-modal
// shell as ProjectMemoryPopup, props-driven except for the edit save, which
// calls the API directly and reports the result up via onUpdate. Opens in a
// read-only preview by default (the sidebar's profile icon) — initialEditing
// switches it straight to editing instead, for the empty-state hint's
// "Заполнить вручную" button, where the user has already signaled intent to
// edit.
export function ProfilePopup({ profile, initialEditing, onClose, onUpdate }: ProfilePopupProps) {
  const [editing, setEditing] = useState(initialEditing ?? false)
  const [nameDraft, setNameDraft] = useState(profile.name)
  const [stackDraft, setStackDraft] = useState(() => listToLines(profile.stack))
  const [styleDraft, setStyleDraft] = useState(profile.style)
  const [formatDraft, setFormatDraft] = useState(profile.format)
  const [constraintsDraft, setConstraintsDraft] = useState(() => listToLines(profile.constraints))
  const [saving, setSaving] = useState(false)

  function startEditing() {
    setNameDraft(profile.name)
    setStackDraft(listToLines(profile.stack))
    setStyleDraft(profile.style)
    setFormatDraft(profile.format)
    setConstraintsDraft(listToLines(profile.constraints))
    setEditing(true)
  }

  async function handleSave() {
    setSaving(true)
    try {
      const updated = await updateProfile({
        name: nameDraft.trim(),
        stack: linesToList(stackDraft),
        style: styleDraft.trim(),
        format: formatDraft.trim(),
        constraints: linesToList(constraintsDraft),
      })
      onUpdate(updated)
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
            <p className="text-sm font-medium text-foreground">Профиль пользователя</p>
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
            Персонализация — применяется в каждом запросе, во всех чатах и проектах (кроме лабораторий).
          </p>

          {editing ? (
            <div className="mt-3.5 flex flex-col gap-3.5">
              <div>
                <p className="text-xs font-medium text-muted-foreground">Имя</p>
                <input
                  autoFocus
                  value={nameDraft}
                  onChange={(event) => setNameDraft(event.target.value)}
                  placeholder="Как к вам обращаться"
                  className="mt-1.5 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
                />
              </div>
              <div>
                <p className="text-xs font-medium text-muted-foreground">
                  Стек (по одному на строку)
                </p>
                <textarea
                  value={stackDraft}
                  onChange={(event) => setStackDraft(event.target.value)}
                  placeholder="Например: Flutter"
                  rows={2}
                  className="mt-1.5 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
                />
              </div>
              <div>
                <p className="text-xs font-medium text-muted-foreground">Стиль общения</p>
                <input
                  value={styleDraft}
                  onChange={(event) => setStyleDraft(event.target.value)}
                  placeholder="Например: неформальный, на ты"
                  className="mt-1.5 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
                />
              </div>
              <div>
                <p className="text-xs font-medium text-muted-foreground">Формат ответов</p>
                <input
                  value={formatDraft}
                  onChange={(event) => setFormatDraft(event.target.value)}
                  placeholder="Например: коротко, без вступлений"
                  className="mt-1.5 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
                />
              </div>
              <div>
                <p className="text-xs font-medium text-muted-foreground">
                  Ограничения (по одному на строку)
                </p>
                <textarea
                  value={constraintsDraft}
                  onChange={(event) => setConstraintsDraft(event.target.value)}
                  rows={4}
                  className="mt-1.5 w-full rounded-md border border-border bg-transparent p-2 text-xs outline-none focus-visible:border-ring"
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
                <p className="text-xs font-medium text-muted-foreground">Имя</p>
                <p className="mt-1.5 text-sm text-foreground">
                  {profile.name || <span className="text-muted-foreground">Не задано</span>}
                </p>
              </div>

              <div className="mt-3.5">
                <p className="text-xs font-medium text-muted-foreground">Стек</p>
                {profile.stack.length === 0 ? (
                  <p className="mt-1.5 text-sm text-muted-foreground">Не задано</p>
                ) : (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {profile.stack.map((item) => (
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

              <div className="mt-3.5">
                <p className="text-xs font-medium text-muted-foreground">Стиль общения</p>
                <p className="mt-1.5 text-sm text-foreground">
                  {profile.style || <span className="text-muted-foreground">Не задано</span>}
                </p>
              </div>

              <div className="mt-3.5">
                <p className="text-xs font-medium text-muted-foreground">Формат ответов</p>
                <p className="mt-1.5 text-sm text-foreground">
                  {profile.format || <span className="text-muted-foreground">Не задано</span>}
                </p>
              </div>

              <div className="mt-4 border-t border-border pt-3.5">
                <p className="text-xs font-medium text-muted-foreground">Ограничения</p>
                {profile.constraints.length === 0 ? (
                  <p className="mt-1.5 text-sm text-muted-foreground">Пока ничего не зафиксировано</p>
                ) : (
                  <ul className="mt-2 flex flex-col gap-1.5">
                    {profile.constraints.map((constraint, index) => (
                      <li
                        key={index}
                        className="rounded-md border border-border px-2.5 py-1.5 text-xs text-foreground"
                      >
                        {constraint}
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
