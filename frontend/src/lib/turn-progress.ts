import type { AgentReply, ToolCallRecord } from '@/lib/api'

// One event of a streamed chat turn (see backend/turn_stream.go). "done" and
// "error" aren't here: they end the stream and settle its promise instead.
export type TurnStreamEvent =
  | { type: 'routing'; round: number }
  | { type: 'answering' }
  | {
      type: 'tool_call_started'
      id: string
      server: string
      server_name: string
      tool: string
      arguments: Record<string, unknown>
    }
  | { type: 'tool_call_finished'; id: string; record: ToolCallRecord }
  | { type: 'answer'; reply: AgentReply }

// LiveToolCall is a tool call as the UI sees it while the turn runs: first
// only what was asked (running), then its stored record once it returns.
export interface LiveToolCall {
  id: string
  server: string
  tool: string
  arguments: Record<string, unknown>
  record?: ToolCallRecord
}

// TurnProgress is everything known so far about a turn still in flight.
export interface TurnProgress {
  // routing: deciding whether (more) tool data is needed; answering: writing
  // the reply. Starts as routing — the server always decides first.
  phase: 'routing' | 'answering'
  calls: LiveToolCall[]
  // The final reply, once it's ready — the turn then only has memory
  // updates left before it completes.
  answer?: AgentReply
}

export const EMPTY_TURN_PROGRESS: TurnProgress = { phase: 'routing', calls: [] }

export function applyTurnEvent(progress: TurnProgress, event: TurnStreamEvent): TurnProgress {
  switch (event.type) {
    case 'routing':
      return { ...progress, phase: 'routing' }
    case 'answering':
      return { ...progress, phase: 'answering' }
    case 'tool_call_started':
      return {
        ...progress,
        calls: [
          ...progress.calls,
          { id: event.id, server: event.server, tool: event.tool, arguments: event.arguments },
        ],
      }
    case 'tool_call_finished':
      return {
        ...progress,
        calls: progress.calls.map((call) =>
          call.id === event.id ? { ...call, record: event.record } : call,
        ),
      }
    case 'answer':
      return { ...progress, answer: event.reply }
  }
}
