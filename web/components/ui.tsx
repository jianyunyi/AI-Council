'use client'

import {FormEvent, ReactNode, useEffect, useRef, useState} from 'react'
import {STATE_META, uiStateOf} from '@/lib/task-state'

/* Status is always encoded three ways: color, symbol, Chinese label (§1.1). */
export function StatusBadge({state, size = 'sm'}: {state: string; size?: 'sm' | 'md'}) {
  const key = uiStateOf(state)
  const meta = STATE_META[key]
  return (
    <span className={`badge badge--${key}${size === 'md' ? ' badge--md' : ''}`} aria-label={`状态：${meta.label}`}>
      <i className="badge-sym" aria-hidden="true">{meta.symbol}</i>
      {meta.label}
    </span>
  )
}

export type ConfirmState = {
  title: string
  description?: ReactNode
  confirmLabel: string
  /* Level 3 (§4.3): the user must type this exact text before confirming. */
  typeName?: string
  /* Level 2 with a required free-text reason (e.g. task rejection). */
  textLabel?: string
  textPlaceholder?: string
  minLength?: number
}

/* Native <dialog> + showModal (§3.4 / §4.3). The browser provides focus
   containment, inert background, Esc handling and focus return; no focus
   trap, role or aria-modal is added. `autofocus` sits on Cancel. */
export function ConfirmDialog({request, onResolve}: {
  request: ConfirmState | null
  onResolve: (confirmed: boolean, text: string) => void
}) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const formRef = useRef<HTMLFormElement>(null)
  const [text, setText] = useState('')
  const pending = useRef(false)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (request && !dialog.open) {
      setText('')
      pending.current = true
      try { dialog.showModal() } catch { dialog.setAttribute('open', '') }
    }
    if (!request && dialog.open) dialog.close('cancel')
  }, [request])

  const typeNameOk = !request?.typeName || text === request.typeName
  const lengthOk = !request?.minLength || text.trim().length >= request.minLength
  const canConfirm = Boolean(request) && typeNameOk && lengthOk

  function handleClose() {
    const dialog = dialogRef.current
    if (!dialog || !pending.current) return
    pending.current = false
    const value = dialog.returnValue
    dialog.returnValue = ''
    formRef.current?.reset()
    onResolve(value === 'confirm', text)
  }

  function confirm(event: FormEvent) {
    event.preventDefault()
    if (!canConfirm) return
    if (formRef.current && !formRef.current.reportValidity()) return
    dialogRef.current?.close('confirm')
  }

  return (
    <dialog ref={dialogRef} aria-labelledby="confirm-dialog-title" onClose={handleClose}>
      {request && <form ref={formRef} method="dialog" onSubmit={confirm}>
        <h2 id="confirm-dialog-title">{request.title}</h2>
        {request.description}
        {request.typeName && (
          <label>输入「{request.typeName}」以确认
            <input value={text} onChange={event => setText(event.target.value)} required autoComplete="off" aria-label={`输入 ${request.typeName} 以确认`} />
          </label>
        )}
        {request.textLabel && (
          <label>{request.textLabel}
            <textarea value={text} onChange={event => setText(event.target.value)} required minLength={request.minLength}
              placeholder={request.textPlaceholder} aria-label={request.textLabel} />
          </label>
        )}
        <div className="dlg-actions">
          <button type="button" className="secondary" autoFocus onClick={() => dialogRef.current?.close('cancel')}>取消</button>
          <button type="submit" className="danger" disabled={!canConfirm}>{request.confirmLabel}</button>
        </div>
      </form>}
    </dialog>
  )
}

/* Toasts (§3.4): non-blocking, bottom-right; errors stay until dismissed. */
export type ToastSpec = {id: number; message: string; error?: boolean; actionLabel?: string; onAction?: () => void}

const TOAST_EVENT = 'aicouncil:toast'
let toastSeq = 0

export function showToast(spec: Omit<ToastSpec, 'id'>) {
  window.dispatchEvent(new CustomEvent<ToastSpec>(TOAST_EVENT, {detail: {...spec, id: ++toastSeq}}))
}

export function ToastHost() {
  const [toasts, setToasts] = useState<ToastSpec[]>([])
  useEffect(() => {
    const listener = (event: Event) => {
      const spec = (event as CustomEvent<ToastSpec>).detail
      setToasts(current => [...current, spec])
      if (!spec.error) window.setTimeout(() => setToasts(current => current.filter(item => item.id !== spec.id)), 5000)
    }
    window.addEventListener(TOAST_EVENT, listener)
    return () => window.removeEventListener(TOAST_EVENT, listener)
  }, [])
  if (!toasts.length) return null
  return (
    <div className="toast-stack" aria-live="polite">
      {toasts.map(toast => (
        <div key={toast.id} className={`toast${toast.error ? ' toast--error' : ''}`} role={toast.error ? 'alert' : 'status'}>
          <span>{toast.message}</span>
          {toast.actionLabel && <button className="small secondary" onClick={() => { toast.onAction?.(); setToasts(current => current.filter(item => item.id !== toast.id)) }}>{toast.actionLabel}</button>}
          <button className="small ghost" aria-label="关闭通知" onClick={() => setToasts(current => current.filter(item => item.id !== toast.id))}>✕</button>
        </div>
      ))}
    </div>
  )
}
