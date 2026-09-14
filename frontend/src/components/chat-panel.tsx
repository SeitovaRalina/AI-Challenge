import { useEffect, useRef, useState } from 'react'
import { HelpCircle, Minimize2, X } from 'lucide-react'

import emptyStateGif from '@/assets/empty_state.gif'
import { ChatEstimateCard } from '@/components/chat-estimate-card'
import { Markdown } from '@/components/markdown'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { cn } from 'cn'
import type { AgentMessage, CompressionEvent, Estimate, TokenUsage } from '@/lib/api'

const EXAMPLE_TASK =
  'Обновить устаревшее Flutter-приложение до новой версии Flutter, обновить зависимости, исправить проблемы сборки под iOS и Android и подготовить новые билды.'

const COMPOSER_MAX_HEIGHT = 200
const TOKENS_COMMAND = '/tokens'
const COMPRESS_COMMAND = '/compress'

interface SlashCommand {
  name: string
  description: string
}

// Extend this list as more slash commands are added — the composer's
// autocomplete dropdown is driven entirely by it.
const SLASH_COMMANDS: SlashCommand[] = [
  { name: TOKENS_COMMAND, description: 'токены и стоимость диалога' },
  { name: COMPRESS_COMMAND, description: 'сжать историю сейчас' },
]

interface ChatPanelProps {
  messages: AgentMessage[]
  estimate: Estimate | null
  isSending: boolean
  error: string | null
  onSend: (message: string) => void
  onForceCompress: () => Promise<boolean>
  onSetCompressionEnabled: (enabled: boolean) => void
  lastContextTokens: number
  cumulativeTotalTokens: number
  cumulativeCostUsd?: number
  contextTokenLimit: number
  compressionEnabled: boolean
  historyKeepLastN: number
  summarizedMessageCount: number
  rawMessageCount: number
  compressionEvents: CompressionEvent[]
}

