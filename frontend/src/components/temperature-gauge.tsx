interface TemperatureGaugeProps {
  values: number[]
  max?: number
}

// A horizontal thermometer marking the compared sampling temperatures along
// a cold-to-hot gradient, so the low/balanced/high split reads at a glance
// before the detailed cards below spell out what each one produced.
export function TemperatureGauge({ values, max = 1.5 }: TemperatureGaugeProps) {
  return (
    <div className="flex flex-col gap-3 px-1">
      <div
        className="relative h-2 w-full rounded-full"
        style={{
          background: 'linear-gradient(to right, #3b82f6, #f59e0b, #ef4444)',
        }}
      >
        {values.map((value) => (
          <span
            key={value}
            className="absolute top-1/2 size-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-background bg-foreground shadow"
            style={{ left: `${(value / max) * 100}%` }}
          />
        ))}
      </div>
      <div className="relative h-4 text-xs font-mono text-muted-foreground">
        {values.map((value) => (
          <span
            key={value}
            className="absolute -translate-x-1/2"
            style={{ left: `${(value / max) * 100}%` }}
          >
            {value}
          </span>
        ))}
      </div>
    </div>
  )
}
