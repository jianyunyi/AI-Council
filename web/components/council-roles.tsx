'use client'

import {useCallback, useEffect, useRef, useState} from 'react'
import {showToast} from '@/components/ui'
import {
  ApiError, CouncilRole, CouncilRolesView, CouncilScope, CouncilSlot, CouncilValidationIssue,
  deleteCouncilRoles, getCouncilRoles, listProviderKeys, listProviderModels, listWorkspaces,
  ProviderKeyProfile, putCouncilRoles, validateCouncilRoles,
} from '@/lib/api'
import {Workspace} from '@/lib/types'

const ROLE_META: {role: CouncilRole; label: string; hint: string; max: number}[] = [
  {role: 'proposer', label: '提案', hint: '1–8 个席位，单个失败自动跳过', max: 8},
  {role: 'reviewer', label: '评审', hint: '0–8 个席位，可为空（跳过互评）', max: 8},
  {role: 'judge', label: '裁决', hint: '恰好 1 个主选 + 至多 2 个故障转移', max: 3},
  {role: 'red_team', label: '红队', hint: '0–1 个主选 + 至多 2 个故障转移', max: 3},
]

const SOURCE_LABEL: Record<string, string> = {
  workspace: '本工作区配置',
  global: '全局配置',
  legacy: '默认（按已配置档案推导）',
}

function previewToSlots(view: CouncilRolesView): CouncilSlot[] {
  const preview = view.resolved_preview
  const groups = [preview.proposers, preview.reviewers, preview.judge, preview.red_team]
  const slots: CouncilSlot[] = []
  for (const group of groups) {
    for (const slot of group ?? []) {
      slots.push({
        role: slot.role as CouncilRole, ordinal: slot.ordinal,
        provider_profile_id: slot.provider_profile_id, model: slot.model,
        params: slot.params, enabled: true,
      })
    }
  }
  return slots
}

