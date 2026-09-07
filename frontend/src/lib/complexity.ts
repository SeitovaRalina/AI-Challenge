import type { Complexity } from '@/lib/api'

export const complexityStyles: Record<Complexity, string> = {
  low: 'bg-primary/10 text-primary',
  medium: 'bg-warning/20 text-warning-foreground',
  high: 'bg-destructive/10 text-destructive',
}

export const complexityLabels: Record<Complexity, string> = {
  low: 'низкая',
  medium: 'средняя',
  high: 'высокая',
}
