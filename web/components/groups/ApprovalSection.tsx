'use client'

import { useState } from 'react'
import {
  approveRequirement,
  GroupApiError,
  retryPushdown,
  retryRedTeam,
  type RequirementView,
} from '@/lib/api/groups'
import { blockedReasonLabel } from '@/lib/requirements'

const RT_LABEL: Record<string, string> = {
  queued: '排队中',
  running: '检查中',
  clear: '已通过',
  flagged: '未通过',
  error: '出错',
  superseded: '已作废',
}

const PD_LABEL: Record<string, string> = {
  approved: '已批准',
  task_created: '任务已创建',
  task_approved: '任务已批准',
  executing: '执行中',
  succeeded: '执行完成',
  failed: '执行失败',
  cancelled: '已取消',
}

/**
 * Red-team gate + D1 human approval + push-down status for one
 * requirement card (Part B P8). Renders when the requirement carries a
 * red-team check or a push-down, or sits in a P8 state. Approval is a
 * two-step confirm: the plan hash sent is the one the red-team check
 * (or the push-down record) carries, and the backend re-verifies it
 * against the passing round and the live proposal.
 */
export default function ApprovalSection({
  groupId,
  requirement,
  onChanged,
}: {
  groupId: string
  requirement: RequirementView
  onChanged: () => void
}) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirming, setConfirming] = useState(false)

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await fn()
      setConfirming(false)
      onChanged()
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const rt = requirement.redteam
  const pd = requirement.pushdown
  const actions = requirement.allowed_actions ?? []
  const planHash = rt?.plan_hash ?? pd?.plan_hash ?? ''

  return (
    <div className="approval-section">
      {rt && (
        <div className="approval-block">
          <p>
            <span className="badge badge-muted">红队检查</span>{' '}
            <strong>{RT_LABEL[rt.status] ?? rt.status}</strong>
            {rt.seat_model ? <span className="muted"> · {rt.seat_model}</span> : null}
          </p>
          {rt.independence === 'degraded' && (
            <p className="approval-warn">⚠ 没有独立于受派/评审模型的红队席位，本次使用了相同模型。</p>
          )}
          {rt.summary && <p className="muted">{rt.summary}</p>}
          {actions.includes('retry_redteam') && (
            <div className="requirement-actions">
              <button
                type="button"
                className="btn btn-sm"
                disabled={busy}
                onClick={() => void act(() => retryRedTeam(groupId, requirement.id))}
              >
                重试红队检查
              </button>
            </div>
          )}
        </div>
      )}

      {requirement.state === 'awaiting_approval' && (
        <div className="approval-block">
          {requirement.can_approve ? (
            confirming ? (
              <div className="requirement-actions">
                <span>批准后将立即在受派模型的工作区执行该计划，确认批准？</span>
                <button
                  type="button"
                  className="btn btn-sm"
                  disabled={busy}
                  onClick={() =>
                    void act(() => approveRequirement(groupId, requirement.id, planHash, requirement.version))
                  }
                >
                  确认批准并执行
                </button>
                <button type="button" className="btn btn-sm" disabled={busy} onClick={() => setConfirming(false)}>
                  取消
                </button>
              </div>
            ) : (
              <div className="requirement-actions">
                <button type="button" className="btn btn-sm" disabled={busy} onClick={() => setConfirming(true)}>
                  批准执行
                </button>
              </div>
            )
          ) : (
            <p className="muted">{blockedReasonLabel(requirement.approve_blocked_reason) || '当前不能批准'}</p>
          )}
        </div>
      )}

      {pd && (
        <div className="approval-block">
          <p>
            <span className="badge badge-muted">下推执行</span>{' '}
            <strong>{PD_LABEL[pd.status] ?? pd.status}</strong>
            {pd.last_phase ? <span className="muted"> · 阶段：{pd.last_phase}</span> : null}
          </p>
          {pd.failure_category && (
            <p className="approval-warn">失败类别：{pd.failure_category}</p>
          )}
          {pd.last_error && <p className="muted">{pd.last_error}</p>}
          {actions.includes('retry_pushdown') && (
            <div className="requirement-actions">
              <button
                type="button"
                className="btn btn-sm"
                disabled={busy}
                onClick={() => void act(() => retryPushdown(groupId, requirement.id))}
              >
                重试下推
              </button>
            </div>
          )}
        </div>
      )}

      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
