import type { TurnStreamEvent } from '@/lib/turn-progress'

export type Complexity = 'low' | 'medium' | 'high'

export interface Subtask {
  name: string
  description: string
  estimated_hours_min: number
  estimated_hours_max: number
}

export interface Estimate {
  summary: string
  category: string
  complexity: Complexity
  estimated_hours_min: number
  estimated_hours_max: number
  risks: string[]
  assumptions: string[]
  subtasks?: Subtask[]
}

export interface RawResult {
  text: string
  length_chars: number
  latency_ms: number
}

export interface ResolvedCompareOptions {
  max_tokens: number
  max_items: number
  temperature: number
  use_stop_instruction: boolean
}

export interface ControlledResult {
  estimate: Estimate
  length_chars: number
  latency_ms: number
  options: ResolvedCompareOptions
}

export interface Comparison {
  task: string
  uncontrolled: RawResult
  controlled: ControlledResult
}

export interface CompareOptions {
  maxTokens: number
  maxItems: number
  temperature: number
  useStopInstruction: boolean
}

export interface ExpertTurn {
  role: string
  text: string
}

export type ReasoningStrategy =
  | 'direct'
  | 'step_by_step'
  | 'meta_prompt'
  | 'expert_panel'

export interface ReasoningStep {
  reasoning?: string
  panel?: ExpertTurn[]
  generated_prompt?: string
  estimate: Estimate
  length_chars: number
  latency_ms: number
}

export interface ReasoningVerdict {
  differs: boolean
  most_accurate: ReasoningStrategy
  rationale: string
}

export interface ReasoningComparison {
  task: string
  direct: ReasoningStep
  step_by_step: ReasoningStep
  meta_prompt: ReasoningStep
  expert_panel: ReasoningStep
  verdict: ReasoningVerdict
}

export interface TemperatureResult {
  temperature: number
  text: string
  length_chars: number
  latency_ms: number
}

export interface TemperatureAnalysis {
  temperature: number
  accuracy: string
  creativity: string
  diversity: string
  best_for: string[]
}

export interface TemperatureVerdict {
  analysis: TemperatureAnalysis[]
  summary: string
}

export interface TemperatureComparison {
  task: string
  results: TemperatureResult[]
  verdict: TemperatureVerdict
}

export type ModelTier = 'weak' | 'medium' | 'strong'

export interface ModelResult {
  tier: ModelTier
  model_id: string
  name: string
  description: string
  docs_url: string
  text: string
  latency_ms: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cost_usd?: number
}

export interface ModelQualityAssessment {
  tier: ModelTier
  quality: string
}

export interface ModelVerdict {
  quality: ModelQualityAssessment[]
  summary: string
}

export interface ModelComparison {
  task: string
  results: ModelResult[]
  verdict: ModelVerdict
}

export class ApiError extends Error {}

async function request<T>(
  url: string,
  init?: { method?: string; body?: Record<string, unknown> },
): Promise<T> {
  const response = await fetch(url, {
    method: init?.method ?? 'GET',
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
    body: init?.body ? JSON.stringify(init.body) : undefined,
  })

  const responseBody = await response.json().catch(() => null)

  if (!response.ok) {
    const message =
      responseBody && typeof responseBody.error === 'string'
        ? responseBody.error
        : `Запрос завершился с ошибкой ${response.status}`
    throw new ApiError(message)
  }

  return responseBody as T
}

function postJson<T>(url: string, body: Record<string, unknown>): Promise<T> {
  return request<T>(url, { method: 'POST', body })
}

export function estimateTask(task: string): Promise<Estimate> {
  return postJson<Estimate>('/api/estimate', { task })
}

export function compareFormats(
  task: string,
  options: CompareOptions,
): Promise<Comparison> {
  return postJson<Comparison>('/api/compare', {
    task,
    max_tokens: options.maxTokens,
    max_items: options.maxItems,
    temperature: options.temperature,
    use_stop_instruction: options.useStopInstruction,
  })
}

export function compareControlled(
  task: string,
  options: CompareOptions,
): Promise<ControlledResult> {
  return postJson<ControlledResult>('/api/compare/controlled', {
    task,
    max_tokens: options.maxTokens,
    max_items: options.maxItems,
    temperature: options.temperature,
    use_stop_instruction: options.useStopInstruction,
  })
}

export function compareReasoning(task: string): Promise<ReasoningComparison> {
  return postJson<ReasoningComparison>('/api/reasoning', { task })
}

