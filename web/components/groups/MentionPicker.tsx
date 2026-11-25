'use client'

import type { MentionCandidate } from '@/lib/api/groups'

export default function MentionPicker({
  items,
  activeIndex,
  onPick,
}: {
  items: MentionCandidate[]
  activeIndex: number
  onPick: (c: MentionCandidate) => void
}) {
  if (items.length === 0) return null
  return (
    <ul className="mention-picker" role="listbox" aria-label="提及候选">
      {items.map((c, i) => (
        <li key={`${c.handle}-${c.is_member}`}>
          <button
            type="button"
            role="option"
            aria-selected={i === activeIndex}
            className={i === activeIndex ? 'mention-option mention-option-active' : 'mention-option'}
            onMouseDown={(e) => {
              e.preventDefault() // keep textarea focus
              onPick(c)
            }}
          >
            <span className="mention-option-handle">@{c.handle}</span>
            <span className="mention-option-name">{c.display_name}</span>
            {!c.is_member && <span className="badge badge-warning">未加入 · 将添加</span>}
            {c.is_member && c.member_type === 'ai_model' && <span className="badge badge-muted">AI</span>}
          </button>
        </li>
      ))}
    </ul>
  )
}
