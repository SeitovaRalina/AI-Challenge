import { useEffect, useRef, useState } from 'react'
import { ArrowRight, CheckCircle2, Loader2, Minimize2, Sparkles, X, XCircle } from 'lucide-react'

import emptyStateGif from '@/assets/empty_state.gif'
import { BranchToolbar } from '@/components/branch-toolbar'
import { ChatEstimateCard } from '@/components/chat-estimate-card'
import { ContextPopup } from '@/components/context-popup'
import { ContextStrategySelect } from '@/components/context-strategy-select'
import { Markdown } from '@/components/markdown'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { cn } from 'cn'
import type {
  AgentMessage,
  BranchSummary,
  Checkpoint,
  CompressionEvent,
  ContextStrategy,
  Estimate,
  FanOutStatus,
  TaskMemory,
  TokenUsage,
  UserProfile,
} from '@/lib/api'
import { isRealStrategy, STRATEGY_META } from '@/lib/strategy'

const EXAMPLE_TASK =
  'Обновить устаревшее Flutter-приложение до новой версии Flutter, обновить зависимости, исправить проблемы сборки под iOS и Android и подготовить новые билды.'

// Sent as a normal user message when the empty-state hint's "Начать
// интервью" is clicked — deliberately NOT a task description, and says so
// explicitly, since agentSystemPrompt otherwise treats a chat's first
// message as one. Everything the user reveals in the exchanges that follow
// still flows through the ordinary per-turn profile extraction (day 12) —
// this is just a conversation starter, no new backend mechanism.
//
// Names exactly UserProfile's 5 fields, nothing else, and asks for a closing
// summary — went through two real bugs before landing here:
//   1. An earlier version never mentioned stack at all, silently dropping
//      whatever the user said about it (UserProfile had no field for it yet
//      — see the Stack field's own doc comment).
//   2. A version that spelled out 5 numbered imperatives, several of them
//      negations ("не спрашивай ни о чём другом", "не продолжай задавать
//      вопросы"), reproducibly made the model return an EMPTY completion one
//      turn later — day 11's task-memory extraction faithfully captured
//      those meta-instructions as "task constraints" and re-injected them as
//      a system message, and that redundant, negation-heavy instruction
//      stack broke the main turn (confirmed by reproducing it with a
//      manually-set Chat.Task carrying the same constraints, and by testing
//      that this shorter, less directive-dense version does not reproduce
//      it, across a full 5-question run). Keep this message short and light
//      on "не делай X" phrasing — that's a real constraint, not style
//      preference, given what already broke here once.
const ONBOARDING_KICKOFF_MESSAGE =
  'Не задача для оценки, а знакомство — узнай, как меня зовут, мой обычный стек, стиль общения и формат ответов, есть ли особые ограничения. Спрашивай по одному вопросу, потом кратко подытожь и на этом закончи.'

const COMPOSER_MAX_HEIGHT = 200
const TOKENS_COMMAND = '/tokens'
const COMPRESS_COMMAND = '/compress'
const CONTEXT_COMMAND = '/context'
const ANALYZE_COMMAND = '/analyze'

interface SlashCommand {
  name: string
  description: string
}

interface ChatPanelProps {
  chatId: string
  messages: AgentMessage[]
  estimate: Estimate | null
  isSending: boolean
  error: string | null
  onSend: (message: string) => void
  onForceCompress: () => Promise<boolean>
  contextStrategy: ContextStrategy
  onSetContextStrategy: (strategy: ContextStrategy) => void
  lastContextTokens: number
  cumulativeTotalTokens: number
  cumulativeCostUsd?: number
  contextTokenLimit: number
  historyKeepLastN: number
  summarizedMessageCount: number
  rawMessageCount: number
  compressionEvents: CompressionEvent[]
  facts?: Record<string, string>
  task?: TaskMemory
  onUpdateTask: (task: TaskMemory) => void
  branches: BranchSummary[]
  checkpoints: Checkpoint[]
  activeBranchId?: string
  onCreateCheckpoint: (label: string) => void
  onCreateBranch: (checkpointIndex: number, fromBranchId: string, label: string) => void
  onSelectBranch: (branchId: string) => void
  isLabChat: boolean
  isLabCoordinator: boolean
  onAnalyzeLab: () => void
  coordinatorTitle?: string
  onJumpToCoordinator?: () => void
  fanOut?: FanOutStatus[]
  onJumpToChat?: (chatId: string) => void
  profile?: UserProfile
  onOpenProfile: () => void
}