export function compareTemperatures(task: string): Promise<TemperatureComparison> {
  return postJson<TemperatureComparison>('/api/temperature', { task })
}

export function compareModels(task: string): Promise<ModelComparison> {
  return postJson<ModelComparison>('/api/models', { task })
}

export interface ChatSummary {
  id: string
  title: string
  created_at: string
  lab_id?: string
  project_id?: string
  context_strategy: ContextStrategy
  is_lab_coordinator?: boolean
  task_state: TaskState
}

// TaskState is day 13's task state machine: the process a chat's task is
// going through (not what the agent knows about it — that's TaskMemory).
// Stage is always server-computed, never user-set directly — done is the
// one exception, via setTaskDone.
export type TaskStage = 'intake' | 'clarifying' | 'estimated' | 'done'

export interface TaskState {
  stage: TaskStage
  step: string
  expected_action: string
}

// TaskMemory is day 11's working-memory layer: this one chat's own task
// data (goal, agreed constraints, answers gathered so far) — scoped to this
// chat, distinct from the long-term memory a Project shares across chats.
export interface TaskMemory {
  goal: string
  constraints: string[]
  clarifying_answers: Record<string, string>
}

// UserProfile is day 12's personalization layer: a single global profile
// (there is only ever one) applied to every non-lab chat, distinct from
// TaskMemory (this chat) and Project (this project's chats).
export interface UserProfile {
  name: string
  stack: string[]
  style: string
  format: string
  constraints: string[]
}

export interface TokenUsage {
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cost_usd?: number
}

export interface AgentMessage {
  role: 'user' | 'assistant'
  content: string
  created_at: string
  usage?: TokenUsage
  is_lab_analysis?: boolean
  // Set only on assistant messages from a real turn — the stage as of
  // right after that reply, durable across reload (see task_state.go).
  task_state?: TaskState
  // invariant_conflict (day 14): set straight from this turn's own LLM
  // call — the project invariants (if any) this reply's proposal would
  // have violated, verbatim; empty/absent when there was no conflict.
  invariant_conflict?: string[]
  // invariant_diff (day 14): set after the turn, once the async
  // invariants-extraction side-call finishes — new invariants this
  // exchange caused the agent to record. Distinct from invariant_conflict
  // (violating EXISTING invariants vs. detecting new ones).
  invariant_diff?: InvariantDiff
  // tool_calls (day 17): MCP tool calls the agent made while producing
  // this assistant message, in order.
  tool_calls?: ToolCallRecord[]
}

export type ActivityKind = 'commit' | 'pr_opened' | 'pr_merged' | 'review' | 'issue_comment' | 'meeting'

// ActivityEvent is the GitHub Activity MCP server's normalized unit of work.
export interface ActivityEvent {
  id: string
  source: string
  kind: ActivityKind
  repo: string
  title: string
  url: string
  occurred_at: string
  author: string
  ref?: string
  // ends_at (day 20): only set for kind "meeting" — every other kind is
  // instantaneous.
  ends_at?: string
}

// ToolCallRecord is one MCP tool call as stored on an assistant message.
// result is the tool's structured output — for get_activity, events trimmed
// to 50 (events_omitted counts the rest).
export interface ToolCallRecord {
  server: string
  server_name: string
  tool: string
  arguments: Record<string, unknown>
  ok: boolean
  error?: string
  duration_ms: number
  result?: {
    events?: ActivityEvent[]
    events_omitted?: number
    counts?: Partial<Record<ActivityKind, number>>
    repos?: unknown[]
    warnings?: string[]
    [key: string]: unknown
  }
}

export interface CompressionEvent {
  fold_end: number
  folded_count: number
  summarized_total: number
  summary: string
  created_at: string
  manual: boolean
}

// ContextStrategy mirrors the backend's ContextStrategy: sliding_window and
// sticky_facts are the cheap, always-on strategies; branching is the odd one
// out (its "messages" are the ACTIVE branch's, not the whole chat's);
// rolling_summary is day 9's own strategy, kept as a 4th option outside labs.
export type ContextStrategy =
  | 'sliding_window'
  | 'sticky_facts'
  | 'branching'
  | 'rolling_summary'
  | 'coordinator'

export interface Checkpoint {
  index: number
  branch_id: string
  label: string
  created_at: string
}

