'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  forceCloseReview,
  getReview,
  GroupApiError,
  reviseRequirement,
  startReview,
  submitReviewDecision,
  type MemberView,
  type RequirementView,
  type ReviewStateView,
  type ReviewVerdict,
} from '@/lib/api/groups'
import {
  activeStopIndex,
  DECISION_STATUS_LABEL,
  REQUIREMENT_STEPS,
  REVIEW_OUTCOME_LABEL,
  REVIEW_VERDICT_LABEL,
  reviseBlockedNote,
  reviseHint,
} from '@/lib/requirements'

/**
 * Review rounds UI for one requirement card (Part B P7). Renders only
 * for the review lifecycle states; the parent card mounts it when the
 * requirement has a proposal or is in a review state. Verdicts are
 * blind while a round is open — the API simply does not send them, so
 * there is nothing here that could leak one.
 */
export default function ReviewSection({
  groupId,
  requirement,
  members,
  bump,
  onChanged,
  onReviewChat,
}: {
  groupId: string
  requirement: RequirementView
  members: MemberView[]
  bump: number
  onChanged: () => void
  onReviewChat?: () => void
}) {
  const [review, setReview] = useState<ReviewStateView | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [verdict, setVerdict] = useState<ReviewVerdict>('approve')
  const [rationale, setRationale] = useState('')
  const [note, setNote] = useState('')

  const load = useCallback(async () => {
    try {
      setReview(await getReview(groupId, requirement.id))
      setError('')
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '加载评审失败')
    }
  }, [groupId, requirement.id])

  useEffect(() => {
    void load()
  }, [load, bump, requirement.state])

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await fn()
      await load()
      onChanged()
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '操作失败')
      void load()
    } finally {
      setBusy(false)
    }
  }

  const eligible = useMemo(
    () =>
      members.filter((m) => {
        if (m.removed_at || m.id === requirement.assignee_member_id) return false
        if (m.member_type === 'ai_model') return m.ai_status === 'active'
        return m.group_role === 'owner' || m.group_role === 'operator'
      }),
    [members, requirement.assignee_member_id],
  )

  const canStart = requirement.state === 'proposed' && (requirement.allowed_actions?.includes('start_review') ?? false)
  const openRound = review?.rounds.find((r) => r.status === 'open')
  const closedRounds = review?.rounds.filter((r) => r.status === 'closed') ?? []
  const current = activeStopIndex(requirement.state)

  return (
    <div className="review-section">
      <ol className="review-steps" aria-label="进度">
        {REQUIREMENT_STEPS.map((label, i) => (
          <li key={label} className={i < current ? 'step-done' : i === current ? 'step-current' : 'step-todo'}>
            {label}
          </li>
        ))}
      </ol>

      {canStart && (
        <div className="review-start">
          <span className="muted">选择评审者（不能是提案的 AI 本人）：</span>
          <div className="review-picker">
            {eligible.map((m) => (
              <label key={m.id} className="review-picker-item">
                <input
                  type="checkbox"
                  checked={selected.has(m.id)}
                  onChange={(e) => {
                    setSelected((prev) => {
                      const next = new Set(prev)
                      if (e.target.checked) next.add(m.id)
                      else next.delete(m.id)
                      return next
                    })
                  }}
                />
                @{m.handle}
                {m.member_type === 'ai_model' ? '（AI）' : ''}
              </label>
            ))}
            {eligible.length === 0 && <span className="muted">群内暂无可评审的成员</span>}
          </div>
          <div className="requirement-actions">
            <button
              type="button"
              className="btn btn-sm"
              disabled={busy || selected.size === 0}
              onClick={() => void act(() => startReview(groupId, requirement.id, Array.from(selected)))}
            >
              发起评审
            </button>
            {onReviewChat && (
              <button type="button" className="btn btn-sm" onClick={onReviewChat}>
                在聊天中发起评审
              </button>
            )}
          </div>
        </div>
      )}

      {(requirement.state === 'in_review' || requirement.state === 'result_in_review') && openRound && (
        <div className="review-open">
          <p className="muted">
            第 {openRound.proposal_round} 轮{openRound.stage === 'result' ? '结果' : '方案'}评审中 · 规则：
            {openRound.rule === 'quorum' ? `达到 ${openRound.quorum ?? 1} 票` : '全体一致'} ·
            裁决在轮次结束前互相不可见
          </p>
          <ul className="review-decisions">
            {openRound.decisions.map((d) => (
              <li key={d.id}>
                <span>@{d.reviewer_handle ?? d.reviewer_member_id}</span>
                <span className="badge badge-muted">{DECISION_STATUS_LABEL[d.status] ?? d.status}</span>
              </li>
            ))}
          </ul>
          {review?.can_decide && (
            <div className="review-decide">
              <div className="review-verdicts" role="radiogroup" aria-label="我的裁决">
                {(Object.keys(REVIEW_VERDICT_LABEL) as ReviewVerdict[]).map((v) => (
                  <button
                    key={v}
                    type="button"
                    role="radio"
                    aria-checked={verdict === v}
                    className={verdict === v ? 'chip chip-active' : 'chip'}
                    onClick={() => setVerdict(v)}
                  >
                    {REVIEW_VERDICT_LABEL[v]}
                  </button>
                ))}
              </div>
              <textarea
                value={rationale}
                onChange={(e) => setRationale(e.target.value)}
                rows={2}
                placeholder="理由（可选，轮次结束后公开）"
                aria-label="评审理由"
              />
              <button
                type="button"
                className="btn btn-sm btn-primary"
                disabled={busy}
                onClick={() => void act(() => submitReviewDecision(groupId, requirement.id, verdict, rationale))}
              >
                提交评审
              </button>
            </div>
          )}
          {review?.can_force_close && (
            <button
              type="button"
              className="btn btn-sm text-danger"
              disabled={busy}
              onClick={() => {
                if (window.confirm('强制结束本轮评审？未提交的裁决将被撤回。'))
                  void act(() => forceCloseReview(groupId, requirement.id))
              }}
            >
              强制结束评审
            </button>
          )}
        </div>
      )}

      {requirement.state === 'changes_requested' && review?.can_revise && (
        <div className="review-revise">
          {reviseHint(requirement) && <p className="muted">{reviseHint(requirement)}</p>}
          <textarea
            value={note}
            onChange={(e) => setNote(e.target.value)}
            rows={2}
            placeholder="给提案 AI 的修订说明（可选）"
            aria-label="修订说明"
          />
          <button
            type="button"
            className="btn btn-sm"
            disabled={busy}
            onClick={() => void act(() => reviseRequirement(groupId, requirement.id, note))}
          >
            修订并重新提案
          </button>
        </div>
      )}

      {requirement.state === 'changes_requested' && review && !review.can_revise && (
        <p className="muted">{reviseBlockedNote(requirement)}</p>
      )}

      {requirement.state === 'review_passed' && <p className="muted">评审已通过，下一步为人工批准（尚未开启）。</p>}

      {closedRounds.map((r) => (
        <div key={r.id} className="review-round-closed">
          <p className="muted">
            第 {r.proposal_round} 轮{r.stage === 'result' ? '结果评审' : '方案评审'} · 结果：{REVIEW_OUTCOME_LABEL[r.outcome ?? ''] ?? r.outcome}
            {r.revision_note ? ` · 修订说明：${r.revision_note}` : ''}
          </p>
          {r.outcome !== 'superseded' && (
            <ul className="review-decisions">
              {r.decisions.map((d) => (
                <li key={d.id}>
                  <span>@{d.reviewer_handle ?? d.reviewer_member_id}</span>
                  {d.status === 'decided' && d.verdict ? (
                    <span>
                      {REVIEW_VERDICT_LABEL[d.verdict] ?? d.verdict}
                      {d.rationale ? `：${d.rationale}` : ''}
                    </span>
                  ) : (
                    <span className="badge badge-muted">{DECISION_STATUS_LABEL[d.status] ?? d.status}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      ))}

      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
