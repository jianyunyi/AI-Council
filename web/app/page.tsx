'use client'

import Link from 'next/link'
import {useWorkspace} from '@/components/app-shell'
import {TaskLink} from '@/components/task-link'
import {StatusBadge} from '@/components/ui'
import {planScale, stageIndexOf, STAGES, uiStateOf} from '@/lib/task-state'
import {useIndexedTasks} from '@/lib/use-tasks'

export default function Home() {
  const {tasks, loading} = useIndexedTasks()
  const {workspaces} = useWorkspace()
  const rootOf = (id?: string) => workspaces.find(workspace => workspace.id === id)?.root ?? (id ? id.slice(0, 8) : '—')

  const waiting = tasks.filter(task => task.state === 'AWAITING_APPROVAL')
  const active = tasks.filter(task => ['delib', 'exec'].includes(uiStateOf(task.state)))
  const done = tasks.filter(task => task.state === 'SUCCEEDED')
  const failed = tasks.filter(task => task.state === 'FAILED')
  const recent = tasks.slice(0, 6)

  return (
    <div className="page-stack">
      <div className="page-head">
        <div>
          <h1>指挥中心</h1>
          <p className="page-sub">待批准队列、进行中的协商与最近任务。</p>
        </div>
        <div className="page-actions">
          <Link className="btn secondary" href="/tasks">全部任务</Link>
          <Link className="btn primary" href="/tasks/new">＋ 新建任务</Link>
        </div>
      </div>

      <section className="kpi-grid" aria-label="任务概览">
        <Link className="kpi kpi--wait" href="/tasks">
          <span className="kpi__num">{waiting.length}</span>
          <span className="kpi__label">◆ 待批准</span>
          <span className="kpi__hint">需要你逐项核对后放行</span>
        </Link>
        <div className="kpi">
          <span className="kpi__num">{active.length}</span>
          <span className="kpi__label">◐ 协商 / 执行中</span>
          <span className="kpi__hint">多模型协商与受控执行进行中</span>
        </div>
        <div className="kpi">
          <span className="kpi__num">{done.length}</span>
          <span className="kpi__label">✓ 已完成</span>
          <span className="kpi__hint">执行并通过验证的任务</span>
        </div>
        <div className="kpi">
          <span className="kpi__num">{failed.length}</span>
          <span className="kpi__label">✕ 失败待处理</span>
          <span className="kpi__hint">执行或协商失败，需要复盘</span>
        </div>
      </section>

      {loading && <p role="status">正在读取任务…</p>}
      {!loading && tasks.length === 0 && (
        <section className="card">
          <h2>还没有任务</h2>
          <p className="muted">新建一个任务，让多个模型独立提案、匿名互审、裁判决策，再由你批准后执行。本机任务索引保存在当前浏览器中；任务详情可通过任务 ID 直接打开。</p>
          <p><Link className="btn primary" href="/tasks/new">＋ 新建任务</Link></p>
        </section>
      )}

      <div className="home-grid">
        <section className="card" aria-labelledby="queue-title">
          <h2 id="queue-title">待批准队列</h2>
          {waiting.length === 0 && <p className="empty">✓ 当前没有待批准的计划。</p>}
          {waiting.map(task => (
            <div className="queue-item" key={task.id}>
              <div className="queue-item__main">
                <TaskLink task={task} />
                <div className="queue-item__meta">{rootOf(task.workspace_id)} · {planScale(task)} · 计划 v{task.plan_version ?? '—'}</div>
              </div>
              <StatusBadge state={task.state} size="md" />
              <Link className="btn small primary" href={`/tasks/${encodeURIComponent(task.id)}`}>去批准</Link>
            </div>
          ))}
        </section>

        <section className="card" aria-labelledby="active-title">
          <h2 id="active-title">进行中</h2>
          {active.length === 0 && <p className="empty">当前没有协商或执行中的任务。</p>}
          {active.map(task => {
            const index = stageIndexOf(task.state)
            const stage = STAGES[Math.min(Math.max(index, 0), STAGES.length - 1)]
            return (
              <div className="queue-item" key={task.id}>
                <div className="queue-item__main">
                  <TaskLink task={task} />
                  <div className="queue-item__meta">当前阶段：{stage?.label ?? task.state}（第 {Math.max(index + 1, 1)} / {STAGES.length} 步）</div>
                </div>
                <StatusBadge state={task.state} size="md" />
              </div>
            )
          })}
        </section>
      </div>

      <section className="card" aria-labelledby="recent-title">
        <h2 id="recent-title">最近任务</h2>
        {recent.length === 0 && <p className="empty">暂无记录。</p>}
        {recent.map(task => (
          <div className="queue-item" key={task.id}>
            <div className="queue-item__main">
              <TaskLink task={task} />
              <div className="queue-item__meta">{rootOf(task.workspace_id)} · {planScale(task)}</div>
            </div>
            <StatusBadge state={task.state} />
            <Link className="btn small secondary" href={`/tasks/${encodeURIComponent(task.id)}`}>查看</Link>
          </div>
        ))}
        {tasks.length > 0 && <p className="small muted">共 {tasks.length} 个任务保存在本机索引中。<Link href="/tasks">查看全部</Link></p>}
      </section>
    </div>
  )
}