export interface BranchSummary {
  id: string
  label: string
  parent_id?: string
  fork_index: number
  message_count: number
  created_at: string
}

// One strategy chat's progress through the coordinator's most recent
// fan-out — only ever present on the coordinator's own ChatDetail/AgentReply.
export interface FanOutStatus {
  chat_id: string
  strategy: Exclude<ContextStrategy, 'coordinator'>
  status: 'pending' | 'done' | 'failed'
  error?: string
  started_at: string
}

// Fields shared by ChatDetail and AgentReply: every strategy's own current
// state, always populated from live chat state regardless of which strategy
// is actually active (empty/zero for the ones that don't apply).
interface StrategyState {
  context_strategy: ContextStrategy
  history_keep_last_n: number
  summarized_message_count: number
  raw_message_count: number
  facts?: Record<string, string>
  branches?: BranchSummary[]
  active_branch_id?: string
  lab_id?: string
  is_lab_coordinator?: boolean
  fan_out?: FanOutStatus[]
  project_id?: string
  project?: Project
  task?: TaskMemory
  profile?: UserProfile
  task_state: TaskState
}

export interface ChatDetail extends StrategyState {
  id: string
  title: string
  created_at: string
  messages: AgentMessage[]
  estimate: Estimate | null
  last_context_tokens: number
  cumulative_total_tokens: number
  cumulative_cost_usd?: number
  context_token_limit: number
  compression_events: CompressionEvent[]
  checkpoints?: Checkpoint[]
}

export interface AgentReply extends StrategyState {
  reply: string
  estimate: Estimate | null
  title: string
  usage: TokenUsage | null
  user_message_created_at: string
  assistant_message_created_at: string
  last_context_tokens: number
  cumulative_total_tokens: number
  cumulative_cost_usd?: number
  context_token_limit: number
  new_compression_event?: CompressionEvent | null
  // Mirror what got stamped onto this turn's own assistant message — see
  // AgentMessage's own doc comments.
  invariant_conflict?: string[]
  invariant_diff?: InvariantDiff
  tool_calls?: ToolCallRecord[]
}

export interface ForceCompressResult {
  compressed: boolean
  chat: ChatDetail
}

export interface Lab {
  id: string
  label: string
  chat_ids: string[]
  coordinator_chat_id: string
  created_at: string
}

// Project is day 11's long-term memory layer: a folder of chats that share
// known_stack/notes across every chat in it, outliving any single chat.
export interface Project {
  id: string
  name: string
  known_stack?: string[]
  notes?: string[]
  // invariants (day 14) — hard constraints the assistant must never
  // violate, extracted automatically with a much stricter bar than
  // known_stack/notes, and only ever shrunk through updateProjectInvariants.
  invariants?: string[]
  created_at: string
}

// InvariantDiff describes how a project's invariants changed as a result of
// one turn (day 14) — stamped onto the assistant message that caused it.
export interface InvariantDiff {
  added?: string[]
  removed?: string[]
}

export function listChats(): Promise<ChatSummary[]> {
  return request<ChatSummary[]>('/api/agent/chats')
}

export function createChat(projectId?: string): Promise<ChatSummary> {
  return postJson<ChatSummary>('/api/agent/chats', projectId ? { project_id: projectId } : {})
}

export function getChat(chatId: string): Promise<ChatDetail> {
  return request<ChatDetail>(`/api/agent/chats/${chatId}`)
}

export function deleteChat(chatId: string): Promise<void> {
  return request<void>(`/api/agent/chats/${chatId}`, { method: 'DELETE' })
}

export function renameChat(chatId: string, title: string): Promise<ChatSummary> {
  return request<ChatSummary>(`/api/agent/chats/${chatId}`, {
    method: 'PATCH',
    body: { title },
  })
}

