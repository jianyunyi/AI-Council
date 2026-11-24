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
  /* Optional single-select shown above the text field (e.g. rejection category). */
  selectLabel?: string
  selectOptions?: readonly (readonly [string, string])[]
  selectDefault?: string
}

/* Native <dialog> + showModal (§3.4 / §4.3). The browser provides focus
   containment, inert background, Esc handling and focus return; no focus
   trap, role or aria-modal is added. `autofocus` sits on Cancel.
   Where the platform lacks dialog methods (older engines, jsdom), we fall
   back to the `open` attribute plus a synthetic close event. */
export function openDialog(dialog: HTMLDialogElement) {
  if (typeof dialog.showModal === 'function') {
    try { dialog.showModal(); return } catch { /* fall through */ }
  }
  dialog.setAttribute('open', '')
}

export function closeDialog(dialog: HTMLDialogElement, value: string) {
  if (typeof dialog.close === 'function') {
    try { dialog.close(value); return } catch { /* fall through */ }
  }
  dialog.returnValue = value
  dialog.removeAttribute('open')
  dialog.dispatchEvent(new Event('close'))
}
export function ConfirmDialog({request, onResolve}: {
  request: ConfirmState | null
  onResolve: (confirmed: boolean, text: string, selectValue: string) => void
}) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const formRef = useRef<HTMLFormElement>(null)
  const [text, setText] = useState('')
  const [selectValue, setSelectValue] = useState('')
  const pending = useRef(false)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (request && !dialog.open) {
      setText('')
      setSelectValue(request.selectDefault ?? request.selectOptions?.[0]?.[0] ?? '')
      pending.current = true
      openDialog(dialog)
    }
    if (!request && dialog.open) closeDialog(dialog, 'cancel')
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
    onResolve(value === 'confirm', text, selectValue)
  }

  function confirm(event: FormEvent) {
    event.preventDefault()
    if (!canConfirm) return
    if (formRef.current && !formRef.current.reportValidity()) return
    if (dialogRef.current) closeDialog(dialogRef.current, 'confirm')
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
        {request.selectLabel && request.selectOptions && (
          <label>{request.selectLabel}
            <select value={selectValue} onChange={event => setSelectValue(event.target.value)} aria-label={request.selectLabel}>
              {request.selectOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
          </label>
        )}
        {request.textLabel && (
          <label>{request.textLabel}
            <textarea value={text} onChange={event => setText(event.target.value)} required minLength={request.minLength}
              placeholder={request.textPlaceholder} aria-label={request.textLabel} />
          </label>
        )}
        <div className="dlg-actions">
          <button type="button" className="secondary" autoFocus onClick={() => { if (dialogRef.current) closeDialog(dialogRef.current, 'cancel') }}>取消</button>
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
