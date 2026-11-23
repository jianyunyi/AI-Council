'use client'

import {FormEvent, useEffect, useState} from 'react'
import {useRouter} from 'next/navigation'
import {useWorkspace} from '@/components/app-shell'
import {createTask, createWorkspace, listWorkspaces, startTask} from '@/lib/api'
import {getDesktopRuntime} from '@/lib/desktop'
import {desktopBridge} from '@/lib/desktop-bridge'
import {rememberTask} from '@/lib/task-index'
import type {Workspace} from '@/lib/types'

const DRAFT_KEY = 'aicouncil.draft.newTask'
const STEPS = ['需求', '协商', '批准', '执行']

type Draft = {requirement: string; criteria: string[]}

export default function NewTask() {
  const router = useRouter()
  const shell = useWorkspace()
  const [requirement, setRequirement] = useState('')
  const [criteria, setCriteria] = useState<string[]>([''])
  const [newCriterion, setNewCriterion] = useState('')
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [workspaceID, setWorkspaceID] = useState('')
  const [loadingWorkspaces, setLoadingWorkspaces] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [draftRestored, setDraftRestored] = useState(false)

  /* Restore the autosaved draft (§2.3: 表单自动保存草稿). */
  useEffect(() => {
    try {
      const raw = window.localStorage.getItem(DRAFT_KEY)
      if (raw) {
        const draft = JSON.parse(raw) as Draft
        if (draft.requirement || draft.criteria?.some(item => item.trim())) {
          setRequirement(draft.requirement ?? '')
          setCriteria(draft.criteria?.length ? draft.criteria : [''])
          setDraftRestored(true)
        }
      }
    } catch { /* a corrupt draft is simply ignored */ }
  }, [])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      try { window.localStorage.setItem(DRAFT_KEY, JSON.stringify({requirement, criteria} satisfies Draft)) } catch { /* ignore */ }
    }, 300)
    return () => window.clearTimeout(timer)
  }, [requirement, criteria])

  useEffect(() => {
    let active = true
    async function load() {
      try {
        let available = await listWorkspaces()
        const bridge = desktopBridge()
        if (bridge) {
          try {
            const status = await bridge.status()
            if (status.state === 'ready' && status.workspace && !available.some(workspace => workspace.root === status.workspace)) {
              const registered = await createWorkspace('.')
              available = [registered, ...available]
            }
          } catch { /* The API list remains usable when the local runtime is stopped. */ }
        }
        if (active) {
          setWorkspaces(available)
          setWorkspaceID(current => current || (available.some(item => item.id === shell.currentId) ? shell.currentId : available[0]?.id ?? ''))
        }
      } catch (err) {
        if (active) setError(err instanceof Error ? err.message : '无法加载工作区')
      } finally {
        if (active) setLoadingWorkspaces(false)
      }
    }
    void load()
    return () => { active = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const acceptance = criteria.map(item => item.trim()).filter(Boolean)
  const selected = workspaces.find(workspace => workspace.id === workspaceID)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!workspaceID) { setError('请选择工作区。'); return }
    if (!requirement.trim()) { setError('请填写需求。'); return }
    if (!acceptance.length) { setError('请至少填写一条验收标准。'); return }
    setBusy(true)
    setError('')
    try {
      const task = await createTask({workspaceId: workspaceID, requirement, acceptance})
      rememberTask(task.id)
      await startTask(task.id)
      try { window.localStorage.removeItem(DRAFT_KEY) } catch { /* ignore */ }
      router.push(getDesktopRuntime() ? `/tasks/view?id=${encodeURIComponent(task.id)}` : `/tasks/${task.id}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '任务提交失败')
      setBusy(false)
    }
  }

  function discardDraft() {
    setRequirement('')
    setCriteria([''])
    setDraftRestored(false)
    try { window.localStorage.removeItem(DRAFT_KEY) } catch { /* ignore */ }
  }

  return (
    <div className="operation-page">
      <header className="operation-band">
        <div className="page-head">
          <div>
            <h1>新建任务</h1>
            <p className="page-sub">写清需求与验收标准，议会负责协商出可批准的计划。</p>
          </div>
        </div>
        <ol className="steps" aria-label="任务流程">
          {STEPS.map((step, index) => (
            <li key={step} className={index === 0 ? 'on' : undefined} aria-current={index === 0 ? 'step' : undefined}>{step}</li>
          ))}
        </ol>
      </header>

      <section className="operation-plane">
        <form className="card" onSubmit={submit} aria-label="新建任务表单">
          {draftRestored && (
            <p role="status">已恢复上次未提交的草稿。<button type="button" className="small ghost" onClick={discardDraft}>放弃草稿</button></p>
          )}
          <label>工作区
            <select required value={workspaceID} onChange={event => setWorkspaceID(event.target.value)} disabled={loadingWorkspaces}>
              <option value="">{loadingWorkspaces ? '正在加载工作区…' : '请选择已注册工作区'}</option>
              {workspaces.map(workspace => (
                <option key={workspace.id} value={workspace.id}>{workspace.root}{workspace.dirty ? '（Git 有未提交更改）' : ''}</option>
              ))}
            </select>
          </label>
          {selected?.dirty && <p className="field-note">该工作区 Git 有未提交更改，计划会以当前工作区状态为准。</p>}
          {!loadingWorkspaces && !workspaces.length && <p role="alert">尚未注册工作区。桌面版请先选择工作区并启动本地服务。</p>}

          <label>需求
            <textarea required rows={6} value={requirement} onChange={event => setRequirement(event.target.value)}
              placeholder="描述要完成的工作。支持 Markdown。" aria-describedby="requirement-count" />
          </label>
          <p className="field-note" id="requirement-count">{requirement.length} 字 · 支持 Markdown · 写清目标与约束，协商质量会更好。</p>

          <fieldset className="criteria-editor">
            <legend>验收标准</legend>
            {criteria.map((criterion, index) => (
              <div className="criteria-row" key={index}>
                <input value={criterion} aria-label={`验收标准 ${index + 1}`}
                  onChange={event => setCriteria(current => current.map((item, i) => i === index ? event.target.value : item))} />
                <button type="button" className="small ghost" aria-label={`删除验收标准 ${index + 1}`}
                  onClick={() => setCriteria(current => current.filter((_, i) => i !== index))}>删除</button>
              </div>
            ))}
            <div className="criteria-row">
              <input value={newCriterion} onChange={event => setNewCriterion(event.target.value)} placeholder="新增一条验收标准" aria-label="新增验收标准" />
              <button type="button" className="small secondary" onClick={() => {
                if (!newCriterion.trim()) return
                setCriteria(current => [...current.filter(item => item.trim()), newCriterion])
                setNewCriterion('')
              }}>添加</button>
            </div>
            <p className="field-note">每条标准都会成为验证阶段的检查项，至少填写一条。</p>
          </fieldset>

          {/* Quorum 与最大轮次未接后端（创建任务接口不接受这两个参数），按设计先隐藏，待后端支持后再上。 */}

          {error && <p role="alert">{error}</p>}
          <div className="form-actions">
            <button type="submit" disabled={busy || loadingWorkspaces || !workspaceID}>{busy ? '正在启动…' : '开始协商'}</button>
          </div>
        </form>

        <aside className="card flow-card" aria-label="协商流程说明">
          <h2>协商如何进行</h2>
          <ol className="flow-list">
            <li><strong>分析</strong><span>议会先理解需求与代码库。</span></li>
            <li><strong>提案与互评</strong><span>多个模型独立提案，匿名互评。</span></li>
            <li><strong>裁决与红队</strong><span>裁判合成计划，红队挑错后定稿。</span></li>
            <li><strong>你批准</strong><span>计划停在待批准，由你逐项核对后放行。</span></li>
            <li><strong>执行与验证</strong><span>按计划执行，并按验收标准逐项验证。</span></li>
          </ol>
        </aside>
      </section>
    </div>
  )
}