export function ChatPanel({
  messages,
  estimate,
  isSending,
  error,
  onSend,
  onForceCompress,
  onSetCompressionEnabled,
  lastContextTokens,
  cumulativeTotalTokens,
  cumulativeCostUsd,
  contextTokenLimit,
  compressionEnabled,
  historyKeepLastN,
  summarizedMessageCount,
  rawMessageCount,
  compressionEvents,
}: ChatPanelProps) {
  const [draft, setDraft] = useState('')
  const [tokensPopupOpen, setTokensPopupOpen] = useState(false)
  const [textareaFocused, setTextareaFocused] = useState(false)
  const [selectedSuggestion, setSelectedSuggestion] = useState(0)
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Only offer suggestions while the draft is still just the command token
  // itself (no space yet — none of today's commands take arguments).
  const suggestions =
    textareaFocused && draft.startsWith('/') && !draft.includes(' ')
      ? SLASH_COMMANDS.filter((c) => c.name.startsWith(draft))
      : []

  useEffect(() => {
    setSelectedSuggestion(0)
  }, [draft])

  function applySuggestion(command: SlashCommand) {
    setDraft(command.name)
    textareaRef.current?.focus()
  }

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [messages, isSending])

  // Grows the composer with the draft up to COMPOSER_MAX_HEIGHT, so a long
  // message stays visible while typing instead of scrolling inside a
  // single-line box; only switches on its own scrollbar once actually capped.
  useEffect(() => {
    const textarea = textareaRef.current
    if (!textarea) return
    textarea.style.height = 'auto'
    const contentHeight = textarea.scrollHeight
    const capped = contentHeight > COMPOSER_MAX_HEIGHT
    textarea.style.height = `${Math.min(contentHeight, COMPOSER_MAX_HEIGHT)}px`
    textarea.style.overflowY = capped ? 'auto' : 'hidden'
  }, [draft])

  useEffect(() => {
    if (!tokensPopupOpen) return
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') setTokensPopupOpen(false)
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [tokensPopupOpen])

  function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    const trimmed = draft.trim()
    if (!trimmed) return

    // /tokens is a local, offline command — it never reaches the LLM and
    // never touches chat state, so it must work even while a message is
    // in flight (isSending) — it's just inspecting whatever is already
    // known, not competing with the in-flight request for anything.
    if (trimmed === TOKENS_COMMAND) {
      setTokensPopupOpen(true)
      setDraft('')
      return
    }

    if (isSending) return

    // /compress is a real backend action (its own LLM call), not a chat
    // message — never appended to history, handled the same way /tokens
    // intercepts before reaching onSend. Its result shows up as an in-chat
    // compression notice (or the error banner if there was nothing to fold),
    // so it doesn't need to also force the /tokens popup open.
    if (trimmed === COMPRESS_COMMAND) {
      setDraft('')
      void onForceCompress()
      return
    }

    onSend(trimmed)
    setDraft('')
  }

  function handleKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (suggestions.length > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault()
        setSelectedSuggestion((i) => (i + 1) % suggestions.length)
        return
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault()
        setSelectedSuggestion((i) => (i - 1 + suggestions.length) % suggestions.length)
        return
      }
      if (event.key === 'Escape') {
        event.preventDefault()
        setDraft('')
        return
      }
      if (event.key === 'Tab') {
        event.preventDefault()
        applySuggestion(suggestions[selectedSuggestion])
        return
      }
      if (event.key === 'Enter' && !event.shiftKey && draft !== suggestions[selectedSuggestion].name) {
        event.preventDefault()
        applySuggestion(suggestions[selectedSuggestion])
        return
      }
    }

    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      handleSubmit(event)
    }
  }

  const lastUsage = [...messages].reverse().find((m) => m.usage)?.usage

  // Each compression event anchors to the exact Messages index it folded up
  // to (fold_end), so its notice always renders at the point in history
  // where the fold actually happened — stable across reloads, since
  // Messages itself is never reordered or trimmed.
  const eventsBeforeIndex = new Map<number, CompressionEvent[]>()
  for (const event of compressionEvents) {
    const bucket = eventsBeforeIndex.get(event.fold_end) ?? []
    bucket.push(event)
    eventsBeforeIndex.set(event.fold_end, bucket)
  }

  return (
    <div className="flex h-full min-h-0 flex-col lg:flex-row">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto px-6 py-6">
          {messages.length === 0 ? (
            <div className="flex h-full min-h-[24rem] flex-col items-center justify-center gap-3 px-6 text-center">
              <img src={emptyStateGif} alt="" className="h-24 w-24" />
              <p className="text-sm font-medium text-foreground">
                Опишите задачу разработки
              </p>
              <p className="max-w-sm text-sm text-muted-foreground">
                Например: «{EXAMPLE_TASK}»
              </p>
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              {messages.map((message, index) => (
                <div key={index} className="flex flex-col gap-4">
                  {eventsBeforeIndex.get(index)?.map((event, i) => (
                    <CompressionNotice key={`compression-${index}-${i}`} event={event} />
                  ))}
                  <MessageBubble message={message} />
                </div>
              ))}
              {eventsBeforeIndex.get(messages.length)?.map((event, i) => (
                <CompressionNotice key={`compression-end-${i}`} event={event} />
              ))}
              {isSending && <TypingIndicator />}
            </div>
          )}
        </div>

        {error && (
          <div className="px-6">
            <Alert variant="destructive">
              <AlertTitle>Не удалось отправить сообщение</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          </div>
        )}

        <div className="relative flex-shrink-0 pb-6">
          {tokensPopupOpen && (
            <TokensPopup
              lastUsage={lastUsage}
              lastContextTokens={lastContextTokens}
              contextTokenLimit={contextTokenLimit}
              cumulativeTotalTokens={cumulativeTotalTokens}
              cumulativeCostUsd={cumulativeCostUsd}
              compressionEnabled={compressionEnabled}
              onCompressionEnabledChange={onSetCompressionEnabled}
              historyKeepLastN={historyKeepLastN}
              summarizedMessageCount={summarizedMessageCount}
              rawMessageCount={rawMessageCount}
              onClose={() => setTokensPopupOpen(false)}
            />
          )}

          <form onSubmit={handleSubmit} className="flex px-6 pt-2">
            <div className="relative flex-1">
              {suggestions.length > 0 && (
                <div className="absolute bottom-full left-0 mb-2 w-80 overflow-hidden rounded-lg border border-border bg-card py-1 shadow-lg">
                  {suggestions.map((command, index) => (
                    <button
                      key={command.name}
                      type="button"
                      onMouseDown={(event) => {
                        // preventDefault keeps focus on the textarea, so this
                        // fires before any blur-driven close of the dropdown.
                        event.preventDefault()
                        applySuggestion(command)
                      }}
                      onMouseEnter={() => setSelectedSuggestion(index)}
                      className={cn(
                        'flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm',
                        index === selectedSuggestion
                          ? 'bg-accent text-foreground'
                          : 'text-foreground',
                      )}
                    >
                      <span className="font-mono">{command.name}</span>
                      <span className="truncate text-xs text-muted-foreground">
                        {command.description}
                      </span>
                    </button>
                  ))}
                </div>
              )}
              <textarea
                ref={textareaRef}
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={handleKeyDown}
                onFocus={() => setTextareaFocused(true)}
                onBlur={() => setTextareaFocused(false)}
                placeholder="Опишите задачу, уточните детали или введите команду через /…"
                rows={1}
                className="block max-h-[200px] min-h-11 w-full resize-none overflow-hidden rounded-lg border border-input bg-transparent py-2.5 pr-24 pl-3 text-sm leading-relaxed outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
              />
              <Button
                type="submit"
                size="sm"
                disabled={!draft.trim() || (isSending && draft.trim() !== TOKENS_COMMAND)}
                className="absolute right-1.5 bottom-1.5"
              >
                {isSending ? 'Отправляем…' : 'Отправить'}
              </Button>
            </div>
          </form>

          {contextTokenLimit > 0 && (
            <div className="flex items-center justify-center gap-2 px-6 pt-1.5 text-[11px] text-muted-foreground">
              <span>
                Контекст: {lastContextTokens.toLocaleString('ru-RU')} /{' '}
                {contextTokenLimit.toLocaleString('ru-RU')}
              </span>
              {cumulativeCostUsd != null && (
                <>
                  <span aria-hidden>·</span>
                  <span>${cumulativeCostUsd.toFixed(4)}</span>
                </>
              )}
            </div>
          )}
        </div>
      </div>

      {estimate && (
        <div className="h-72 flex-shrink-0 px-6 pb-6 lg:h-full lg:w-[26rem] lg:py-6 lg:pl-0">
          <ChatEstimateCard estimate={estimate} />
        </div>
      )}
    </div>
  )
}

