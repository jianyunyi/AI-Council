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
}

export function stateMeta(state: string): { label: string; tone: string } {
  return REQUIREMENT_STATE_META[state as RequirementState] ?? { label: state, tone: 'muted' }
}

/** Retry is offered for the two failure terminals. */
export function canRetry(req: Pick<RequirementView, 'state'>): boolean {
  return req.state === 'proposal_failed' || req.state === 'failed'
}

/** Cancel is offered while a requirement is still in flight or proposed. */
export function canCancel(req: Pick<RequirementView, 'state'>): boolean {
  return req.state === 'dispatched' || req.state === 'proposing' || req.state === 'proposed'
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
