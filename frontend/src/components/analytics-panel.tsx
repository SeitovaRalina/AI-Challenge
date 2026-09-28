import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertCircle, ChevronRight, Loader2 } from 'lucide-react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from 'recharts'
import { cn } from 'cn'

import { Alert, AlertTitle } from '@/components/ui/alert'
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import {
  ApiError,
  getAnalytics,
  listRepoProjects,
  setRepoProject,
  type Analytics,
  type AnalyticsPeriod,
  type RepoProject,
  type SessionCategory,
} from '@/lib/api'

// AnalyticsPanel is day 19's «Аналитика» screen: everything the composition
// pipeline (list_events -> build_sessions -> save_sessions, run
// automatically after every collection — see «Активность») produced,
// rendered as charts. All numbers come from worklog.get_analytics; nothing
// here is computed client-side beyond chart layout.

const CATEGORY_META: Record<SessionCategory, { label: string; color: string }> = {
  development: { label: 'Разработка', color: 'var(--color-chart-1)' },
  review: { label: 'Code review', color: 'var(--color-chart-2)' },
  other: { label: 'Прочее', color: 'var(--color-chart-3)' },
}

const CHART_CONFIG: ChartConfig = {
  development_hours: { label: 'Разработка', color: 'var(--color-chart-1)' },
  review_hours: { label: 'Code review', color: 'var(--color-chart-2)' },
  other_hours: { label: 'Прочее', color: 'var(--color-chart-3)' },
  hours: { label: 'Часы', color: 'var(--color-chart-1)' },
}

const PERIODS: { value: AnalyticsPeriod; label: string }[] = [
  { value: '7d', label: '7 дней' },
  { value: '30d', label: '30 дней' },
  { value: '90d', label: '90 дней' },
]

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

          <ChartCard title="Время по проекту">
            <ChartContainer config={CHART_CONFIG} className="aspect-auto h-[220px] w-full">
              <BarChart data={data.time_by_project} layout="vertical" margin={{ left: 8 }}>
                <CartesianGrid horizontal={false} />
                <XAxis type="number" dataKey="hours" hide />
                <YAxis type="category" dataKey="project" width={140} tickLine={false} axisLine={false} />
                <ChartTooltip content={<ChartTooltipContent hideLabel formatter={(v) => `${v} ч`} />} />
                <Bar dataKey="hours" fill="var(--color-chart-1)" radius={4} />
              </BarChart>
            </ChartContainer>
          </ChartCard>

          <ChartCard title="По дням: разработка / review">
            <ChartContainer config={CHART_CONFIG} className="aspect-auto h-[220px] w-full">
              <BarChart data={data.by_day}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="date" tickFormatter={formatDayTick} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                <YAxis tickLine={false} axisLine={false} width={28} />
                <ChartTooltip content={<ChartTooltipContent labelFormatter={(v) => formatDayTick(String(v))} />} />
                <Bar dataKey="development_hours" stackId="a" fill="var(--color-chart-1)" radius={[0, 0, 4, 4]} />
                <Bar dataKey="review_hours" stackId="a" fill="var(--color-chart-2)" />
                <Bar dataKey="other_hours" stackId="a" fill="var(--color-chart-3)" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ChartContainer>
          </ChartCard>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <ChartCard title="Состав активности">
              <ChartContainer config={CHART_CONFIG} className="mx-auto aspect-square h-[220px]">
                <PieChart>
                  <ChartTooltip content={<ChartTooltipContent hideLabel formatter={(v) => `${v} ч`} />} />
                  <Pie data={data.composition} dataKey="hours" nameKey="category" innerRadius={50} outerRadius={80} strokeWidth={2}>
                    {data.composition.map((c) => (
                      <Cell key={c.category} fill={CATEGORY_META[c.category].color} />
                    ))}
                  </Pie>
                </PieChart>
              </ChartContainer>
              <CompositionLegend composition={data.composition} />
            </ChartCard>

            <ChartCard title="Недельный тренд">
              <ChartContainer config={CHART_CONFIG} className="aspect-auto h-[220px] w-full">
                <LineChart data={data.weekly_trend}>
                  <CartesianGrid vertical={false} />
                  <XAxis dataKey="week_start" tickFormatter={formatDayTick} tickLine={false} axisLine={false} />
                  <YAxis tickLine={false} axisLine={false} width={28} />
                  <ChartTooltip
                    content={<ChartTooltipContent labelFormatter={(v) => formatDayTick(String(v))} formatter={(v) => `${v} ч`} />}
                  />
                  <Line type="monotone" dataKey="hours" stroke="var(--color-chart-1)" strokeWidth={2} dot={{ r: 3 }} />
                </LineChart>
              </ChartContainer>
              <p className="mt-1 text-center text-xs text-muted-foreground">Последние 8 недель, независимо от выбранного периода</p>
            </ChartCard>
          </div>

          <ChartCard title="Активность по часам и дням недели">
            <Heatmap heatmap={data.heatmap} />
          </ChartCard>

          <p className="text-xs text-muted-foreground">
            Сессия — блок событий не дальше 45 минут друг от друга, начинается за 30 минут до первого события. Категория и
            репозиторий сессии — по большинству её событий. Проект — по вашей привязке репозитория ниже; без привязки —
            «Без проекта». Это не оценка продуктивности, только подсчёт часов и событий.
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
      <Kpi label="Всего часов" value={formatHours(kpi.total_hours)} />
      <Kpi label="Разработка" value={formatHours(kpi.development_hours)} />
      <Kpi label="Code review" value={formatHours(kpi.review_hours)} />
      <Kpi label="Активных дней" value={String(kpi.active_days)} />
      <Kpi label="Сессий" value={String(kpi.sessions_count)} />
      <Kpi label="Репозиториев" value={String(kpi.repo_count)} />
      <Kpi label="Проектов" value={String(kpi.project_count)} />
    </div>
  )
}

