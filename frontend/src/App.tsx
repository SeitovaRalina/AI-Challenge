import { useEffect, useState } from 'react'
import { PanelLeftOpen } from 'lucide-react'

import { ChatPanel } from '@/components/chat-panel'
import { CompareOptionsForm } from '@/components/compare-options'
import { EstimateResult } from '@/components/estimate-result'
import { FormatComparison } from '@/components/format-comparison'
import { ModelComparison } from '@/components/model-comparison'
import { ModelLineup } from '@/components/model-lineup'
import { OnboardingInterview } from '@/components/onboarding-interview'
import { ProfilePopup } from '@/components/profile-popup'
import { ProjectMemoryPopup } from '@/components/project-memory-popup'
import {
  ReasoningComparison,
  type ReasoningReaction,
} from '@/components/reasoning-comparison'
import { ReasoningStatusPanel } from '@/components/reasoning-status-panel'
import { Sidebar, type DemoMode } from '@/components/sidebar'
import { TaskForm } from '@/components/task-form'
import { TemperatureComparison } from '@/components/temperature-comparison'
import {
  analyzeLab,
  ApiError,
  compareControlled,
  compareFormats,
  compareModels,
  compareReasoning,
  compareTemperatures,
  createBranch,
  createChat,
  createCheckpoint,
  createLab,
  createProject,
  deleteChat,
  deleteLab,
  deleteProject,
  estimateTask,
  forceCompress,
  getChat,
  getProfile,
  listChats,
  listProjects,
  postAgentMessage,
  renameChat,
  setActiveBranch,
  setContextStrategy,
  type ChatDetail,
  type ChatSummary,
  type Comparison,
  type CompareOptions,
  type ContextStrategy,
  type Estimate,
  type ModelComparison as ModelComparisonData,
  type Project,
  type RawResult,
  type TaskMemory,
  type UserProfile,
  type ReasoningComparison as ReasoningComparisonData,
  type TemperatureComparison as TemperatureComparisonData,
} from '@/lib/api'

type Status = 'idle' | 'loading' | 'error' | 'success'
type Mode = 'chat' | DemoMode

