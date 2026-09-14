import { useEffect, useState } from 'react'
import { PanelLeftOpen } from 'lucide-react'

import { ChatPanel } from '@/components/chat-panel'
import { CompareOptionsForm } from '@/components/compare-options'
import { EstimateResult } from '@/components/estimate-result'
import { FormatComparison } from '@/components/format-comparison'
import { ModelComparison } from '@/components/model-comparison'
import { ModelLineup } from '@/components/model-lineup'
import {
  ReasoningComparison,
  type ReasoningReaction,
} from '@/components/reasoning-comparison'
import { ReasoningStatusPanel } from '@/components/reasoning-status-panel'
import { Sidebar, type DemoMode } from '@/components/sidebar'
import { TaskForm } from '@/components/task-form'
import { TemperatureComparison } from '@/components/temperature-comparison'
import {
  ApiError,
  compareControlled,
  compareFormats,
  compareModels,
  compareReasoning,
  compareTemperatures,
  createChat,
  deleteChat,
  estimateTask,
  forceCompress,
  getChat,
  listChats,
  postAgentMessage,
  renameChat,
  setCompressionEnabled,
  type ChatDetail,
  type ChatSummary,
  type Comparison,
  type CompareOptions,
  type Estimate,
  type ModelComparison as ModelComparisonData,
  type RawResult,
  type ReasoningComparison as ReasoningComparisonData,
  type TemperatureComparison as TemperatureComparisonData,
} from '@/lib/api'

type Status = 'idle' | 'loading' | 'error' | 'success'
type Mode = 'chat' | DemoMode

const DEFAULT_COMPARE_OPTIONS: CompareOptions = {
  maxTokens: 1000,
  maxItems: 3,
  temperature: 0.2,
  useStopInstruction: true,
}

const MODE_COPY: Record<Mode, { title: string; description: string }> = {
  chat: {
    title: 'Ассистент по оценке задач',
    description:
      'Опишите задачу разработки в чате — ассистент даст предварительную AI-оценку и будет уточнять её по мере разговора. Это общая оценка — она пока ничего не знает о вашей личной истории работы.',
  },
  estimate: {
    title: 'День 1 — Оценка задачи разработки (демо)',
    description:
      'Первая, одноразовая версия оценки: один запрос — один ответ, без диалога. Текущий рабочий вариант — чат слева.',
  },
  compare: {
    title: 'День 2 — Сравнение форматов ответа (демо)',
    description:
      'Один и тот же запрос уходит в LLM дважды: без ограничений формата и с явным форматом, лимитом длины и условием завершения.',
  },
  reasoning: {
    title: 'День 3 — Способы рассуждения (демо)',
    description:
      'Одна и та же задача решается через LLM четырьмя способами: прямой ответ, пошаговое рассуждение, мета-промпт (модель сама составляет промпт) и группа экспертов.',
  },
  temperature: {
    title: 'День 4 — Температура (демо)',
    description:
      'Один и тот же запрос уходит в LLM трижды — с temperature 0, 0.7 и 1.2, — чтобы сравнить точность, креативность и разнообразие ответов и понять, для каких задач подходит каждая настройка.',
  },
  models: {
    title: 'День 5 — Версии моделей (демо)',
    description:
      'Один и тот же запрос решают три модели возрастающей мощности — от слабой до сильной, — чтобы сравнить качество, скорость и стоимость ответа и понять, когда доплата за более мощную модель оправдана.',
  },
}

