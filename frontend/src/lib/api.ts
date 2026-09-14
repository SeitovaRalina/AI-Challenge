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
  context_strategy: ContextStrategy
  is_lab_coordinator?: boolean
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

export function listChats(): Promise<ChatSummary[]> {
  return request<ChatSummary[]>('/api/agent/chats')
}

export function createChat(): Promise<ChatSummary> {
  return postJson<ChatSummary>('/api/agent/chats', {})
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

export function postAgentMessage(
  chatId: string,
  message: string,
): Promise<AgentReply> {
  return postJson<AgentReply>(`/api/agent/chats/${chatId}/messages`, { message })
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
