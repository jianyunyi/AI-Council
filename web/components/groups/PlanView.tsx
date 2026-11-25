'use client'

import type { ExecutionPlan } from '@/lib/types'

/** Read-only rendering of an execution plan inside a requirement card. */
export default function PlanView({ plan }: { plan: ExecutionPlan }) {
  return (
    <div className="plan-view">
      {plan.patches?.length > 0 && (
        <div>
          <strong>补丁（{plan.patches.length}）</strong>
          <ul>
            {plan.patches.map((p, i) => (
              <li key={i}>
                <code>{p.path}</code>
                <details>
                  <summary>查看 diff</summary>
                  <pre className="plan-diff">{p.unified_diff}</pre>
                </details>
              </li>
            ))}
          </ul>
        </div>
      )}
      {plan.commands?.length > 0 && (
        <div>
          <strong>命令（{plan.commands.length}）</strong>
          <ul>
            {plan.commands.map((c, i) => (
              <li key={i}>
                <code>
                  {c.executable} {(c.args ?? []).join(' ')}
                </code>
                {c.purpose && <span className="muted"> — {c.purpose}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      {(plan.verification_commands?.length ?? 0) > 0 && (
        <div>
          <strong>验证命令（{plan.verification_commands!.length}）</strong>
          <ul>
            {plan.verification_commands!.map((c, i) => (
              <li key={i}>
                <code>
                  {c.executable} {(c.args ?? []).join(' ')}
                </code>
              </li>
            ))}
          </ul>
        </div>
      )}
      {plan.acceptance?.length > 0 && (
        <div>
          <strong>验收标准</strong>
          <ul>
            {plan.acceptance.map((a, i) => (
              <li key={i}>{a}</li>
            ))}
          </ul>
        </div>
      )}
      <p className="muted">该方案尚未执行：需要评审通过并由人工批准后，才会下放到该 AI 的工作区执行。</p>
    </div>
  )
}