// Identifies one send-like operation's target for the pendingKeys set below:
// a chat by itself, or (for a branching chat) one specific branch within it —
// branches share a chat id, so the id alone can't tell two of them apart.
function chatKey(chatId: string, branchId?: string): string {
  return `${chatId}:${branchId ?? ''}`
}

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
  const [projects, setProjects] = useState<Project[]>([])
  const [projectMemoryPopupId, setProjectMemoryPopupId] = useState<string | null>(null)
  const [profile, setProfile] = useState<UserProfile | null>(null)
  const [profilePopupOpen, setProfilePopupOpen] = useState(false)
  const [profilePopupEditing, setProfilePopupEditing] = useState(false)
  const [interviewOpen, setInterviewOpen] = useState(false)

  // Single source of truth for a Project's known_stack/notes — called
  // whenever a fresh Project object arrives (mount, createProject, or a
  // ChatDetail/AgentReply that carries one), so the sidebar's memory popup
  // never shows stale data even when it wasn't this project's own chat that
  // just updated it.
  function upsertProject(project: Project) {
    setProjects((prev) =>
      prev.some((p) => p.id === project.id)
        ? prev.map((p) => (p.id === project.id ? project : p))
        : [...prev, project],
    )
  }
  // Which chat/branch keys (see chatKey) currently have a send-like request
  // in flight — a Set, not one shared boolean, so switching to a branch (or
  // chat) with nothing in flight never shows another branch's spinner, and
  // switching away from one that's still sending doesn't lose it either.
  const [pendingKeys, setPendingKeys] = useState<Set<string>>(new Set())
  // The optimistic user bubble for a still-in-flight send, per chat/branch
  // key — a plain local append to activeChat.messages doesn't survive
  // navigating away and back, since switching replaces activeChat wholesale
  // with a fresh server fetch, and the server doesn't have this message yet
  // (PostMessage only saves user+assistant together, once the reply is in).
  // Re-applied by withPendingMessage whenever a fetched ChatDetail is about
  // to become activeChat, so switching back to a branch that's still
  // sending shows the message again instead of a loader over nothing.
  const [pendingSends, setPendingSends] = useState<Map<string, { content: string; sentAt: string }>>(
    new Map(),
  )
  const [chatError, setChatError] = useState<string | null>(null)

  function beginPending(key: string) {
    setPendingKeys((prev) => new Set(prev).add(key))
  }
  function endPending(key: string) {
    setPendingKeys((prev) => {
      if (!prev.has(key)) return prev
      const next = new Set(prev)
      next.delete(key)
      return next
    })
  }

  function withPendingMessage(detail: ChatDetail): ChatDetail {
    const pending = pendingSends.get(chatKey(detail.id, detail.active_branch_id))
    if (!pending) return detail
    return {
      ...detail,
      messages: [...detail.messages, { role: 'user', content: pending.content, created_at: pending.sentAt }],
    }
  }

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
        const existingProjects = await listProjects()
        setProjects(existingProjects)
        const existingProfile = await getProfile()
        setProfile(existingProfile)

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
            context_strategy: 'sliding_window',
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

  // While the coordinator's most recent fan-out still has a strategy chat
  // pending, poll its detail so FanOutPanel updates live (in progress →
  // replied/failed) without the user having to reload or re-select the chat.
  // Self-terminating: once the fetched fan_out has nothing left pending, this
  // effect's dependency flips to false and no new interval is scheduled.
  useEffect(() => {
    if (!activeChatId || !activeChat?.is_lab_coordinator) return
    const pending = activeChat.fan_out?.some((entry) => entry.status === 'pending')
    if (!pending) return

    const chatId = activeChatId
    const interval = window.setInterval(async () => {
      try {
        const detail = await getChat(chatId)
        setActiveChat((prev) => (prev && prev.id === chatId ? detail : prev))
      } catch {
        // Transient poll failure — the next tick will retry.
      }
    }, 1500)
    return () => window.clearInterval(interval)
  }, [activeChatId, activeChat?.is_lab_coordinator, activeChat?.fan_out])

  async function handleNewChat(projectId?: string) {
    setMode('chat')
    try {
      const created = await createChat(projectId)
      setChats((prev) => [...prev, created])
      setActiveChatId(created.id)
      setActiveChat({
        ...created,
        messages: [],
        estimate: null,
        last_context_tokens: 0,
        cumulative_total_tokens: 0,
        context_token_limit: 0,
        context_strategy: 'sliding_window',
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
      setActiveChat(withPendingMessage(detail))
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
      setActiveChat(withPendingMessage(detail))
    } catch (err) {
      setChatError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
    }
  }

  async function handleSendMessage(message: string) {
    if (!activeChatId) return
    const chatId = activeChatId
    // Branches share one chat id, so switching tabs alone doesn't change
    // chatId — capturing the branch too is what lets the merge below tell
    // "still looking at the branch this was sent from" apart from "looking
    // at a sibling branch of the same chat" once the reply comes back.
    const branchId = activeChat?.active_branch_id
    const key = chatKey(chatId, branchId)
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
    beginPending(key)
    setPendingSends((prev) => new Map(prev).set(key, { content: message, sentAt: optimisticSentAt }))
    setChatError(null)

    try {
      const reply = await postAgentMessage(chatId, message)
      setActiveChat((prev) => {
        // The user may have switched chats or branches while this was in
        // flight — prev is now a different conversation's state, fetched
        // fresh from the server when they switched. Splicing this reply
        // into it would corrupt whatever's currently on screen (dropping
        // its real last message via slice(0, -1) and appending this one's
        // instead) — the backend already saved the reply to the right
        // place regardless; switching back re-fetches it correctly.
        if (!prev || prev.id !== chatId || prev.active_branch_id !== branchId) return prev
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
          context_strategy: reply.context_strategy,
          history_keep_last_n: reply.history_keep_last_n,
          summarized_message_count: reply.summarized_message_count,
          raw_message_count: reply.raw_message_count,
          facts: reply.facts,
          task: reply.task,
          branches: reply.branches,
          active_branch_id: reply.active_branch_id,
          lab_id: reply.lab_id,
          is_lab_coordinator: reply.is_lab_coordinator,
          fan_out: reply.fan_out,
          compression_events: reply.new_compression_event
            ? [...prev.compression_events, reply.new_compression_event]
            : prev.compression_events,
        }
      })
      setChats((prev) =>
        prev.map((chat) => (chat.id === chatId ? { ...chat, title: reply.title } : chat)),
      )
      if (reply.project) upsertProject(reply.project)
      if (reply.profile) setProfile(reply.profile)
    } catch (err) {
      setChatError(
        err instanceof ApiError ? err.message : 'Непредвиденная ошибка.',
      )
    } finally {
      endPending(key)
      setPendingSends((prev) => {
        if (!prev.has(key)) return prev
        const next = new Map(prev)
        next.delete(key)
        return next
      })
    }
  }

  async function handleForceCompress(): Promise<boolean> {
    if (!activeChatId) return false
    const chatId = activeChatId
    const key = chatKey(chatId)
    beginPending(key)
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
      endPending(key)
    }
  }

  async function handleSetContextStrategy(strategy: ContextStrategy) {
    if (!activeChatId) return
    const chatId = activeChatId
    try {
      const updated = await setContextStrategy(chatId, strategy)
      setActiveChat((prev) => (prev && prev.id === chatId ? updated : prev))
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  // The actual PATCH already happened inside TaskSection (context-popup.tsx)
  // before this is called — this just merges the already-saved result into
  // activeChat, same as every other "server told us the fresh state" path.
  function handleUpdateTask(task: TaskMemory) {
    setActiveChat((prev) => (prev ? { ...prev, task } : prev))
  }

  async function handleCreateCheckpoint(label: string) {
    if (!activeChatId) return
    const chatId = activeChatId
    try {
      const updated = await createCheckpoint(chatId, label)
      setActiveChat((prev) => (prev && prev.id === chatId ? updated : prev))
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleCreateBranch(checkpointIndex: number, fromBranchId: string, label: string) {
    if (!activeChatId) return
    const chatId = activeChatId
    try {
      const updated = await createBranch(chatId, checkpointIndex, fromBranchId, label)
      setActiveChat((prev) => (prev && prev.id === chatId ? updated : prev))
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleSelectBranch(branchId: string) {
    if (!activeChatId) return
    const chatId = activeChatId
    try {
      const updated = await setActiveBranch(chatId, branchId)
      setActiveChat((prev) => (prev && prev.id === chatId ? withPendingMessage(updated) : prev))
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleNewLab(label: string) {
    setMode('chat')
    try {
      const { chats: created } = await createLab(label)
      setChats((prev) => [...prev, ...created])
      const coordinator = created.find((chat) => chat.is_lab_coordinator) ?? created[0]
      const detail = await getChat(coordinator.id)
      setActiveChatId(coordinator.id)
      setActiveChat(detail)
      setChatError(null)
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleDeleteLab(labId: string) {
    try {
      await deleteLab(labId)
      const remaining = chats.filter((chat) => chat.lab_id !== labId)
      setChats(remaining)

      if (activeChat?.lab_id !== labId) return

      if (remaining.length === 0) {
        await handleNewChat()
        return
      }
      const next = remaining[remaining.length - 1]
      const detail = await getChat(next.id)
      setActiveChatId(next.id)
      setActiveChat(withPendingMessage(detail))
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleNewProject(name: string) {
    try {
      const project = await createProject(name)
      upsertProject(project)
      setChatError(null)
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleDeleteProject(projectId: string) {
    try {
      await deleteProject(projectId)
      setProjects((prev) => prev.filter((p) => p.id !== projectId))
      setChats((prev) =>
        prev.map((chat) => (chat.project_id === projectId ? { ...chat, project_id: undefined } : chat)),
      )
      setActiveChat((prev) =>
        prev && prev.project_id === projectId ? { ...prev, project_id: undefined, project: undefined } : prev,
      )
      if (projectMemoryPopupId === projectId) setProjectMemoryPopupId(null)
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    }
  }

  async function handleJumpToCoordinator() {
    if (!activeChat?.lab_id) return
    const coordinator = chats.find(
      (chat) => chat.lab_id === activeChat.lab_id && chat.is_lab_coordinator,
    )
    if (!coordinator) return
    await handleSelectChat(coordinator.id)
  }

  async function handleAnalyzeLab() {
    if (!activeChat?.lab_id) return
    const chatId = activeChat.id
    const labId = activeChat.lab_id
    const key = chatKey(chatId)
    beginPending(key)
    setChatError(null)
    try {
      const reply = await analyzeLab(labId)
      setActiveChat((prev) =>
        prev && prev.id === chatId
          ? {
              ...prev,
              messages: [
                ...prev.messages,
                {
                  role: 'assistant' as const,
                  content: reply.reply,
                  created_at: reply.assistant_message_created_at,
                  usage: reply.usage ?? undefined,
                  is_lab_analysis: true,
                },
              ],
              cumulative_total_tokens: reply.cumulative_total_tokens,
              cumulative_cost_usd: reply.cumulative_cost_usd,
            }
          : prev,
      )
    } catch (err) {
      setChatError(err instanceof ApiError ? err.message : 'Непредвиденная ошибка.')
    } finally {
      endPending(key)
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
            projects={projects}
            activeChatId={mode === 'chat' ? activeChatId : null}
            activeDemo={activeDemo}
            onNewChat={() => handleNewChat()}
            onNewLab={handleNewLab}
            onNewProject={handleNewProject}
            onNewChatInProject={handleNewChat}
            onSelectChat={handleSelectChat}
            onSelectDemo={(demo) => setMode(demo)}
            onCollapse={() => setSidebarCollapsed(true)}
            onRenameChat={handleRenameChat}
            onDeleteChat={handleDeleteChat}
            onDeleteLab={handleDeleteLab}
            onDeleteProject={handleDeleteProject}
            onOpenProjectMemory={setProjectMemoryPopupId}
            onOpenProfile={() => {
              setProfilePopupEditing(false)
              setProfilePopupOpen(true)
            }}
          />
        )}

        {projectMemoryPopupId &&
          (() => {
            const project = projects.find((p) => p.id === projectMemoryPopupId)
            return project ? (
              <ProjectMemoryPopup project={project} onClose={() => setProjectMemoryPopupId(null)} onUpdate={upsertProject} />
            ) : null
          })()}

        {profilePopupOpen && profile && (
          <ProfilePopup
            profile={profile}
            initialEditing={profilePopupEditing}
            onClose={() => setProfilePopupOpen(false)}
            onUpdate={setProfile}
          />
        )}

        {interviewOpen && profile && (
          <OnboardingInterview
            profile={profile}
            onClose={() => setInterviewOpen(false)}
            onUpdate={setProfile}
          />
        )}

        {mode === 'chat' ? (
          <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
            <ChatPanel
              chatId={activeChatId ?? ''}
              messages={activeChat?.messages ?? []}
              estimate={activeChat?.estimate ?? null}
              isSending={pendingKeys.has(chatKey(activeChatId ?? '', activeChat?.active_branch_id))}
              error={chatError}
              onSend={handleSendMessage}
              onForceCompress={handleForceCompress}
              contextStrategy={activeChat?.context_strategy ?? 'sliding_window'}
              onSetContextStrategy={handleSetContextStrategy}
              lastContextTokens={activeChat?.last_context_tokens ?? 0}
              cumulativeTotalTokens={activeChat?.cumulative_total_tokens ?? 0}
              cumulativeCostUsd={activeChat?.cumulative_cost_usd}
              contextTokenLimit={activeChat?.context_token_limit ?? 0}
              historyKeepLastN={activeChat?.history_keep_last_n ?? 10}
              summarizedMessageCount={activeChat?.summarized_message_count ?? 0}
              rawMessageCount={activeChat?.raw_message_count ?? 0}
              compressionEvents={activeChat?.compression_events ?? []}
              facts={activeChat?.facts}
              task={activeChat?.task}
              onUpdateTask={handleUpdateTask}
              branches={activeChat?.branches ?? []}
              checkpoints={activeChat?.checkpoints ?? []}
              activeBranchId={activeChat?.active_branch_id}
              onCreateCheckpoint={handleCreateCheckpoint}
              onCreateBranch={handleCreateBranch}
              onSelectBranch={handleSelectBranch}
              isLabChat={Boolean(activeChat?.lab_id)}
              isLabCoordinator={Boolean(activeChat?.is_lab_coordinator)}
              onAnalyzeLab={handleAnalyzeLab}
              coordinatorTitle={
                activeChat?.lab_id
                  ? chats.find(
                      (chat) => chat.lab_id === activeChat.lab_id && chat.is_lab_coordinator,
                    )?.title
                  : undefined
              }
              onJumpToCoordinator={handleJumpToCoordinator}
              fanOut={activeChat?.fan_out}
              onJumpToChat={handleSelectChat}
              profile={profile ?? undefined}
              onOpenProfile={() => {
                setProfilePopupEditing(true)
                setProfilePopupOpen(true)
              }}
              onStartInterview={() => setInterviewOpen(true)}
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
