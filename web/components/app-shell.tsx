'use client'

import Link from 'next/link'
import {AuthProvider,useAuth} from './auth-session'

function Header(){
 const auth=useAuth()
 const canManage=auth.can('admin:users')||auth.can('admin:roles')
 return <header className="app-shell__header">
  <Link className="app-shell__brand" href="/" aria-label="AI Council 首页"><strong>AI Council</strong><span>多模型协商与受控执行</span></Link>
  <nav aria-label="主导航">
   <Link href="/tasks/new">任务</Link>
   <Link href="/providers">模型</Link>
   <Link href="/workspaces">工作区</Link>
   {canManage&&<Link href="/admin/users">用户管理</Link>}
   {auth.status==='authenticated'?<Link className="nav-account" href="/account"><span className="presence-dot" aria-hidden="true"/>{auth.identity?.subject}</Link>:auth.isDesktop?<span className="nav-runtime">本地会话</span>:<Link href="/login">登录</Link>}
  </nav>
 </header>
}

export function AppShell({children}:{children:React.ReactNode}){
 return <AuthProvider><Header/><main className="app-shell__main">{children}</main></AuthProvider>
}
