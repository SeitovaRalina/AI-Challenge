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
