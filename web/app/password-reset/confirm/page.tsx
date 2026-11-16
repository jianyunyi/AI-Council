'use client'

import Link from 'next/link'
import {FormEvent,useState} from 'react'
import {confirmPasswordReset} from '@/lib/api'

export default function ConfirmPasswordResetPage(){
 const [token,setToken]=useState('');const [password,setPassword]=useState('');const [busy,setBusy]=useState(false);const [done,setDone]=useState(false);const [error,setError]=useState('')
 async function submit(event:FormEvent){event.preventDefault();setBusy(true);setError('');try{await confirmPasswordReset({token:token.trim(),password});setDone(true);setToken('');setPassword('')}catch(reason){setError(reason instanceof Error?reason.message:'重置 token 无效或已过期。')}finally{setBusy(false)}}
 return <section className="auth-layout" aria-labelledby="confirm-reset-title"><div className="auth-intro"><h1 id="confirm-reset-title">设置新密码</h1><p>重置成功后，所有已登录会话将立即失效。</p></div><form className="auth-card" onSubmit={submit}><div><h2>确认密码重置</h2><p>粘贴邮件中的 token 并设置新密码。</p></div>{done?<p role="status">密码已更新。请使用新密码重新登录。</p>:<><label>重置 token<input value={token} onChange={e=>setToken(e.target.value)} autoComplete="one-time-code" required autoFocus/></label><label>新密码<input type="password" value={password} onChange={e=>setPassword(e.target.value)} autoComplete="new-password" minLength={12} maxLength={1024} required/><span className="form-hint">至少 12 个字符。</span></label>{error&&<p role="alert">{error}</p>}<button disabled={busy||!token.trim()||password.length<12}>{busy?'正在更新…':'更新密码'}</button></>}<Link className="text-link" href="/login">前往登录</Link></form></section>
}
