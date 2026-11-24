'use client'

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  addMember,
  archiveGroup,
  editMessage,
  getGroup,
  GroupApiError,
  listMessages,
  postMessage,
  updateGroup,
  type GroupDetail,
  type GroupEvent,
  type MessageView,
} from '@/lib/api/groups'
import { useGroupStream } from '@/hooks/useGroupStream'
import { me } from '@/lib/api'
import Composer from './Composer'
import MemberSidebar from './MemberSidebar'
import MessageList from './MessageList'

const PAGE = 50

function newClientMsgId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `cm-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

// GroupSpace is the client body of /groups/[id]. The route file itself is a
// server wrapper that exports generateStaticParams for the desktop build
// (same pattern as app/tasks/[id]).
export default function GroupSpace({ id }: { id: string }) {
  const [group, setGroup] = useState<GroupDetail | null>(null)
  const [messages, setMessages] = useState<MessageView[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [error, setError] = useState('')
  const [notFound, setNotFound] = useState(false)
  const [replyTo, setReplyTo] = useState<{ id: string; preview: string } | null>(null)
  const [reqBump, setReqBump] = useState(0)
  const [editing, setEditing] = useState<MessageView | null>(null)
  const [editText, setEditText] = useState('')
  const [mySubject, setMySubject] = useState('')
  const draftKey = useRef(newClientMsgId())
  const bottomRef = useRef<HTMLDivElement>(null)

  // My member row: user handles are account subjects (see the backend user
  // directory), so /auth/me identifies it exactly. In legacy static-token
  // mode /auth/me is unavailable and the affordances that need it (edit
  // button, self-leave) simply stay hidden — the server enforces authorship
  // and membership regardless.
  useEffect(() => {
    me()
      .then((identity) => setMySubject(identity.subject.toLowerCase()))
      .catch(() => setMySubject(''))
  }, [])
  const myMemberId = useMemo(
    () => group?.members.find((m) => !m.removed_at && m.member_type === 'user' && mySubject !== '' && m.handle === mySubject)?.id,
    [group, mySubject],
  )

  const loadGroup = useCallback(async () => {
    try {
      const g = await getGroup(id)
      setGroup(g)
      setNotFound(false)
    } catch (err) {
      if (err instanceof GroupApiError && err.status === 404) setNotFound(true)
      else setError(err instanceof GroupApiError ? err.message : '加载群信息失败')
    }
  }, [id])

  const loadLatest = useCallback(async () => {
    try {
      const page = await listMessages(id, { limit: PAGE })
      setMessages(page.items)
      setHasMore(page.has_more)
    } catch (err) {
      if (err instanceof GroupApiError && err.status === 404) setNotFound(true)
    }
  }, [id])

  useEffect(() => {
    void loadGroup()
    void loadLatest()
  }, [loadGroup, loadLatest])

  const upsertMessage = useCallback((m: MessageView) => {
    setMessages((prev) => {
      const idx = prev.findIndex((x) => x.id === m.id)
      if (idx >= 0) {
        const next = [...prev]
        next[idx] = m
        return next
      }
      return [...prev, m].sort((a, b) => a.seq - b.seq)
    })
  }, [])

  const onEvent = useCallback(
    (ev: GroupEvent) => {
      switch (ev.type) {
        case 'message.created':
        case 'message.updated':
          upsertMessage(ev.data as MessageView)
          break
        case 'group.updated':
        case 'group.archived':
        case 'member.added':
        case 'member.updated':
        case 'member.removed':
          void loadGroup()
          break
        case 'requirement.state_changed':
          setReqBump((b) => b + 1)
          break
        default:
          break // P7+: review/dispatch-result events land here
      }
    },
    [loadGroup, upsertMessage],
  )

  const { status: streamStatus } = useGroupStream(id, group?.last_event_seq ?? 0, onEvent)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [messages.length])

  const loadOlder = async () => {
    if (messages.length === 0 || loadingOlder) return
    setLoadingOlder(true)
    try {
      const page = await listMessages(id, { before_seq: messages[0].seq, limit: PAGE })
      setMessages((prev) => [...page.items, ...prev])
      setHasMore(page.has_more)
    } finally {
      setLoadingOlder(false)
    }
  }

  const send = async (body: string, replyToId?: string, intent?: 'chat' | 'dispatch') => {
    const res = await postMessage(id, {
      body_md: body,
      client_msg_id: draftKey.current,
      reply_to_message_id: replyToId,
      ...(intent ? { intent } : {}),
    })
    draftKey.current = newClientMsgId() // rotate only after success; failures retry with the same key
    upsertMessage(res.message)
  }

  const addAi = async (aiModelId: string) => {
    await addMember(id, { member_type: 'ai_model', ai_model_id: aiModelId })
    await loadGroup()
  }

  const saveEdit = async () => {
    if (!editing) return
    try {
      const m = await editMessage(id, editing.id, editText)
      upsertMessage(m)
      setEditing(null)
    } catch (err) {
      setError(err instanceof GroupApiError ? err.message : '编辑失败')
    }
  }

  if (notFound) {
    return (
      <section className="group-ui">
        <h1>群聊不存在或你不是成员</h1>
        <p className="muted">出于隐私，非成员访问统一返回 404。</p>
      </section>
    )
  }
  if (!group) {
    return (
      <section className="group-ui">
        {error ? (
          <p className="alert alert-danger" role="alert">
            {error}
          </p>
        ) : (
          <p className="muted">加载中…</p>
        )}
      </section>
    )
  }

  const archived = group.status === 'archived'
  const canPost = !archived && !!group.my_role

  return (
    <section className="group-ui group-space">
      <div className="group-main">
        <header className="page-head">
          <div>
            <h1>{group.name}</h1>
            {group.description && <p className="muted">{group.description}</p>}
            <p className="muted">
              状态：{archived ? '已归档' : '进行中'} · 连接：{streamStatus === 'open' ? '已连接' : streamStatus}
              {' · '}人工批准：开启（锁定）
            </p>
          </div>
          {group.can_manage && !archived && (
            <div className="head-actions">
              <button
                type="button"
                className="btn"
                onClick={async () => {
                  const name = window.prompt('新的群名', group.name)
                  if (!name || name === group.name) return
                  try {
                    setGroup(await updateGroup(id, { name, expected_version: group.version }))
                  } catch (err) {
                    setError(err instanceof GroupApiError ? err.message : '改名失败')
                  }
                }}
              >
                改名
              </button>
              <button
                type="button"
                className="btn text-danger"
                onClick={async () => {
                  if (!window.confirm('归档后群变为只读，共享工作区将归档。确定？')) return
                  try {
                    setGroup(await archiveGroup(id))
                  } catch (err) {
                    setError(err instanceof GroupApiError ? err.message : '归档失败')
                  }
                }}
              >
                归档群
              </button>
            </div>
          )}
        </header>

        {error && (
          <p className="alert alert-danger" role="alert">
            {error}
          </p>
        )}

        {hasMore && (
          <button type="button" className="btn" onClick={() => void loadOlder()} disabled={loadingOlder}>
            {loadingOlder ? '加载中…' : '加载更早的消息'}
          </button>
        )}

        <MessageList
          messages={messages}
          members={group.members}
          currentMemberId={myMemberId}
          groupId={id}
          reqBump={reqBump}
          canManageGroup={group.can_manage}
          onReply={(m) => setReplyTo({ id: m.id, preview: m.body_md.slice(0, 60) })}
          onEdit={(m) => {
            setEditing(m)
            setEditText(m.body_md)
          }}
        />
        <div ref={bottomRef} />

        {editing && (
          <div className="edit-box">
            <label>
              编辑消息（10 分钟内）
              <textarea value={editText} onChange={(e) => setEditText(e.target.value)} rows={3} aria-label="编辑消息" />
            </label>
            <div className="edit-actions">
              <button type="button" className="btn" onClick={() => setEditing(null)}>
                取消
              </button>
              <button type="button" className="btn btn-primary" onClick={() => void saveEdit()}>
                保存
              </button>
            </div>
          </div>
        )}

        {canPost ? (
          <Composer
            groupId={id}
            replyTo={replyTo}
            onCancelReply={() => setReplyTo(null)}
            onSend={send}
            onAddAiMember={addAi}
            canDispatch={group.my_role === 'owner' || group.my_role === 'operator' || group.can_manage}
            aiHandles={new Set(group.members.filter((m) => !m.removed_at && m.ai_model_id).map((m) => m.handle))}
          />
        ) : (
          <p className="muted">{archived ? '群已归档，只读。' : '你不是该群成员，不能发言。'}</p>
        )}
      </div>

      <MemberSidebar groupId={id} members={group.members} canManage={group.can_manage} myMemberId={myMemberId} onChanged={() => void loadGroup()} />
    </section>
  )
}