function MessageBubble({ message }: { message: AgentMessage }) {
  const isUser = message.role === 'user'
  const tokenCount = isUser
    ? message.usage?.prompt_tokens
    : message.usage?.completion_tokens

  return (
    <div
      className={cn(
        'flex max-w-[85%] flex-col gap-1',
        isUser ? 'self-end items-end' : 'self-start items-start',
      )}
    >
      <div
        className={cn(
          'rounded-xl px-4 py-2.5',
          isUser
            ? 'bg-primary text-primary-foreground'
            : 'border border-border bg-card',
        )}
      >
        {isUser ? (
          <p className="text-sm leading-relaxed whitespace-pre-wrap">
            {message.content}
          </p>
        ) : (
          <Markdown>{message.content}</Markdown>
        )}
      </div>
      <span className="px-1 text-[11px] text-muted-foreground">
        {formatTime(message.created_at)}
        {tokenCount != null && ` · ${tokenCount.toLocaleString('ru-RU')} токенов`}
      </span>
    </div>
  )
}

// CompressionNotice marks the exact point in the chat timeline where history
// compression folded older messages into a summary — deliberately not a
// bubble on either side (it's neither the user nor the model speaking), so
// it renders centered, muted, and in italics, with the actual summary text
// visible right there for anyone reviewing the conversation.
function CompressionNotice({ event }: { event: CompressionEvent }) {
  return (
    <div className="flex flex-col items-center gap-1 py-1 text-center">
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <Minimize2 className="h-3 w-3" />
        <span>
          {event.manual ? 'Сжатие вручную' : 'Автосжатие'} · {formatTime(event.created_at)} ·{' '}
          свёрнуто {event.folded_count} сообщ. (всего в резюме: {event.summarized_total})
        </span>
      </div>
      <p className="max-w-lg text-[11px] text-muted-foreground/80 italic">{event.summary}</p>
    </div>
  )
}

