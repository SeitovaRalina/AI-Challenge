import { useState } from 'react'
import {
  AlertCircle,
  ChevronRight,
  GitCommitHorizontal,
  GitMerge,
  GitPullRequest,
  MessageSquare,
  ScanEye,
  Wrench,
} from 'lucide-react'
import { cn } from 'cn'

import type { ActivityEvent, ActivityKind, ToolCallRecord } from '@/lib/api'

const KIND_META: Record<ActivityKind, { label: string; icon: typeof GitCommitHorizontal }> = {
  commit: { label: 'коммит', icon: GitCommitHorizontal },
  pr_opened: { label: 'PR открыт', icon: GitPullRequest },
  pr_merged: { label: 'PR смёржен', icon: GitMerge },
  review: { label: 'ревью', icon: ScanEye },
  issue_comment: { label: 'комментарий', icon: MessageSquare },
}

// ToolCallList shows the MCP tool calls an assistant message was built on
// (day 17): which tool, with what arguments, what came back — so the reply
// below can be checked against the raw result.
export function ToolCallList({ calls }: { calls: ToolCallRecord[] }) {
  return (
    <div className="mb-2 flex flex-col gap-1.5">
      {calls.map((call, index) => (
        <ToolCallCard key={index} call={call} />
      ))}
    </div>
  )
}

function ToolCallCard({ call }: { call: ToolCallRecord }) {
  const [open, setOpen] = useState(false)
  const events = call.result?.events
  const summary = resultSummary(call)

  return (
    <div
      className={cn(
        'rounded-lg border text-xs',
        call.ok ? 'border-border bg-muted/40' : 'border-destructive/40 bg-destructive/5',
      )}
    >
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-start gap-2 px-2.5 py-2 text-left"
      >
        {call.ok ? (
          <Wrench className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-destructive" />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
            <span className="text-muted-foreground">Вызов инструмента</span>
            <span className="font-mono text-foreground">
              {call.server} › {call.tool}
            </span>
            <span className="text-muted-foreground">· {call.duration_ms} мс</span>
          </div>
          <ArgChips args={call.arguments} />
          <div className={cn('mt-1', call.ok ? 'text-foreground' : 'text-destructive')}>
            {call.ok ? summary : `Ошибка: ${call.error}`}
          </div>
        </div>
        <ChevronRight
          className={cn('mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')}
        />
      </button>
      {open && (
        <div className="border-t border-border px-2.5 py-2">
          {events && events.length > 0 ? (
            <EventList events={events} omitted={call.result?.events_omitted} />
          ) : (
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] text-muted-foreground">
              {JSON.stringify(call.ok ? call.result : call.error, null, 2)}
            </pre>
          )}
          {call.result?.warnings && call.result.warnings.length > 0 && (
            <ul className="mt-2 list-disc pl-4 text-warning">
              {call.result.warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

function ArgChips({ args }: { args: Record<string, unknown> }) {
  const entries = Object.entries(args ?? {})
  if (entries.length === 0) {
    return <div className="mt-1 text-muted-foreground">без аргументов</div>
  }
  return (
    <div className="mt-1 flex flex-wrap gap-1">
      {entries.map(([key, value]) => (
        <span key={key} className="rounded bg-background px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
          {key}: {Array.isArray(value) ? value.join(', ') : String(value)}
        </span>
      ))}
    </div>
  )
}

function resultSummary(call: ToolCallRecord): string {
  const result = call.result
  if (!result) return 'Результат получен'
  if (result.counts) {
    const total = Object.values(result.counts).reduce((sum, n) => sum + (n ?? 0), 0)
    if (total === 0) return 'Событий не найдено'
    const parts = (Object.entries(result.counts) as [ActivityKind, number][])
      .filter(([, n]) => n > 0)
      .map(([kind, n]) => `${KIND_META[kind]?.label ?? kind}: ${n}`)
    return `${total} ${eventsWord(total)} — ${parts.join(', ')}`
  }
  if (Array.isArray(result.repos)) {
    return `${result.repos.length} ${reposWord(result.repos.length)}`
  }
  return 'Результат получен'
}

function EventList({ events, omitted }: { events: ActivityEvent[]; omitted?: number }) {
  return (
    <>
      <ul className="flex max-h-72 flex-col gap-1 overflow-y-auto">
        {events.map((event) => {
          const meta = KIND_META[event.kind]
          const Icon = meta?.icon ?? GitCommitHorizontal
          return (
            <li key={event.id} className="flex items-start gap-1.5">
              <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                {formatEventTime(event.occurred_at)}
              </span>
              <span className="min-w-0">
                <a
                  href={event.url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-foreground underline-offset-2 hover:underline"
                >
                  {event.title}
                </a>
                <span className="text-muted-foreground">
                  {' '}
                  · {event.repo.split('/').pop()}
                  {event.kind === 'commit' && event.ref ? ` · ${event.ref}` : ''}
                </span>
              </span>
            </li>
          )
        })}
      </ul>
      {omitted ? (
        <p className="mt-1.5 text-muted-foreground">…и ещё {omitted} (ассистент видел все)</p>
      ) : null}
    </>
  )
}

function formatEventTime(iso: string): string {
  return new Date(iso).toLocaleString('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function plural(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10
  const mod100 = n % 100
  if (mod10 === 1 && mod100 !== 11) return one
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few
  return many
}

function eventsWord(n: number): string {
  return plural(n, 'событие', 'события', 'событий')
}

function reposWord(n: number): string {
  return plural(n, 'репозиторий', 'репозитория', 'репозиториев')
}
