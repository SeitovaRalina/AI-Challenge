import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertCircle, ChevronLeft, ChevronRight, Loader2, RefreshCw } from 'lucide-react'
import { Bar, BarChart, CartesianGrid, Cell, Line, LineChart, XAxis, YAxis } from 'recharts'
import { cn } from 'cn'

import { Alert, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import {
  ApiError,
  generateWeeklySummary,
  getAnalytics,
  getDayTimeline,
  getWeeklySummary,
  listRepoProjects,
  setRepoProject,
  type Analytics,
  type AnalyticsPeriod,
  type DayTimeline,
  type RepoProject,
  type TimelineBlock,
  type WeeklySummary,
} from '@/lib/api'

// AnalyticsPanel is day 19's «Аналитика» screen, extended in day 20 with
// meetings (from the Calendar MCP server, via mcp-worklog's meeting
// sessions): everything the composition pipeline (list_events ->
// build_sessions -> save_sessions, run automatically after every collection
// — see «Активность») produced, rendered as charts. All numbers come from
// worklog.get_analytics/get_day_timeline; nothing here is computed
// client-side beyond chart layout.
//
// Colors are deliberately real (not the app's neutral grayscale tokens):
// on a screen whose whole point is telling projects, commit types and
// meetings apart at a glance, color is the mechanism, not decoration.

const PALETTE = ['#6366f1', '#22c55e', '#f59e0b', '#ec4899', '#06b6d4', '#8b5cf6', '#ef4444', '#14b8a6']
const HOURS_COLOR = '#6366f1'
// MEETING_COLOR is used consistently everywhere a meeting needs to read as
// "not work" at a glance: the by-day stacked bar, the day timeline, the
// meetings KPI accent.
const MEETING_COLOR = '#f59e0b'

function colorFor(index: number): string {
  return PALETTE[index % PALETTE.length]
}

const CHART_CONFIG: ChartConfig = {
  hours: { label: 'Часы', color: HOURS_COLOR },
  count: { label: 'Коммитов', color: HOURS_COLOR },
  work_hours: { label: 'Работа', color: HOURS_COLOR },
  meeting_hours: { label: 'Встречи', color: MEETING_COLOR },
}

const PERIODS: { value: AnalyticsPeriod; label: string }[] = [
  { value: '7d', label: '7 дней' },
  { value: '30d', label: '30 дней' },
  { value: '90d', label: '90 дней' },
]

function periodLabel(period: AnalyticsPeriod): string {
  return PERIODS.find((p) => p.value === period)?.label ?? period
}

export function AnalyticsPanel() {
  const [period, setPeriod] = useState<AnalyticsPeriod>('30d')
  const [data, setData] = useState<Analytics | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [repos, setRepos] = useState<RepoProject[] | null>(null)
  const [reposError, setReposError] = useState<string | null>(null)

  const load = useCallback(() => {
    getAnalytics(period)
      .then((d) => {
        setData(d)
        setError(null)
      })
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Не удалось загрузить аналитику'))
  }, [period])

  const loadRepos = useCallback(() => {
    listRepoProjects()
      .then((r) => {
        setRepos(r.repos)
        setReposError(null)
      })
      .catch((err) => setReposError(err instanceof ApiError ? err.message : 'Не удалось загрузить репозитории'))
  }, [])

  useEffect(load, [load])
  useEffect(loadRepos, [loadRepos])

  async function handleSetProject(repo: string, project: string) {
    const updated = await setRepoProject(repo, project)
    setRepos((prev) => prev?.map((r) => (r.repo === repo ? updated : r)) ?? [updated])
    load() // projects are resolved at read time — reflected immediately, no rebuild
  }

  if (error && !data) {
    return (
      <Alert variant="destructive">
        <AlertCircle />
        <AlertTitle>{error}</AlertTitle>
      </Alert>
    )
  }

  return (
    <div className="flex max-w-4xl flex-col gap-4">
      <WeeklySummaryCard />

      <div className="flex items-center justify-end">
        <PeriodTabs period={period} onChange={setPeriod} />
      </div>

      {!data ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Загрузка…
        </div>
      ) : data.kpi.sessions_count === 0 ? (
        <div className="rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">
          За этот период сессий нет. {data.warnings?.join(' ')}
        </div>
      ) : (
        <>
          <KpiRow kpi={data.kpi} />

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <ChartCard title="Время по проекту">
              <ChartContainer config={CHART_CONFIG} className="aspect-auto h-[220px] w-full">
                <BarChart data={data.time_by_project} layout="vertical" margin={{ left: 8 }}>
                  <CartesianGrid horizontal={false} />
                  <XAxis type="number" dataKey="hours" hide />
                  <YAxis type="category" dataKey="project" width={120} tickLine={false} axisLine={false} />
                  <ChartTooltip content={<ChartTooltipContent hideLabel formatter={(v) => `${v} ч`} />} />
                  <Bar dataKey="hours" radius={4}>
                    {data.time_by_project.map((p, i) => (
                      <Cell key={p.project} fill={colorFor(i)} />
                    ))}
                  </Bar>
                </BarChart>
              </ChartContainer>
            </ChartCard>

            <ChartCard title="Типы коммитов">
              <ChartContainer config={CHART_CONFIG} className="aspect-auto h-[220px] w-full">
                <BarChart data={data.commit_types} layout="vertical" margin={{ left: 8 }}>
                  <CartesianGrid horizontal={false} />
                  <XAxis type="number" dataKey="count" hide />
                  <YAxis type="category" dataKey="type" width={100} tickLine={false} axisLine={false} />
                  <ChartTooltip content={<ChartTooltipContent hideLabel formatter={(v) => `${v}`} />} />
                  <Bar dataKey="count" radius={4}>
                    {data.commit_types.map((c, i) => (
                      <Cell key={c.type} fill={colorFor(i)} />
                    ))}
                  </Bar>
                </BarChart>
              </ChartContainer>
            </ChartCard>
          </div>

          <ChartCard title="Часы по дням">
            <ChartContainer config={CHART_CONFIG} className="aspect-auto h-[200px] w-full">
              <BarChart data={data.by_day}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="date" tickFormatter={formatDayTick} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                <YAxis tickLine={false} axisLine={false} width={28} />
                <ChartTooltip
                  content={<ChartTooltipContent labelFormatter={(v) => formatDayTick(String(v))} formatter={(v) => `${v} ч`} />}
                />
                <Bar dataKey="work_hours" stackId="hours" fill={HOURS_COLOR} radius={[0, 0, 4, 4]} />
                <Bar dataKey="meeting_hours" stackId="hours" fill={MEETING_COLOR} radius={[4, 4, 0, 0]} />
              </BarChart>
            </ChartContainer>
            <p className="mt-1 text-center text-xs text-muted-foreground">
              <span style={{ color: HOURS_COLOR }}>Работа</span> + <span style={{ color: MEETING_COLOR }}>встречи</span>; время встречи
              никогда не засчитывается ещё и как работа
            </p>
          </ChartCard>

          <ChartCard title="Таймлайн дня">
            <DayTimelineView />
          </ChartCard>

          <ChartCard title="Недельный тренд">
            <ChartContainer config={CHART_CONFIG} className="aspect-auto h-[200px] w-full">
              <LineChart data={data.weekly_trend}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="week_start" tickFormatter={formatDayTick} tickLine={false} axisLine={false} />
                <YAxis tickLine={false} axisLine={false} width={28} />
                <ChartTooltip
                  content={<ChartTooltipContent labelFormatter={(v) => formatDayTick(String(v))} formatter={(v) => `${v} ч`} />}
                />
                <Line type="monotone" dataKey="hours" stroke={HOURS_COLOR} strokeWidth={2} dot={{ r: 3, fill: HOURS_COLOR }} />
              </LineChart>
            </ChartContainer>
            <p className="mt-1 text-center text-xs text-muted-foreground">Последние 8 недель, независимо от выбранного периода</p>
          </ChartCard>

          <ChartCard title="Активность по часам и дням недели">
            <Heatmap heatmap={data.heatmap} />
            <p className="mt-1 text-center text-xs text-muted-foreground">
              Среднее число минут работы в этот час — по всем таким дням недели за период ({periodLabel(period)})
            </p>
          </ChartCard>

          <p className="text-xs text-muted-foreground">
            Сессия — блок событий в одном репозитории не дальше 45 минут друг от друга, начинается за 30 минут до первого
            события. Работа сразу в нескольких репозиториях в одном окне времени даёт по сессии на каждый — ни один не
            теряется. Проект по умолчанию — имя самого репозитория; объединить несколько репозиториев под одной меткой можно
            ниже. Это не оценка продуктивности, только подсчёт часов и коммитов.
          </p>
        </>
      )}

      <RepoProjectMapping repos={repos} error={reposError} onSet={handleSetProject} />
    </div>
  )
}

