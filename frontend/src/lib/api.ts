export type Complexity = 'low' | 'medium' | 'high'

export interface Estimate {
  summary: string
  category: string
  complexity: Complexity
  estimated_hours_min: number
  estimated_hours_max: number
  risks: string[]
  assumptions: string[]
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

async function postJson<T>(url: string, body: Record<string, unknown>): Promise<T> {
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
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
