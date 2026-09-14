import { useState } from 'react'
import { GitBranch, MapPin } from 'lucide-react'

import { cn } from 'cn'
import type { BranchSummary, Checkpoint } from '@/lib/api'

interface BranchToolbarProps {
  branches: BranchSummary[]
  checkpoints: Checkpoint[]
  activeBranchId: string
  onSelectBranch: (branchId: string) => void
  onCreateCheckpoint: (label: string) => void
  onCreateBranch: (checkpointIndex: number, fromBranchId: string, label: string) => void
}

// BranchToolbar sits above the message list only while the chat's strategy
// is branching: tabs to switch which branch is active, plus the two actions
// that make branching demoable — a checkpoint marks "fork me from here",
// and a new branch is forked from one. Calling "new branch" twice from the
// same checkpoint with different labels is how "2 branches from one point"
// happens.
export function BranchToolbar({
  branches,
  checkpoints,
  activeBranchId,
  onSelectBranch,
  onCreateCheckpoint,
  onCreateBranch,
}: BranchToolbarProps) {
  const [branchFormOpen, setBranchFormOpen] = useState(false)

  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-border px-6 py-2">
      {branches.map((branch) => (
        <button
          key={branch.id}
          type="button"
          onClick={() => onSelectBranch(branch.id)}
          className={cn(
            'rounded-full border px-3 py-1 text-xs transition-colors',
            branch.id === activeBranchId
              ? 'border-primary bg-primary/10 text-primary'
              : 'border-border text-muted-foreground hover:bg-accent hover:text-foreground',
          )}
        >
          {branch.label}
          <span className="ml-1.5 text-[10px] text-muted-foreground/80">
            {branch.message_count}
          </span>
        </button>
      ))}

      <div className="ml-1 flex items-center gap-1.5">
        <button
          type="button"
          onClick={() => {
            const label = `checkpoint ${checkpoints.length + 1}`
            onCreateCheckpoint(label)
          }}
          title="Отметить текущий момент диалога как checkpoint"
          className="flex items-center gap-1 rounded-full border border-border px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <MapPin className="h-3 w-3" />
          Checkpoint
        </button>
        <button
          type="button"
          onClick={() => setBranchFormOpen((open) => !open)}
          disabled={checkpoints.length === 0}
          title={checkpoints.length === 0 ? 'Сначала поставьте checkpoint' : 'Создать ветку от checkpoint'}
          className="flex items-center gap-1 rounded-full border border-border px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-40"
        >
          <GitBranch className="h-3 w-3" />
          Новая ветка
        </button>
      </div>

      {branchFormOpen && (
        <NewBranchForm
          checkpoints={checkpoints}
          onCreate={(checkpointIndex, fromBranchId, label) => {
            onCreateBranch(checkpointIndex, fromBranchId, label)
            setBranchFormOpen(false)
          }}
          onCancel={() => setBranchFormOpen(false)}
        />
      )}
    </div>
  )
}

function NewBranchForm({
  checkpoints,
  onCreate,
  onCancel,
}: {
  checkpoints: Checkpoint[]
  onCreate: (checkpointIndex: number, fromBranchId: string, label: string) => void
  onCancel: () => void
}) {
  const [checkpointKey, setCheckpointKey] = useState(
    `${checkpoints[checkpoints.length - 1].branch_id}:${checkpoints[checkpoints.length - 1].index}`,
  )
  const [label, setLabel] = useState('')

  function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    const trimmed = label.trim()
    if (!trimmed) return
    const [fromBranchId, indexStr] = checkpointKey.split(':')
    onCreate(Number(indexStr), fromBranchId, trimmed)
  }

  return (
    <form
      onSubmit={handleSubmit}
      className="flex w-full items-center gap-1.5 pt-1.5"
    >
      <select
        value={checkpointKey}
        onChange={(event) => setCheckpointKey(event.target.value)}
        className="rounded-md border border-border bg-transparent px-2 py-1 text-xs text-foreground"
      >
        {checkpoints.map((checkpoint, index) => (
          <option key={index} value={`${checkpoint.branch_id}:${checkpoint.index}`}>
            {checkpoint.label} ({checkpoint.branch_id}, сообщ. {checkpoint.index})
          </option>
        ))}
      </select>
      <input
        autoFocus
        value={label}
        onChange={(event) => setLabel(event.target.value)}
        placeholder="Название ветки"
        className="min-w-0 flex-1 rounded-md border border-border bg-transparent px-2 py-1 text-xs text-foreground outline-none placeholder:text-muted-foreground focus-visible:border-ring"
      />
      <button
        type="submit"
        disabled={!label.trim()}
        className="rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground disabled:opacity-40"
      >
        Создать
      </button>
      <button
        type="button"
        onClick={onCancel}
        className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:text-foreground"
      >
        Отмена
      </button>
    </form>
  )
}
