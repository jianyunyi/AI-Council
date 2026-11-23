'use client'

import Link from 'next/link'
import {useEffect, useState} from 'react'
import {getArtifact} from '@/lib/api'
import type {ArtifactEnvelope, VerificationResult} from '@/lib/types'

function VerificationView({content}: {content: VerificationResult}) {
  const steps = content.steps ?? []
  return (
    <div className="tw">
      <table className="tbl">
        <thead><tr><th>步骤</th><th>类型</th><th>退出码</th><th>状态</th><th>耗时</th><th>输出</th></tr></thead>
        <tbody>
          {steps.map((step, index) => (
            <tr key={index}>
              <td>{step.name}</td>
              <td>{step.kind}</td>
              <td>{step.exit_code}</td>
              <td>{step.status ?? (step.exit_code === 0 ? '通过' : '失败')}</td>
              <td>{step.duration_ms != null ? `${step.duration_ms} ms` : '—'}</td>
              <td>{[step.stdout, step.stderr].filter(Boolean).join('\n') ? <pre className="artifact-json" style={{maxHeight: 160}}>{[step.stdout, step.stderr].filter(Boolean).join('\n')}</pre> : '—'}</td>
            </tr>
          ))}
          {steps.length === 0 && <tr><td colSpan={6} className="empty-cell">该报告没有步骤明细。</td></tr>}
        </tbody>
      </table>
    </div>
  )
}

export function ArtifactReview({params}: {params: {id: string; artifactId: string}}) {
  const taskId = decodeURIComponent(params.id)
  const artifactId = decodeURIComponent(params.artifactId)
  const [artifact, setArtifact] = useState<ArtifactEnvelope>()
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    getArtifact(taskId, artifactId)
      .then(result => { if (active) setArtifact(result) })
      .catch(reason => { if (active) setError(`制品读取失败（不存在，或内容哈希校验未通过）：${reason instanceof Error ? reason.message : JSON.stringify(reason)}`) })
    return () => { active = false }
  }, [taskId, artifactId])

  return (
    <div className="page-stack">
      <div className="page-head">
        <div>
          <h1>制品审阅</h1>
          <p className="page-sub">任务 <Link href={`/tasks/${encodeURIComponent(taskId)}`}>{taskId}</Link> · 制品 {artifactId}</p>
        </div>
        <div className="page-actions">
          <Link className="btn secondary" href="/artifacts">返回制品</Link>
        </div>
      </div>

      {error && <p role="alert">{error}</p>}
      {!artifact && !error && <p role="status">正在读取制品…</p>}
      {artifact && (
        <section className="card">
          <dl className="artifact-meta">
            <div><dt>类型</dt><dd>{artifact.type}</dd></div>
            <div><dt>结构版本</dt><dd>{artifact.schema_version}</dd></div>
            <div><dt>内容哈希</dt><dd>{artifact.content_hash}</dd></div>
            {artifact.parent_id && <div><dt>父制品</dt><dd>{artifact.parent_id}</dd></div>}
          </dl>
          <p className="small muted">服务端在读取时已按内容哈希校验：哈希不一致的制品会被拒绝（404）。</p>
          {artifact.type === 'verification_report'
            ? <VerificationView content={artifact.content as VerificationResult} />
            : <pre className="artifact-json">{JSON.stringify(artifact.content, null, 2)}</pre>}
        </section>
      )}
    </div>
  )
}
