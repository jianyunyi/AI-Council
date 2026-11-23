'use client'

import {useState} from 'react'
import {usePrefs} from '@/components/theme'
import {diffStats} from '@/lib/task-state'
import type {Patch} from '@/lib/types'

type Line = {kind: 'add' | 'del' | 'ctx' | 'hunk' | 'meta'; text: string}

function parseLines(diff: string): Line[] {
  return diff.split('\n').map(raw => {
    if (raw.startsWith('@@')) return {kind: 'hunk', text: raw}
    if (raw.startsWith('+++') || raw.startsWith('---') || raw.startsWith('diff ') || raw.startsWith('index ')) return {kind: 'meta', text: raw}
    if (raw.startsWith('+')) return {kind: 'add', text: raw.slice(1)}
    if (raw.startsWith('-')) return {kind: 'del', text: raw.slice(1)}
    return {kind: 'ctx', text: raw.startsWith(' ') ? raw.slice(1) : raw}
  })
}

function Unified({lines}: {lines: Line[]}) {
  return (
    <div className="diff" role="table" aria-label="统一差异">
      {lines.map((line, index) => (
        <div key={index} className={`diff-line diff-line--${line.kind}`} role="row">
          <span className="diff-sign" aria-hidden="true">{line.kind === 'add' ? '+' : line.kind === 'del' ? '-' : ''}</span>
          <span className="diff-text">{line.text || ' '}</span>
        </div>
      ))}
    </div>
  )
}

function SideBySide({lines}: {lines: Line[]}) {
  const rows: {left?: Line; right?: Line}[] = []
  let pendingDel: Line[] = []
  const flush = () => { pendingDel.forEach(line => rows.push({left: line})); pendingDel = [] }
  for (const line of lines) {
    if (line.kind === 'del') { pendingDel.push(line); continue }
    if (line.kind === 'add') {
      const left = pendingDel.shift()
      rows.push(left ? {left, right: line} : {right: line})
      continue
    }
    flush()
    rows.push(line.kind === 'ctx' ? {left: line, right: line} : {left: line, right: line})
  }
  flush()
  return (
    <div className="diff diff--split" role="table" aria-label="双列差异">
      {rows.map((row, index) => (
        <div key={index} className="diff-line" role="row">
          <span className={`diff-cell${row.left ? ` diff-line--${row.left.kind}` : ''}`}>{row.left?.text ?? ''}</span>
          <span className={`diff-cell${row.right ? ` diff-line--${row.right.kind}` : ''}`}>{row.right?.text ?? ''}</span>
        </div>
      ))}
    </div>
  )
}

/* One file patch: header (path, +a −d, 已阅 marker, expand) + body.
   The diff body stays mounted when collapsed so review state and text
   remain inspectable; visibility is a styling concern only. */
export function PatchView({patch, awaiting, read, onMarkRead, defaultOpen = true}: {
  patch: Patch
  awaiting?: boolean
  read?: boolean
  onMarkRead?: () => void
  defaultOpen?: boolean
}) {
  const {diffView} = usePrefs()
  const [open, setOpen] = useState(defaultOpen)
  const stats = diffStats(patch.unified_diff)
  const lines = parseLines(patch.unified_diff)
  return (
    <div className="patch">
      <div className="patch__head">
        <span className="patch__path">{patch.path}</span>
        <span className="patch__stats">+{stats.add} −{stats.del}</span>
        {awaiting && (read
          ? <span className="patch__read">✓ 已阅</span>
          : <button type="button" className="small secondary" onClick={onMarkRead}>标记已阅</button>)}
        <button type="button" className="small ghost" aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? '收起 diff' : '查看 diff'}</button>
      </div>
      <div hidden={!open}>
        {diffView === 'side-by-side' ? <SideBySide lines={lines} /> : <Unified lines={lines} />}
      </div>
    </div>
  )
}
