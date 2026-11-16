'use client'

import Link from 'next/link'
import {FormEvent,useState} from 'react'
import {register} from '@/lib/api'

export default function RegisterPage(){
 const [email,setEmail]=useState('');const [password,setPassword]=useState('');const [busy,setBusy]=useState(false);const [sent,setSent]=useState(false);const [error,setError]=useState('')
 async function submit(event:FormEvent){event.preventDefault();setBusy(true);setError('');try{await register({email:email.trim(),password});setPassword('');setSent(true)}catch(reason){setError(reason instanceof Error?reason.message:'注册暂不可用，请稍后重试。')}finally{setBusy(false)}}
 return <section className="auth-layout" aria-labelledby="register-title"><div className="auth-intro"><h1 id="register-title">建立你的<br/>协作空间</h1><p>验证邮箱后即可使用个人账户。工作区和模型凭据按账户隔离。</p><div className="trust-note"><span aria-hidden="true"/><p><strong>邮箱验证</strong><br/>验证完成前账户不能登录。</p></div></div><form className="auth-card" onSubmit={submit}><div><h2>创建账户</h2><p>使用可接收邮件的地址完成验证。</p></div>{sent?<><p role="status">如果邮件服务已配置，验证 token 已发送到你的邮箱。请在登录前完成验证。</p><Link className="text-link" href="/verify-email">输入验证 token</Link></>:<><label>邮箱<input type="email" value={email} onChange={e=>setEmail(e.target.value)} autoComplete="email" required autoFocus/></label><label>密码<input type="password" value={password} onChange={e=>setPassword(e.target.value)} autoComplete="new-password" minLength={12} maxLength={1024} required/><span className="form-hint">至少 12 个字符。</span></label>{error&&<p role="alert">{error}</p>}<button disabled={busy||!email.trim()||password.length<12}>{busy?'正在创建…':'创建账户'}</button></>}<div className="auth-links"><Link href="/login">返回登录</Link><Link href="/verify-email">已有验证 token</Link></div></form></section>
}
