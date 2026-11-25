'use client'

import { useEffect, useState } from 'react'
import {
  addMember,
  GroupApiError,
  listUsers,
  type AddMemberInput,
  type MemberType,
  type UserSummary,
} from '@/lib/api/groups'
import { listAIModels, type AIModel } from '@/lib/api'

export default function AddMemberForm({
  groupId,
  onAdded,
}: {
  groupId: string
  onAdded: () => void
}) {
  const [type, setType] = useState<MemberType>('ai_model')
  const [models, setModels] = useState<AIModel[]>([])
  const [users, setUsers] = useState<UserSummary[] | null>(null)
  const [modelId, setModelId] = useState('')
  const [userId, setUserId] = useState('')
  const [role, setRole] = useState<'admin' | 'operator'>('operator')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    listAIModels()
      .then((items) => setModels(items.filter((m) => m.status === 'active')))
      .catch(() => setModels([]))
    listUsers().then(setUsers).catch(() => setUsers(null))
  }, [])

  const submit = async () => {
    setBusy(true)
    setError('')
    const input: AddMemberInput =
      type === 'ai_model'
        ? { member_type: 'ai_model', ai_model_id: modelId }
        : { member_type: 'user', user_id: userId.trim(), group_role: role }
    try {
      await addMember(groupId, input)
      setModelId('')
      setUserId('')
      onAdded()
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '添加失败')
    } finally {
      setBusy(false)
    }
  }

  const canSubmit = type === 'ai_model' ? modelId !== '' : userId.trim() !== ''

  return (
    <form
      className="add-member"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <div className="add-member-tabs" role="tablist" aria-label="成员类型">
        <button type="button" role="tab" aria-selected={type === 'ai_model'} className={type === 'ai_model' ? 'chip chip-active' : 'chip'} onClick={() => setType('ai_model')}>
          AI 模型
        </button>
        <button type="button" role="tab" aria-selected={type === 'user'} className={type === 'user' ? 'chip chip-active' : 'chip'} onClick={() => setType('user')}>
          用户
        </button>
      </div>

      {type === 'ai_model' ? (
        <label>
          模型
          <select value={modelId} onChange={(e) => setModelId(e.target.value)} aria-label="选择 AI 模型">
            <option value="">请选择…</option>
            {models.map((m) => (
              <option key={m.id} value={m.id} disabled={m.workspace?.status !== 'ready'}>
                @{m.handle} — {m.display_name}
                {m.workspace?.status !== 'ready' ? '（工作区未就绪）' : ''}
              </option>
            ))}
          </select>
        </label>
      ) : (
        <>
          <label>
            用户
            {users ? (
              <select value={userId} onChange={(e) => setUserId(e.target.value)} aria-label="选择用户">
                <option value="">请选择…</option>
                {users.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.display_name} (@{u.username})
                  </option>
                ))}
              </select>
            ) : (
              <input value={userId} onChange={(e) => setUserId(e.target.value)} placeholder="输入用户 ID 或用户名" aria-label="用户 ID 或用户名" />
            )}
          </label>
          <label>
            角色
            <select value={role} onChange={(e) => setRole(e.target.value as 'admin' | 'operator')} aria-label="群内角色">
              <option value="operator">成员（operator）</option>
              <option value="admin">管理员（admin）</option>
            </select>
          </label>
        </>
      )}

      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
      <button type="submit" className="btn btn-primary" disabled={busy || !canSubmit}>
        {busy ? '添加中…' : '添加成员'}
      </button>
    </form>
  )
}
