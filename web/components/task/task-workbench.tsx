'use client'

import Link from 'next/link'
import {useCallback, useEffect, useState} from 'react'
import {useWorkspace} from '@/components/app-shell'
import {useOptionalAuth} from '@/components/auth-session'
import {taskTitle} from '@/components/task-link'
import {ConfirmDialog, ConfirmState, showToast, StatusBadge} from '@/components/ui'
import {approveTask, cancelTask, executeTask, getTask, rejectTask, startTask} from '@/lib/api'
import {subscribe, CouncilEvent} from '@/lib/events'
import {rememberTask} from '@/lib/task-index'
import {STAGES, stageIndexOf, stateLabel, uiStateOf} from '@/lib/task-state'
import type {Command, Task} from '@/lib/types'
import {ApprovalPanel} from './approval-panel'
import {PatchView} from './diff'

const CANCELLABLE = new Set(['DRAFT', 'ANALYZING', 'PROPOSING', 'REVIEWING', 'JUDGING', 'RED_TEAM', 'REPLANNING', 'AWAITING_APPROVAL'])
const TERMINAL = new Set(['SUCCEEDED', 'FAILED', 'CANCELLED'])

function CommandList({commands, title}: {commands: Command[]; title: string}) {
  if (!commands.length) return null
  return (
    <>
      <h3>{title}</h3>
      {commands.map((command, index) => (
        <div key={index} className="plan-command">
          <code>{command.executable} {command.args.join(' ')}</code>
          <span className="plan-meta">工作目录：{command.work_dir || '（工作区根）'} · 超时：{command.timeout_seconds ?? '默认'}s{command.purpose ? ` · ${command.purpose}` : ''}</span>
        </div>
      ))}
    </>
  )
}

