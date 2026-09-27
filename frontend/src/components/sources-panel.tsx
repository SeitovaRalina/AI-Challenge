import { useEffect, useMemo, useState } from 'react'
import { AlertCircle, ChevronRight, Loader2, Plug, Search } from 'lucide-react'
import { cn } from 'cn'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  ApiError,
  connectMcpServer,
  listMcpServers,
  type McpConnection,
  type McpServer,
  type McpTool,
} from '@/lib/api'

// SourcesPanel is day 16's «Источники» screen: the public MCP servers this
// instance knows about, a manual connect (initialize + tools/list), and the
// tools each server exposes. Nothing here calls a tool.
export function SourcesPanel() {
  const [servers, setServers] = useState<McpServer[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [connecting, setConnecting] = useState<Set<string>>(new Set())

  useEffect(() => {
    listMcpServers()
      .then(setServers)
      .catch((err) =>
        setLoadError(err instanceof ApiError ? err.message : 'Не удалось загрузить список источников'),
      )
  }, [])

  async function handleConnect(id: string) {
    setConnecting((prev) => new Set(prev).add(id))
    try {
      const updated = await connectMcpServer(id)
      setServers((prev) => prev?.map((s) => (s.id === id ? updated : s)) ?? [updated])
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.message : 'Не удалось подключиться')
    } finally {
      setConnecting((prev) => {
        const next = new Set(prev)
        next.delete(id)
        return next
      })
    }
  }

  if (loadError && !servers) {
    return (
      <Alert variant="destructive" className="max-w-3xl">
        <AlertCircle />
        <AlertTitle>{loadError}</AlertTitle>
      </Alert>
    )
  }
  if (!servers) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Загрузка…
      </div>
    )
  }

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      {servers.map((server) => (
        <ServerCard
          key={server.id}
          server={server}
          isConnecting={connecting.has(server.id)}
          onConnect={() => handleConnect(server.id)}
        />
      ))}
    </div>
  )
}

function ServerCard({
  server,
  isConnecting,
  onConnect,
}: {
  server: McpServer
  isConnecting: boolean
  onConnect: () => void
}) {
  const conn = server.last_connection
  return (
    <section className="rounded-lg border border-border bg-card">
      <div className="flex items-start gap-3 p-4">
        <span className="mt-0.5 flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <Plug className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-medium text-foreground">{server.name}</h2>
            <StatusBadge conn={conn} isConnecting={isConnecting} />
            {server.read_only && <Badge variant="outline">только чтение</Badge>}
          </div>
          <p className="mt-1 truncate font-mono text-xs text-muted-foreground">{server.url}</p>
          {server.token_env && (
            <p className="mt-1 text-xs text-muted-foreground">
              Токен <span className="font-mono">{server.token_env}</span>:{' '}
              {server.token_set ? 'задан' : 'не задан'}
            </p>
          )}
        </div>
        <Button variant={conn ? 'outline' : 'default'} onClick={onConnect} disabled={isConnecting}>
          {isConnecting && <Loader2 className="animate-spin" />}
          {conn ? 'Обновить' : 'Подключить'}
        </Button>
      </div>

      {conn?.status === 'error' && (
        <div className="border-t border-border p-4">
          <Alert variant="destructive">
            <AlertCircle />
            <AlertTitle>{errorTitle(conn, server)}</AlertTitle>
            {conn.error && (
              <AlertDescription className="font-mono text-xs break-all">{conn.error}</AlertDescription>
            )}
          </Alert>
        </div>
      )}

      {conn?.status === 'connected' && conn.result && (
        <>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-3 border-t border-border p-4 text-sm sm:grid-cols-4">
            <Fact label="Сервер" value={conn.result.server_name} hint={conn.result.server_version} />
            <Fact label="Протокол MCP" value={conn.result.protocol_version} />
            <Fact label="Инструментов" value={String(conn.result.tools.length)} />
            <Fact label="Подключение" value={`${conn.result.duration_ms} мс`} hint={formatTime(conn.checked_at)} />
          </dl>
          <ToolList tools={conn.result.tools} />
        </>
      )}
    </section>
  )
}

function StatusBadge({ conn, isConnecting }: { conn?: McpConnection; isConnecting: boolean }) {
  if (isConnecting) return <Badge variant="secondary">подключение…</Badge>
  if (!conn) return <Badge variant="secondary">не подключался</Badge>
  if (conn.status === 'error') return <Badge variant="destructive">ошибка</Badge>
  return (
    <Badge className="bg-emerald-500/15 text-emerald-700 dark:text-emerald-400">подключено</Badge>
  )
}

