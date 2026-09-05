'use client'

import Link from 'next/link'
import {useState} from 'react'
import {useAuth} from '@/components/auth-session'

export default function AccountPage(){
 const auth=useAuth()
 const [error,setError]=useState('')
 const [busy,setBusy]=useState(false)
 async function exit(){setBusy(true);setError('');try{await auth.signOut()}catch(reason){setError(reason instanceof Error?reason.message:'退出失败')}finally{setBusy(false)}}
 if(auth.status==='loading')return <p role="status" className="state-panel">正在读取账户信息…</p>
 if(auth.status==='error')return <section className="state-panel" role="alert"><h1>暂时无法读取账户</h1><p>{auth.error?.message??'会话服务不可用'}。请检查网络后重试。</p><button className="secondary" onClick={()=>void auth.refresh()}>重试</button></section>
 if(auth.status!=='authenticated'||!auth.identity)return <section className="state-panel" role="alert"><h1>尚未登录</h1><p>登录后可以查看当前角色、权限和会话有效期。</p><Link className="text-link" href="/login">前往登录</Link></section>
 const {identity}=auth
 return <section className="account-page" aria-labelledby="account-title">
  <header className="page-heading"><div><h1 id="account-title">账户与权限</h1><p>这里显示服务端认可的当前身份，不包含任何密码或访问令牌。</p></div><button className="secondary" onClick={exit} disabled={busy}>{busy?'正在退出…':'退出登录'}</button></header>
  {error&&<p role="alert">{error} 会话已在本机清除，你可以重新登录。</p>}
  <div className="identity-band"><div><span>当前账户</span><strong>{identity.subject}</strong></div><div><span>角色数量</span><strong>{identity.roles.length}</strong></div><div><span>权限数量</span><strong>{identity.permissions.length}</strong></div></div>
  <div className="account-grid">
   <section className="detail-section"><h2>已分配角色</h2><p>角色聚合一组可审计的操作权限。</p>{identity.roles.length?<ul className="tag-list">{identity.roles.map(role=><li key={role}>{role}</li>)}</ul>:<p className="empty-copy">当前没有分配角色。</p>}</section>
   <section className="detail-section"><h2>有效权限</h2><p>每次 API 调用都会以这些权限为依据重新校验。</p>{identity.permissions.length?<ul className="permission-list">{identity.permissions.map(permission=><li key={permission}><code>{permission}</code></li>)}</ul>:<p className="empty-copy">当前没有可用权限。</p>}</section>
  </div>
  <p className="session-expiry">会话到期：{identity.expires_at?new Intl.DateTimeFormat('zh-CN',{dateStyle:'medium',timeStyle:'short'}).format(new Date(identity.expires_at)):'由本地运行时管理'}</p>
 </section>
}
