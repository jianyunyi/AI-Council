import { describe, expect, it } from 'vitest'
import {
  activeStopIndex,
  artifactStatusLabel,
  blockedReasonLabel,
  canCancel,
  canRetry,
  completionLabel,
  executionOutcomeLabel,
  lastLiveStop,
  previewDispatch,
  REQUIREMENT_STEPS,
  reviseBlockedNote,
  reviseHint,
  stateMeta,
} from './requirements'
import type { RequirementView } from './api/groups'

const req = (state: RequirementView['state']): Pick<RequirementView, 'state'> => ({ state })

describe('requirement helpers', () => {
  it('labels every P6 state', () => {
    expect(stateMeta('dispatched').label).toBe('已派发')
    expect(stateMeta('proposing').label).toBe('正在生成方案')
    expect(stateMeta('proposed').label).toBe('已提案 · 待审核')
    expect(stateMeta('proposal_failed').tone).toBe('danger')
    expect(stateMeta('failed').tone).toBe('danger')
    expect(stateMeta('cancelled').tone).toBe('muted')
    expect(stateMeta('something_else').label).toBe('something_else')
  })

  it('gates retry and cancel by state', () => {
    expect(canRetry(req('proposal_failed'))).toBe(true)
    expect(canRetry(req('failed'))).toBe(true)
    expect(canRetry(req('proposed'))).toBe(false)
    expect(canCancel(req('dispatched'))).toBe(true)
    expect(canCancel(req('proposing'))).toBe(true)
    expect(canCancel(req('proposed'))).toBe(true)
    expect(canCancel(req('cancelled'))).toBe(false)
    expect(canCancel(req('failed'))).toBe(false)
  })

  it('labels the P7 review states and extends cancel to them', () => {
    expect(stateMeta('in_review').label).toBe('评审中')
    expect(stateMeta('changes_requested').label).toBe('评审要求修改')
    expect(stateMeta('review_passed').label).toBe('评审通过 · 待人工批准')
    expect(stateMeta('rejected').tone).toBe('danger')
    expect(canCancel(req('in_review'))).toBe(true)
    expect(canCancel(req('changes_requested'))).toBe(true)
    expect(canCancel(req('review_passed'))).toBe(true)
    expect(canCancel(req('rejected'))).toBe(false)
    expect(canRetry(req('in_review'))).toBe(false)
  })

  it('labels the P8 approval/execution states and extends cancel to them', () => {
    expect(stateMeta('awaiting_approval').label).toBe('待审批')
    expect(stateMeta('approved').label).toBe('已批准')
    expect(stateMeta('executing').label).toBe('执行中')
    expect(stateMeta('executed').label).toBe('已执行')
    expect(stateMeta('execution_failed').label).toBe('执行失败')
    expect(stateMeta('execution_failed').tone).toBe('danger')
    expect(canCancel(req('awaiting_approval'))).toBe(true)
    expect(canCancel(req('approved'))).toBe(true)
    expect(canCancel(req('executing'))).toBe(true)
    // P9 supersedes the two P8 assertions below: execution no longer
    // ends the requirement, so cancel stays available (see the P9
    // block) — executed/execution_failed are cancellable now.
    expect(canCancel(req('executed'))).toBe(true)
    expect(canCancel(req('execution_failed'))).toBe(true)
  })

  it('labels the P9 result states and walks the 8-stop stepper', () => {
    expect(stateMeta('result_submitted').label).toBe('结果已提交')
    expect(stateMeta('result_in_review').label).toBe('结果评审中')
    expect(stateMeta('completed').label).toBe('已完成')
    expect(canCancel(req('result_submitted'))).toBe(true)
    expect(canCancel(req('result_in_review'))).toBe(true)
    expect(canCancel(req('completed'))).toBe(false)
    expect(REQUIREMENT_STEPS).toHaveLength(8)
    expect(lastLiveStop).toBe(7)
    expect(activeStopIndex('dispatched')).toBe(0)
    expect(activeStopIndex('proposed')).toBe(1)
    expect(activeStopIndex('in_review')).toBe(2)
    expect(activeStopIndex('awaiting_approval')).toBe(3)
    expect(activeStopIndex('approved')).toBe(4)
    expect(activeStopIndex('executed')).toBe(5)
    expect(activeStopIndex('execution_failed')).toBe(5)
    expect(activeStopIndex('result_submitted')).toBe(6)
    expect(activeStopIndex('result_in_review')).toBe(6)
    expect(activeStopIndex('completed')).toBe(7)
  })

  it('labels P9 outcomes, artifacts and revision hints', () => {
    expect(completionLabel('accepted')).toBe('已完成')
    expect(completionLabel('accepted_failed')).toBe('已完成（接受失败结果）')
    expect(completionLabel(undefined)).toBe('')
    expect(executionOutcomeLabel('succeeded')).toBe('执行成功')
    expect(executionOutcomeLabel('not_executed')).toBe('未执行')
    expect(artifactStatusLabel('copied')).toBe('已复制')
    expect(artifactStatusLabel('excluded_kind')).toContain('日志')
    expect(artifactStatusLabel('digest_mismatch')).toContain('未复制')
    const resultReq = { revision_source: 'result' as const, max_cycles: 2 }
    expect(reviseHint(resultReq)).toContain('人工审批')
    expect(reviseHint({ revision_source: 'proposal' as const })).toBe('')
    expect(reviseBlockedNote(resultReq)).toContain('执行轮次上限')
    expect(reviseBlockedNote({ revision_source: 'proposal' as const, max_cycles: 2 })).toContain('提案次数上限')
  })

  it('explains why approval is blocked', () => {
    expect(blockedReasonLabel('not_owner_or_operator')).toBe('只有群主或操作员可以批准')
    expect(blockedReasonLabel('state_review_passed')).toBe('等待红队检查完成')
    expect(blockedReasonLabel('redteam_running')).toBe('红队检查进行中')
    expect(blockedReasonLabel('redteam_error')).toBe('红队检查出错，等待重试')
    expect(blockedReasonLabel('plan_changed')).toBe('计划已变化，需要重新评审')
    expect(blockedReasonLabel('workspace_not_ready')).toBe('执行工作区尚未就绪')
    expect(blockedReasonLabel('state_executing')).toBe('当前状态不能批准')
    expect(blockedReasonLabel('')).toBe('')
    expect(blockedReasonLabel(undefined)).toBe('')
  })

  it('previews dispatch fan-out from AI mentions only', () => {
    const ai = new Set(['claude-x', 'gpt-y'])
    expect(previewDispatch('@claude-x plan this', ai)).toEqual({ aiHandles: ['claude-x'], ok: true, problem: '' })
    expect(previewDispatch('@claude-x and @gpt-y both', ai).aiHandles).toEqual(['claude-x', 'gpt-y'])
    expect(previewDispatch('@claude-x @claude-x twice', ai).aiHandles).toEqual(['claude-x'])
    const none = previewDispatch('@bob plan this', ai)
    expect(none.ok).toBe(false)
    expect(none.problem).toContain('AI')
    expect(previewDispatch('no mentions at all', ai).ok).toBe(false)
    // Mentions inside code fences do not count (mirrors the backend parser).
    expect(previewDispatch('```\n@claude-x\n```', ai).ok).toBe(false)
  })
})
