'use client'

import { useCallback, useEffect, useState } from 'react'
import {
  cancelRequirement,
  getRequirement,
  GroupApiError,
  retryRequirement,
  type MemberView,
  type RequirementView,
} from '@/lib/api/groups'
import { canCancel, canRetry, stateMeta } from '@/lib/requirements'
import ApprovalSection from './ApprovalSection'
import PlanView from './PlanView'
import ReviewSection from './ReviewSection'

const REVIEW_STATES: ReadonlySet<string> = new Set(['proposed', 'in_review', 'changes_requested', 'review_passed', 'rejected'])
const P8_STATES: ReadonlySet<string> = new Set(['awaiting_approval', 'approved', 'executing', 'executed', 'execution_failed'])

/**
 * Live card for one requirement, rendered in place of a dispatch_card /
 * proposal message body. It fetches the requirement on mount and whenever
 * `bump` changes (GroupSpace bumps on requirement.state_changed stream
 * events), and offers retry/cancel to the requester or a group manager.
 */
export default function RequirementCard({
  groupId,
  requirementId,
  bump,
  myMemberId,
  canManage,
  members,
  onReviewChat,
}: {
  groupId: string
  requirementId: string
  bump: number
  myMemberId?: string
  canManage?: boolean
  /** P7: when provided (the message list always provides it), the card
   * grows the review section for review-lifecycle states. */
  members?: MemberView[]
  onReviewChat?: (req: RequirementView) => void
}) {
  const [req, setReq] = useState<RequirementView | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [showPlan, setShowPlan] = useState(false)

  const load = useCallback(async () => {
    try {
      setReq(await getRequirement(groupId, requirementId))
      setError('')
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '加载需求失败')
    }
  }, [groupId, requirementId])

  useEffect(() => {
    void load()
  }, [load, bump])

  if (!req) {
    return (
      <div className="requirement-card" data-requirement-id={requirementId}>
        {error ? (
          <span className="text-danger">{error}</span>
        ) : (
          <span className="muted">加载需求…</span>
        )}
      </div>
    )
  }

  const meta = stateMeta(req.state)
  const mine = !!myMemberId && req.requested_by_member_id === myMemberId
  const mayAct = !!(canManage || mine)

  const act = async (fn: () => Promise<RequirementView>) => {
    setBusy(true)
    setError('')
    try {
      setReq(await fn())
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '操作失败')
      void load()
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="requirement-card" data-requirement-id={req.id}>
      <div className="requirement-head">
        <span className={`badge req-tone-${meta.tone}`}>{meta.label}</span>
        <span>
          需求 → <strong>@{req.assignee_handle}</strong>
        </span>
        {req.job && req.job.state === 'queued' && <span className="muted">排队中</span>}
      </div>
      <div className="requirement-title">{req.title}</div>
      {req.failure_code && (
        <p className="requirement-failure" role="alert">
          {req.failure_code}
          {req.failure_detail ? `：${req.failure_detail}` : ''}
        </p>
      )}
      {req.proposal && (
        <div className="requirement-proposal">
          <p>{req.proposal.summary}</p>
          <button type="button" className="btn btn-sm" onClick={() => setShowPlan((v) => !v)}>
            {showPlan ? '收起方案' : '查看方案'}
          </button>
          {showPlan && <PlanView plan={req.proposal.plan} />}
        </div>
      )}
      {members && REVIEW_STATES.has(req.state) && (
        <ReviewSection
          groupId={groupId}
          requirement={req}
          members={members}
          bump={bump}
          onChanged={() => void load()}
          onReviewChat={onReviewChat ? () => onReviewChat(req) : undefined}
        />
      )}
      {(req.redteam || req.pushdown || P8_STATES.has(req.state)) && (
        <ApprovalSection groupId={groupId} requirement={req} onChanged={() => void load()} />
      )}
      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
      {mayAct && (canRetry(req) || canCancel(req)) && (
        <div className="requirement-actions">
          {canRetry(req) && (
            <button type="button" className="btn btn-sm" disabled={busy} onClick={() => void act(() => retryRequirement(groupId, req.id))}>
              重试
            </button>
          )}
          {canCancel(req) && (
            <button
              type="button"
              className="btn btn-sm text-danger"
              disabled={busy}
              onClick={() => {
                if (window.confirm('取消这个需求？')) void act(() => cancelRequirement(groupId, req.id, 'cancelled from card'))
              }}
            >
              取消需求
            </button>
          )}
        </div>
      )}
    </div>
  )
}
