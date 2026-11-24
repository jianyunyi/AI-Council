'use client'

import Link from 'next/link'
import {FormEvent, useState} from 'react'
import {useRouter} from 'next/navigation'
import {TaskLink} from '@/components/task-link'
import {useTasks} from '@/lib/use-tasks'

/* The artifact store is content-addressed and only reachable by ID through
   GET /tasks/:id/artifacts/:artifactId. The task payload exposes exactly one
   artifact ID (verification_artifact_id), so this page lists those, links to
   each task's plan tab, and offers a by-ID lookup for everything else. */
export default function ArtifactsPage() {
  const {tasks, loading} = useTasks()
  const router = useRouter()
  const [taskId, setTaskId] = useState('')
  const [artifactId, setArtifactId] = useState('')
  const verifiable = tasks.filter(task => task.verification_artifact_id)

  function open(event: FormEvent) {
    event.preventDefault()
    if (taskId.trim() && artifactId.trim()) {
      router.push(`/tasks/${encodeURIComponent(taskId.trim())}/artifacts/${encodeURIComponent(artifactId.trim())}`)
    }
  }

  return (
    <div className="page-stack">
      <div className="page-head">
        <div>
          <h1>制品</h1>
          <p className="page-sub">计划、验证报告等制品按内容哈希寻址，打开时由服务端校验哈希。</p>
        </div>
      </div>

      <section className="card" aria-labelledby="verify-title">
        <h2 id="verify-title">验证报告</h2>
        {loading && <p role="status">正在读取任务…</p>}
        {!loading && verifiable.length === 0 && <p className="empty">暂无验证报告。任务执行并完成验证后，报告会出现在这里。</p>}
        <div className="tw">
          <table className="tbl">
            <thead><tr><th>任务</th><th>制品</th><th>操作</th></tr></thead>
            <tbody>
              {verifiable.map(task => (
                <tr key={task.id}>
                  <td><TaskLink task={task} /></td>
                  <td>验证报告<div className="small muted">{task.verification_artifact_id}</div></td>
                  <td><Link className="btn small secondary" href={`/tasks/${encodeURIComponent(task.id)}/artifacts/${encodeURIComponent(task.verification_artifact_id!)}`}>审阅</Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="card" aria-labelledby="plan-title">
        <h2 id="plan-title">执行计划</h2>
        <p className="muted">每个任务的计划（补丁、命令、验收与恢复）随任务一同保存，在任务详情的「计划」页签中审阅。</p>
        <div className="tw">
          <table className="tbl">
            <thead><tr><th>任务</th><th>计划版本</th><th>操作</th></tr></thead>
            <tbody>
              {tasks.filter(task => task.state !== 'DRAFT').map(task => (
                <tr key={task.id}>
                  <td><TaskLink task={task} /></td>
                  <td>v{task.plan_version}</td>
                  <td><Link className="btn small secondary" href={`/tasks/${encodeURIComponent(task.id)}`}>打开任务</Link></td>
                </tr>
              ))}
              {!loading && tasks.filter(task => task.state !== 'DRAFT').length === 0 && (
                <tr><td colSpan={3} className="empty-cell">暂无已生成计划的任务。</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="card" aria-labelledby="lookup-title">
        <h2 id="lookup-title">按 ID 打开制品</h2>
        <form onSubmit={open} className="form-grid">
          <label>任务 ID<input value={taskId} onChange={event => setTaskId(event.target.value)} required aria-label="任务 ID" /></label>
          <label>制品 ID<input value={artifactId} onChange={event => setArtifactId(event.target.value)} required aria-label="制品 ID" /></label>
          <div className="form-actions"><button type="submit">打开制品</button></div>
        </form>
      </section>
    </div>
  )
}
