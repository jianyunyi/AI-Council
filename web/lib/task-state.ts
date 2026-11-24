import type {Task, TaskSummary} from './types'

/* Backend runstate -> the six visible UI states (DESIGN v2 §1.1).
   State is always encoded three ways: color token, symbol, Chinese label. */
export type UiStateKey = 'draft' | 'delib' | 'wait' | 'exec' | 'done' | 'fail' | 'cancelled'

export const STATE_META: Record<UiStateKey, {label: string; symbol: string}> = {
  draft: {label: '草稿', symbol: '○'},
  delib: {label: '协商中', symbol: '◐'},
  wait: {label: '待批准', symbol: '◆'},
  exec: {label: '执行中', symbol: '▶'},
  done: {label: '完成', symbol: '✓'},
  fail: {label: '失败', symbol: '✕'},
  cancelled: {label: '已取消', symbol: '○'},
}

const DELIB = new Set(['ANALYZING', 'PROPOSING', 'REVIEWING', 'JUDGING', 'RED_TEAM', 'REPLANNING', 'PLANNING'])
const EXEC = new Set(['EXECUTING', 'VERIFYING'])

export function uiStateOf(state: string): UiStateKey {
  if (state === 'DRAFT') return 'draft'
  if (state === 'AWAITING_APPROVAL') return 'wait'
  if (state === 'SUCCEEDED') return 'done'
  if (state === 'FAILED') return 'fail'
  if (state === 'CANCELLED') return 'cancelled'
  if (EXEC.has(state)) return 'exec'
  if (DELIB.has(state)) return 'delib'
  return 'delib'
}

export function stateLabel(state: string): string {
  return STATE_META[uiStateOf(state)].label
}

/* Deliberation stages in order (DESIGN v2 §2.4 timeline). */
export const STAGES: {key: string; label: string}[] = [
  {key: 'ANALYZING', label: '分析'},
  {key: 'PROPOSING', label: '提案'},
  {key: 'REVIEWING', label: '互评'},
  {key: 'JUDGING', label: '裁决'},
  {key: 'RED_TEAM', label: '红队'},
  {key: 'REPLANNING', label: '重新规划'},
  {key: 'AWAITING_APPROVAL', label: '待批准'},
  {key: 'EXECUTING', label: '执行'},
  {key: 'VERIFYING', label: '验证'},
]

const STAGE_ORDER = new Map(STAGES.map((stage, index) => [stage.key, index]))

export function stageIndexOf(state: string): number {
  if (state === 'DRAFT') return -1
  if (state === 'SUCCEEDED') return STAGES.length
  if (state === 'FAILED' || state === 'CANCELLED') return STAGES.length
  return STAGE_ORDER.get(state) ?? 0
}

export function diffStats(unifiedDiff: string): {add: number; del: number} {
  let add = 0
  let del = 0
  for (const line of unifiedDiff.split('\n')) {
    if (line.startsWith('+') && !line.startsWith('+++')) add++
    else if (line.startsWith('-') && !line.startsWith('---')) del++
  }
  return {add, del}
}

/* "3 文件 · +42 −7 · 2 命令" (DESIGN v2 §2.1 plan scale column). */
export function planScale(task: Task): string {
  const plan = task.plan
  if (!plan) return '计划未生成'
  let add = 0
  let del = 0
  for (const patch of plan.patches ?? []) {
    const stats = diffStats(patch.unified_diff)
    add += stats.add
    del += stats.del
  }
  const commands = (plan.commands ?? []).length + (plan.verification_commands ?? []).length
  return `${(plan.patches ?? []).length} 文件 · +${add} −${del} · ${commands} 命令`
}

export function groupTasks<T extends {state: string}>(tasks: T[]): {key: UiStateKey; tasks: T[]}[] {
  const order: UiStateKey[] = ['wait', 'exec', 'delib', 'done', 'fail', 'draft', 'cancelled']
  const groups = new Map<UiStateKey, T[]>()
  for (const task of tasks) {
    const key = uiStateOf(task.state)
    const bucket = groups.get(key) ?? []
    bucket.push(task)
    groups.set(key, bucket)
  }
  return order.filter(key => groups.has(key)).map(key => ({key, tasks: groups.get(key)!}))
}

/* Plan scale for list summaries: the list DTO carries patch/command counts
   instead of the plan body, so line-level diff stats are unavailable here. */
export function summaryScale(task: TaskSummary): string {
  if (task.state === 'DRAFT') return '计划未生成'
  return `${task.patch_count} 文件 · ${task.command_count} 命令`
}
