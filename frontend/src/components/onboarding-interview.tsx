import { useState } from 'react'
import { X } from 'lucide-react'

import { updateProfile, type UserProfile } from '@/lib/api'

interface OnboardingInterviewProps {
  profile: UserProfile
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

type StepKind = 'text' | 'lines'

interface Step {
  key: 'name' | 'stack' | 'style' | 'format' | 'constraints'
  question: string
  kind: StepKind
  placeholder: string
  optional?: boolean
}

const STEPS: Step[] = [
  { key: 'name', question: 'Как к вам обращаться?', kind: 'text', placeholder: 'Например: Ралина' },
  {
    key: 'stack',
    question: 'На каком стеке вы обычно пишете?',
    kind: 'lines',
    placeholder: 'По одному пункту на строку, например:\nFlutter\nPython',
  },
  {
    key: 'style',
    question: 'Какой стиль общения вам удобен?',
    kind: 'text',
    placeholder: 'Например: неформальный, на ты',
  },
  {
    key: 'format',
    question: 'В каком формате вам удобны ответы?',
    kind: 'text',
    placeholder: 'Например: коротко, без вступлений',
  },
  {
    key: 'constraints',
    question: 'Есть особые ограничения к ответам?',
    kind: 'lines',
    placeholder: 'По одному пункту на строку — необязательно',
    optional: true,
  },
]

// OnboardingInterview fills the profile through 5 fixed, scripted questions
// — one at a time, feels like the assistant is asking, but nothing here is
// LLM-generated or LLM-sequenced. An earlier version sent one big kickoff
// message and let the model both ask the questions AND decide when to stop;
// across three separate real runs it either drifted into unrelated
// follow-up questions, wrapped up after 2 of 5, or (with a heavily-negated
// prompt) made day 11's task-memory extraction capture its own
// meta-instructions and re-inject them into the next turn, breaking the
// main LLM call outright. A fixed, code-driven sequence has none of that
// risk — it can't drift or stop early — at the cost of feeling a little
// less like a free-form conversation. Answers are collected locally and
// written in one PATCH /api/profile call at the end, same path as
// ProfilePopup's manual edit.
export function OnboardingInterview({ profile, onClose, onUpdate }: OnboardingInterviewProps) {
  const [stepIndex, setStepIndex] = useState(0)
  const [answers, setAnswers] = useState({
    name: profile.name,
    stack: listToLines(profile.stack),
    style: profile.style,
    format: profile.format,
    constraints: listToLines(profile.constraints),
  })
  const [saving, setSaving] = useState(false)

  const step = STEPS[stepIndex]
  const isLast = stepIndex === STEPS.length - 1
  const value = answers[step.key]

  function setValue(next: string) {
    setAnswers((prev) => ({ ...prev, [step.key]: next }))
  }

  function goBack() {
    setStepIndex((i) => Math.max(0, i - 1))
  }

  async function goNext() {
    if (!isLast) {
      setStepIndex((i) => i + 1)
      return
    }
    setSaving(true)
    try {
      const updated = await updateProfile({
        name: answers.name.trim(),
        stack: linesToList(answers.stack),
        style: answers.style.trim(),
        format: answers.format.trim(),
        constraints: linesToList(answers.constraints),
      })
      onUpdate(updated)
      onClose()
    } finally {
      setSaving(false)
    }
  }

  function handleKeyDown(event: React.KeyboardEvent) {
    if (event.key === 'Enter' && (step.kind === 'text' || !event.shiftKey)) {
      event.preventDefault()
      goNext()
    }
  }

  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/40" onClick={onClose} />
      <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
        <div
          onClick={(event) => event.stopPropagation()}
          className="w-full max-w-md rounded-xl border border-border bg-card p-4 shadow-lg"
        >
          <div className="flex items-center justify-between">
            <p className="text-sm font-medium text-foreground">
              Знакомство · {stepIndex + 1} из {STEPS.length}
            </p>
            <button
              type="button"
              onClick={onClose}
              className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>

          <p className="mt-3.5 text-sm text-foreground">{step.question}</p>

          <div className="mt-2">
            {step.kind === 'text' ? (
              <input
                autoFocus
                value={value}
                onChange={(event) => setValue(event.target.value)}
                onKeyDown={handleKeyDown}
                placeholder={step.placeholder}
                className="w-full rounded-md border border-border bg-transparent p-2 text-sm outline-none focus-visible:border-ring"
              />
            ) : (
              <textarea
                autoFocus
                value={value}
                onChange={(event) => setValue(event.target.value)}
                placeholder={step.placeholder}
                rows={3}
                className="w-full rounded-md border border-border bg-transparent p-2 text-sm outline-none focus-visible:border-ring"
              />
            )}
          </div>

          <div className="mt-3.5 flex items-center gap-1.5">
            <button
              type="button"
              onClick={goBack}
              disabled={stepIndex === 0 || saving}
              className="rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground disabled:opacity-40"
            >
              Назад
            </button>
            <div className="flex-1" />
            {step.optional && !isLast && (
              <button
                type="button"
                onClick={goNext}
                disabled={saving}
                className="rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground"
              >
                Пропустить
              </button>
            )}
            <button
              type="button"
              onClick={goNext}
              disabled={saving}
              className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-40"
            >
              {saving ? 'Сохраняем…' : isLast ? 'Готово' : 'Далее'}
            </button>
          </div>
        </div>
      </div>
    </>
  )
}