function StageTimeline({task, events}: {task: Task; events: CouncilEvent[]}) {
  const current = stageIndexOf(task.state)
  const failed = task.state === 'FAILED'
  return (
    <section className="card" aria-label="时间线">
      <h2>时间线</h2>
      <ol className="timeline">
        {STAGES.map((stage, index) => {
          const state = task.state === 'SUCCEEDED' || index < current ? 'done' : index === current ? (failed ? 'fail' : 'current') : 'todo'
          return (
            <li key={stage.key} className={`tl-step tl-step--${state}`}>
              <span className="tl-dot" aria-hidden="true">{state === 'done' ? '✓' : state === 'fail' ? '✕' : state === 'current' ? '◐' : '○'}</span>
              <span className="tl-label">{stage.label}</span>
              <span className="tl-state">{state === 'done' ? '已完成' : state === 'fail' ? '失败' : state === 'current' ? '当前' : '未开始'}</span>
            </li>
          )
        })}
        {task.state === 'CANCELLED' && <li className="tl-step tl-step--fail"><span className="tl-dot" aria-hidden="true">✕</span><span className="tl-label">已取消</span><span className="tl-state">终止</span></li>}
      </ol>
      <h3>实时事件</h3>
      {events.length === 0 && <p className="small muted">暂无事件（协商与执行事件会通过实时通道推送到这里）。</p>}
      {events.slice(-12).map(event => <p key={event.id} className="small">#{event.id} {event.event}</p>)}
      <p className="small muted">协商记录明细（各模型提案与互评原文）后端暂未提供接口，时间线先按阶段与事件呈现。</p>
    </section>
  )
}

function StatusCard({task, onStart}: {task: Task; onStart: () => Promise<void>}) {
  const [busy, setBusy] = useState(false)
  const key = uiStateOf(task.state)
  return (
    <section className="card" aria-label="任务状态">
      <h2>{stateLabel(task.state)}</h2>
      {key === 'draft' && <>
        <p className="muted">任务已创建，尚未开始协商。</p>
        <button disabled={busy} onClick={() => { setBusy(true); void onStart().finally(() => setBusy(false)) }}>{busy ? '启动中…' : '开始协商'}</button>
      </>}
      {key === 'delib' && <p className="muted">议会协商进行中（当前：{task.state}）。计划生成后会停在「待批准」，等你核对放行。</p>}
      {key === 'exec' && <p className="muted">正在按已批准的计划执行，随后会按验收标准逐项验证。</p>}
      {key === 'done' && <p className="muted">执行完成并通过验证。{task.verification_artifact_id && <>可在「验证」页签或<Link href={`/tasks/${encodeURIComponent(task.id)}/artifacts/${encodeURIComponent(task.verification_artifact_id)}`}>制品审阅</Link>中查看验证报告。</>}</p>}
      {key === 'fail' && <p className="muted">任务失败。可以查看时间线与验证结果定位原因；需要重做时请新建任务。</p>}
      {key === 'cancelled' && <p className="muted">任务已取消，协商与执行均已停止。</p>}
    </section>
  )
}

export function TaskWorkbench({id}: {id: string}) {
  const [task, setTask] = useState<Task | null>(null)
  const [events, setEvents] = useState<CouncilEvent[]>([])
  const [error, setError] = useState('')
  const [tab, setTab] = useState<'plan' | 'verify'>('plan')
  const [readDiffs, setReadDiffs] = useState<ReadonlySet<string>>(new Set())
  const [confirm, setConfirm] = useState<ConfirmState | null>(null)
  const {workspaces} = useWorkspace()
  const auth = useOptionalAuth()
  const canApprove = !auth || auth.isDesktop || auth.can('task:approve')

  const load = useCallback(() => {
    return getTask(id).then(result => { setTask(result); rememberTask(result.id) }).catch(err => setError(err instanceof Error ? err.message : '任务加载失败'))
  }, [id])

  useEffect(() => {
    void load()
    return subscribe(id, event => {
      setEvents(current => current.some(item => item.id === event.id) ? current : [...current, event])
      if (event.event === 'state.changed') void load()
    })
  }, [id, load])

  if (!task) {
    return (
      <div className="page-stack">
        <p><Link href="/tasks">← 任务列表</Link></p>
        <h1>任务 {id}</h1>
        {error ? <p role="alert">{error}</p> : <p role="status">加载任务中…</p>}
      </div>
    )
  }

  const patches = task.plan?.patches ?? []
  const awaiting = task.state === 'AWAITING_APPROVAL'

  async function approve() {
    if (!task || !task.approval_hash || !task.plan_version) return
    setError('')
    try {
      const approved = await approveTask(id, task.plan_version, task.approval_hash)
      setTask(approved)
      showToast({message: '计划已批准。请在签名有效期内单独执行。'})
    } catch (err) {
      setError(err instanceof Error ? err.message : '批准失败')
    }
  }

  async function execute() {
    setError('')
    try {
      const executed = await executeTask(id)
      setTask(executed)
      showToast({message: '已开始执行。'})
    } catch (err) {
      setError(err instanceof Error ? err.message : '执行失败')
    }
  }

  async function reject(reason: string) {
    setError('')
    try {
      const rejected = await rejectTask(id, reason)
      setTask(rejected)
      setReadDiffs(new Set())
      showToast({message: '计划已驳回，任务回到协商阶段。'})
    } catch (err) {
      setError(err instanceof Error ? err.message : '驳回失败')
    }
  }

  async function resolveCancel(confirmed: boolean) {
    setConfirm(null)
    if (!confirmed) return
    try {
      const cancelled = await cancelTask(id)
      setTask(cancelled)
      showToast({message: '任务已取消。'})
    } catch (err) {
      setError(err instanceof Error ? err.message : '取消失败')
    }
  }

  const workspaceRoot = workspaces.find(workspace => workspace.id === task.workspace_id)?.root

  return (
    <div className="page-stack">
      <div className="page-head">
        <div>
          <p className="small"><Link href="/tasks">← 任务列表</Link></p>
          <h1><span data-vt="task-title">{taskTitle(task)}</span></h1>
          <p className="page-sub">
            {task.id} · {workspaceRoot ?? task.workspace_id ?? '—'} · 计划 v{task.plan_version ?? task.plan?.version ?? '—'}
          </p>
        </div>
        <div className="page-actions">
          <StatusBadge state={task.state} size="md" />
          {CANCELLABLE.has(task.state) && !task.approved && (
            <button className="btn danger" onClick={() => setConfirm({
              title: '取消任务？',
              description: <p>任务将被取消，协商与执行都会停止。此操作不可恢复。</p>,
              confirmLabel: '取消任务',
            })}>取消任务</button>
          )}
        </div>
      </div>

      <p className="requirement-full">{task.requirement}</p>
      {error && <p role="alert">{error}</p>}

      <div className="task-grid">
        <div className="task-col-main">
          <div className="card">
            <div className="tabs" role="tablist" aria-label="任务详情">
              <button role="tab" aria-selected={tab === 'plan'} onClick={() => setTab('plan')}>计划</button>
              <button role="tab" aria-selected={tab === 'verify'} onClick={() => setTab('verify')}>验证</button>
            </div>

            {tab === 'plan' && (
              <div role="tabpanel" aria-label="计划">
                {!task.plan && <p className="empty">{TERMINAL.has(task.state) ? '该任务没有生成计划。' : '计划生成中…协商完成后，计划会停在这里等待批准。'}</p>}
                {task.plan && (
                  <>
                    <p className="plan-summary">
                      <span>{patches.length} 个文件</span>
                      <span>{(task.plan.commands ?? []).length} 条命令</span>
                      <span>{(task.plan.acceptance ?? []).length} 条验收</span>
                      <span>{(task.plan.recovery ?? []).length} 条恢复</span>
                    </p>
                    <h3>文件变更</h3>
                    {patches.map(patch => (
                      <PatchView key={patch.path} patch={patch} awaiting={awaiting && !task.approved}
                        read={readDiffs.has(patch.path)}
                        onMarkRead={() => setReadDiffs(current => new Set(current).add(patch.path))} />
                    ))}
                    {patches.length === 0 && <p className="empty">本计划没有文件变更。</p>}
                    <CommandList commands={task.plan.commands ?? []} title="执行命令" />
                    <CommandList commands={task.plan.verification_commands ?? []} title="验证命令" />
                    <h3>验收标准</h3>
                    <ul>{(task.plan.acceptance ?? []).map((item, index) => <li key={index}>{item}</li>)}</ul>
                    <h3>恢复策略</h3>
                    <ul>{(task.plan.recovery ?? []).map((item, index) => <li key={index}>{item}</li>)}</ul>
                  </>
                )}
              </div>
            )}

            {tab === 'verify' && (
              <div role="tabpanel" aria-label="验证">
                {!task.verification && !task.verification_artifact_id && <p className="empty">尚无验证结果。执行完成后，验证步骤与结果会显示在这里。</p>}
                {task.verification && (
                  <>
                    <p>状态：{task.verification.status ?? '—'}{task.verification.error_code ? ` · 错误码：${task.verification.error_code}` : ''}</p>
                    <div className="tw">
                      <table className="tbl">
                        <thead><tr><th>步骤</th><th>类型</th><th>退出码</th><th>状态</th><th>耗时</th></tr></thead>
                        <tbody>
                          {(task.verification.steps ?? []).map((step, index) => (
                            <tr key={index}><td>{step.name}</td><td>{step.kind}</td><td>{step.exit_code}</td><td>{step.status ?? (step.exit_code === 0 ? '通过' : '失败')}</td><td>{step.duration_ms != null ? `${step.duration_ms} ms` : '—'}</td></tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                    {task.verification.actual_diff && <><h3>实际变更</h3><pre className="artifact-json">{task.verification.actual_diff}</pre></>}
                  </>
                )}
                {task.verification_artifact_id && (
                  <p><Link className="btn secondary" href={`/tasks/${encodeURIComponent(task.id)}/artifacts/${encodeURIComponent(task.verification_artifact_id)}`}>打开验证报告制品</Link></p>
                )}
              </div>
            )}
          </div>
        </div>

        <aside className="task-col-side">
          {awaiting
            ? <ApprovalPanel task={task} diffsRead={readDiffs.size} diffsTotal={patches.length} canApprove={canApprove}
                onPlanOpened={() => {}} onApprove={approve} onExecute={execute} onReject={reject} />
            : <StatusCard task={task} onStart={async () => { const started = await startTask(id); setTask(started) }} />}
        </aside>

        <div className="task-col-tl">
          <StageTimeline task={task} events={events} />
        </div>
      </div>

      <ConfirmDialog request={confirm} onResolve={resolveCancel} />
    </div>
  )
}
