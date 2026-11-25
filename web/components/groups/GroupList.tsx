'use client'

import Link from 'next/link'
import { useCallback, useEffect, useState } from 'react'
import { GroupApiError, listGroups, type GroupStatus, type GroupSummary } from '@/lib/api/groups'
import GroupCreateForm from './GroupCreateForm'

export default function GroupList() {
  const [items, setItems] = useState<GroupSummary[]>([])
  const [status, setStatus] = useState<GroupStatus>('active')
  const [q, setQ] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showCreate, setShowCreate] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const r = await listGroups({ status, q: q || undefined, limit: 50 })
      setItems(r.items)
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [status, q])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <section className="group-ui">
      <header className="page-head">
        <h1>群聊</h1>
        <button type="button" className="btn btn-primary" onClick={() => setShowCreate((s) => !s)} aria-expanded={showCreate}>
          {showCreate ? '取消' : '新建群聊'}
        </button>
      </header>

      {showCreate && <GroupCreateForm />}

      <div className="toolbar">
        <div className="tabs" role="tablist" aria-label="状态筛选">
          <button type="button" role="tab" aria-selected={status === 'active'} className={status === 'active' ? 'chip chip-active' : 'chip'} onClick={() => setStatus('active')}>
            进行中
          </button>
          <button type="button" role="tab" aria-selected={status === 'archived'} className={status === 'archived' ? 'chip chip-active' : 'chip'} onClick={() => setStatus('archived')}>
            已归档
          </button>
        </div>
        <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索群名…" aria-label="搜索群名" />
      </div>

      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
      {loading ? (
        <p className="muted">加载中…</p>
      ) : items.length === 0 ? (
        <p className="muted">暂无群聊。</p>
      ) : (
        <ul className="group-list">
          {items.map((g) => (
            <li key={g.id} className="group-card">
              <Link href={`/groups/${g.id}`} className="group-card-link">
                <span className="group-card-name">{g.name}</span>
                {g.description && <span className="muted">{g.description}</span>}
                <span className="group-card-meta muted">
                  {g.member_count} 名成员 · {g.ai_member_count} 个 AI · 最近消息 #{g.last_message_seq}
                  {g.my_role ? ` · 我是 ${g.my_role}` : ''}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
