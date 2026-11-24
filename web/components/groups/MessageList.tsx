'use client'

import { useMemo } from 'react'
import type { MemberView, MessageView } from '@/lib/api/groups'
import { splitSegments } from '@/lib/mentions'
import RequirementCard from './RequirementCard'

const EDIT_WINDOW_MS = 10 * 60 * 1000

/** Part B P8 system cards posted by the red-team gate / push-down /
 * mirror into the group stream. */
const SYSTEM_KIND_LABEL: Record<string, string> = {
  redteam_result: '红队检查',
  approval_notice: '人工审批',
  task_status: '执行状态',
  execution_failure: '执行失败',
}

function Body({ msg }: { msg: MessageView }) {
  const resolved = useMemo(
    () => new Set(msg.mentions.filter((m) => m.resolution === 'resolved').map((m) => m.handle)),
    [msg.mentions],
  )
  const segs = useMemo(() => splitSegments(msg.body_md, resolved), [msg.body_md, resolved])
  return (
    <p className="message-body">
      {segs.map((s, i) =>
        s.mention ? (
          <span key={i} className={s.mention.resolved ? 'mention mention-resolved' : 'mention mention-unresolved'}>
            {s.text}
          </span>
        ) : (
          <span key={i}>{s.text}</span>
        ),
      )}
    </p>
  )
}

export default function MessageList({
  messages,
  members,
  currentMemberId,
  onReply,
  onEdit,
  groupId,
  reqBump,
  canManageGroup,
  onReviewChat,
}: {
  messages: MessageView[]
  members: MemberView[]
  currentMemberId?: string
  onReply: (m: MessageView) => void
  onEdit: (m: MessageView) => void
  /** P6: when set, dispatch_card/proposal messages render as live requirement cards. */
  groupId?: string
  reqBump?: number
  canManageGroup?: boolean
  /** P7: ask the composer to start a chat-driven review of this requirement. */
  onReviewChat?: (req: import('@/lib/api/groups').RequirementView) => void
}) {
  if (messages.length === 0) {
    return <p className="muted">暂无消息，来发第一条吧。</p>
  }
  return (
    <ol className="message-list">
      {messages.map((m) => {
        const mine = m.author?.id === currentMemberId
        const editable = mine && m.kind === 'text' && Date.now() - new Date(m.created_at).getTime() < EDIT_WINDOW_MS
        const isCard = (m.kind === 'dispatch_card' || m.kind === 'proposal') && !!m.requirement_id && !!groupId
        const systemLabel = SYSTEM_KIND_LABEL[m.kind]
        return (
          <li key={m.id} className={systemLabel ? 'message system-card' : 'message'} data-seq={m.seq}>
            <div className="message-head">
              <strong>{m.author?.display_name ?? (m.author_type === 'system' ? '系统' : '已移除成员')}</strong>
              {systemLabel && <span className="badge badge-muted">{systemLabel}</span>}
              {m.author?.member_type === 'ai_model' && <span className="badge badge-muted">AI</span>}
              <time className="muted" dateTime={m.created_at}>
                {new Date(m.created_at).toLocaleTimeString()}
              </time>
              {m.edited_at && <span className="muted">(已编辑)</span>}
              {m.status === 'failed' && <span className="badge badge-warning">发送失败</span>}
            </div>
            {m.reply_to_message_id && <div className="message-reply muted">↩ 回复 #{m.reply_to_message_id.slice(0, 8)}</div>}
            {isCard ? (
              <RequirementCard
                groupId={groupId!}
                requirementId={m.requirement_id!}
                bump={reqBump ?? 0}
                myMemberId={currentMemberId}
                canManage={canManageGroup}
                members={members}
                onReviewChat={onReviewChat}
              />
            ) : (
              <>
                <Body msg={m} />
                {!systemLabel && (
                  <div className="message-actions">
                    <button type="button" className="btn btn-sm" onClick={() => onReply(m)}>
                      回复
                    </button>
                    {editable && (
                      <button type="button" className="btn btn-sm" onClick={() => onEdit(m)}>
                        编辑
                      </button>
                    )}
                  </div>
                )}
              </>
            )}
          </li>
        )
      })}
    </ol>
  )
}
