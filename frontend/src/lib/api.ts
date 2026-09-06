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

export interface ControlledResult {
  estimate: Estimate
  length_chars: number
  latency_ms: number
}

export interface Comparison {
  task: string
  uncontrolled: RawResult
  controlled: ControlledResult
}

export class ApiError extends Error {}

async function postJson<T>(url: string, task: string): Promise<T> {
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task }),
  })

  const body = await response.json().catch(() => null)

  if (!response.ok) {
    const message =
      body && typeof body.error === 'string'
        ? body.error
        : `Запрос завершился с ошибкой ${response.status}`
    throw new ApiError(message)
  }

  return body as T
}

export function estimateTask(task: string): Promise<Estimate> {
  return postJson<Estimate>('/api/estimate', task)
}

export function compareFormats(task: string): Promise<Comparison> {
  return postJson<Comparison>('/api/compare', task)
}