export function ChatPanel({
  chatId,
  messages,
  estimate,
  isSending,
  error,
  onSend,
  onForceCompress,
  contextStrategy,
  onSetContextStrategy,
  lastContextTokens,
  cumulativeTotalTokens,
  cumulativeCostUsd,
  contextTokenLimit,
  historyKeepLastN,
  summarizedMessageCount,
  rawMessageCount,
  compressionEvents,
  facts,
  task,
  onUpdateTask,
  branches,
  checkpoints,
  activeBranchId,
  onCreateCheckpoint,
  onCreateBranch,
  onSelectBranch,
  isLabChat,
  isLabCoordinator,
  onAnalyzeLab,
  coordinatorTitle,
  onJumpToCoordinator,
  fanOut,
  onJumpToChat,
  profile,
  onOpenProfile,
}: ChatPanelProps) {
  const [draft, setDraft] = useState('')
  const [tokensPopupOpen, setTokensPopupOpen] = useState(false)
  const [contextPopupOpen, setContextPopupOpen] = useState(false)
  const [textareaFocused, setTextareaFocused] = useState(false)
  const [selectedSuggestion, setSelectedSuggestion] = useState(0)
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // A fan-out still running means the 3 strategy chats' history isn't
  // settled yet — sending another message now would race a second fan-out
  // against the first on the same chats (see PostCoordinatorMessage's own
  // ErrFanOutInProgress guard), so the coordinator blocks input until every
  // strategy has either replied or failed.
  const fanOutPending = fanOut?.some((f) => f.status === 'pending') ?? false

  // Only the lab coordinator accepts direct input — its strategy chats exist
  // purely to show each strategy's own result, so the comparison always
  // reflects the same fanned-out input. The coordinator itself has no
  // strategy of its own (nothing to window/summarize), so its only command
  // is /analyze; a strategy chat gets /tokens + /context but never /analyze.
  const canSendMessages = !isLabChat || (isLabCoordinator && !fanOutPending)
  const slashCommands: SlashCommand[] = isLabCoordinator
    ? [{ name: ANALYZE_COMMAND, description: 'сравнить стратегии лаборатории' }]
    : [
        { name: TOKENS_COMMAND, description: 'токены и стоимость диалога' },
        { name: CONTEXT_COMMAND, description: 'что сейчас в контексте' },
        ...(contextStrategy === 'rolling_summary'
          ? [{ name: COMPRESS_COMMAND, description: 'сжать историю сейчас' }]
          : []),
      ]

  // Only offer suggestions while the draft is still just the command token
  // itself (no space yet — none of today's commands take arguments).
  const suggestions =
    textareaFocused && draft.startsWith('/') && !draft.includes(' ')
      ? slashCommands.filter((c) => c.name.startsWith(draft))
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
  }, [messages, isSending, fanOut])

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

  useEffect(() => {
    if (!contextPopupOpen) return
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') setContextPopupOpen(false)
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [contextPopupOpen])

  function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    const trimmed = draft.trim()
    if (!trimmed) return

    // /tokens is a local, offline command — it never reaches the LLM and
    // never touches chat state, so it must work even while a message is
    // in flight (isSending) — it's just inspecting whatever is already
    // known, not competing with the in-flight request for anything. Always
    // intercepted locally (even where it's not advertised, e.g. the
    // coordinator) so it's never accidentally sent as a real message.
    if (trimmed === TOKENS_COMMAND) {
      setTokensPopupOpen(true)
      setDraft('')
      return
    }

    // /context is also local/offline (like /tokens) — it only reads
    // whatever the current chat state already is, so it works mid-send too.
    if (trimmed === CONTEXT_COMMAND) {
      setContextPopupOpen(true)
      setDraft('')
      return
    }

    if (isSending || fanOutPending) return

    // /compress is a real backend action (its own LLM call), not a chat
    // message — never appended to history, handled the same way /tokens
    // intercepts before reaching onSend. Its result shows up as an in-chat
    // compression notice (or the error banner if there was nothing to fold).
    if (trimmed === COMPRESS_COMMAND) {
      setDraft('')
      void onForceCompress()
      return
    }

    if (trimmed === ANALYZE_COMMAND) {
      setDraft('')
      onAnalyzeLab()
      return
    }

    if (!canSendMessages) return
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
        {contextStrategy === 'branching' && (
          <BranchToolbar
            branches={branches}
            checkpoints={checkpoints}
            activeBranchId={activeBranchId ?? 'main'}
            onSelectBranch={onSelectBranch}
            onCreateCheckpoint={onCreateCheckpoint}
            onCreateBranch={onCreateBranch}
          />
        )}
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
              {!isLabChat && !profile?.name && (
                <Alert className="mt-2 max-w-sm text-left">
                  <AlertTitle>Ассистент вас пока не знает</AlertTitle>
                  <AlertDescription>
                    Заполните профиль — и ассистент будет обращаться по имени и
                    подстраиваться под ваш стиль в каждом чате.
                  </AlertDescription>
                  <div className="mt-2 flex items-center gap-2">
                    <Button size="sm" onClick={() => onSend(ONBOARDING_KICKOFF_MESSAGE)}>
                      Начать интервью с ассистентом
                    </Button>
                    <Button size="sm" variant="ghost" onClick={onOpenProfile}>
                      Заполнить вручную
                    </Button>
                  </div>
                </Alert>
              )}
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              {messages.map((message, index) => (
                <div key={index} className="flex flex-col gap-4">
                  {eventsBeforeIndex.get(index)?.map((event, i) => (
                    <CompressionNotice key={`compression-${index}-${i}`} event={event} />
                  ))}
                  {message.is_lab_analysis ? (
                    <LabAnalysisNotice message={message} />
                  ) : (
                    <MessageBubble message={message} />
                  )}
                </div>
              ))}
              {eventsBeforeIndex.get(messages.length)?.map((event, i) => (
                <CompressionNotice key={`compression-end-${i}`} event={event} />
              ))}
              {isLabCoordinator && fanOut && fanOut.length > 0 && (
                <FanOutPanel fanOut={fanOut} onJumpToChat={onJumpToChat} />
              )}
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
              onClose={() => setTokensPopupOpen(false)}
            />
          )}

          {contextPopupOpen && (
            <ContextPopup
              chatId={chatId}
              strategy={contextStrategy}
              historyKeepLastN={historyKeepLastN}
              messages={messages}
              facts={facts}
              task={task}
              onUpdateTask={onUpdateTask}
              branches={branches}
              activeBranchId={activeBranchId}
              compressionEvents={compressionEvents}
              summarizedMessageCount={summarizedMessageCount}
              rawMessageCount={rawMessageCount}
              onClose={() => setContextPopupOpen(false)}
            />
          )}

          {isLabChat && !isLabCoordinator && (
            <div className="mx-6 mb-2 flex items-center justify-between gap-3 rounded-lg border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
              <span>
                Эта стратегия — часть лаборатории
                {coordinatorTitle ? <> «{coordinatorTitle}»</> : null}. Пишите в координаторском чате.
              </span>
              {onJumpToCoordinator && (
                <button
                  type="button"
                  onClick={onJumpToCoordinator}
                  className="flex-shrink-0 rounded-md border border-border px-2 py-1 font-medium text-foreground transition-colors hover:bg-accent"
                >
                  Перейти →
                </button>
              )}
            </div>
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
                placeholder={
                  isLabCoordinator
                    ? fanOutPending
                      ? 'Ждём ответы стратегий на предыдущее сообщение…'
                      : 'Сообщение уйдёт во все стратегии лаборатории, или введите /analyze…'
                    : canSendMessages
                      ? 'Опишите задачу, уточните детали или введите команду через /…'
                      : 'Только команды (/tokens, /context) — обычные сообщения пишите в координаторском чате'
                }
                rows={1}
                className="block max-h-[200px] min-h-11 w-full resize-none overflow-hidden rounded-lg border border-input bg-transparent py-2.5 pr-24 pl-3 text-sm leading-relaxed outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
              />
              <Button
                type="submit"
                size="sm"
                disabled={
                  !draft.trim() ||
                  (isSending && draft.trim() !== TOKENS_COMMAND && draft.trim() !== CONTEXT_COMMAND) ||
                  (!canSendMessages && draft.trim() !== TOKENS_COMMAND && draft.trim() !== CONTEXT_COMMAND)
                }
                className="absolute right-1.5 bottom-1.5"
              >
                {isSending ? 'Отправляем…' : 'Отправить'}
              </Button>
            </div>
          </form>

          <div className="flex items-center justify-center gap-2 px-6 pt-1.5 text-[11px] text-muted-foreground">
            {isLabCoordinator ? (
              <span className="rounded-md border border-primary/40 bg-primary/5 px-2 py-1 text-primary">
                Координатор
              </span>
            ) : (
              isRealStrategy(contextStrategy) && (
                <ContextStrategySelect
                  value={contextStrategy}
                  historyKeepLastN={historyKeepLastN}
                  disabled={isLabChat}
                  onChange={onSetContextStrategy}
                />
              )
            )}
            {contextTokenLimit > 0 && !isLabCoordinator && (
              <>
                <span aria-hidden>·</span>
                <span>
                  Контекст: {lastContextTokens.toLocaleString('ru-RU')} /{' '}
                  {contextTokenLimit.toLocaleString('ru-RU')}
                </span>
              </>
            )}
            {cumulativeCostUsd != null && (
              <>
                <span aria-hidden>·</span>
                <span>${cumulativeCostUsd.toFixed(4)}</span>
              </>
            )}
          </div>
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
  onClose,
}: {
  lastUsage?: TokenUsage
  lastContextTokens: number
  contextTokenLimit: number
  cumulativeTotalTokens: number
  cumulativeCostUsd?: number
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
      </div>
    </>
  )
}

