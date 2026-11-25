'use client'

import { useState } from 'react'
import { removeMember, updateMember, type GroupRole, type MemberView } from '@/lib/api/groups'
import AddMemberForm from './AddMemberForm'

const ROLE_LABEL: Record<GroupRole, string> = {
  owner: '群主',
  admin: '管理员',
  operator: '成员',
  ai: 'AI',
}

export default function MemberSidebar({
  groupId,
  members,
  canManage,
  myMemberId,
  onChanged,
}: {
  groupId: string
  members: MemberView[]
  canManage: boolean
  myMemberId?: string
  onChanged: () => void
}) {
  const [showAdd, setShowAdd] = useState(false)
  const [error, setError] = useState('')
  const active = members.filter((m) => !m.removed_at)
  const users = active.filter((m) => m.member_type === 'user')
  const ais = active.filter((m) => m.member_type === 'ai_model')

  const act = async (fn: () => Promise<unknown>) => {
    setError('')
    try {
      await fn()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    }
  }

  const row = (m: MemberView) => (
    <li key={m.id} className="member-row">
      <div className="member-main">
        <span className="member-name">{m.display_name}</span>
        <span className="muted">@{m.handle}</span>
        <span className="badge badge-muted">{ROLE_LABEL[m.group_role]}</span>
        {m.member_type === 'ai_model' && m.ai_status && m.ai_status !== 'active' && (
          <span className="badge badge-warning">{m.ai_status}</span>
        )}
        {m.id === myMemberId && <span className="badge badge-success">我</span>}
      </div>
      {canManage && m.member_type === 'user' && m.group_role !== 'owner' && (
        <select
          aria-label={`调整 ${m.display_name} 的角色`}
          value={m.group_role}
          onChange={(e) => void act(() => updateMember(groupId, m.id, { group_role: e.target.value as GroupRole }))}
        >
          <option value="operator">成员</option>
          <option value="admin">管理员</option>
        </select>
      )}
      {(canManage || m.id === myMemberId) && m.group_role !== 'owner' && (
        <button
          type="button"
          className="btn btn-sm text-danger"
          onClick={() => {
            if (window.confirm(m.id === myMemberId ? '确定退出该群？' : `确定将 ${m.display_name} 移出群？`)) {
              void act(() => removeMember(groupId, m.id))
            }
          }}
        >
          {m.id === myMemberId ? '退出' : '移除'}
        </button>
      )}
    </li>
  )

  return (
    <aside className="member-sidebar" aria-label="群成员">
      <h2>AI 成员 · {ais.length}</h2>
      <ul className="member-list">{ais.map(row)}</ul>
      <h2>成员 · {users.length}</h2>
      <ul className="member-list">{users.map(row)}</ul>
      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
      {canManage && (
        <>
          <button type="button" className="btn" onClick={() => setShowAdd((s) => !s)} aria-expanded={showAdd}>
            {showAdd ? '收起' : '添加成员'}
          </button>
          {showAdd && (
            <AddMemberForm
              groupId={groupId}
              onAdded={() => {
                setShowAdd(false)
                onChanged()
              }}
            />
          )}
        </>
      )}
    </aside>
  )
}
