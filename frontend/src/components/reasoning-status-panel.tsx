import { Frown, Meh, Smile } from 'lucide-react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { ReasoningReaction } from '@/components/reasoning-comparison'
import type { ReasoningComparison as ReasoningComparisonData } from '@/lib/api'
import { cn } from 'cn'

interface ReasoningStatusPanelProps {
  status: 'idle' | 'loading' | 'error' | 'success'
  comparison: ReasoningComparisonData | null
  reaction: ReasoningReaction
}

// Sits to the right of the reasoning task form (same spot EstimateResult and
// CompareOptionsForm use on the other two tabs): a short summary of how well
// the four strategies agreed and whether a human has signed off on that yet.
export function ReasoningStatusPanel({
  status,
  comparison,
  reaction,
}: ReasoningStatusPanelProps) {
  if (status !== 'success' || !comparison) return null

  const agreement = comparison.verdict.differs
    ? { label: 'Модели разошлись во мнениях', tone: 'text-warning-foreground' }
    : { label: 'Модели сошлись во мнении', tone: 'text-primary' }

  const approval =
    reaction === 'like'
      ? {
          label: 'Вы подтвердили оценку',
          tone: 'bg-primary/10 text-primary',
          Icon: Smile,
        }
      : reaction === 'dislike'
        ? {
            label: 'Вы отклонили оценку',
            tone: 'bg-destructive/10 text-destructive',
            Icon: Frown,
          }
        : {
            label: 'Ждёт вашей оценки',
            tone: 'bg-muted text-muted-foreground',
            Icon: Meh,
          }

  return (
    <Card className="h-full">
      <CardHeader>
        <CardTitle className="text-sm font-medium text-muted-foreground">
          Итог сравнения
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-1 items-center justify-between gap-4">
        <div className="flex flex-col gap-1.5">
          <p className={cn('text-sm font-medium', agreement.tone)}>
            {agreement.label}
          </p>
          <p className="text-sm text-muted-foreground">{approval.label}</p>
        </div>
        <span
          className={cn(
            'flex aspect-square h-[65%] shrink-0 items-center justify-center self-center rounded-2xl',
            approval.tone,
          )}
        >
          <approval.Icon className="size-full p-3" />
        </span>
      </CardContent>
    </Card>
  )
}
