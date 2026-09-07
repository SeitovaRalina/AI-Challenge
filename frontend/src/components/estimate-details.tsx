import type { Estimate } from '@/lib/api'

interface EstimateDetailsProps {
  estimate: Estimate
}

export function EstimateDetails({ estimate }: EstimateDetailsProps) {
  return (
    <>
      <div>
        <p className="text-xs font-medium tracking-wide text-muted-foreground">
          Оценка трудозатрат
        </p>
        <p className="font-mono text-3xl font-medium tabular-nums text-foreground">
          {estimate.estimated_hours_min}–{estimate.estimated_hours_max}{' '}
          <span className="text-lg font-normal text-muted-foreground">
            часов
          </span>
        </p>
      </div>

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
    </>
  )
}