// streamAgentMessage sends one chat message over the streaming endpoint
// (day 17): the final AgentReply resolves the promise, and onEvent is called
// while the turn runs — tool routing, MCP tool calls, the reply as soon as
// it's ready (see backend/turn_stream.go). Errors reject with the backend's
// Russian messages. interview flags this turn as part of day 12's
// onboarding interview — the backend injects a turn-only system message
// steering the reply away from task estimation, invisible to task/profile
// extraction (see interviewModeSystemPrompt in backend/agent_turn.go).
export async function streamAgentMessage(
  chatId: string,
  message: string,
  interview: boolean | undefined,
  onEvent: (event: TurnStreamEvent) => void,
): Promise<AgentReply> {
  const response = await fetch(`/api/agent/chats/${chatId}/messages/stream`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ message, ...(interview ? { interview: true } : {}) }),
  })
  // Validation failures (unknown chat, empty message) come back before the
  // stream starts, as an ordinary JSON error.
  if (!response.ok || !response.body) {
    const body = await response.json().catch(() => null)
    throw new ApiError(
      body && typeof body.error === 'string'
        ? body.error
        : `Запрос завершился с ошибкой ${response.status}`,
    )
  }

  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
  let buffer = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += value
    let boundary: number
    while ((boundary = buffer.indexOf('\n\n')) >= 0) {
      const frame = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      let event = 'message'
      const dataLines: string[] = []
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim()
        else if (line.startsWith('data:')) dataLines.push(line.slice(5).trimStart())
      }
      if (dataLines.length === 0) continue
      const data = JSON.parse(dataLines.join('\n'))
      switch (event) {
        case 'done':
          return data as AgentReply
        case 'error':
          throw new ApiError(typeof data.error === 'string' ? data.error : 'Непредвиденная ошибка.')
        case 'answer':
          onEvent({ type: 'answer', reply: data as AgentReply })
          break
        case 'routing':
        case 'answering':
        case 'tool_call_started':
        case 'tool_call_finished':
          onEvent({ type: event, ...data } as TurnStreamEvent)
          break
      }
    }
  }
  throw new ApiError('Соединение прервалось до получения ответа — обновите чат, ответ мог сохраниться.')
}

export function forceCompress(chatId: string): Promise<ForceCompressResult> {
  return postJson<ForceCompressResult>(`/api/agent/chats/${chatId}/compress`, {})
}

export function setContextStrategy(
  chatId: string,
  strategy: ContextStrategy,
): Promise<ChatDetail> {
  return request<ChatDetail>(`/api/agent/chats/${chatId}/strategy`, {
    method: 'PATCH',
    body: { strategy },
  })
}

export function createCheckpoint(chatId: string, label: string): Promise<ChatDetail> {
  return postJson<ChatDetail>(`/api/agent/chats/${chatId}/checkpoints`, { label })
}

export function createBranch(
  chatId: string,
  checkpointIndex: number,
  fromBranchId: string,
  label: string,
): Promise<ChatDetail> {
  return postJson<ChatDetail>(`/api/agent/chats/${chatId}/branches`, {
    checkpoint_index: checkpointIndex,
    from_branch_id: fromBranchId,
    label,
  })
}

export function setActiveBranch(chatId: string, branchId: string): Promise<ChatDetail> {
  return request<ChatDetail>(`/api/agent/chats/${chatId}/active-branch`, {
    method: 'PATCH',
    body: { branch_id: branchId },
  })
}

export interface CreateLabResult {
  lab: Lab
  chats: ChatSummary[]
}

export function createLab(label: string): Promise<CreateLabResult> {
  return postJson<CreateLabResult>('/api/labs', { label })
}

export function analyzeLab(labId: string): Promise<AgentReply> {
  return postJson<AgentReply>(`/api/labs/${labId}/analyze`, {})
}

export function deleteLab(labId: string): Promise<void> {
  return request<void>(`/api/labs/${labId}`, { method: 'DELETE' })
}

export function listProjects(): Promise<Project[]> {
  return request<Project[]>('/api/projects')
}

export function createProject(name: string): Promise<Project> {
  return postJson<Project>('/api/projects', { name })
}

export function getProject(projectId: string): Promise<Project> {
  return request<Project>(`/api/projects/${projectId}`)
}

export function deleteProject(projectId: string): Promise<void> {
  return request<void>(`/api/projects/${projectId}`, { method: 'DELETE' })
}

// updateProjectMemory lets the user manually add, edit, or delete a
// project's long-term memory (the explicit counterpart to the automatic
// per-turn extraction) — overwrites known_stack/notes wholesale.
export function updateProjectMemory(
  projectId: string,
  knownStack: string[],
  notes: string[],
): Promise<Project> {
  return request<Project>(`/api/projects/${projectId}/memory`, {
    method: 'PATCH',
    body: { known_stack: knownStack, notes },
  })
}

// updateProjectInvariants lets the user manually add, edit, or remove a
// project's invariants (day 14) — the only way to shrink the list, since the
// automatic per-turn extraction never removes an entry itself.
export function updateProjectInvariants(
  projectId: string,
  invariants: string[],
): Promise<Project> {
  return request<Project>(`/api/projects/${projectId}/invariants`, {
    method: 'PATCH',
    body: { invariants },
  })
}

