'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { GroupApiError, resolveMentions, type MentionCandidate } from '@/lib/api/groups'
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
  onSend,
  onAddAiMember,
}: {
  groupId: string
  disabled?: boolean
  replyTo?: { id: string; preview: string } | null
  onCancelReply?: () => void
  onSend: (body: string, replyToId?: string) => Promise<void>
  onAddAiMember?: (aiModelId: string) => Promise<void>
}) {
  const [text, setText] = useState('')
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
    setSending(true)
    setError('')
    try {
      await onSend(body, replyTo?.id)
      setText('')
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
