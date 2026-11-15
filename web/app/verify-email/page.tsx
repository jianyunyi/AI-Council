'use client'

import Link from 'next/link'
import {FormEvent,useState} from 'react'
import {verifyEmail} from '@/lib/api'

export default function VerifyEmailPage(){
 const [token,setToken]=useState('');const [busy,setBusy]=useState(false);const [done,setDone]=useState(false);const [error,setError]=useState('')
 async function submit(event:FormEvent){event.preventDefault();setBusy(true);setError('');try{await verifyEmail(token.trim());setDone(true);setToken('')}catch(reason){setError(reason instanceof Error?reason.message:'验证 token 无效或已过期。')}finally{setBusy(false)}}
 return <section className="auth-layout" aria-labelledby="verify-title"><div className="auth-intro"><h1 id="verify-title">完成邮箱<br/>验证</h1><p>邮件中的一次性 token 只可使用一次，并会在 24 小时后过期。</p></div><form className="auth-card" onSubmit={submit}><div><h2>验证账户</h2><p>将邮件中的 token 粘贴到下方。</p></div>{done?<p role="status">邮箱已验证。现在可以登录。</p>:<><label>验证 token<input value={token} onChange={e=>setToken(e.target.value)} autoComplete="one-time-code" required autoFocus/></label>{error&&<p role="alert">{error}</p>}<button disabled={busy||!token.trim()}>{busy?'正在验证…':'验证邮箱'}</button></>}<Link className="text-link" href="/login">前往登录</Link></form></section>
}