function PeriodTabs({ period, onChange }: { period: AnalyticsPeriod; onChange: (p: AnalyticsPeriod) => void }) {
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

function KpiRow({ kpi }: { kpi: Analytics['kpi'] }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      <Kpi label="Всего часов" value={formatHours(kpi.total_hours)} accentColor={HOURS_COLOR} />
      <Kpi label="Часов на встречах" value={formatHours(kpi.meeting_hours)} accentColor={MEETING_COLOR} />
      <Kpi label="Активных дней" value={String(kpi.active_days)} />
      <Kpi label="Рабочих сессий" value={String(kpi.sessions_count)} />
      <Kpi label="Встреч" value={String(kpi.meetings_count)} />
      <Kpi label="Репозиториев" value={String(kpi.repo_count)} />
      <Kpi label="Проектов" value={String(kpi.project_count)} />
    </div>
  )
}

function Kpi({ label, value, accentColor }: { label: string; value: string; accentColor?: string }) {
  return (
    <div
      className={cn('rounded-lg border p-3', accentColor ? 'border-transparent' : 'border-border bg-card')}
      style={accentColor ? { backgroundColor: `color-mix(in oklch, ${accentColor} 12%, transparent)` } : undefined}
    >
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={cn('mt-0.5 text-lg font-medium', accentColor ? '' : 'text-foreground')} style={accentColor ? { color: accentColor } : undefined}>
        {value}
      </dd>
    </div>
  )
}

function ChartCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <h3 className="mb-2 text-sm font-medium text-foreground">{title}</h3>
      {children}
    </section>
  )
}

// Monday-first display order over data indexed Go/JS-style (0 = Sunday).
const WEEKDAY_DISPLAY_ORDER = [1, 2, 3, 4, 5, 6, 0]
const HOURS = Array.from({ length: 24 }, (_, h) => h)

// A cell is an average per occurrence of that weekday (see Analytics['heatmap']
// doc), so it's naturally bounded to [0, 60] — the intensity scale can be
// fixed against that ceiling instead of the current view's own max, which
// keeps the same color meaning the same across different periods.
const HEATMAP_MAX_MINUTES = 60

function Heatmap({ heatmap }: { heatmap: Analytics['heatmap'] }) {
  const byCell = useMemo(() => {
    const m = new Map<string, number>()
    for (const cell of heatmap) m.set(`${cell.weekday}-${cell.hour}`, cell.avg_minutes)
    return m
  }, [heatmap])
  const labelByWeekday = useMemo(() => {
    const m = new Map<number, string>()
    for (const cell of heatmap) m.set(cell.weekday, cell.weekday_label)
    return m
  }, [heatmap])

  return (
    <div className="overflow-x-auto">
      <div className="inline-grid min-w-full gap-0.5" style={{ gridTemplateColumns: '2rem repeat(24, minmax(1rem, 1fr))' }}>
        <div />
        {HOURS.map((h) => (
          <div key={h} className="text-center text-[9px] text-muted-foreground">
            {h % 3 === 0 ? h : ''}
          </div>
        ))}
        {WEEKDAY_DISPLAY_ORDER.map((wd) => (
          <div key={wd} className="contents">
            <div className="flex items-center text-xs text-muted-foreground">{labelByWeekday.get(wd) ?? ''}</div>
            {HOURS.map((h) => {
              const avgMinutes = byCell.get(`${wd}-${h}`) ?? 0
              const intensity = avgMinutes / HEATMAP_MAX_MINUTES
              return (
                <div
                  key={h}
                  title={`${labelByWeekday.get(wd) ?? ''} ${h}:00 — в среднем ${avgMinutes} мин`}
                  className="aspect-square rounded-sm"
                  style={{
                    backgroundColor:
                      intensity > 0 ? `color-mix(in oklch, ${HOURS_COLOR} ${Math.round(10 + intensity * 90)}%, transparent)` : 'var(--muted)',
                  }}
                />
              )
            })}
          </div>
        ))}
      </div>
    </div>
  )
}