// TokensPopup is the /tokens command's output — a small floating panel above
// the composer, in the spirit of the Claude VS Code extension's own context/
// cost popup. It's the one place on the whole page a progress bar is allowed
// to appear; everywhere else token info stays plain text.
function TokensPopup({
  lastUsage,
  lastContextTokens,
  contextTokenLimit,
  cumulativeTotalTokens,
  cumulativeCostUsd,
  compressionEnabled,
  onCompressionEnabledChange,
  historyKeepLastN,
  summarizedMessageCount,
  rawMessageCount,
  onClose,
}: {
  lastUsage?: TokenUsage
  lastContextTokens: number
  contextTokenLimit: number
  cumulativeTotalTokens: number
  cumulativeCostUsd?: number
  compressionEnabled: boolean
  onCompressionEnabledChange: (enabled: boolean) => void
  historyKeepLastN: number
  summarizedMessageCount: number
  rawMessageCount: number
  onClose: () => void
}) {
  const percent =
    contextTokenLimit > 0
      ? Math.min(100, Math.round((lastContextTokens / contextTokenLimit) * 100))
      : 0
  const barColor =
    percent >= 90 ? 'bg-destructive' : percent >= 70 ? 'bg-warning' : 'bg-primary'

  return (
    <>
      <div className="fixed inset-0 z-40" onClick={onClose} />
      <div className="absolute right-6 bottom-full z-50 mb-3 w-80 max-w-[calc(100vw-3rem)] rounded-xl border border-border bg-card p-4 shadow-lg">
        <div className="flex items-center justify-between">
          <p className="text-sm font-medium text-foreground">Токены</p>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>

        <div className="mt-3.5">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">Контекст диалога</span>
            {contextTokenLimit > 0 ? (
              <span className="font-mono tabular-nums text-foreground">
                {lastContextTokens.toLocaleString('ru-RU')} /{' '}
                {contextTokenLimit.toLocaleString('ru-RU')}
              </span>
            ) : (
              <span className="text-muted-foreground">лимит не задан</span>
            )}
          </div>
          {contextTokenLimit > 0 && (
            <>
              <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className={cn('h-full rounded-full transition-[width] duration-500 ease-out', barColor)}
                  style={{ width: `${percent}%` }}
                />
              </div>
              <p className="mt-1 text-right text-[11px] text-muted-foreground">{percent}%</p>
            </>
          )}
        </div>

        <div className="mt-4 flex flex-col gap-1">
          <p className="mb-0.5 text-xs font-medium text-muted-foreground">Последний запрос</p>
          {lastUsage ? (
            <>
              <StatRow label="Prompt" value={lastUsage.prompt_tokens.toLocaleString('ru-RU')} />
              <StatRow label="Completion" value={lastUsage.completion_tokens.toLocaleString('ru-RU')} />
              <StatRow label="Всего" value={lastUsage.total_tokens.toLocaleString('ru-RU')} />
              {lastUsage.cost_usd != null && (
                <StatRow label="Стоимость" value={`$${lastUsage.cost_usd.toFixed(4)}`} />
              )}
            </>
          ) : (
            <p className="text-sm text-muted-foreground">нет данных</p>
          )}
        </div>

        <div className="mt-4 flex flex-col gap-1">
          <p className="mb-0.5 text-xs font-medium text-muted-foreground">Весь диалог</p>
          <StatRow label="Всего токенов" value={cumulativeTotalTokens.toLocaleString('ru-RU')} />
          {cumulativeCostUsd != null && (
            <StatRow label="Стоимость" value={`$${cumulativeCostUsd.toFixed(4)}`} />
          )}
        </div>

        <div className="mt-4 border-t border-border pt-3.5">
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1 text-xs text-muted-foreground">
              Сжатие истории
              <CompressionInfoHint historyKeepLastN={historyKeepLastN} />
            </span>
            <CompressionSwitch checked={compressionEnabled} onChange={onCompressionEnabledChange} />
          </div>
          {summarizedMessageCount > 0 ? (
            <p className="mt-1.5 text-[11px] text-muted-foreground">
              Резюме {summarizedMessageCount} сообщений + {rawMessageCount} последних как есть
            </p>
          ) : (
            <p className="mt-1.5 text-[11px] text-muted-foreground">
              Пока ничего не сжато ({rawMessageCount} сообщений в истории)
            </p>
          )}
        </div>
      </div>
    </>
  )
}

