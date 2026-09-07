// Fixed cold-to-hot colors, shared with the analysis table so each
// temperature reads as the same color everywhere on the page.
export const TEMPERATURE_COLORS: Record<string, string> = {
  '0': '#3b82f6',
  '0.7': '#f59e0b',
  '1.2': '#ef4444',
}

interface TemperatureGaugeProps {
  values: number[]
  max?: number
}

// A horizontal thermometer marking the compared sampling temperatures along
// a cold-to-hot gradient. Rendered on a fixed light surface regardless of the
// app's theme — a gradient this literal (real "cold/hot" colors) reads as an
// illustration rather than themed UI, and stays legible either way.
export function TemperatureGauge({ values, max = 1.5 }: TemperatureGaugeProps) {
  return (
    <div className="rounded-xl border border-slate-200 bg-white px-5 py-5">
      <div
        className="relative h-2 w-full rounded-full"
        style={{
          background: `linear-gradient(to right, ${TEMPERATURE_COLORS['0']}, ${TEMPERATURE_COLORS['0.7']}, ${TEMPERATURE_COLORS['1.2']})`,
        }}
      >
        {values.map((value) => (
          <span
            key={value}
            className="absolute top-1/2 size-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white bg-slate-900 shadow"
            style={{ left: `${(value / max) * 100}%` }}
          />
        ))}
      </div>
      <div className="relative mt-3 h-4 text-xs font-mono text-slate-500">
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