function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 text-lg font-medium text-foreground">{value}</dd>
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

function CompositionLegend({ composition }: { composition: Analytics['composition'] }) {
  const total = composition.reduce((sum, c) => sum + c.hours, 0)
  return (
    <ul className="mt-2 flex flex-col gap-1 text-xs">
      {composition
        .filter((c) => c.hours > 0)
        .map((c) => (
          <li key={c.category} className="flex items-center gap-1.5">
            <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: CATEGORY_META[c.category].color }} />
            <span className="text-muted-foreground">{CATEGORY_META[c.category].label}</span>
            <span className="ml-auto text-foreground">
              {formatHours(c.hours)} · {total > 0 ? Math.round((c.hours / total) * 100) : 0}%
            </span>
          </li>
        ))}
    </ul>
  )
}

// Monday-first display order over data indexed Go/JS-style (0 = Sunday).
const WEEKDAY_DISPLAY_ORDER = [1, 2, 3, 4, 5, 6, 0]
const HOURS = Array.from({ length: 24 }, (_, h) => h)

function Heatmap({ heatmap }: { heatmap: Analytics['heatmap'] }) {
  const byCell = useMemo(() => {
    const m = new Map<string, number>()
    for (const cell of heatmap) m.set(`${cell.weekday}-${cell.hour}`, cell.minutes)
    return m
  }, [heatmap])
  const maxMinutes = useMemo(() => Math.max(1, ...heatmap.map((c) => c.minutes)), [heatmap])
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
              const minutes = byCell.get(`${wd}-${h}`) ?? 0
              const intensity = minutes / maxMinutes
              return (
                <div
                  key={h}
                  title={`${labelByWeekday.get(wd) ?? ''} ${h}:00 — ${minutes} мин`}
                  className="aspect-square rounded-sm"
                  style={{ backgroundColor: intensity > 0 ? `color-mix(in oklch, var(--color-chart-1) ${Math.round(intensity * 100)}%, transparent)` : 'var(--muted)' }}
                />
              )
            })}
          </div>
        ))}
      </div>
    </div>
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
        Репозитории и проекты
        <span className="ml-auto text-xs font-normal text-muted-foreground">
          {repos.filter((r) => r.project).length} из {repos.length} привязано
        </span>
      </button>
      {open && (
        <ul className="divide-y divide-border border-t border-border">
          {repos.map((r) => (
            <RepoProjectRow key={r.repo} repo={r} onSet={onSet} />
          ))}
        </ul>
      )}
    </section>
  )
}

function RepoProjectRow({ repo, onSet }: { repo: RepoProject; onSet: (repo: string, project: string) => Promise<void> }) {
  const [value, setValue] = useState(repo.project ?? '')
  const [saving, setSaving] = useState(false)

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
        placeholder="Без проекта"
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
