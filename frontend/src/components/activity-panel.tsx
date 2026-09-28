import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AlertCircle, CheckCircle2, ChevronRight, Clock, Loader2 } from 'lucide-react'
import { cn } from 'cn'

import { Alert, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { KIND_META } from '@/components/tool-call-card'
import {
  ApiError,
  getActivityDigest,
  getActivityEvents,
  getActivityStatus,
  triggerActivityCollect,
  type ActivityDigest,
  type ActivityEventsResult,
  type ActivityKind,
  type ActivityPeriod,
  type CollectorRun,
  type CollectorStatus,
} from '@/lib/api'

// ActivityPanel is day 18's «Активность» screen: the background collector's
// own state (last run, next run, a manual trigger, the run log) plus what it
// has stored so far — a digest and the event feed, both read straight from
// the Worklog MCP server, the same tools the chat agent calls. No charts
// here (day 19); the digest is counts, not a picture of them.

// ACTIVITY_STATUS_POLL_MS: the collector runs unattended, so this is how
// often the open screen checks on it without the person reloading the page.
const ACTIVITY_STATUS_POLL_MS = 4000

export function ActivityPanel() {
  const [status, setStatus] = useState<CollectorStatus | null>(null)
  const [statusError, setStatusError] = useState<string | null>(null)
  const [triggering, setTriggering] = useState(false)
  const [period, setPeriod] = useState<ActivityPeriod>('today')
  const [digest, setDigest] = useState<ActivityDigest | null>(null)
  const [digestError, setDigestError] = useState<string | null>(null)
  const [events, setEvents] = useState<ActivityEventsResult | null>(null)
  const [eventsError, setEventsError] = useState<string | null>(null)
  const [repoFilter, setRepoFilter] = useState('')
  const [kindFilter, setKindFilter] = useState<ActivityKind | ''>('')

  const loadData = useCallback(() => {
    getActivityDigest(period)
      .then((d) => {
        setDigest(d)
        setDigestError(null)
      })
      .catch((err) => setDigestError(err instanceof ApiError ? err.message : 'Не удалось загрузить сводку'))
    getActivityEvents(period, { repo: repoFilter || undefined, kind: kindFilter || undefined })
      .then((e) => {
        setEvents(e)
        setEventsError(null)
      })
      .catch((err) => setEventsError(err instanceof ApiError ? err.message : 'Не удалось загрузить события'))
  }, [period, repoFilter, kindFilter])

  useEffect(loadData, [loadData])

  // The collector runs on its own, unattended, so the screen polls its
  // status rather than waiting for a click — a run started by the schedule
  // (or, in another tab, another click of "Собрать сейчас") is otherwise
  // invisible until the page happens to be reloaded. lastRunKeyRef tracks
  // the most recent run seen (running or finished); when it changes to a
  // finished run, the digest and event feed that run produced are reloaded
  // too. The very first poll only seeds the ref — it must not immediately
  // refetch data that useEffect(loadData) above just fetched.
  const lastRunKeyRef = useRef<string | null>(null)
  const seenFirstPollRef = useRef(false)
  useEffect(() => {
    let cancelled = false
    const poll = () => {
      getActivityStatus()
        .then((s) => {
          if (cancelled) return
          setStatus(s)
          setStatusError(null)
          const latest = s.current ?? s.runs[0] ?? null
          const key = latest ? `${latest.trigger}:${latest.started_at}:${latest.status}` : null
          if (!seenFirstPollRef.current) {
            seenFirstPollRef.current = true
            lastRunKeyRef.current = key
            return
          }
          if (key !== lastRunKeyRef.current) {
            lastRunKeyRef.current = key
            if (!s.current) loadData()
          }
        })
        .catch((err) => {
          if (!cancelled) setStatusError(err instanceof ApiError ? err.message : 'Не удалось загрузить статус сборщика')
        })
    }
    poll()
    const timer = setInterval(poll, ACTIVITY_STATUS_POLL_MS)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [loadData])

  async function handleCollectNow() {
    setTriggering(true)
    try {
      const s = await triggerActivityCollect()
      setStatus(s)
      setStatusError(null)
    } catch (err) {
      setStatusError(err instanceof ApiError ? err.message : 'Не удалось запустить сбор')
    } finally {
      setTriggering(false)
    }
  }

  const repoOptions = useMemo(() => digest?.by_repo.map((r) => r.repo) ?? [], [digest])

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      {statusError && !status ? (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>{statusError}</AlertTitle>
        </Alert>
      ) : !status ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Загрузка…
        </div>
      ) : (
        <CollectorCard status={status} triggering={triggering} onCollectNow={handleCollectNow} />
      )}

      <section className="rounded-lg border border-border bg-card">
        <div className="flex flex-wrap items-center gap-2 border-b border-border p-4">
          <h2 className="mr-auto text-sm font-medium text-foreground">Сводка</h2>
          <PeriodTabs period={period} onChange={setPeriod} />
        </div>
        {digestError ? (
          <Alert variant="destructive" className="m-4">
            <AlertCircle />
            <AlertTitle>{digestError}</AlertTitle>
          </Alert>
        ) : !digest ? (
          <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> Загрузка…
          </div>
        ) : (
          <DigestSummary digest={digest} />
        )}
      </section>

      <section className="rounded-lg border border-border bg-card">
        <div className="flex flex-wrap items-center gap-2 border-b border-border p-4">
          <h2 className="mr-auto text-sm font-medium text-foreground">События</h2>
          {repoOptions.length > 0 && (
            <select
              value={repoFilter}
              onChange={(e) => setRepoFilter(e.target.value)}
              className="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground"
            >
              <option value="">Все репозитории</option>
              {repoOptions.map((r) => (
                <option key={r} value={r}>
                  {r.split('/').pop()}
                </option>
              ))}
            </select>
          )}
          <select
            value={kindFilter}
            onChange={(e) => setKindFilter(e.target.value as ActivityKind | '')}
            className="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground"
          >
            <option value="">Все типы</option>
            {(Object.keys(KIND_META) as ActivityKind[]).map((k) => (
              <option key={k} value={k}>
                {KIND_META[k].label}
              </option>
            ))}
          </select>
        </div>
        {eventsError ? (
          <Alert variant="destructive" className="m-4">
            <AlertCircle />
            <AlertTitle>{eventsError}</AlertTitle>
          </Alert>
        ) : !events ? (
          <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> Загрузка…
          </div>
        ) : (
          <EventFeed result={events} />
        )}
      </section>
    </div>
  )
}

