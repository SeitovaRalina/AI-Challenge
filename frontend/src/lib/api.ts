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

export class ApiError extends Error {}

export async function estimateTask(task: string): Promise<Estimate> {
  const response = await fetch('/api/estimate', {
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

  return body as Estimate
}
