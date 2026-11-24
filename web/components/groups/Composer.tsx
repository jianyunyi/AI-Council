'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { GroupApiError, resolveMentions, type MentionCandidate } from '@/lib/api/groups'
import { previewDispatch } from '@/lib/requirements'
import MentionPicker from './MentionPicker'

interface ActiveQuery {
  start: number // index of '@'
  end: number // caret index
  query: string
}

function findQuery(text: string, caret: number): ActiveQuery | null {
  let i = caret - 1
  while (i >= 0 && /[A-Za-z0-9._-]/.test(text[i])) i--
  if (i < 0 || text[i] !== '@') return null
  if (i > 0 && /[A-Za-z0-9._@-]/.test(text[i - 1])) return null // inside an email etc.
  const query = text.slice(i + 1, caret)
  if (query.length > 32) return null
  return { start: i, end: caret, query }
}

export default function Composer({
  groupId,
  disabled,
  replyTo,
  onCancelReply,
  reviewTarget,
  onClearReviewTarget,
  onSend,
  onAddAiMember,
  canDispatch,
  aiHandles,
}: {
  groupId: string
  disabled?: boolean
  replyTo?: { id: string; preview: string } | null
  onCancelReply?: () => void
  /** P7: when set, the next send opens a review round on this requirement
   * with the @mentioned members as reviewers. */
  reviewTarget?: { requirementId: string; title: string } | null
  onClearReviewTarget?: () => void
  onSend: (body: string, replyToId?: string, intent?: 'chat' | 'dispatch' | 'review', requirementId?: string) => Promise<void>
  onAddAiMember?: (aiModelId: string) => Promise<void>
  /** P6: owners/operators may flip the composer into dispatch mode. */
  canDispatch?: boolean
  aiHandles?: ReadonlySet<string>
}) {
  const [text, setText] = useState('')
  const [dispatchMode, setDispatchMode] = useState(false)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const [query, setQuery] = useState<ActiveQuery | null>(null)
  const [candidates, setCandidates] = useState<MentionCandidate[]>([])
  const [active, setActive] = useState(0)
  const taRef = useRef<HTMLTextAreaElement>(null)
  const debounce = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const refreshCandidates = useCallback(
    (q: ActiveQuery | null) => {
      if (debounce.current) clearTimeout(debounce.current)
      if (!q) {
        setCandidates([])
        return
      }
      debounce.current = setTimeout(() => {
        resolveMentions(groupId, q.query)
          .then((r) => {
            setCandidates(r.items)
            setActive(0)
          })
          .catch(() => setCandidates([]))
      }, 120)
    },
    [groupId],
  )

  useEffect(() => () => {
    if (debounce.current) clearTimeout(debounce.current)
  }, [])

  const syncQuery = (value: string, caret: number) => {
    const q = findQuery(value, caret)
    setQuery(q)
    refreshCandidates(q)
  }

  const pick = async (c: MentionCandidate) => {
    if (!query) return
    if (!c.is_member && c.ai_model_id && onAddAiMember) {
      try {
        await onAddAiMember(c.ai_model_id)
      } catch (err) {
        setError(err instanceof GroupApiError ? err.message : '添加 AI 成员失败')
        return
      }
    }
    const insert = `@${c.handle} `
    const next = text.slice(0, query.start) + insert + text.slice(query.end)
    setText(next)
    setQuery(null)
    setCandidates([])
    requestAnimationFrame(() => {
      const pos = query.start + insert.length
      taRef.current?.focus()
      taRef.current?.setSelectionRange(pos, pos)
    })
  }

  const submit = async () => {
    const body = text.trimEnd()
    if (!body.trim() || sending || disabled) return
    if (dispatchMode) {
      // The backend rejects a dispatch with no resolved AI mention
      // (422 no_ai_mention); warn here instead of failing the send.
      const preview = previewDispatch(body, aiHandles ?? new Set())
      if (!preview.ok) {
        setError(preview.problem)
        return
      }
    }
    setSending(true)
    setError('')
    try {
      if (reviewTarget) {
        await onSend(body, undefined, 'review', reviewTarget.requirementId)
        onClearReviewTarget?.()
      } else {
        await onSend(body, replyTo?.id, dispatchMode ? 'dispatch' : undefined)
      }
      setText('')
      setDispatchMode(false)
      setQuery(null)
      setCandidates([])
      onCancelReply?.()
    } catch (err) {
      // Keep the draft; a client_msg_id retry happens in the parent via the same draft key.
      setError(err instanceof GroupApiError ? err.message : '发送失败，请重试')
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="composer">
      {canDispatch && (
        <div className="composer-intent">
          <button
            type="button"
            className={dispatchMode ? 'chip chip-active' : 'chip'}
            aria-pressed={dispatchMode}
            onClick={() => setDispatchMode((v) => !v)}
          >
            派发需求
          </button>
          {dispatchMode && <span className="muted">派发模式：@ 到的每位 AI 都会收到一条需求并给出方案；评审通过且人工批准后才会执行。</span>}
        </div>
      )}
      {reviewTarget && (
        <div className="composer-reply">
          <span className="muted">评审模式：发送后将开启对需求「{reviewTarget.title}」的评审，@ 到的成员为评审者</span>
          <button type="button" className="btn btn-sm" onClick={onClearReviewTarget}>
            取消
          </button>
        </div>
      )}
      {replyTo && (
        <div className="composer-reply">
          <span className="muted">回复：{replyTo.preview}</span>
          <button type="button" className="btn btn-sm" onClick={onCancelReply}>
            取消
          </button>
        </div>
      )}
      <div className="composer-box">
        {candidates.length > 0 && query && <MentionPicker items={candidates} activeIndex={active} onPick={pick} />}
        <textarea
          ref={taRef}
          value={text}
          rows={3}
          disabled={disabled}
          placeholder="输入消息，@ 提及成员或 AI 模型…（Enter 发送，Shift+Enter 换行）"
          aria-label="消息输入"
          onChange={(e) => {
            setText(e.target.value)
            syncQuery(e.target.value, e.target.selectionStart ?? e.target.value.length)
          }}
          onClick={(e) => syncQuery(text, (e.target as HTMLTextAreaElement).selectionStart ?? 0)}
          onKeyDown={(e) => {
            if (candidates.length > 0 && query) {
              if (e.key === 'ArrowDown') {
                e.preventDefault()
                setActive((a) => (a + 1) % candidates.length)
                return
              }
              if (e.key === 'ArrowUp') {
                e.preventDefault()
                setActive((a) => (a - 1 + candidates.length) % candidates.length)
                return
              }
              if (e.key === 'Enter' || e.key === 'Tab') {
                e.preventDefault()
                void pick(candidates[active])
                return
              }
              if (e.key === 'Escape') {
                setCandidates([])
                return
              }
            }
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              void submit()
            }
          }}
        />
        <button type="button" className="btn btn-primary" disabled={disabled || sending || !text.trim()} onClick={() => void submit()}>
          {sending ? '发送中…' : '发送'}
        </button>
      </div>
      {error && (
        <p className="alert alert-danger" role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