function CollectorCard({
  status,
  triggering,
  onCollectNow,
}: {
  status: CollectorStatus
  triggering: boolean
  onCollectNow: () => void
}) {
  const [logOpen, setLogOpen] = useState(false)
  const running = status.current

  return (
    <section className="rounded-lg border border-border bg-card">
      <div className="flex flex-wrap items-center gap-3 p-4">
        <span className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          {running ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : status.enabled ? (
            <CheckCircle2 className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <AlertCircle className="h-4 w-4 text-muted-foreground" />
          )}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-medium text-foreground">Фоновый сборщик</h2>
            {running ? (
              <Badge variant="secondary">сбор идёт…</Badge>
            ) : status.enabled ? (
              <Badge className="bg-emerald-500/15 text-emerald-700 dark:text-emerald-400">включён</Badge>
            ) : (
              <Badge variant="outline">выключен</Badge>
            )}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">{collectorSummaryLine(status)}</p>
        </div>
        <Button onClick={onCollectNow} disabled={triggering || Boolean(running) || !status.enabled} variant="outline">
          {(triggering || running) && <Loader2 className="animate-spin" />}
          Собрать сейчас
        </Button>
      </div>

      {status.sync_error && (
        <div className="border-t border-border p-4">
          <Alert variant="destructive">
            <AlertCircle />
            <AlertTitle>Worklog MCP недоступен: {status.sync_error}</AlertTitle>
          </Alert>
        </div>
      )}

      {status.runs.length > 0 && (
        <div className="border-t border-border">
          <button
            type="button"
            onClick={() => setLogOpen((v) => !v)}
            className="flex w-full items-center gap-2 px-4 py-2.5 text-left text-sm text-muted-foreground hover:bg-accent/50"
          >
            <ChevronRight className={cn('h-4 w-4 transition-transform', logOpen && 'rotate-90')} />
            Журнал запусков ({status.runs.length})
          </button>
          {logOpen && (
            <ul className="flex flex-col gap-2 px-4 pb-4">
              {status.runs.map((run, i) => (
                <RunRow key={i} run={run} />
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  )
}

function collectorSummaryLine(status: CollectorStatus): string {
  if (!status.enabled) return status.disabled_reason ?? 'выключен'
  const parts: string[] = []
  const last = status.runs[0]
  if (last?.finished_at) {
    parts.push(
      `последний сбор в ${formatTime(last.finished_at)} — ${
        last.status === 'ok' ? `+${last.inserted} новых, ${last.duplicates} дублей` : `ошибка: ${last.error}`
      }`,
    )
  } else {
    parts.push('сбор ещё не выполнялся')
  }
  if (status.next_run_at) {
    parts.push(`следующий ${formatRelativeFuture(status.next_run_at)}`)
  }
  return parts.join(' · ')
}

function RunRow({ run }: { run: CollectorRun }) {
  const [open, setOpen] = useState(false)
  return (
    <li className="rounded-md border border-border/60 text-xs">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left"
      >
        <ChevronRight className={cn('h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')} />
        <span className="text-muted-foreground">{formatTime(run.started_at)}</span>
        <span className="text-muted-foreground">· {triggerLabel(run.trigger)}</span>
        <span className={run.status === 'error' ? 'text-destructive' : 'text-foreground'}>
          {run.status === 'running'
            ? 'выполняется…'
            : run.status === 'error'
              ? `ошибка: ${run.error}`
              : `+${run.inserted} новых, ${run.duplicates} дублей из ${run.fetched}`}
        </span>
      </button>
      {open && (
        <ul className="flex flex-col gap-1 border-t border-border/60 px-2.5 py-2">
          {run.steps.map((step, i) => (
            <li key={i} className="flex items-start gap-1.5">
              {step.ok ? (
                <CheckCircle2 className="mt-0.5 h-3 w-3 shrink-0 text-emerald-600 dark:text-emerald-400" />
              ) : (
                <AlertCircle className="mt-0.5 h-3 w-3 shrink-0 text-destructive" />
              )}
              <span className="font-mono text-muted-foreground">
                {step.server} › {step.tool}
              </span>
              {step.detail && <span className="text-muted-foreground">({step.detail})</span>}
              <span className={step.ok ? 'text-foreground' : 'text-destructive'}>
                {step.ok ? step.summary : step.error}
              </span>
              <span className="ml-auto shrink-0 text-muted-foreground">{step.duration_ms} мс</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

function triggerLabel(trigger: CollectorRun['trigger']): string {
  switch (trigger) {
    case 'startup':
      return 'при запуске'
    case 'manual':
      return 'вручную'
    default:
      return 'по расписанию'
  }
}

const PERIODS: { value: ActivityPeriod; label: string }[] = [
  { value: 'today', label: 'Сегодня' },
  { value: '7d', label: '7 дней' },
  { value: '30d', label: '30 дней' },
]

function PeriodTabs({ period, onChange }: { period: ActivityPeriod; onChange: (p: ActivityPeriod) => void }) {
  return (
    <div className="flex gap-1 rounded-md bg-muted p-0.5">
      {PERIODS.map((p) => (
        <button
          key={p.value}
          type="button"
          onClick={() => onChange(p.value)}
          className={cn(
            'rounded px-2.5 py-1 text-xs font-medium transition-colors',
            period === p.value ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {p.label}
        </button>
      ))}
    </div>
  )
}

function DigestSummary({ digest }: { digest: ActivityDigest }) {
  if (digest.total === 0) {
    return (
      <div className="p-4 text-sm text-muted-foreground">
        Событий не найдено. {!digest.covered && digest.warnings?.join(' ')}
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-4 p-4">
      {digest.warnings && digest.warnings.length > 0 && (
        <ul className="list-disc pl-4 text-xs text-warning">
          {digest.warnings.map((w, i) => (
            <li key={i}>{w}</li>
          ))}
        </ul>
      )}
      <div className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
        <Kpi label="Всего событий" value={String(digest.total)} />
        <Kpi label="Активных дней" value={String(digest.active_days)} />
        <Kpi label="Встреч" value={String(digest.meetings_count)} />
        <Kpi label="Репозиториев" value={String(digest.by_repo.length)} />
        <Kpi
          label="Период"
          value={`${formatDate(digest.from)} – ${formatDate(digest.to)}`}
        />
      </div>

      {/* meeting already has its own KPI above, and no repository to group
          under below — left out of this per-kind breakdown to avoid saying
          the same count twice. */}
      <div className="flex flex-wrap gap-1.5">
        {(Object.keys(KIND_META) as ActivityKind[])
          .filter((k) => k !== 'meeting' && (digest.counts[k] ?? 0) > 0)
          .map((k) => (
            <span key={k} className="rounded-full bg-muted px-2.5 py-1 text-xs text-foreground">
              {KIND_META[k].label}: {digest.counts[k]}
            </span>
          ))}
      </div>

      <div>
        <h3 className="mb-1.5 text-xs font-medium text-muted-foreground">По репозиториям</h3>
        <ul className="flex flex-col gap-1">
          {digest.by_repo.slice(0, 8).map((r) => (
            <li key={r.repo} className="flex items-center gap-2 text-sm">
              <span className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">{r.repo}</span>
              <span className="text-muted-foreground">{r.total}</span>
            </li>
          ))}
        </ul>
      </div>

      <div>
        <h3 className="mb-1.5 text-xs font-medium text-muted-foreground">По дням</h3>
        <ul className="flex flex-col gap-0.5">
          {digest.by_day.map((d) => (
            <li key={d.date} className="flex items-center gap-2 text-sm">
              <span className="w-24 shrink-0 font-mono text-xs text-muted-foreground">
                {formatDate(d.date)} · {d.weekday}
              </span>
              <span className={d.total > 0 ? 'text-foreground' : 'text-muted-foreground'}>{d.total || '—'}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate font-medium text-foreground">{value}</dd>
    </div>
  )
}

function EventFeed({ result }: { result: ActivityEventsResult }) {
  if (result.events.length === 0) {
    return <div className="p-4 text-sm text-muted-foreground">Событий не найдено</div>
  }
  return (
    <>
      <ul className="flex max-h-96 flex-col gap-1 overflow-y-auto p-4">
        {result.events.map((event) => {
          const meta = KIND_META[event.kind]
          const Icon = meta?.icon ?? Clock
          return (
            <li key={`${event.source}:${event.id}`} className="flex items-start gap-1.5 text-xs">
              <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="shrink-0 font-mono text-muted-foreground">{formatDateTime(event.occurred_at)}</span>
              <span className="min-w-0">
                <a href={event.url} target="_blank" rel="noreferrer" className="text-foreground underline-offset-2 hover:underline">
                  {event.title}
                </a>
                <span className="text-muted-foreground"> · {event.repo.split('/').pop()}</span>
              </span>
            </li>
          )
        })}
      </ul>
      {result.truncated && (
        <p className="border-t border-border px-4 py-2 text-xs text-muted-foreground">
          Показаны не все события ({result.events.length} из {result.total}) — сузьте период или фильтр.
        </p>
      )}
    </>
  )
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
}

function formatDateTime(iso: string): string {
  return formatTime(iso)
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
}

function formatRelativeFuture(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now()
  if (ms <= 0) return 'сейчас'
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `через ${seconds} ${pluralRu(seconds, 'секунду', 'секунды', 'секунд')}`
  const minutes = Math.round(ms / 60000)
  if (minutes < 60) return `через ${minutes} мин`
  const hours = Math.round(minutes / 60)
  return `через ${hours} ч`
}

function pluralRu(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10
  const mod100 = n % 100
  if (mod10 === 1 && mod100 !== 11) return one
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few
  return many
}