function App() {
  const [mode, setMode] = useState<Mode>('chat')
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)

  const [chats, setChats] = useState<ChatSummary[]>([])
  const [activeChatId, setActiveChatId] = useState<string | null>(null)
  const [activeChat, setActiveChat] = useState<ChatDetail | null>(null)
  const [chatSending, setChatSending] = useState(false)
  const [chatError, setChatError] = useState<string | null>(null)

  const [estimateStatus, setEstimateStatus] = useState<Status>('idle')
  const [estimate, setEstimate] = useState<Estimate | null>(null)
  const [estimateError, setEstimateError] = useState<string | null>(null)

  const [compareStatus, setCompareStatus] = useState<Status>('idle')
  const [comparison, setComparison] = useState<Comparison | null>(null)
  const [compareError, setCompareError] = useState<string | null>(null)
  const [compareOptions, setCompareOptions] = useState<CompareOptions>(
    DEFAULT_COMPARE_OPTIONS,
  )
  const [uncontrolledCache, setUncontrolledCache] = useState<{
    task: string
    result: RawResult
  } | null>(null)
  const [uncontrolledReused, setUncontrolledReused] = useState(false)

  const [reasoningStatus, setReasoningStatus] = useState<Status>('idle')
  const [reasoningComparison, setReasoningComparison] =
    useState<ReasoningComparisonData | null>(null)
  const [reasoningError, setReasoningError] = useState<string | null>(null)
  const [reasoningReaction, setReasoningReaction] =
    useState<ReasoningReaction>(null)

  const [temperatureStatus, setTemperatureStatus] = useState<Status>('idle')
  const [temperatureComparison, setTemperatureComparison] =
    useState<TemperatureComparisonData | null>(null)
  const [temperatureError, setTemperatureError] = useState<string | null>(null)

  const [modelsStatus, setModelsStatus] = useState<Status>('idle')
  const [modelsComparison, setModelsComparison] =
    useState<ModelComparisonData | null>(null)
  const [modelsError, setModelsError] = useState<string | null>(null)

  // On first load, resume the most recently created chat, or start a fresh
  // one if none exist yet.
  useEffect(() => {
    async function init() {
      try {
        const existing = await listChats()
        if (existing.length === 0) {
          const created = await createChat()
          setChats([created])
          setActiveChatId(created.id)
          setActiveChat({
            ...created,
            messages: [],
            estimate: null,
            last_context_tokens: 0,
            cumulative_total_tokens: 0,
            context_token_limit: 0,
            compression_enabled: true,
            history_keep_last_n: 10,
            summarized_message_count: 0,
            raw_message_count: 0,
            compression_events: [],
          })
          return
        }
        setChats(existing)
        const mostRecent = existing[existing.length - 1]
        const detail = await getChat(mostRecent.id)
        setActiveChatId(detail.id)
        setActiveChat(detail)
      } catch (err) {
        setChatError(
          err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
        )
      }
    }
    init()
  }, [])

  async function handleNewChat() {
    setMode('chat')
    try {
      const created = await createChat()
      setChats((prev) => [...prev, created])
      setActiveChatId(created.id)
      setActiveChat({
        ...created,
        messages: [],
        estimate: null,
        last_context_tokens: 0,
        cumulative_total_tokens: 0,
        context_token_limit: 0,
        compression_enabled: true,
        history_keep_last_n: 10,
        summarized_message_count: 0,
        raw_message_count: 0,
        compression_events: [],
      })
      setChatError(null)
    } catch (err) {
      setChatError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
    }
  }

  async function handleSelectChat(id: string) {
    setMode('chat')
    if (id === activeChatId) return
    try {
      const detail = await getChat(id)
      setActiveChatId(id)
      setActiveChat(detail)
      setChatError(null)
    } catch (err) {
      setChatError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
    }
  }

  async function handleRenameChat(id: string, title: string) {
    try {
      const updated = await renameChat(id, title)
      setChats((prev) => prev.map((chat) => (chat.id === id ? updated : chat)))
      setActiveChat((prev) => (prev && prev.id === id ? { ...prev, title: updated.title } : prev))
    } catch (err) {
      setChatError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
    }
  }

  async function handleDeleteChat(id: string) {
    try {
      await deleteChat(id)
      const remaining = chats.filter((chat) => chat.id !== id)
      setChats(remaining)

      if (id !== activeChatId) return

      if (remaining.length === 0) {
        await handleNewChat()
        return
      }
      const next = remaining[remaining.length - 1]
      const detail = await getChat(next.id)
      setActiveChatId(next.id)
      setActiveChat(detail)
    } catch (err) {
      setChatError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
    }
  }

  async function handleSendMessage(message: string) {
    if (!activeChatId) return
    const chatId = activeChatId
    const optimisticSentAt = new Date().toISOString()

    setActiveChat((prev) =>
      prev
        ? {
            ...prev,
            messages: [
              ...prev.messages,
              { role: 'user', content: message, created_at: optimisticSentAt },
            ],
          }
        : prev,
    )
    setChatSending(true)
    setChatError(null)

    try {
      const reply = await postAgentMessage(chatId, message)
      setActiveChat((prev) => {
        if (!prev) return prev
        // Replace the optimistic user message with the authoritative
        // timestamp/usage the backend actually recorded for it.
        const messages = [
          ...prev.messages.slice(0, -1),
          {
            role: 'user' as const,
            content: message,
            created_at: reply.user_message_created_at,
            usage: reply.usage ?? undefined,
          },
          {
            role: 'assistant' as const,
            content: reply.reply,
            created_at: reply.assistant_message_created_at,
            usage: reply.usage ?? undefined,
          },
        ]
        return {
          ...prev,
          messages,
          estimate: reply.estimate,
          title: reply.title,
          last_context_tokens: reply.last_context_tokens,
          cumulative_total_tokens: reply.cumulative_total_tokens,
          cumulative_cost_usd: reply.cumulative_cost_usd,
          context_token_limit: reply.context_token_limit,
          compression_enabled: reply.compression_enabled,
          history_keep_last_n: reply.history_keep_last_n,
          summarized_message_count: reply.summarized_message_count,
          raw_message_count: reply.raw_message_count,
          compression_events: reply.new_compression_event
            ? [...prev.compression_events, reply.new_compression_event]
            : prev.compression_events,
        }
      })
      setChats((prev) =>
        prev.map((chat) => (chat.id === chatId ? { ...chat, title: reply.title } : chat)),
      )
    } catch (err) {
      setChatError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
    } finally {
      setChatSending(false)
    }
  }

  async function handleForceCompress(): Promise<boolean> {
    if (!activeChatId) return false
    const chatId = activeChatId
    setChatSending(true)
    setChatError(null)
    try {
      const result = await forceCompress(chatId)
      setActiveChat((prev) => (prev && prev.id === chatId ? result.chat : prev))
      if (!result.compressed) {
        setChatError('Сжимать нечего — сырых сообщений не больше, чем нужно оставить как есть.')
      }
      return result.compressed
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
      return false
    } finally {
      setChatSending(false)
    }
  }

  async function handleSetCompressionEnabled(enabled: boolean) {
    if (!activeChatId) return
    const chatId = activeChatId
    try {
      const updated = await setCompressionEnabled(chatId, enabled)
      setActiveChat((prev) => (prev && prev.id === chatId ? updated : prev))
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleEstimateSubmit(task: string) {
    setEstimateStatus('loading')
    setEstimateError(null)
    try {
      const result = await estimateTask(task)
      setEstimate(result)
      setEstimateStatus('success')
    } catch (err) {
      setEstimateError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setEstimateStatus('error')
    }
  }

  async function handleCompareSubmit(task: string) {
    setCompareStatus('loading')
    setCompareError(null)
    try {
      if (uncontrolledCache && uncontrolledCache.task === task) {
        const controlled = await compareControlled(task, compareOptions)
        setComparison({ task, uncontrolled: uncontrolledCache.result, controlled })
        setUncontrolledReused(true)
      } else {
        const result = await compareFormats(task, compareOptions)
        setComparison(result)
        setUncontrolledCache({ task, result: result.uncontrolled })
        setUncontrolledReused(false)
      }
      setCompareStatus('success')
    } catch (err) {
      setCompareError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setCompareStatus('error')
    }
  }

  async function handleReasoningSubmit(task: string) {
    setReasoningStatus('loading')
    setReasoningError(null)
    setReasoningReaction(null)
    try {
      const result = await compareReasoning(task)
      setReasoningComparison(result)
      setReasoningStatus('success')
    } catch (err) {
      setReasoningError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setReasoningStatus('error')
    }
  }

  async function handleTemperatureSubmit(task: string) {
    setTemperatureStatus('loading')
    setTemperatureError(null)
    try {
      const result = await compareTemperatures(task)
      setTemperatureComparison(result)
      setTemperatureStatus('success')
    } catch (err) {
      setTemperatureError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setTemperatureStatus('error')
    }
  }

  async function handleModelsSubmit(task: string) {
    setModelsStatus('loading')
    setModelsError(null)
    try {
      const result = await compareModels(task)
      setModelsComparison(result)
      setModelsStatus('success')
    } catch (err) {
      setModelsError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
      setModelsStatus('error')
    }
  }

  const activeDemo = mode === 'chat' ? null : mode

  return (
    <div className="flex h-screen flex-col bg-background">
      <header className="flex flex-shrink-0 items-center gap-2 border-b border-border px-6 py-4">
        {sidebarCollapsed && (
          <button
            type="button"
            onClick={() => setSidebarCollapsed(false)}
            title="Показать панель"
            className="-ml-1 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <PanelLeftOpen className="h-4 w-4" />
          </button>
        )}
        <span className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-sm font-semibold text-primary-foreground">
          W
        </span>
        <span className="text-sm font-medium text-foreground">
          Work Intelligence
        </span>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {!sidebarCollapsed && (
          <Sidebar
            chats={chats}
            activeChatId={mode === 'chat' ? activeChatId : null}
            activeDemo={activeDemo}
            onNewChat={handleNewChat}
            onSelectChat={handleSelectChat}
            onSelectDemo={(demo) => setMode(demo)}
            onCollapse={() => setSidebarCollapsed(true)}
            onRenameChat={handleRenameChat}
            onDeleteChat={handleDeleteChat}
          />
        )}

        {mode === 'chat' ? (
          <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
            <ChatPanel
              messages={activeChat?.messages ?? []}
              estimate={activeChat?.estimate ?? null}
              isSending={chatSending}
              error={chatError}
              onSend={handleSendMessage}
              onForceCompress={handleForceCompress}
              onSetCompressionEnabled={handleSetCompressionEnabled}
              lastContextTokens={activeChat?.last_context_tokens ?? 0}
              cumulativeTotalTokens={activeChat?.cumulative_total_tokens ?? 0}
              cumulativeCostUsd={activeChat?.cumulative_cost_usd}
              contextTokenLimit={activeChat?.context_token_limit ?? 0}
              compressionEnabled={activeChat?.compression_enabled ?? true}
              historyKeepLastN={activeChat?.history_keep_last_n ?? 10}
              summarizedMessageCount={activeChat?.summarized_message_count ?? 0}
              rawMessageCount={activeChat?.raw_message_count ?? 0}
              compressionEvents={activeChat?.compression_events ?? []}
            />
          </main>
        ) : (
          <main className="flex-1 overflow-y-auto px-6 py-8">
            <div className="mb-6 max-w-2xl">
              <h1 className="text-2xl font-medium text-foreground">
                {MODE_COPY[mode].title}
              </h1>
              <p className="mt-1.5 text-sm text-muted-foreground">
                {MODE_COPY[mode].description}
              </p>
            </div>

          {mode === 'estimate' && (
            <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
              <TaskForm
                onSubmit={handleEstimateSubmit}
                isSubmitting={estimateStatus === 'loading'}
              />
              <EstimateResult
                status={estimateStatus}
                estimate={estimate}
                error={estimateError}
              />
            </div>
          )}

          {mode === 'compare' && (
            <div className="flex flex-col gap-8">
              <div className="grid grid-cols-1 gap-8 lg:grid-cols-[2fr_1fr]">
                <TaskForm
                  onSubmit={handleCompareSubmit}
                  isSubmitting={compareStatus === 'loading'}
                  submitLabel="Сравнить"
                  submittingLabel="Сравниваем…"
                  noteBeforeSubmit={
                    <p className="font-mono text-xs text-primary">
                      Запрос «с ограничениями» отправится с параметрами:
                      max_tokens={compareOptions.maxTokens}, max_items=
                      {compareOptions.maxItems}, temperature=
                      {compareOptions.temperature}, stop_instruction=
                      {compareOptions.useStopInstruction ? 'да' : 'нет'}
                    </p>
                  }
                />
                <CompareOptionsForm
                  value={compareOptions}
                  onChange={setCompareOptions}
                />
              </div>
              <FormatComparison
                status={compareStatus}
                comparison={comparison}
                error={compareError}
                uncontrolledReused={uncontrolledReused}
              />
            </div>
          )}

          {mode === 'reasoning' && (
            <div className="flex flex-col gap-8">
              <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
                <TaskForm
                  onSubmit={handleReasoningSubmit}
                  isSubmitting={reasoningStatus === 'loading'}
                  submitLabel="Решить"
                  submittingLabel="Решаем…"
                />
                <ReasoningStatusPanel
                  status={reasoningStatus}
                  comparison={reasoningComparison}
                  reaction={reasoningReaction}
                />
              </div>
              <ReasoningComparison
                status={reasoningStatus}
                comparison={reasoningComparison}
                error={reasoningError}
                reaction={reasoningReaction}
                onReactionChange={setReasoningReaction}
              />
            </div>
          )}

          {mode === 'temperature' && (
            <div className="flex flex-col gap-8">
              <div className="max-w-2xl">
                <TaskForm
                  onSubmit={handleTemperatureSubmit}
                  isSubmitting={temperatureStatus === 'loading'}
                  submitLabel="Сравнить"
                  submittingLabel="Сравниваем…"
                />
              </div>
              <TemperatureComparison
                status={temperatureStatus}
                comparison={temperatureComparison}
                error={temperatureError}
              />
            </div>
          )}

          {mode === 'models' && (
            <div className="flex flex-col gap-8">
              <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
                <TaskForm
                  onSubmit={handleModelsSubmit}
                  isSubmitting={modelsStatus === 'loading'}
                  submitLabel="Сравнить"
                  submittingLabel="Сравниваем…"
                />
                <ModelLineup />
              </div>
              <ModelComparison
                status={modelsStatus}
                comparison={modelsComparison}
                error={modelsError}
              />
            </div>
          )}
          </main>
        )}
      </div>
    </div>
  )
}

export default App