export function CouncilRolesSection() {
  const [scopeType, setScopeType] = useState<'global' | 'workspace'>('global')
  const [workspaceId, setWorkspaceId] = useState('')
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [profiles, setProfiles] = useState<ProviderKeyProfile[]>([])
  const [view, setView] = useState<CouncilRolesView | null>(null)
  const [slots, setSlots] = useState<CouncilSlot[]>([])
  const [issues, setIssues] = useState<CouncilValidationIssue[]>([])
  const [conflict, setConflict] = useState(false)
  const [unavailable, setUnavailable] = useState(false)
  const [busy, setBusy] = useState(false)
  const [models, setModels] = useState<Record<string, string[]>>({})
  const loadedScope = useRef('')

  const scope: CouncilScope = scopeType === 'global' ? {scopeType: 'global'} : {scopeType: 'workspace', scopeId: workspaceId}
  const scopeKey = `${scope.scopeType}:${scope.scopeId ?? ''}`

  useEffect(() => {
    listProviderKeys().then(setProfiles).catch(() => setProfiles([]))
    listWorkspaces().then(items => {
      setWorkspaces(items)
      if (items.length > 0) setWorkspaceId(current => current || items[0].id)
    }).catch(() => setWorkspaces([]))
  }, [])

  const load = useCallback(async () => {
    if (scopeType === 'workspace' && !workspaceId) return
    try {
      const next = await getCouncilRoles(scope)
      setView(next)
      setSlots(next.roles.length > 0 ? next.roles : previewToSlots(next))
      setIssues([])
      setConflict(false)
      setUnavailable(false)
      loadedScope.current = scopeKey
    } catch (error) {
      if (error instanceof ApiError && (error.status === 503 || error.status === 404)) {
        setUnavailable(true)
        return
      }
      showToast({message: error instanceof Error ? error.message : '角色分配加载失败', error: true})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey])

  useEffect(() => { load() }, [load])

  // Live validation against the server-side rules.
  useEffect(() => {
    if (!view || loadedScope.current !== scopeKey) return
    const timer = setTimeout(() => {
      validateCouncilRoles(scope, slots)
        .then(result => setIssues(result.errors))
        .catch(() => {})
    }, 300)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slots, scopeKey])

  const ensureModels = useCallback((profileId: string) => {
    if (!profileId || models[profileId]) return
    listProviderModels(profileId)
      .then(result => setModels(current => ({...current, [profileId]: result.models.map(model => model.id)})))
      .catch(() => {})
  }, [models])

  function slotsFor(role: CouncilRole) {
    return slots.filter(slot => slot.role === role).sort((a, b) => a.ordinal - b.ordinal)
  }

  function replaceRole(role: CouncilRole, next: CouncilSlot[]) {
    const renumbered = next.map((slot, index) => ({...slot, ordinal: index}))
    setSlots(current => [...current.filter(slot => slot.role !== role), ...renumbered])
  }

  function updateSlot(role: CouncilRole, ordinal: number, patch: Partial<CouncilSlot>) {
    replaceRole(role, slotsFor(role).map(slot => (slot.ordinal === ordinal ? {...slot, ...patch} : slot)))
  }

  function addSlot(role: CouncilRole) {
    const profile = profiles[0]
    if (!profile) return
    ensureModels(profile.id)
    replaceRole(role, [...slotsFor(role), {
      role, ordinal: slotsFor(role).length,
      provider_profile_id: profile.id, model: profile.model, enabled: true,
    }])
  }

  function removeSlot(role: CouncilRole, ordinal: number) {
    replaceRole(role, slotsFor(role).filter(slot => slot.ordinal !== ordinal))
  }

  async function save() {
    if (!view) return
    setBusy(true)
    try {
      const next = await putCouncilRoles(scope, view.version, slots)
      setView(next)
      setSlots(next.roles)
      setIssues([])
      setConflict(false)
      showToast({message: '议会角色分配已保存。'})
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true)
      } else if (error instanceof ApiError && error.status === 422 && error.issues) {
        setIssues(error.issues)
        showToast({message: '分配未通过校验，请修正标红问题。', error: true})
      } else {
        showToast({message: error instanceof Error ? error.message : '保存失败', error: true})
      }
    } finally { setBusy(false) }
  }

  async function revertWorkspace() {
    setBusy(true)
    try {
      const next = await deleteCouncilRoles(scope)
      setView(next)
      setSlots(next.roles.length > 0 ? next.roles : previewToSlots(next))
      showToast({message: '已删除本工作区配置，恢复继承全局/默认。'})
    } catch (error) {
      showToast({message: error instanceof Error ? error.message : '删除失败', error: true})
    } finally { setBusy(false) }
  }

  if (unavailable) {
    return (
      <section className="card" aria-labelledby="council-roles-title">
        <h2 id="council-roles-title">议会角色分配</h2>
        <p className="muted">当前服务未启用角色分配（需要服务端模式与提供方密钥存储）。</p>
      </section>
    )
  }

  const preview = view?.resolved_preview
  const inherits = scopeType === 'workspace' && view && view.effective_source !== 'workspace'

  return (
    <section className="card" aria-labelledby="council-roles-title">
      <h2 id="council-roles-title">议会角色分配</h2>
      <p className="muted">为提案、评审、裁决、红队四个角色指定提供方档案与模型。工作区配置覆盖全局配置；都没有时按旧版默认（每个已配密钥的档案出一个席位）运行。保存后新任务按新分配组建议会，运行开始时会把生效配置快照写入运行记录。</p>

      <div className="form-actions" role="group" aria-label="配置范围">
        <button className={scopeType === 'global' ? '' : 'secondary'} onClick={() => setScopeType('global')}>全局</button>
        <button className={scopeType === 'workspace' ? '' : 'secondary'} onClick={() => setScopeType('workspace')} disabled={workspaces.length === 0}>工作区</button>
        {scopeType === 'workspace' && (
          <select aria-label="选择工作区" value={workspaceId} onChange={event => setWorkspaceId(event.target.value)}>
            {workspaces.map(ws => <option key={ws.id} value={ws.id}>{ws.root || ws.id}</option>)}
          </select>
        )}
        {view && <span className="badge">生效来源：{SOURCE_LABEL[view.effective_source]}{inherits ? '（继承）' : ''} · 版本 {view.version}</span>}
      </div>

      {conflict && (
        <p role="alert">配置已被其他会话更新（版本冲突）。<button className="secondary" onClick={load}>重新加载</button></p>
      )}
      {profiles.length === 0 && <p role="alert">尚未配置任何提供方档案，请先在上方添加提供方密钥。</p>}

      <div style={{display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(230px,1fr))', gap: 12}}>
        {ROLE_META.map(meta => (
          <div key={meta.role} style={{border: '1px solid var(--border, #8884)', borderRadius: 8, padding: 10}}>
            <h3 style={{margin: '0 0 2px', fontSize: 14}}>{meta.label}</h3>
            <p className="muted" style={{margin: '0 0 8px', fontSize: 12}}>{meta.hint}</p>
            {slotsFor(meta.role).map(slot => (
              <div key={slot.ordinal} style={{borderTop: '1px dashed var(--border, #8884)', padding: '8px 0'}}>
                <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'center'}}>
                  <strong style={{fontSize: 12}}>{meta.role === 'judge' || meta.role === 'red_team' ? (slot.ordinal === 0 ? '主选' : `故障转移 ${slot.ordinal}`) : `席位 ${slot.ordinal + 1}`}</strong>
                  <button className="secondary" aria-label={`删除${meta.label}席位 ${slot.ordinal + 1}`} onClick={() => removeSlot(meta.role, slot.ordinal)}>删除</button>
                </div>
                <label style={{display: 'block', fontSize: 12}}>提供方档案
                  <select value={slot.provider_profile_id} onChange={event => {
                    const profile = profiles.find(item => item.id === event.target.value)
                    if (profile) { ensureModels(profile.id); updateSlot(meta.role, slot.ordinal, {provider_profile_id: profile.id, model: profile.model}) }
                  }}>
                    {profiles.map(profile => <option key={profile.id} value={profile.id}>{profile.provider} · {profile.model}{profile.has_key ? '' : '（无密钥）'}</option>)}
                  </select>
                </label>
                <label style={{display: 'block', fontSize: 12}}>模型
                  <select value={slot.model} onFocus={() => ensureModels(slot.provider_profile_id)} onChange={event => updateSlot(meta.role, slot.ordinal, {model: event.target.value})}>
                    {(models[slot.provider_profile_id] ?? [slot.model]).map(model => <option key={model} value={model}>{model}</option>)}
                  </select>
                </label>
                <label style={{display: 'block', fontSize: 12}}>温度（0–2，可空）
                  <input type="number" min={0} max={2} step={0.1} value={slot.params?.temperature ?? ''}
                    onChange={event => {
                      const raw = event.target.value
                      const params = {...(slot.params ?? {})}
                      if (raw === '') delete params.temperature; else params.temperature = Number(raw)
                      updateSlot(meta.role, slot.ordinal, {params})
                    }} />
                </label>
                <label style={{display: 'flex', gap: 6, alignItems: 'center', fontSize: 12}}>
                  <input type="checkbox" checked={slot.enabled} onChange={event => updateSlot(meta.role, slot.ordinal, {enabled: event.target.checked})} /> 启用
                </label>
              </div>
            ))}
            <button className="secondary" onClick={() => addSlot(meta.role)} disabled={slotsFor(meta.role).length >= meta.max || profiles.length === 0}>添加席位</button>
          </div>
        ))}
      </div>

      {issues.length > 0 && (
        <ul role="alert" style={{margin: '10px 0'}}>
          {issues.map((issueItem, index) => <li key={index}>{issueItem.message}（{issueItem.code}）</li>)}
        </ul>
      )}

      <div className="form-actions">
        <button onClick={save} disabled={busy || !view || issues.length > 0}>保存分配</button>
        {scopeType === 'workspace' && view && view.version > 0 && (
          <button className="danger" onClick={revertWorkspace} disabled={busy}>删除本工作区配置（恢复继承）</button>
        )}
      </div>

      {preview && (
        <div style={{marginTop: 10}}>
          <h3 style={{fontSize: 14, margin: '0 0 6px'}}>生效配置预览</h3>
          <p className="muted" style={{fontSize: 12}}>
            提案：{preview.proposers.map(slot => `${slot.provider}/${slot.model}`).join('、') || '无'}；
            评审：{preview.reviewers.map(slot => `${slot.provider}/${slot.model}`).join('、') || '无'}；
            裁决：{preview.judge.map(slot => `${slot.provider}/${slot.model}`).join(' → ') || '无'}；
            红队：{preview.red_team.map(slot => `${slot.provider}/${slot.model}`).join(' → ') || '无'}。
          </p>
        </div>
      )}
    </section>
  )
}