// updateChatTask lets the user manually add, edit, or delete a chat's own
// working memory — the explicit counterpart to the automatic per-turn
// extraction.
export function updateChatTask(chatId: string, task: TaskMemory): Promise<TaskMemory> {
  return request<TaskMemory>(`/api/agent/chats/${chatId}/task`, {
    method: 'PATCH',
    body: { ...task },
  })
}

export function getProfile(): Promise<UserProfile> {
  return request<UserProfile>('/api/profile')
}

// updateProfile lets the user manually add, edit, or delete the global
// profile — the explicit counterpart to the automatic per-turn extraction,
// and the only path for name (the model never infers it).
export function updateProfile(profile: UserProfile): Promise<UserProfile> {
  return request<UserProfile>('/api/profile', {
    method: 'PATCH',
    body: { ...profile },
  })
}

// setTaskDone is day 13's manual accept/reopen action — the only way a chat
// moves between "estimated" and "done" (the model never decides this).
export function setTaskDone(chatId: string, done: boolean): Promise<TaskState> {
  return request<TaskState>(`/api/agent/chats/${chatId}/task-state`, {
    method: 'PATCH',
    body: { done },
  })
}

export interface McpToolParam {
  name: string
  type?: string
  description?: string
  required: boolean
}

export interface McpTool {
  name: string
  title?: string
  description?: string
  read_only: boolean
  params: McpToolParam[]
}

export interface McpListResult {
  protocol_version: string
  server_name: string
  server_version?: string
  instructions?: string
  tools: McpTool[]
  duration_ms: number
}

export type McpErrorKind = 'no_token' | 'unauthorized' | 'unreachable' | 'timeout' | 'protocol'

export interface McpConnection {
  status: 'connected' | 'error'
  checked_at: string
  result?: McpListResult
  error_kind?: McpErrorKind
  error_status?: number
  error?: string
}

// McpServer is day 16's public MCP server as the backend reports it —
// whether a token is configured (never the token itself), and the outcome
// of the last connect attempt, if any.
export interface McpServer {
  id: string
  name: string
  // http: remote server at url; stdio: local subprocess started by the
  // backend (command is display-only).
  transport: 'http' | 'stdio'
  url?: string
  command?: string
  // own: implemented by this product (day 17+), vs a public third-party one.
  own: boolean
  token_env?: string
  read_only: boolean
  token_set: boolean
  last_connection?: McpConnection
}

export function listMcpServers(): Promise<McpServer[]> {
  return request<McpServer[]>('/api/mcp/servers')
}

// connectMcpServer runs initialize + tools/list on the backend. A server-side
// failure (bad token, unreachable) still resolves — it comes back as
// last_connection.status === 'error', not as a rejected promise.
export function connectMcpServer(id: string): Promise<McpServer> {
  return request<McpServer>(`/api/mcp/servers/${id}/connect`, { method: 'POST' })
}

// ---- Day 18: background activity collector («Активность» screen) ----

export type ActivityPeriod = 'today' | '7d' | '30d'

export interface CollectorStep {
  server: string
  tool: string
  detail?: string
  ok: boolean
  error?: string
  duration_ms: number
  summary?: string
}

export interface CollectorRun {
  trigger: 'startup' | 'schedule' | 'manual'
  started_at: string
  finished_at?: string
  status: 'running' | 'ok' | 'error'
  error?: string
  backfill: boolean
  window_since?: string
  window_until?: string
  fetched: number
  inserted: number
  duplicates: number
  warnings: string[]
  steps: CollectorStep[]
  digest?: {
    today: DigestTotals
    week: DigestTotals
  }
}

export interface RepoTotal {
  repo: string
  total: number
}

export interface DigestTotals {
  from: string
  to: string
  total: number
  counts: Partial<Record<ActivityKind, number>>
  active_days: number
  top_repos: RepoTotal[]
}

export interface SyncState {
  source: string
  synced_since: string
  synced_until: string
  updated_at: string
}

export interface CollectorStatus {
  enabled: boolean
  disabled_reason?: string
  interval_seconds: number
  next_run_at?: string
  current?: CollectorRun
  runs: CollectorRun[]
  sync?: { sources: SyncState[]; total_events: number; database: string }
  sync_error?: string
}

