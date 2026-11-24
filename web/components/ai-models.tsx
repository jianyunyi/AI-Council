'use client'

import {useCallback,useEffect,useRef,useState} from 'react'
import {ConfirmDialog,ConfirmState,showToast} from '@/components/ui'
import {useOptionalAuth} from '@/components/auth-session'
import {AIModel,createAIModel,disableAIModel,listAIModels,listProviderKeys,listProviderModels,ProviderKeyProfile,retryAIModelWorkspace,updateAIModel} from '@/lib/api'

const HANDLE_RE = /^[a-z0-9][a-z0-9-]{1,31}$/

function suggestHandle(model:string) {
  return model.toLowerCase().replace(/[^a-z0-9-]+/g,'-').replace(/^-+|-+$/g,'').slice(0,32)
}

const WS_STATUS_LABEL:Record<string,string> = {provisioning:'开通中',ready:'就绪',error:'失败',archived:'已归档'}
const MODEL_STATUS_LABEL:Record<string,string> = {active:'启用',disabled:'已停用',degraded:'降级'}

export function AIModelsSection() {
  const auth = useOptionalAuth()
  const canManage = !auth || auth.isDesktop || auth.can('ai_model:manage')
  const [items,setItems] = useState<AIModel[]>([])
  const [profiles,setProfiles] = useState<ProviderKeyProfile[]>([])
  const [error,setError] = useState('')
  const [loading,setLoading] = useState(true)
  const [busy,setBusy] = useState(false)
  const [formOpen,setFormOpen] = useState(false)
  const [confirm,setConfirm] = useState<ConfirmState|null>(null)
  const [pendingDisable,setPendingDisable] = useState<AIModel|null>(null)
  const timer = useRef<ReturnType<typeof setTimeout>|null>(null)

  const load = useCallback(async () => {
    try {
      const raw = await listAIModels()
      const list = Array.isArray(raw) ? raw : []
      setItems(list)
      setError('')
      if (timer.current) clearTimeout(timer.current)
      if (list.some(m => m.workspace?.status === 'provisioning')) {
        timer.current = setTimeout(() => { void load() }, 3000)
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '读取 AI 模型失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
    listProviderKeys().then(setProfiles).catch(() => setProfiles([]))
    return () => { if (timer.current) clearTimeout(timer.current) }
  }, [load])

  async function act(fn:() => Promise<unknown>) {
    setBusy(true)
    try {
      await fn()
      await load()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  function askDisable(model:AIModel) {
    setPendingDisable(model)
    setConfirm({
      title:`停用 @${model.handle}？`,
      description:<p>停用后其私有工作区会被归档（不会删除），重新启用时自动恢复并重新开通。</p>,
      confirmLabel:'停用模型',
    })
  }

  async function resolveDisable(confirmed:boolean) {
    setConfirm(null)
    const model = pendingDisable
    setPendingDisable(null)
    if (!confirmed || !model) return
    await act(async () => {
      await disableAIModel(model.id)
      showToast({message:`已停用 @${model.handle}。`})
    })
  }

  return (
    <section className="card">
      <h2>AI 模型</h2>
      <p className="page-sub">每个启用的模型都有一个专属私有工作区，群聊中用 @handle 提及它。</p>
      {error && <p role="alert">{error}</p>}
      {loading ? <p role="status">正在读取 AI 模型…</p> : items.length === 0 ? (
        <p className="empty">还没有启用任何 AI 模型。</p>
      ) : (
        <div className="tw"><table className="tbl">
          <thead><tr><th>Handle</th><th>模型</th><th>厂商</th><th>状态</th><th>工作区</th><th>操作</th></tr></thead>
          <tbody>{items.map(m => (
            <tr key={m.id}>
              <td><code>@{m.handle}</code><div className="small muted">{m.display_name}</div></td>
              <td>{m.model}</td>
              <td>{m.provider_name || m.provider_profile_id}<div className="small muted">{m.provider_type}</div></td>
              <td><span className="badge">{MODEL_STATUS_LABEL[m.status] ?? m.status}</span></td>
              <td>{m.workspace ? (
                <span className="badge" title={m.workspace.last_error || ''}>{WS_STATUS_LABEL[m.workspace.status] ?? m.workspace.status}</span>
              ) : <span className="muted">—</span>}</td>
              <td>{canManage && <>
                {m.workspace?.status === 'error' && <button className="btn small" disabled={busy} onClick={() => void act(() => retryAIModelWorkspace(m.id))}>重试开通</button>}
                {m.status !== 'disabled' && <button className="btn small danger" disabled={busy} onClick={() => askDisable(m)}>停用</button>}
                {m.status === 'disabled' && <button className="btn small" disabled={busy} onClick={() => void act(() => updateAIModel(m.id,{status:'active'}))}>重新启用</button>}
              </>}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      {canManage && !formOpen && (
        <div className="form-actions"><button onClick={() => setFormOpen(true)}>启用模型</button></div>
      )}
      {canManage && formOpen && (
        <EnableModelForm profiles={profiles} onCancel={() => setFormOpen(false)} onCreated={async () => { setFormOpen(false); await load() }} />
      )}
      <ConfirmDialog request={confirm} onResolve={resolveDisable}/>
    </section>
  )
}

function EnableModelForm({profiles,onCancel,onCreated}:{profiles:ProviderKeyProfile[];onCancel:()=>void;onCreated:()=>Promise<void>}) {
  const [profileId,setProfileId] = useState('')
  const [models,setModels] = useState<string[]|null>([])
  const [model,setModel] = useState('')
  const [handle,setHandle] = useState('')
  const [displayName,setDisplayName] = useState('')
  const [busy,setBusy] = useState(false)
  const [error,setError] = useState('')

  useEffect(() => {
    if (!profileId) return
    setModels([])
    setModel('')
    listProviderModels(profileId)
      .then(result => setModels(result.models.map(m => m.id)))
      .catch(() => setModels(null))
  }, [profileId])

  const handleValid = HANDLE_RE.test(handle)

  async function submit() {
    setBusy(true)
    setError('')
    try {
      await createAIModel({provider_profile_id:profileId,model,handle,display_name:displayName || undefined})
      showToast({message:`已启用 @${handle}，正在开通其私有工作区。`})
      await onCreated()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '启用失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="form-grid">
      <label>Provider 档案
        <select value={profileId} onChange={event => setProfileId(event.target.value)}>
          <option value="">请选择…</option>
          {profiles.map(p => (
            <option key={p.id} value={p.id} disabled={!p.has_key}>{p.provider} · {p.model}{p.has_key ? '' : '（无密钥）'}</option>
          ))}
        </select>
      </label>
      <label>模型
        {models === null ? (
          <input value={model} onChange={event => setModel(event.target.value)} placeholder="模型 ID" autoComplete="off" />
        ) : (
          <select value={model} onChange={event => { setModel(event.target.value); if (!handle) setHandle(suggestHandle(event.target.value)) }}>
            <option value="">{models.length ? '请选择…' : '读取中…'}</option>
            {models.map(m => <option key={m} value={m}>{m}</option>)}
          </select>
        )}
      </label>
      {models === null && <p className="small muted">模型列表读取失败，可手动输入模型 ID，服务端会校验该档案是否提供此模型。</p>}
      <label>提及 handle
        <span className="key-field"><span>@</span><input value={handle} onChange={event => setHandle(event.target.value.toLowerCase())} autoComplete="off" /></span>
      </label>
      {!handleValid && handle && <p className="small muted">2–32 个字符：小写字母、数字、连字符，且不能以连字符开头。</p>}
      <label>显示名（可选）<input value={displayName} maxLength={120} onChange={event => setDisplayName(event.target.value)} autoComplete="off" /></label>
      <p className="small muted">启用后会为该模型开通一个专属私有工作区。</p>
      {error && <p role="alert">{error}</p>}
      <div className="form-actions">
        <button className="secondary" onClick={onCancel} disabled={busy}>取消</button>
        <button onClick={submit} disabled={busy || !profileId || !model || !handleValid}>{busy ? '正在启用…' : '启用'}</button>
      </div>
    </div>
  )
}
