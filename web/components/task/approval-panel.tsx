'use client'
export function ApprovalPanel({hash,version,onApprove}:{hash:string;version:number;onApprove:()=>void}){return <section className="card"><h2>人工批准执行</h2><p>计划版本：{version}</p><code>{hash||'等待计划哈希'}</code><p>批准前请检查所有变更文件、命令参数、工作目录、超时和恢复策略。</p><label><input type="checkbox" id="confirm"/> 我确认这是本次要执行的不可变计划</label><br/><button onClick={onApprove}>批准并执行</button></section>}
