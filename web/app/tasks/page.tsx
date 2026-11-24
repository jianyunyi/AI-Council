'use client'

import Link from 'next/link'
import {useMemo, useState} from 'react'
import {useWorkspace} from '@/components/app-shell'
import {TaskLink} from '@/components/task-link'
import {ConfirmDialog, ConfirmState, showToast, StatusBadge} from '@/components/ui'
import {cancelTask} from '@/lib/api'
import {groupTasks, REJECTION_CATEGORY_LABELS, summaryScale, STATE_META, UiStateKey, uiStateOf} from '@/lib/task-state'
import {useTasks} from '@/lib/use-tasks'
import type {TaskSummary} from '@/lib/types'

const CANCELLABLE = new Set(['DRAFT', 'ANALYZING', 'PROPOSING', 'REVIEWING', 'JUDGING', 'RED_TEAM', 'REPLANNING', 'AWAITING_APPROVAL'])

export default function TaskListPage() {
  const {tasks, loading, reload} = useTasks()
  const {workspaces} = useWorkspace()
  const [query, setQuery] = useState('')
  const [stateFilter, setStateFilter] = useState<UiStateKey | 'all'>('all')
  const [grouped, setGrouped] = useState(true)
  const [confirm, setConfirm] = useState<ConfirmState | null>(null)
  const [pendingCancel, setPendingCancel] = useState<TaskSummary | null>(null)

  const rootOf = (id?: string) => workspaces.find(workspace => workspace.id === id)?.root ?? (id ? id.slice(0, 8) : '—')
  const counts = useMemo(() => {
    const map = new Map<UiStateKey, number>()
    for (const task of tasks) map.set(uiStateOf(task.state), (map.get(uiStateOf(task.state)) ?? 0) + 1)
    return map
  }, [tasks])

  const filtered = tasks.filter(task => {
    if (stateFilter !== 'all' && uiStateOf(task.state) !== stateFilter) return false
    const needle = query.trim().toLowerCase()
    if (needle && !`${task.title} ${task.requirement_excerpt} ${task.id}`.toLowerCase().includes(needle)) return false
    return true
  })

  function askCancel(task: TaskSummary) {
    setPendingCancel(task)
    setConfirm({
      title: '取消任务？',
      description: <p>任务「{task.title}」将被取消，协商与执行都会停止。此操作不可恢复。</p>,
      confirmLabel: '取消任务',
    })
  }

  async function resolveConfirm(confirmed: boolean) {
    setConfirm(null)
    const task = pendingCancel
    setPendingCancel(null)
    if (!confirmed || !task) return
    try {
      await cancelTask(task.id)
      showToast({message: '任务已取消。'})
      reload()
    } catch (error) {
      showToast({message: `取消失败：${error instanceof Error ? error.message : JSON.stringify(error)}`, error: true})
    }
  }

  function renderTable(rows: TaskSummary[]) {
    return (
      <div className="tw">
        <table className="tbl">
          <thead><tr><th>任务</th><th>状态</th><th>工作区</th><th>计划规模</th><th>操作</th></tr></thead>
          <tbody>
            {rows.map(task => (
              <tr key={task.id}>
                <td><TaskLink task={task} /><div className="small muted">{task.id}</div></td>
                <td><StatusBadge state={task.state} />{task.last_rejection && <div className="small muted">最近驳回 · {REJECTION_CATEGORY_LABELS[task.last_rejection.category] ?? task.last_rejection.category}</div>}</td>
                <td>{rootOf(task.workspace_id)}</td>
                <td>{summaryScale(task)}</td>
                <td>
                  <div className="row-actions">
                    <Link className="btn small secondary" href={`/tasks/${encodeURIComponent(task.id)}`}>查看</Link>
                    {CANCELLABLE.has(task.state) && <button className="btn small danger" onClick={() => askCancel(task)}>取消</button>}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <div className="page-stack">
      <div className="page-head">
        <div>
          <h1>任务</h1>
          <p className="page-sub">共 {tasks.length} 个任务（服务端列表，按更新时间倒序）。</p>
        </div>
        <div className="page-actions">
          <Link className="btn primary" href="/tasks/new">＋ 新建任务</Link>
        </div>
      </div>

      <div className="list-controls">
        <label className="search-field">搜索任务
          <input type="search" value={query} onChange={event => setQuery(event.target.value)} placeholder="按需求或任务 ID 搜索" aria-label="搜索任务" />
        </label>
        <div className="chip-row" role="group" aria-label="按状态筛选">
          <button className="chip" aria-pressed={stateFilter === 'all'} onClick={() => setStateFilter('all')}>全部 · {tasks.length}</button>
          {(Object.keys(STATE_META) as UiStateKey[]).filter(key => counts.has(key)).map(key => (
            <button key={key} className="chip" aria-pressed={stateFilter === key} onClick={() => setStateFilter(key)}>
              {STATE_META[key].symbol} {STATE_META[key].label} · {counts.get(key)}
            </button>
          ))}
        </div>
        <label className="check-field"><input type="checkbox" checked={grouped} onChange={event => setGrouped(event.target.checked)} />按状态分组</label>
      </div>

      {loading && <p role="status">正在读取任务…</p>}
      {!loading && filtered.length === 0 && (
        <section className="card"><p className="empty">{tasks.length === 0 ? '还没有任务。' : '没有符合筛选条件的任务。'}<Link href="/tasks/new">新建任务</Link></p></section>
      )}

      {!grouped && filtered.length > 0 && renderTable(filtered)}
      {grouped && groupTasks(filtered).map(group => (
        <section key={group.key} aria-label={`${STATE_META[group.key].label}任务`}>
          <h2 className="group-heading">{STATE_META[group.key].symbol} {STATE_META[group.key].label}<span className="small muted">{group.tasks.length} 个</span></h2>
          {renderTable(group.tasks)}
        </section>
      ))}

      <ConfirmDialog request={confirm} onResolve={resolveConfirm} />
    </div>
  )
}
