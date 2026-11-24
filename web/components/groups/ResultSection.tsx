'use client'

import { useMemo, useState } from 'react'
import {
  artifactDownloadUrl,
  GroupApiError,
  startResultReview,
  type MemberView,
  type RequirementView,
} from '@/lib/api/groups'
import { ACCEPTANCE_STATUS_LABEL, artifactStatusLabel, executionOutcomeLabel } from '@/lib/requirements'

/**
 * Execution result UI for one requirement card (Part B P9). Renders
 * the latest submission: outcome, summary (model-written or
 * facts-only), the acceptance report, and the artifact manifest —
 * copied artifacts download through the group endpoint; everything
 * else shows why it was not copied. From result_submitted, the
 * requester (or a manager) can open a result review; the round itself
 * is rendered by ReviewSection.
 */
export default function ResultSection({
  groupId,
  requirement,
  members,
  onChanged,
  onReviewChat,
}: {
  groupId: string
  requirement: RequirementView
  members: MemberView[]
  onChanged: () => void
  onReviewChat?: () => void
}) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const result = requirement.result

  const eligible = useMemo(
    () =>
      members.filter((m) => {
        if (m.removed_at || m.id === requirement.assignee_member_id) return false
        if (m.member_type === 'ai_model') return m.ai_status === 'active'
        return m.group_role === 'owner' || m.group_role === 'operator'
      }),
    [members, requirement.assignee_member_id],
  )

  if (!result) {
    // Executed but the result job has not committed yet.
    return (
      <div className="result-section">
        <p className="muted">执行已结束，正在收集结果…</p>
      </div>
    )
  }

  const canStart =
    requirement.state === 'result_submitted' && (requirement.allowed_actions?.includes('request_result_review') ?? false)

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await fn()
      onChanged()
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="result-section">
      <div className="result-head">
        <span className={`badge ${result.execution_outcome === 'succeeded' ? 'req-tone-ok' : 'req-tone-danger'}`}>
          {executionOutcomeLabel(result.execution_outcome)}
        </span>
        <span className="muted">第 {result.cycle} 轮执行结果</span>
        {result.summary_source === 'facts' && <span className="badge badge-muted">仅事实摘要</span>}
        {result.failure_category && <span className="muted">失败类别：{result.failure_category}</span>}
      </div>

      {result.summary_md && <p className="result-summary">{result.summary_md}</p>}

      {result.acceptance_report && result.acceptance_report.length > 0 && (
        <ul className="result-acceptance">
          {result.acceptance_report.map((item, i) => (
            <li key={i}>
              <span className={`badge ${item.status === 'met' ? 'req-tone-ok' : item.status === 'unmet' ? 'req-tone-danger' : 'badge-muted'}`}>
                {ACCEPTANCE_STATUS_LABEL[item.status] ?? item.status}
              </span>
              <span>{item.acceptance}</span>
              {item.evidence && <span className="muted">证据：{item.evidence}</span>}
            </li>
          ))}
        </ul>
      )}

      {result.artifacts.length > 0 && (
        <ul className="result-artifacts">
          {result.artifacts.map((a) => (
            <li key={a.id}>
              <span>{a.name}</span>
              <span className="muted">
                {a.kind} · {a.size_bytes} B
              </span>
              <span className="badge badge-muted">{artifactStatusLabel(a.status)}</span>
              {a.status === 'copied' && (
                <a className="btn btn-sm" href={artifactDownloadUrl(groupId, requirement.id, a.id)} download>
                  下载
                </a>
              )}
            </li>
          ))}
        </ul>
      )}

      {canStart && (
        <div className="review-start">
          <span className="muted">选择结果评审者（不能是执行 AI 本人）：</span>
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
              onClick={() => void act(() => startResultReview(groupId, requirement.id, Array.from(selected)))}
            >
              发起结果评审
            </button>
            {onReviewChat && (
              <button type="button" className="btn btn-sm" onClick={onReviewChat}>
                在聊天中发起结果评审
              </button>
            )}
          </div>
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
