// Requirement presentation helpers (Part B P6). Kept pure for testing.
// Lives in the flat web/lib layout next to mentions.ts/stream.ts (the
// Quick draft assumed a lib/groups/ directory that the landed P5
// frontend does not have).
import type { RequirementState, RequirementView } from './api/groups'
import { parseMentions } from './mentions'

export const REQUIREMENT_STATE_META: Record<RequirementState, { label: string; tone: 'info' | 'progress' | 'ok' | 'danger' | 'muted' }> = {
  dispatched: { label: '已派发', tone: 'info' },
  proposing: { label: '正在生成方案', tone: 'progress' },
  proposed: { label: '已提案 · 待审核', tone: 'ok' },
  proposal_failed: { label: '提案失败', tone: 'danger' },
  failed: { label: '派发失败', tone: 'danger' },
  cancelled: { label: '已取消', tone: 'muted' },
  in_review: { label: '评审中', tone: 'progress' },
  changes_requested: { label: '评审要求修改', tone: 'danger' },
  review_passed: { label: '评审通过 · 待人工批准', tone: 'ok' },
  rejected: { label: '评审否决', tone: 'danger' },
  awaiting_approval: { label: '待审批', tone: 'ok' },
  approved: { label: '已批准', tone: 'progress' },
  executing: { label: '执行中', tone: 'progress' },
  executed: { label: '已执行', tone: 'ok' },
  execution_failed: { label: '执行失败', tone: 'danger' },
  result_submitted: { label: '结果已提交', tone: 'ok' },
  result_in_review: { label: '结果评审中', tone: 'progress' },
  completed: { label: '已完成', tone: 'ok' },
}

export function stateMeta(state: string): { label: string; tone: string } {
  return REQUIREMENT_STATE_META[state as RequirementState] ?? { label: state, tone: 'muted' }
}

/** Retry is offered for the two failure terminals. */
export function canRetry(req: Pick<RequirementView, 'state'>): boolean {
  return req.state === 'proposal_failed' || req.state === 'failed'
}

/** Cancel is offered while a requirement is in flight, proposed, under
 * review, awaiting revision, review-passed, or in the P8 approval /
 * push-down pipeline (mirrors the backend). */
export function canCancel(req: Pick<RequirementView, 'state'>): boolean {
  return (
    req.state === 'dispatched' ||
    req.state === 'proposing' ||
    req.state === 'proposed' ||
    req.state === 'in_review' ||
    req.state === 'changes_requested' ||
    req.state === 'review_passed' ||
    req.state === 'awaiting_approval' ||
    req.state === 'approved' ||
    req.state === 'executing' ||
    // P9: execution no longer ends the requirement — the result is
    // still collected, submitted and reviewed, and all of it can be
    // cancelled (mirrors the backend).
    req.state === 'executed' ||
    req.state === 'execution_failed' ||
    req.state === 'result_submitted' ||
    req.state === 'result_in_review'
  )
}

/** The full Part B pipeline as stepper stops (P9: 8 stops). */
export const REQUIREMENT_STEPS = ['派发', '提案', '评审', '人工批准', '下推', '执行', '结果评审', '完成'] as const

/** Index of the stepper stop a state currently sits at. */
export function activeStopIndex(state: string): number {
  switch (state) {
    case 'dispatched':
    case 'failed':
    case 'cancelled':
      return 0
    case 'proposing':
    case 'proposed':
    case 'proposal_failed':
      return 1
    case 'in_review':
    case 'changes_requested':
    case 'rejected':
      return 2
    case 'review_passed':
    case 'awaiting_approval':
      return 3
    case 'approved':
      return 4
    case 'executing':
    case 'executed':
    case 'execution_failed':
      return 5
    case 'result_submitted':
    case 'result_in_review':
      return 6
    case 'completed':
      return 7
    default:
      return 0
  }
}

/** The last stop a live (non-terminal) requirement can reach. */
export const lastLiveStop = REQUIREMENT_STEPS.length - 1

/** Completion badge text: a failed execution that reviewers accepted
 * is never presented as an unqualified success. */
export function completionLabel(outcome?: string): string {
  switch (outcome) {
    case 'accepted':
      return '已完成'
    case 'accepted_failed':
      return '已完成（接受失败结果）'
    default:
      return ''
  }
}

