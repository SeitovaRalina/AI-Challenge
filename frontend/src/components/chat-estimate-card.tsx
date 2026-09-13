import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { complexityLabels, complexityStyles } from '@/lib/complexity'
import type { Estimate } from '@/lib/api'

interface ChatEstimateCardProps {
  estimate: Estimate
}

// The headline (hours + complexity) stays pinned in CardHeader so it's always
// visible; only CardContent (risks/assumptions, which can grow long) scrolls.
export function ChatEstimateCard({ estimate }: ChatEstimateCardProps) {
  return (
    <Card className="h-full gap-0">
      <CardHeader className="flex-shrink-0 flex-row items-start justify-between gap-3 border-b border-border pb-4">
        <div>
          <CardTitle>{estimate.category}</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">{estimate.summary}</p>
          <p className="mt-3 font-mono text-3xl font-medium tabular-nums text-foreground">
            {estimate.estimated_hours_min}–{estimate.estimated_hours_max}{' '}
            <span className="text-lg font-normal text-muted-foreground">
              часов
            </span>
          </p>
        </div>
        <Badge className={complexityStyles[estimate.complexity]}>
          {complexityLabels[estimate.complexity]}
        </Badge>
      </CardHeader>

      <CardContent className="flex-1 overflow-y-auto pt-4">
        <div className="flex flex-col gap-5">
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
