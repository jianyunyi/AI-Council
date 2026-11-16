'use client'

import Link from 'next/link'
import {FormEvent,useEffect,useState} from 'react'
import {useRouter} from 'next/navigation'
import {useAuth} from '@/components/auth-session'
import {listOAuthProviders,oauthStartURL} from '@/lib/api'

export default function LoginPage(){
 const auth=useAuth()
 const router=useRouter()
 const [subject,setSubject]=useState('')
 const [password,setPassword]=useState('')
 const [busy,setBusy]=useState(false)
 const [error,setError]=useState('')
 const [oauthProviders,setOAuthProviders]=useState<string[]>([])
 useEffect(()=>{let active=true;listOAuthProviders().then(result=>{if(active)setOAuthProviders(result.providers)}).catch(()=>{});return()=>{active=false}},[])
 async function submit(event:FormEvent){
  event.preventDefault();setBusy(true);setError('')
  try{await auth.signIn(subject.trim(),password);setPassword('');router.push('/account')}
  catch(reason){setError(reason instanceof Error?reason.message:'登录失败，请检查账号和密码。')}
  finally{setBusy(false)}
 }
 if(auth.status==='authenticated')return <section className="auth-layout"><div className="auth-card"><h1>你已登录</h1><p>当前账户为 <strong>{auth.identity?.subject}</strong>，可以继续进入控制台。</p><Link className="text-link" href="/account">查看账户与权限</Link></div></section>
 return <section className="auth-layout" aria-labelledby="login-title">
  <div className="auth-intro"><h1 id="login-title">安全进入<br/>协商控制台</h1><p>身份信息仅通过受保护的同域 Cookie 传递。浏览器不会保存访问令牌或密码。</p><div className="trust-note"><span aria-hidden="true"/><p><strong>最小权限访问</strong><br/>所有执行、批准和管理操作均由服务端再次校验。</p></div></div>
  <form className="auth-card" onSubmit={submit}>
   <div><h2>登录 AI Council</h2><p>使用管理员分配的账户继续。</p></div>
   <label>账号<input value={subject} onChange={event=>setSubject(event.target.value)} autoComplete="username" required autoFocus placeholder="例如 admin"/></label>
   <label>密码<input value={password} onChange={event=>setPassword(event.target.value)} type="password" autoComplete="current-password" required/></label>
   {error&&<p role="alert">{error} 请重试，或联系管理员确认账户状态。</p>}
   <button disabled={busy||!subject.trim()||!password}>{busy?'正在验证…':'登录'}</button>
   <p className="form-hint">连续失败可能触发短时登录保护。</p>
   <div className="auth-links"><Link href="/register">创建账户</Link><Link href="/password-reset">忘记密码</Link></div>
   {oauthProviders.length>0&&<div className="oauth-options" aria-label="第三方登录">{oauthProviders.includes('github')&&<a href={oauthStartURL('github')}>使用 GitHub 登录</a>}{oauthProviders.includes('google')&&<a href={oauthStartURL('google')}>使用 Google 登录</a>}</div>}
  </form>
 </section>
}