// DayTimelineView is day 20's Gantt-style day view: one day, laid out as
// non-overlapping work/meeting blocks straight from get_day_timeline —
// nothing computed here beyond pixel layout.
function DayTimelineView() {
  const [date, setDate] = useState(() => todayISO())
  const [timeline, setTimeline] = useState<DayTimeline | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setTimeline(null)
    getDayTimeline(date)
      .then((t) => {
        setTimeline(t)
        setError(null)
      })
      .catch((err) => setError(err instanceof ApiError ? err.message : 'Не удалось загрузить таймлайн'))
  }, [date])

  function shiftDay(delta: number) {
    const d = new Date(date + 'T00:00:00')
    d.setDate(d.getDate() + delta)
    setDate(toLocalISODate(d))
  }

  return (
    <div>
      <div className="mb-2 flex items-center justify-center gap-2">
        <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => shiftDay(-1)}>
          <ChevronLeft className="h-3.5 w-3.5" />
        </Button>
        <span className="min-w-[9rem] text-center text-xs text-muted-foreground">
          {formatDayTick(date)} {timeline ? `· ${timeline.weekday}` : ''}
        </span>
        <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => shiftDay(1)} disabled={date >= todayISO()}>
          <ChevronRight className="h-3.5 w-3.5" />
        </Button>
      </div>

      {error ? (
        <p className="text-center text-xs text-destructive">{error}</p>
      ) : !timeline ? (
        <div className="flex items-center justify-center gap-2 py-6 text-xs text-muted-foreground">
          <Loader2 className="h-3.5 w-3.5 animate-spin" /> Загрузка…
        </div>
      ) : timeline.blocks.length === 0 ? (
        <p className="py-6 text-center text-xs text-muted-foreground">За этот день ничего не собрано.</p>
      ) : (
        <DayTimelineBar blocks={timeline.blocks} />
      )}
    </div>
  )
}

function DayTimelineBar({ blocks }: { blocks: TimelineBlock[] }) {
  const dayStart = new Date(blocks[0].start)
  dayStart.setHours(0, 0, 0, 0)
  const dayMs = 24 * 60 * 60 * 1000
  const projectColor = useMemo(() => {
    const projects = Array.from(new Set(blocks.filter((b) => b.kind === 'work').map((b) => b.project ?? b.repo ?? '')))
    return new Map(projects.map((p, i) => [p, colorFor(i)]))
  }, [blocks])

  return (
    <div>
      <div className="relative h-8 w-full overflow-hidden rounded-md bg-muted">
        {blocks.map((b, i) => {
          const start = new Date(b.start).getTime() - dayStart.getTime()
          const end = new Date(b.end).getTime() - dayStart.getTime()
          const left = (Math.max(0, start) / dayMs) * 100
          const width = Math.max(0.4, ((Math.min(dayMs, end) - Math.max(0, start)) / dayMs) * 100)
          const color = b.kind === 'meeting' ? MEETING_COLOR : projectColor.get(b.project ?? b.repo ?? '') ?? HOURS_COLOR
          const label =
            b.kind === 'meeting'
              ? (b.titles ?? []).join(', ') || 'Встреча'
              : (b.project ?? b.repo ?? 'Работа')
          return (
            <div
              key={i}
              title={`${formatTimeOfDay(b.start)}–${formatTimeOfDay(b.end)} · ${label}`}
              className="absolute top-0 h-full"
              style={{ left: `${left}%`, width: `${width}%`, backgroundColor: color }}
            />
          )
        })}
      </div>
      <div className="mt-1 flex justify-between text-[10px] text-muted-foreground">
        <span>00:00</span>
        <span>12:00</span>
        <span>24:00</span>
      </div>
    </div>
  )
}

function formatTimeOfDay(iso: string): string {
  return new Date(iso).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
}