export function getActivityStatus(): Promise<CollectorStatus> {
  return request<CollectorStatus>('/api/activity/status')
}

// triggerActivityCollect starts a manual run and returns the status right
// after — the run itself keeps going in the background; poll getActivityStatus
// to watch it finish.
export function triggerActivityCollect(): Promise<CollectorStatus> {
  return request<CollectorStatus>('/api/activity/collect', { method: 'POST' })
}

export interface RepoDigest {
  repo: string
  total: number
  counts: Partial<Record<ActivityKind, number>>
}

export interface DayDigest {
  date: string
  weekday: string
  total: number
  counts: Partial<Record<ActivityKind, number>>
}

export interface ActivityDigest {
  from: string
  to: string
  timezone: string
  coverage: SyncState[]
  covered: boolean
  warnings?: string[]
  total: number
  counts: Partial<Record<ActivityKind, number>>
  by_repo: RepoDigest[]
  by_day: DayDigest[]
  active_days: number
  // meetings_count (day 20): same as counts.meeting, as its own field — a
  // meeting has no repository, so it's excluded from by_repo.
  meetings_count: number
  first_event_at?: string
  last_event_at?: string
}

export function getActivityDigest(period: ActivityPeriod): Promise<ActivityDigest> {
  return request<ActivityDigest>(`/api/activity/digest?period=${period}`)
}

export interface ActivityEventsResult {
  from: string
  to: string
  timezone: string
  coverage: SyncState[]
  covered: boolean
  warnings?: string[]
  events: ActivityEvent[]
  total: number
  counts: Partial<Record<ActivityKind, number>>
  truncated: boolean
}

export function getActivityEvents(
  period: ActivityPeriod,
  filters?: { repo?: string; kind?: ActivityKind },
): Promise<ActivityEventsResult> {
  const params = new URLSearchParams({ period })
  if (filters?.repo) params.set('repo', filters.repo)
  if (filters?.kind) params.set('kind', filters.kind)
  return request<ActivityEventsResult>(`/api/activity/events?${params}`)
}

// ---- Day 19: session pipeline + Analytics screen ----

export type AnalyticsPeriod = '7d' | '30d' | '90d'

export interface AnalyticsKpi {
  // total_hours (day 20): work + meeting hours, deduplicated — a minute
  // inside a meeting is never also counted as work.
  total_hours: number
  meeting_hours: number
  active_days: number
  sessions_count: number
  meetings_count: number
  repo_count: number
  project_count: number
}

export interface ProjectHours {
  project: string
  hours: number
}

export interface DayHours {
  date: string
  weekday: string
  work_hours: number
  meeting_hours: number
  total_hours: number
}

// CommitTypeCount is one Conventional Commits type ("feat(scope): ..." ->
// "feat", scope dropped) and how many commits in the period carried it.
// "other" covers commits with no recognized prefix.
export interface CommitTypeCount {
  type: string
  count: number
}

export interface HeatmapCell {
  weekday: number // 0 = Sunday .. 6 = Saturday (JS Date convention too)
  weekday_label: string
  hour: number
  // Average minutes worked in this hour, on this weekday, per such day in
  // the period (0-60) — not a sum across every week, which would grow with
  // the period's length and could exceed 60.
  avg_minutes: number
}

export interface WeekTrend {
  week_start: string
  week_end: string
  hours: number
}

export interface Analytics {
  from: string
  to: string
  timezone: string
  kpi: AnalyticsKpi
  time_by_project: ProjectHours[]
  by_day: DayHours[]
  commit_types: CommitTypeCount[]
  heatmap: HeatmapCell[]
  weekly_trend: WeekTrend[]
  warnings?: string[]
}

export function getAnalytics(period: AnalyticsPeriod): Promise<Analytics> {
  return request<Analytics>(`/api/analytics?period=${period}`)
}

export interface RepoProject {
  repo: string
  project?: string
}

export function listRepoProjects(): Promise<{ repos: RepoProject[] }> {
  return request<{ repos: RepoProject[] }>('/api/analytics/repos')
}

// setRepoProject is opt-in merging: repo is its own project by default
// (its bare name), and this only matters when you want several repos to
// share one label ("" clears it). Takes effect on the very next
// getAnalytics call — no session rebuild needed, the project is resolved
// at read time.
export function setRepoProject(repo: string, project: string): Promise<RepoProject> {
  // repo is "owner/repo" — the backend's {repo...} wildcard route expects
  // that literal slash as a path separator, not percent-encoded.
  return request<RepoProject>(`/api/analytics/repos/${repo}`, {
    method: 'PATCH',
    body: { project },
  })
}