// CompressionInfoHint is a small, self-contained hover/focus hint — not the
// shared InfoTooltip component, because that one renders through a portal to
// document.body, and inside this already-portal-free floating TokensPopup
// (itself absolutely positioned, not portaled) that put the tooltip content
// in a different, ambiguous stacking position relative to the popup's own
// z-index. Staying a plain descendant with CSS-only hover/focus guarantees
// it paints in the exact same stacking context as the popup around it.
function CompressionInfoHint({ historyKeepLastN }: { historyKeepLastN: number }) {
  return (
    <span className="group relative inline-flex">
      <button
        type="button"
        aria-label="Как работает сжатие истории"
        className="text-muted-foreground hover:text-foreground"
      >
        <HelpCircle className="h-3.5 w-3.5" />
      </button>
      <span
        role="tooltip"
        className="pointer-events-none absolute bottom-full left-1/2 z-10 mb-2 w-64 -translate-x-1/2 rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground opacity-0 shadow-md transition-opacity duration-100 group-hover:opacity-100 group-focus-within:opacity-100"
      >
        Когда несжатых сообщений становится больше {2 * historyKeepLastN} (2×{historyKeepLastN}),
        всё, кроме последних {historyKeepLastN}, сворачивается в краткое резюме — оно
        подставляется в запрос вместо полной истории. Выключить/включить можно в любой
        момент диалога; уже накопленное резюме не теряется. Команда{' '}
        <span className="font-mono">/compress</span> сжимает сразу, не дожидаясь порога.
      </span>
    </span>
  )
}

function CompressionSwitch({
  checked,
  onChange,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative h-5 w-9 flex-shrink-0 rounded-full transition-colors',
        checked ? 'bg-primary' : 'bg-muted',
      )}
    >
      <span
        className={cn(
          'absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-background transition-transform',
          checked && 'translate-x-4',
        )}
      />
    </button>
  )
}

function StatRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono tabular-nums text-foreground">{value}</span>
    </div>
  )
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('ru-RU', {
    hour: '2-digit',
    minute: '2-digit',
  })
}

function TypingIndicator() {
  return (
    <div className="flex w-fit items-center gap-1 self-start rounded-xl border border-border bg-card px-4 py-3">
      <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-foreground [animation-delay:-0.3s]" />
      <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-foreground [animation-delay:-0.15s]" />
      <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted-foreground" />
    </div>
  )
}