// toLocalISODate formats d as YYYY-MM-DD from its LOCAL calendar fields —
// never via toISOString(), which converts to UTC first and silently shifts
// the date for any non-zero timezone offset (the real bug behind "can't
// move the timeline forward": for UTC+6, local midnight of tomorrow is
// still today in UTC, so toISOString() kept returning the same date).
function toLocalISODate(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function todayISO(): string {
  return toLocalISODate(new Date())
}

// WeeklySummaryCard is day 20's "итоги недели": the model's own comparison
// of this week to last, written from facts fetched via get_analytics/
// get_activity_digest — see backend/weekly_summary.go. Shown collapsed to
// nothing until there's something to show (no auto-generate on mount —
// that's what the Friday-evening schedule and the button below are for).
function WeeklySummaryCard() {
  const [summary, setSummary] = useState<WeeklySummary | null | undefined>(undefined)
  const [generating, setGenerating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    getWeeklySummary()
      .then(setSummary)
      .catch(() => setSummary(null))
  }, [])

  async function handleGenerate() {
    setGenerating(true)
    setError(null)
    try {
      setSummary(await generateWeeklySummary())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Не удалось собрать сводку недели')
    } finally {
      setGenerating(false)
    }
  }

  if (summary === undefined) return null

  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <div className="flex items-start justify-between gap-3">
        <h3 className="text-sm font-medium text-foreground">Итоги недели</h3>
        <Button variant="outline" size="sm" onClick={handleGenerate} disabled={generating} className="shrink-0">
          {generating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          {summary ? 'Обновить' : 'Собрать'}
        </Button>
      </div>
      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
      {summary ? (
        <>
          <p className="mt-2 text-sm text-foreground">{summary.text}</p>
          <p className="mt-2 text-xs text-muted-foreground">
            {formatHours(summary.facts.this_week.hours)} на этой неделе против {formatHours(summary.facts.last_week.hours)} на прошлой ·
            обновлено {new Date(summary.generated_at).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })}
          </p>
        </>
      ) : (
        <p className="mt-2 text-xs text-muted-foreground">Сводка ещё не собрана — соберите сейчас или дождитесь вечера пятницы.</p>
      )}
    </section>
  )
}

function RepoProjectMapping({
  repos,
  error,
  onSet,
}: {
  repos: RepoProject[] | null
  error: string | null
  onSet: (repo: string, project: string) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertCircle />
        <AlertTitle>{error}</AlertTitle>
      </Alert>
    )
  }
  if (!repos || repos.length === 0) return null

  return (
    <section className="rounded-lg border border-border bg-card">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-4 py-3 text-left text-sm font-medium text-foreground"
      >
        <ChevronRight className={cn('h-4 w-4 text-muted-foreground transition-transform', open && 'rotate-90')} />
        Объединить репозитории в проект
        <span className="ml-auto text-xs font-normal text-muted-foreground">
          {repos.filter((r) => r.project).length} из {repos.length} объединено
        </span>
      </button>
      {open && (
        <>
          <p className="border-t border-border px-4 py-2 text-xs text-muted-foreground">
            Каждый репозиторий уже виден на графиках под своим именем. Впишите сюда одинаковую метку для нескольких
            репозиториев, чтобы считать их одним проектом — это не обязательно.
          </p>
          <ul className="divide-y divide-border border-t border-border">
            {repos.map((r) => (
              <RepoProjectRow key={r.repo} repo={r} onSet={onSet} />
            ))}
          </ul>
        </>
      )}
    </section>
  )
}

function RepoProjectRow({ repo, onSet }: { repo: RepoProject; onSet: (repo: string, project: string) => Promise<void> }) {
  const [value, setValue] = useState(repo.project ?? '')
  const [saving, setSaving] = useState(false)
  const bareName = repo.repo.split('/').pop() ?? repo.repo

  async function commit() {
    if (value === (repo.project ?? '')) return
    setSaving(true)
    try {
      await onSet(repo.repo, value)
    } finally {
      setSaving(false)
    }
  }

  return (
    <li className="flex items-center gap-3 px-4 py-2.5">
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">{repo.repo}</span>
      <input
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
        placeholder={bareName}
        className="w-40 rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground outline-none focus:border-foreground/30"
      />
      {saving && <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" />}
    </li>
  )
}

function formatHours(h: number): string {
  return `${h.toLocaleString('ru-RU', { maximumFractionDigits: 1 })} ч`
}

function formatDayTick(date: string): string {
  const d = new Date(date + 'T00:00:00')
  if (Number.isNaN(d.getTime())) return date
  return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
}
