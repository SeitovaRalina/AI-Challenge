import { useState } from 'react'
import { ChevronDown, ListChecks } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { complexityLabels, complexityStyles } from '@/lib/complexity'
import type { Estimate } from '@/lib/api'

interface ChatEstimateCardProps {
  estimate: Estimate
}

function capitalize(text: string) {
  if (!text) return text
  return text.charAt(0).toUpperCase() + text.slice(1)
}

// The headline (hours + complexity) stays pinned in CardHeader so it's always
// visible; only CardContent (risks/assumptions, which can grow long) scrolls.
export function ChatEstimateCard({ estimate }: ChatEstimateCardProps) {
  const [subtasksOpen, setSubtasksOpen] = useState(false)

  return (
    <Card className="h-full gap-0">
      <CardHeader className="flex-shrink-0 border-b border-border pb-4">
        <CardTitle>{capitalize(estimate.category)}</CardTitle>
        <p className="mt-1 text-sm text-muted-foreground">{estimate.summary}</p>
        <div className="mt-3 flex items-center gap-3">
          <p className="font-mono text-3xl font-medium tabular-nums text-foreground">
            {estimate.estimated_hours_min}–{estimate.estimated_hours_max}{' '}
            <span className="text-lg font-normal text-muted-foreground">
              часов
            </span>
          </p>
          <Badge className={complexityStyles[estimate.complexity]}>
            {complexityLabels[estimate.complexity]}
          </Badge>
        </div>
      </CardHeader>

      <CardContent className="flex-1 overflow-y-auto pt-4">
        <div className="flex flex-col gap-5">
          {estimate.subtasks && estimate.subtasks.length > 0 && (
            <div>
              <button
                type="button"
                onClick={() => setSubtasksOpen((open) => !open)}
                className="flex w-full items-center justify-between rounded-md border border-primary/20 bg-primary/10 px-3 py-2 text-sm font-medium text-primary transition-colors hover:bg-primary/15"
              >
                <span className="flex items-center gap-2">
                  <ListChecks className="h-4 w-4" />
                  Подзадачи ({estimate.subtasks.length})
                </span>
                <ChevronDown
                  className={`h-4 w-4 transition-transform ${subtasksOpen ? 'rotate-180' : ''}`}
                />
              </button>
              {subtasksOpen && (
                <ol className="mt-2 flex flex-col gap-2.5">
                  {estimate.subtasks.map((subtask, index) => (
                    <li key={subtask.name} className="flex gap-2 rounded-md border border-border p-2.5">
                      <span className="flex-shrink-0 font-mono text-xs text-muted-foreground">
                        {index + 1}.
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-baseline justify-between gap-3">
                          <p className="text-sm font-medium text-foreground">{subtask.name}</p>
                          <p className="flex-shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
                            {subtask.estimated_hours_min}–{subtask.estimated_hours_max} ч
                          </p>
                        </div>
                        <p className="mt-1 text-xs text-muted-foreground">
                          {subtask.description}
                        </p>
                      </div>
                    </li>
                  ))}
                </ol>
              )}
            </div>
          )}

          {estimate.risks.length > 0 && (
            <div>
              <p className="mb-1.5 text-xs font-medium tracking-wide text-muted-foreground">
                Риски
              </p>
              <ul className="flex flex-col gap-1 text-sm text-foreground">
                {estimate.risks.map((risk) => (
                  <li key={risk} className="border-l-2 border-destructive/40 pl-2">
                    {risk}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {estimate.assumptions.length > 0 && (
            <div>
              <p className="mb-1.5 text-xs font-medium tracking-wide text-muted-foreground">
                Допущения
              </p>
              <ul className="flex flex-col gap-1 text-sm text-foreground">
                {estimate.assumptions.map((assumption) => (
                  <li key={assumption} className="border-l-2 border-border pl-2">
                    {assumption}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <p className="text-xs text-muted-foreground">
            Только предварительная AI-оценка — не основана на вашей личной
            истории работы. Уточняйте детали в чате, чтобы её пересчитать.
          </p>
        </div>
      </CardContent>
    </Card>
  )
}