// ---- Day 20: meetings, day timeline, weekly summary ----

export type TimelineKind = 'work' | 'meeting'

export interface TimelineBlock {
  start: string
  end: string
  kind: TimelineKind
  repo?: string
  project?: string
  // titles (plural): several when overlapping meetings were merged into one
  // block.
  titles?: string[]
}

export interface DayTimeline {
  date: string
  weekday: string
  timezone: string
  blocks: TimelineBlock[]
  warnings?: string[]
}

// getDayTimeline is the Gantt-style day view's data source: one day as
// non-overlapping work/meeting blocks, meetings already given priority
// where they overlapped work. date defaults to today when omitted.
export function getDayTimeline(date?: string): Promise<DayTimeline> {
  return request<DayTimeline>(`/api/analytics/timeline${date ? `?date=${date}` : ''}`)
}

export interface WeeklyPeriodFacts {
  from: string
  to: string
  hours: number
  meeting_hours: number
  active_days: number
  top_project?: string
  merged_prs: number
}

export interface WeeklySummary {
  week_start: string
  week_end: string
  generated_at: string
  text: string
  facts: {
    this_week: WeeklyPeriodFacts
    last_week: WeeklyPeriodFacts
  }
}

// getWeeklySummary returns the most recently generated summary, or null if
// none exists yet (nothing generated on demand, and the Friday-evening
// schedule hasn't fired yet).
export function getWeeklySummary(): Promise<WeeklySummary | null> {
  return request<WeeklySummary | null>('/api/analytics/weekly-summary')
}

// generateWeeklySummary triggers generation now (the same thing the Friday
// schedule does automatically) and returns the fresh result.
export function generateWeeklySummary(): Promise<WeeklySummary> {
  return postJson<WeeklySummary>('/api/analytics/weekly-summary', {})
}

// ---- Day 21: RAG index over historical estimate sessions («Похожие задачи» screen) ----

export type ChunkStrategy = 'fixed_size' | 'structural'

export interface StrategyStats {
  strategy: ChunkStrategy
  chunk_count: number
  total_chars: number
  avg_chars: number
}

export interface IndexStatus {
  exists: boolean
  built_at?: string
  source_count: number
  embed_model?: string
  strategies?: StrategyStats[]
}

// getRagIndex reports the current index's state without rebuilding it —
// exists: false (not an error) before the first reindex.
export function getRagIndex(): Promise<IndexStatus> {
  return request<IndexStatus>('/api/rag/index')
}

// reindexRag rebuilds the index from every chat that produced a real
// estimate, via Ollama embeddings — can take a while on the first run.
export function reindexRag(): Promise<IndexStatus> {
  return request<IndexStatus>('/api/rag/reindex', { method: 'POST' })
}

// ---- Day 22: RAG query — question -> retrieval -> LLM, RAG vs no-RAG ----

export interface RetrievedChunk {
  session_id: string
  title: string
  section: string
  chunk_id: string
  score: number
  text: string
}

// Citation is day 24's anti-hallucination check — a verbatim excerpt the
// model claims backs a statement in its answer. verified is computed by
// the backend, never by the model itself: true only when text is an exact
// (whitespace-normalized) substring of the chunk chunk_id actually names.
export interface Citation {
  chunk_id: string
  text: string
  verified: boolean
}

export interface RagAnswer {
  mode: 'rag' | 'no_rag'
  strategy?: ChunkStrategy
  answer: string
  retrieved?: RetrievedChunk[]
  rewritten_question?: string
  candidate_count?: number
  filtered_count?: number
  citations?: Citation[]
  // Day 24: true when the backend skipped the LLM call entirely because
  // nothing retrieved cleared the hard relevance floor — answer is then
  // the fixed "не знаю" text, not a model-generated one.
  low_confidence?: boolean
}

// RagQueryOptions is day 23's rerank/rewrite/filter — all off reproduces
// day 22's exact behavior.
export interface RagQueryOptions {
  rerank?: boolean
  rewrite?: boolean
  minScore?: number
}