// LabAnalysisNotice renders the one message AnalyzeLab appends — a real
// assistant message (is_lab_analysis: true), but shown distinctly from a
// normal reply so it reads as the lab's own comparison verdict, not
// something the current chat's strategy said.
function LabAnalysisNotice({ message }: { message: AgentMessage }) {
  return (
    <div className="self-stretch rounded-xl border border-primary/30 bg-primary/5 px-4 py-3">
      <div className="flex items-center gap-1.5 text-xs font-medium text-primary">
        <Sparkles className="h-3.5 w-3.5" />
        Сравнение стратегий · {formatTime(message.created_at)}
      </div>
      <div className="mt-2 text-sm">
        <Markdown>{message.content}</Markdown>
      </div>
    </div>
  )
}

// FanOutPanel tracks the coordinator's most recent fan-out live: one row per
// strategy chat, in progress / replied (jump straight to it) / failed (with
// the error). Attaches right under the coordinator's own last message rather
// than a separate popup, since it's about what's happening in this chat.
function FanOutPanel({
  fanOut,
  onJumpToChat,
}: {
  fanOut: FanOutStatus[]
  onJumpToChat?: (chatId: string) => void
}) {
  return (
    <div className="flex w-fit min-w-64 flex-col gap-1.5 self-start rounded-xl border border-border bg-card px-4 py-3">
      <span className="text-xs font-medium text-muted-foreground">Прогресс по стратегиям</span>
      {fanOut.map((entry) => {
        const meta = STRATEGY_META[entry.strategy]
        const clickable = entry.status === 'done' && onJumpToChat
        return (
          <div key={entry.chat_id} className="flex flex-col gap-0.5">
            <button
              type="button"
              disabled={!clickable}
              onClick={() => clickable && onJumpToChat(entry.chat_id)}
              className={cn(
                'flex items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs',
                clickable ? 'cursor-pointer hover:bg-accent' : 'cursor-default',
              )}
            >
              <span
                className="inline-flex w-fit shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium"
                style={{ background: `${meta.color}1a`, color: meta.color }}
              >
                <span className="size-1.5 shrink-0 rounded-full" style={{ background: meta.color }} />
                {meta.label}
              </span>
              {entry.status === 'pending' && (
                <span className="flex items-center gap-1 text-muted-foreground">
                  <Loader2 className="h-3 w-3 animate-spin" /> в процессе…
                </span>
              )}
              {entry.status === 'done' && (
                <span className="flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="h-3 w-3" /> ответ получен
                  {onJumpToChat && <ArrowRight className="h-3 w-3" />}
                </span>
              )}
              {entry.status === 'failed' && (
                <span className="flex items-center gap-1 text-destructive">
                  <XCircle className="h-3 w-3" /> ошибка
                </span>
              )}
            </button>
            {entry.status === 'failed' && entry.error && (
              <span className="px-1.5 text-[11px] text-muted-foreground">{entry.error}</span>
            )}
          </div>
        )
      })}
    </div>
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
