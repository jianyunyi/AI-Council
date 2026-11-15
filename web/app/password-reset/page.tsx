'use client'

import Link from 'next/link'
import {FormEvent,useState} from 'react'
import {requestPasswordReset} from '@/lib/api'

export default function PasswordResetPage(){
 const [email,setEmail]=useState('');const [busy,setBusy]=useState(false);const [sent,setSent]=useState(false);const [error,setError]=useState('')
 async function submit(event:FormEvent){event.preventDefault();setBusy(true);setError('');try{await requestPasswordReset(email.trim());setSent(true)}catch(reason){setError(reason instanceof Error?reason.message:'请求暂不可用，请稍后重试。')}finally{setBusy(false)}}
 return <section className="auth-layout" aria-labelledby="reset-title"><div className="auth-intro"><h1 id="reset-title">恢复账户<br/>访问</h1><p>如果邮箱关联账户，系统会发送一次性密码重置 token。</p></div><form className="auth-card" onSubmit={submit}><div><h2>重置密码</h2><p>输入注册邮箱以请求重置。</p></div>{sent?<><p role="status">如果该邮箱关联账户且邮件服务正常，重置 token 已发送。此提示不会透露账户是否存在。</p><Link className="text-link" href="/password-reset/confirm">输入重置 token</Link></>:<><label>邮箱<input type="email" value={email} onChange={e=>setEmail(e.target.value)} autoComplete="email" required autoFocus/></label>{error&&<p role="alert">{error}</p>}<button disabled={busy||!email.trim()}>{busy?'正在提交…':'发送重置邮件'}</button></>}<div className="auth-links"><Link href="/login">返回登录</Link><Link href="/password-reset/confirm">已有重置 token</Link></div></form></section>
}