// queryRag answers one question in either mode. strategy is required when
// mode is 'rag'.
export function queryRag(
  question: string,
  mode: 'rag' | 'no_rag',
  strategy?: ChunkStrategy,
  options?: RagQueryOptions,
): Promise<RagAnswer> {
  return postJson<RagAnswer>('/api/rag/query', {
    question,
    mode,
    strategy,
    rerank: options?.rerank ?? false,
    rewrite: options?.rewrite ?? false,
    min_score: options?.minScore ?? 0,
  })
}

export interface EvalQuestion {
  question: string
  expectation: string
  expected_sources: string[]
}

// getEvalQuestions returns the day-22 control-question set.
export function getEvalQuestions(): Promise<EvalQuestion[]> {
  return request<EvalQuestion[]>('/api/rag/eval')
}

export interface EvalQuestionResult extends EvalQuestion {
  no_rag_answer: string
  rag_answer: string
  retrieved: RetrievedChunk[]
  citations: Citation[]
  low_confidence?: boolean
  expected_source_hit: boolean
  expected_source_check: boolean
  // Present only when the run had rerank/rewrite/filtering on.
  improved_rag_answer?: string
  improved_retrieved?: RetrievedChunk[]
  improved_citations?: Citation[]
  improved_low_confidence?: boolean
}

export interface EvalRunResult {
  strategy: ChunkStrategy
  results: EvalQuestionResult[]
}

// runEval answers all 10 control questions in both modes (RAG on
// strategy, plus no-RAG) — this is day 22's actual "сравнение качества"
// deliverable. Slow: ~20 LLM calls. options is day 23's rerank/rewrite/
// filter, applied uniformly to the RAG side of every question.
export function runEval(strategy: ChunkStrategy, options?: RagQueryOptions): Promise<EvalRunResult> {
  return postJson<EvalRunResult>('/api/rag/eval/run', {
    strategy,
    rerank: options?.rerank ?? false,
    rewrite: options?.rewrite ?? false,
    min_score: options?.minScore ?? 0,
  })
}

export interface EvalProgress {
  step: number
  total: number
  question: string
  stage: 'no_rag' | 'rag' | 'rag_improved'
}

// streamRunEval is runEval with progress: same request, same final
// EvalRunResult, reporting each of the 20 calls as it completes (see
// backend/rag_api.go's evalRunStreamHandler) — a multi-minute run needs a
// real progress bar, not a bare spinner.
export async function streamRunEval(
  strategy: ChunkStrategy,
  onProgress: (progress: EvalProgress) => void,
  options?: RagQueryOptions,
): Promise<EvalRunResult> {
  const response = await fetch('/api/rag/eval/run/stream', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({
      strategy,
      rerank: options?.rerank ?? false,
      rewrite: options?.rewrite ?? false,
      min_score: options?.minScore ?? 0,
    }),
  })
  if (!response.ok || !response.body) {
    const body = await response.json().catch(() => null)
    throw new ApiError(
      body && typeof body.error === 'string'
        ? body.error
        : `Запрос завершился с ошибкой ${response.status}`,
    )
  }

  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
  let buffer = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += value
    let boundary: number
    while ((boundary = buffer.indexOf('\n\n')) >= 0) {
      const frame = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      let event = 'message'
      const dataLines: string[] = []
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim()
        else if (line.startsWith('data:')) dataLines.push(line.slice(5).trimStart())
      }
      if (dataLines.length === 0) continue
      const data = JSON.parse(dataLines.join('\n'))
      if (event === 'done') return data as EvalRunResult
      if (event === 'error') {
        throw new ApiError(typeof data.error === 'string' ? data.error : 'Непредвиденная ошибка.')
      }
      if (event === 'progress') onProgress(data as EvalProgress)
    }
  }
  throw new ApiError('Соединение прервалось до получения результата.')
}

export interface RetrievalQuestionResult {
  question: string
  checked: boolean
  hits: Partial<Record<ChunkStrategy, boolean>>
}

export interface RetrievalHitRate {
  strategy: ChunkStrategy
  hits: number
  total: number
  hit_rate: number
}

export interface RetrievalEvalResult {
  questions: RetrievalQuestionResult[]
  strategies: RetrievalHitRate[]
}

// runRetrievalEval is the secondary, no-LLM comparison: does top-K
// retrieval surface the expected source under each chunking strategy.
export function runRetrievalEval(): Promise<RetrievalEvalResult> {
  return postJson<RetrievalEvalResult>('/api/rag/eval/retrieval', {})
}
