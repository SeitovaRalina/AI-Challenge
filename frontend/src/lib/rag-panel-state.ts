import { useEffect, useRef, useState } from 'react'

import type {
  ChunkStrategy,
  EvalProgress,
  EvalRunResult,
  RagAnswer,
  RetrievalEvalResult,
} from '@/lib/api'

// RagPanelState is the «Похожие задачи» screen's own state — held in
// module scope (below), not component state, so switching to another
// screen (e.g. "Перейти в чат" from a source) and back doesn't lose a
// result that took minutes to compute. Only a full page reload clears it.
export interface RagPanelState {
  strategy: ChunkStrategy
  // Day 23: rerank/rewrite/filter, shared by the compare tool and the
  // eval run — a visible, deliberate choice (see StrategySelect's doc),
  // not a hidden default.
  rerankEnabled: boolean
  rewriteEnabled: boolean
  minScore: number
  compareQuestion: string
  compareLoading: boolean
  compareError: string | null
  compareNoRag: RagAnswer | null
  compareRag: RagAnswer | null
  // compareRagImproved: the 3rd column, only populated when rerank/rewrite/
  // minScore was active for that run — null means "not run with
  // enhancements", not "enhancements found nothing".
  compareRagImproved: RagAnswer | null
  evalRunning: boolean
  evalProgress: EvalProgress | null
  evalError: string | null
  evalResult: EvalRunResult | null
  retrievalLoading: boolean
  retrievalError: string | null
  retrievalResult: RetrievalEvalResult | null
  retrievalDetailsOpen: boolean
}

const defaultState: RagPanelState = {
  strategy: 'structural',
  rerankEnabled: false,
  rewriteEnabled: false,
  minScore: 0,
  compareQuestion: '',
  compareLoading: false,
  compareError: null,
  compareNoRag: null,
  compareRag: null,
  compareRagImproved: null,
  evalRunning: false,
  evalProgress: null,
  evalError: null,
  evalResult: null,
  retrievalLoading: false,
  retrievalError: null,
  retrievalResult: null,
  retrievalDetailsOpen: false,
}

let cache: RagPanelState = { ...defaultState }

// useRagPanelState reads/writes the module-level cache above. A background
// run (streamRunEval etc.) started before unmount keeps updating cache
// even while this screen isn't mounted — update() just skips the (now
// stale) setState call once unmounted, cache itself is always current.
export function useRagPanelState(): [RagPanelState, (patch: Partial<RagPanelState>) => void] {
  const [state, setState] = useState(cache)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    setState(cache) // picks up anything that changed while unmounted
    return () => {
      mountedRef.current = false
    }
  }, [])

  function update(patch: Partial<RagPanelState>) {
    cache = { ...cache, ...patch }
    if (mountedRef.current) setState(cache)
  }

  return [state, update]
}
