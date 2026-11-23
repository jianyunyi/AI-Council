'use client'

import {useEffect, useRef, useState} from 'react'
import {ConfirmDialog, ConfirmState} from '@/components/ui'
import type {Task} from '@/lib/types'

const HOLD_MS = 1200

function useCountdown(expiresAt?: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!expiresAt) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [expiresAt])
  if (!expiresAt) return null
  const ms = expiresAt * 1000 - now
  if (ms <= 0) return {expired: true, label: '签名已过期'}
  const totalSeconds = Math.floor(ms / 1000)
  const label = `${String(Math.floor(totalSeconds / 60)).padStart(2, '0')}:${String(totalSeconds % 60).padStart(2, '0')}`
  return {expired: false, label}
}

/* Approval is deliberately two steps (§2.4): hold-to-approve after four
   checks, then a separate execute action inside the signature window.
   Readiness is visualised by CSS :has() + a CSS counter on the checkboxes;
   the JS below mirrors the same conditions for the actual action. */
export function ApprovalPanel({task, diffsRead, diffsTotal, canApprove, onPlanOpened, onApprove, onExecute, onReject}: {
  task: Task
  diffsRead: number
  diffsTotal: number
  canApprove: boolean
  onPlanOpened: () => void
  onApprove: () => Promise<void>
  onExecute: () => Promise<void>
  onReject: (reason: string) => Promise<void>
}) {
  const [checks, setChecks] = useState({1: false, 2: false, 3: false, 4: false})
  const [planOpened, setPlanOpened] = useState(false)
  const [progress, setProgress] = useState(0)
  const [busy, setBusy] = useState('')
  const [confirm, setConfirm] = useState<ConfirmState | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const countdown = useCountdown(task.approval_expires_at)

  const diffsDone = diffsRead >= diffsTotal
  const allChecked = checks[1] && checks[2] && checks[3] && checks[4]

  function stopHold() {
    if (timer.current) window.clearInterval(timer.current)
    timer.current = undefined
    setProgress(0)
  }

  function beginHold() {
    if (!allChecked || busy) return
    const started = Date.now()
    timer.current = window.setInterval(() => {
      const value = Math.min(1, (Date.now() - started) / HOLD_MS)
      setProgress(value)
      if (value >= 1) {
        stopHold()
        setBusy('approve')
        void onApprove().finally(() => setBusy(''))
      }
    }, 40)
  }

  useEffect(() => () => { if (timer.current) window.clearInterval(timer.current) }, [])

  async function resolveReject(confirmed: boolean, text: string) {
    setConfirm(null)
    if (!confirmed) return
    setBusy('reject')
    try { await onReject(text) } finally { setBusy('') }
  }

  if (!canApprove) {
    return (
      <section className="apbox" aria-label="待批准">
        <h2>◆ 待批准 · 计划 v{task.plan_version ?? task.plan?.version ?? '—'}</h2>
        <p className="muted">你没有批准权限（task:approve），只能查看计划，不能批准或驳回。</p>
      </section>
    )
  }

  if (task.approved) {
    return (
      <section className="apbox apbox--approved" aria-label="已批准">
        <h2>✓ 已批准 · 计划 v{task.plan_version ?? task.plan?.version ?? '—'}</h2>
        <p className="small muted">签名摘要 <code>{task.approval_hash?.slice(0, 16) ?? '—'}…</code></p>
        {countdown && <p className={countdown.expired ? 'countdown countdown--expired' : 'countdown'}>签名有效期 {countdown.label}</p>}
        <button className="btn-exec" disabled={busy === 'execute' || countdown?.expired}
          onClick={() => { setBusy('execute'); void onExecute().finally(() => setBusy('')) }}>
          {busy === 'execute' ? '执行中…' : '执行计划'}
        </button>
        <p className="small muted">批准与执行是两步：执行只会在签名有效期内按已批准的计划进行。</p>
      </section>
    )
  }

  return (
    <section className="apbox" aria-label="待批准">
      <h2>◆ 待批准 · 计划 v{task.plan_version ?? task.plan?.version ?? '—'}</h2>
      <p className="small muted">批准对象哈希 <code>{task.approval_hash?.slice(0, 16) ?? '—'}…</code>。批准后计划不可变，任何改动都会生成新版本并使本次批准失效。</p>

      <details className="plan-original" onToggle={event => { if ((event.target as HTMLDetailsElement).open) { setPlanOpened(true); onPlanOpened() } }}>
        <summary>被签名的计划原文（批准对象）</summary>
        <pre>{JSON.stringify(task.plan ?? {}, null, 2)}</pre>
      </details>

      <div className="ap-checks">
        <label className="ap-check">
          <input type="checkbox" data-k="1" disabled={!planOpened} checked={checks[1]}
            onChange={event => setChecks(current => ({...current, 1: event.target.checked}))} />
          <span>我已打开并阅读被签名的计划原文{!planOpened && '（先打开上方原文）'}</span>
        </label>
        <label className="ap-check">
          <input type="checkbox" data-k="2" disabled={!diffsDone} checked={checks[2]}
            onChange={event => setChecks(current => ({...current, 2: event.target.checked}))} />
          <span>我已逐个查看全部 {diffsTotal} 个文件的 diff（已阅 {diffsRead}/{diffsTotal}）</span>
        </label>
        <label className="ap-check">
          <input type="checkbox" data-k="3" checked={checks[3]}
            onChange={event => setChecks(current => ({...current, 3: event.target.checked}))} />
          <span>我理解批准后将按计划执行命令并修改工作区文件</span>
        </label>
        <label className="ap-check">
          <input type="checkbox" data-k="4" checked={checks[4]}
            onChange={event => setChecks(current => ({...current, 4: event.target.checked}))} />
          <span>我确认验收标准与恢复策略可接受</span>
        </label>
      </div>

      <button type="button" className="hold-btn" aria-disabled={!allChecked} aria-label="按住 1.2 秒批准"
        style={{'--p': progress} as React.CSSProperties}
        onPointerDown={beginHold} onPointerUp={stopHold} onPointerLeave={stopHold}
        onKeyDown={event => { if (event.key === ' ' || event.key === 'Enter') { event.preventDefault(); beginHold() } }}
        onKeyUp={event => { if (event.key === ' ' || event.key === 'Enter') stopHold() }}>
        <span className="hold-fill" aria-hidden="true" />
        <span className="hold-lbl" />
      </button>
      <p className="apbox-note">按住 1.2 秒完成批准；中途松手会取消。批准后还需单独点「执行计划」。</p>

      <button type="button" className="btn danger reject-btn" disabled={busy === 'reject'}
        onClick={() => setConfirm({
          title: '驳回这个计划？',
          description: <p>驳回后任务将回到协商阶段重新生成计划。</p>,
          confirmLabel: '驳回计划',
          textLabel: '驳回理由（至少 5 个字）',
          textPlaceholder: '说明哪里不对、期望怎么改',
          minLength: 5,
        })}>
        驳回
      </button>

      <ConfirmDialog request={confirm} onResolve={resolveReject} />
    </section>
  )
}