function errorTitle(conn: McpConnection, server: McpServer): string {
  const env = server.token_env ?? 'токен'
  switch (conn.error_kind) {
    case 'no_token':
      return `${env} не задан в backend/.env`
    case 'unauthorized':
      return `Сервер отклонил токен${conn.error_status ? ` (${conn.error_status})` : ''} — проверьте ${env}`
    case 'unreachable':
      return 'Сервер недоступен — проверьте URL и сеть'
    case 'timeout':
      return 'Сервер не ответил вовремя'
    default:
      return 'Ошибка протокола MCP'
  }
}

function Fact({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate font-medium text-foreground" title={value}>
        {value}
      </dd>
      {hint && (
        <dd className="truncate text-xs text-muted-foreground" title={hint}>
          {hint}
        </dd>
      )}
    </div>
  )
}

function ToolList({ tools }: { tools: McpTool[] }) {
  const [query, setQuery] = useState('')
  const [expanded, setExpanded] = useState<string | null>(null)
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return tools
    return tools.filter(
      (t) => t.name.toLowerCase().includes(q) || (t.description ?? '').toLowerCase().includes(q),
    )
  }, [tools, query])

  return (
    <div className="border-t border-border">
      <div className="flex items-center gap-2 px-4 py-3">
        <Search className="h-4 w-4 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Поиск по инструментам"
          className="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
        />
        <span className="text-xs text-muted-foreground">
          {filtered.length} из {tools.length}
        </span>
      </div>
      <ul className="divide-y divide-border border-t border-border">
        {filtered.map((tool) => {
          const open = expanded === tool.name
          return (
            <li key={tool.name}>
              <button
                type="button"
                onClick={() => setExpanded(open ? null : tool.name)}
                className="flex w-full items-start gap-2 px-4 py-2.5 text-left transition-colors hover:bg-accent/50"
              >
                <ChevronRight
                  className={cn(
                    'mt-0.5 h-4 w-4 flex-shrink-0 text-muted-foreground transition-transform',
                    open && 'rotate-90',
                  )}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-sm text-foreground">{tool.name}</span>
                    {tool.read_only && <Badge variant="outline">read-only</Badge>}
                    <span className="text-xs text-muted-foreground">
                      {tool.params.length} {paramsWord(tool.params.length)}
                    </span>
                  </div>
                  {!open && tool.description && (
                    <p className="mt-0.5 truncate text-xs text-muted-foreground">
                      {tool.description.split('\n')[0]}
                    </p>
                  )}
                </div>
              </button>
              {open && <ToolDetails tool={tool} />}
            </li>
          )
        })}
        {filtered.length === 0 && (
          <li className="px-4 py-3 text-sm text-muted-foreground">Ничего не найдено</li>
        )}
      </ul>
    </div>
  )
}

function ToolDetails({ tool }: { tool: McpTool }) {
  return (
    <div className="px-10 pb-4">
      {tool.title && <p className="text-sm font-medium text-foreground">{tool.title}</p>}
      {tool.description && (
        <p className="mt-1 text-sm whitespace-pre-line text-muted-foreground">{tool.description}</p>
      )}
      {tool.params.length === 0 ? (
        <p className="mt-3 text-xs text-muted-foreground">Без параметров</p>
      ) : (
        <table className="mt-3 w-full text-left text-xs">
          <thead className="text-muted-foreground">
            <tr className="border-b border-border">
              <th className="py-1.5 pr-3 font-medium">Параметр</th>
              <th className="py-1.5 pr-3 font-medium">Тип</th>
              <th className="py-1.5 font-medium">Описание</th>
            </tr>
          </thead>
          <tbody>
            {tool.params.map((p) => (
              <tr key={p.name} className="border-b border-border/60 align-top last:border-0">
                <td className="py-1.5 pr-3 font-mono whitespace-nowrap text-foreground">
                  {p.name}
                  {p.required && <span className="text-destructive">*</span>}
                </td>
                <td className="py-1.5 pr-3 font-mono whitespace-nowrap text-muted-foreground">
                  {p.type || '—'}
                </td>
                <td className="py-1.5 text-muted-foreground">{p.description || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

function paramsWord(n: number): string {
  const mod10 = n % 10
  const mod100 = n % 100
  if (mod10 === 1 && mod100 !== 11) return 'параметр'
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return 'параметра'
  return 'параметров'
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
