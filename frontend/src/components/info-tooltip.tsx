import { HelpCircle } from 'lucide-react'

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'

interface InfoTooltipProps {
  label: string
  children: React.ReactNode
}

export function InfoTooltip({ label, children }: InfoTooltipProps) {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger
          aria-label={label}
          className="text-muted-foreground hover:text-foreground"
        >
          <HelpCircle className="size-3.5" />
        </TooltipTrigger>
        <TooltipContent>{children}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