export const EXECUTION_OUTCOME_LABEL: Record<string, string> = {
  succeeded: '执行成功',
  failed: '执行失败',
  cancelled: '执行已取消',
  not_executed: '未执行',
}

export function executionOutcomeLabel(outcome?: string): string {
  if (!outcome) return ''
  return EXECUTION_OUTCOME_LABEL[outcome] ?? outcome
}

export const ARTIFACT_STATUS_LABEL: Record<string, string> = {
  copied: '已复制',
  excluded_kind: '已排除（日志等类型不复制）',
  too_large: '超过大小上限，未复制',
  limit_reached: '超过数量上限，未复制',
  digest_missing: '缺少摘要，未复制',
  digest_mismatch: '摘要不一致，未复制',
  error: '复制出错',
}

export function artifactStatusLabel(status?: string): string {
  if (!status) return ''
  return ARTIFACT_STATUS_LABEL[status] ?? status
}

export const ACCEPTANCE_STATUS_LABEL: Record<string, string> = {
  met: '已达成',
  unmet: '未达成',
  unknown: '不确定',
}

/** Shown above the revise box: a result-sourced revision restarts
 * the WHOLE pipeline, including a fresh human approval (D1). */
export function reviseHint(req: Pick<RequirementView, 'revision_source'>): string {
  if (req.revision_source === 'result') {
    return '本轮修订源于结果评审：修订后将开启新的执行轮次，重新经过评审、红队检查与人工审批。'
  }
  return ''
}

/** Why the revise button is gone in changes_requested (a cap was hit). */
export function reviseBlockedNote(req: Pick<RequirementView, 'revision_source' | 'max_cycles'>): string {
  if (req.revision_source === 'result') {
    return `已达到执行轮次上限（最多 ${req.max_cycles || 2} 轮），不能再修订。`
  }
  return '已达到本轮的提案次数上限，不能再修订。'
}

/** Why the approve button is disabled (backend approve_blocked_reason). */
export const APPROVE_BLOCKED_LABEL: Record<string, string> = {
  not_owner_or_operator: '只有群主或操作员可以批准',
  state_review_passed: '等待红队检查完成',
  redteam_queued: '红队检查排队中',
  redteam_running: '红队检查进行中',
  redteam_error: '红队检查出错，等待重试',
  redteam_flagged: '红队检查未通过',
  redteam_missing: '红队检查未完成',
  plan_changed: '计划已变化，需要重新评审',
  workspace_not_ready: '执行工作区尚未就绪',
}

export function blockedReasonLabel(reason?: string): string {
  if (!reason) return ''
  if (APPROVE_BLOCKED_LABEL[reason]) return APPROVE_BLOCKED_LABEL[reason]
  if (reason.startsWith('state_')) return '当前状态不能批准'
  return reason
}

export const REVIEW_OUTCOME_LABEL: Record<string, string> = {
  passed: '通过',
  changes_requested: '要求修改',
  rejected: '否决',
  inconclusive: '无结论',
  superseded: '已被取消',
}

export const REVIEW_VERDICT_LABEL: Record<string, string> = {
  approve: '通过',
  request_changes: '要求修改',
  reject: '否决',
}

export const DECISION_STATUS_LABEL: Record<string, string> = {
  pending: '待评审',
  running: '评审中',
  decided: '已提交',
  error: '出错',
  withdrawn: '已撤回',
}

export interface DispatchPreview {
  /** Handles of mentioned AI members that will receive a requirement. */
  aiHandles: string[]
  /** True when the body is fit to send with intent "dispatch". */
  ok: boolean
  /** Why not (empty when ok). */
  problem: string
}

/**
 * Previews a dispatch body against the group's AI member handles: the
 * backend fans out one requirement per resolved AI mention and rejects
 * the message when there is none (422 no_ai_mention), so the composer
 * can warn before sending instead of failing after.
 */
export function previewDispatch(body: string, aiHandles: ReadonlySet<string>): DispatchPreview {
  const seen = new Set<string>()
  for (const tok of parseMentions(body)) {
    if (aiHandles.has(tok.handle)) seen.add(tok.handle)
  }
  const ai = Array.from(seen)
  if (ai.length === 0) {
    return { aiHandles: [], ok: false, problem: '派发需要 @ 至少一位群内的 AI 成员' }
  }
  return { aiHandles: ai, ok: true, problem: '' }
}
