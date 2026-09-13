import { useEffect, useRef, useState } from 'react'

import emptyStateGif from '@/assets/empty_state.gif'
import { ChatEstimateCard } from '@/components/chat-estimate-card'
import { Markdown } from '@/components/markdown'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { cn } from 'cn'
import type { AgentMessage, Estimate } from '@/lib/api'

const EXAMPLE_TASK =
  'Обновить устаревшее Flutter-приложение до новой версии Flutter, обновить зависимости, исправить проблемы сборки под iOS и Android и подготовить новые билды.'

const COMPOSER_MAX_HEIGHT = 200

interface ChatPanelProps {
  messages: AgentMessage[]
  estimate: Estimate | null
  isSending: boolean
  error: string | null
  onSend: (message: string) => void
}

export function ChatPanel({
  messages,
  estimate,
  isSending,
  error,
  onSend,
}: ChatPanelProps) {
  const [draft, setDraft] = useState('')
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

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

  function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    const trimmed = draft.trim()
    if (!trimmed || isSending) return
    onSend(trimmed)
    setDraft('')
  }

  function handleKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      handleSubmit(event)
    }
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
                <div
                  key={index}
                  className={cn(
                    'max-w-[85%] rounded-xl px-4 py-2.5',
                    message.role === 'user'
                      ? 'self-end bg-primary text-primary-foreground'
                      : 'self-start border border-border bg-card',
                  )}
                >
                  {message.role === 'assistant' ? (
                    <Markdown>{message.content}</Markdown>
                  ) : (
                    <p className="text-sm leading-relaxed whitespace-pre-wrap">
                      {message.content}
                    </p>
                  )}
                </div>
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

        <form
          onSubmit={handleSubmit}
          className="flex flex-shrink-0 px-6 pt-2 pb-6"
        >
          <div className="relative flex-1">
            <textarea
              ref={textareaRef}
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Опишите задачу или уточните детали…"
              rows={1}
              className="block max-h-[200px] min-h-11 w-full resize-none overflow-hidden rounded-lg border border-input bg-transparent py-2.5 pr-24 pl-3 text-sm leading-relaxed outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
            />
            <Button
              type="submit"
              size="sm"
              disabled={!draft.trim() || isSending}
              className="absolute right-1.5 bottom-1.5"
            >
              {isSending ? 'Отправляем…' : 'Отправить'}
            </Button>
          </div>
        </form>
      </div>

      {estimate && (
        <div className="h-72 flex-shrink-0 px-6 pb-6 lg:h-full lg:w-[26rem] lg:py-6 lg:pl-0">
          <ChatEstimateCard estimate={estimate} />
        </div>
      )}
    </div>
  )
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
