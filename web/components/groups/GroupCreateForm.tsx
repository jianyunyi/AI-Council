'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { createGroup, GroupApiError, type AddMemberInput } from '@/lib/api/groups'
import { listAIModels, type AIModel } from '@/lib/api'

export default function GroupCreateForm() {
  const router = useRouter()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [models, setModels] = useState<AIModel[]>([])
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    listAIModels()
      .then((items) => setModels(items.filter((m) => m.status === 'active')))
      .catch(() => setModels([]))
  }, [])

  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const submit = async () => {
    if (!name.trim()) {
      setError('群名不能为空')
      return
    }
    setBusy(true)
    setError('')
    const members: AddMemberInput[] = Array.from(picked).map((id) => ({ member_type: 'ai_model', ai_model_id: id }))
    try {
      const g = await createGroup({ name: name.trim(), description: description.trim() || undefined, members })
      router.push(`/groups/${g.id}`)
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '创建失败，请重试')
      setBusy(false)
    }
  }

  return (
    <form
      className="group-create"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <label>
        群名
        <input value={name} onChange={(e) => setName(e.target.value)} maxLength={80} required placeholder="例如：发布评审群" />
      </label>
      <label>
        描述（可选）
        <input value={description} onChange={(e) => setDescription(e.target.value)} maxLength={280} />
      </label>

      <fieldset>
        <legend>初始 AI 成员（可多选，之后可再加）</legend>
        {models.length === 0 && <p className="muted">暂无已启用的 AI 模型，可先建群。</p>}
        {models.map((m) => (
          <label key={m.id} className="check-row">
            <input
              type="checkbox"
              checked={picked.has(m.id)}
              disabled={m.workspace?.status !== 'ready'}
              onChange={() => toggle(m.id)}
            />
            @{m.handle} — {m.display_name}
            {m.workspace?.status !== 'ready' && <span className="badge badge-warning">工作区未就绪</span>}
          </label>
        ))}
      </fieldset>

      <p className="muted">你将成为群主。人工批准（require_human_approval）默认开启且不可关闭（D1 决策）。</p>
      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
      <button type="submit" className="btn btn-primary" disabled={busy}>
        {busy ? '创建中…' : '创建群聊'}
      </button>
    </form>
  )
}
