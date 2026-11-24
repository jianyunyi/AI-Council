'use client'

import Link from 'next/link'
import {usePathname, useRouter} from 'next/navigation'
import {createContext, FormEvent, useCallback, useContext, useEffect, useRef, useState} from 'react'
import {AuthProvider, useAuth} from './auth-session'
import {PrefsProvider, usePrefs} from './theme'
import {ToastHost} from './ui'
import {listTasks, listWorkspaces, onUnauthorized} from '@/lib/api'
import {apiBase} from '@/lib/desktop'
import {desktopBridge} from '@/lib/desktop-bridge'
import type {Workspace} from '@/lib/types'

/* ---------- Current-workspace context (top-bar switcher, §1.2) ---------- */
type WorkspaceCtxValue = {
  workspaces: Workspace[]
  currentId: string
  setCurrentId: (id: string) => void
  reload: () => void
}
const WorkspaceContext = createContext<WorkspaceCtxValue>({workspaces: [], currentId: '', setCurrentId: () => {}, reload: () => {}})
export function useWorkspace() { return useContext(WorkspaceContext) }

const AUTH_ROUTES = ['/login', '/register', '/password-reset', '/verify-email']

type RunnerState = {tone: 'ok' | 'down' | 'unknown'; label: string}

function useRunnerState(isDesktop: boolean) {
  const [runner, setRunner] = useState<RunnerState>({tone: 'unknown', label: 'Runner 状态未知'})
  useEffect(() => {
    let active = true
    async function check() {
      const bridge = desktopBridge()
      if (bridge) {
        try {
          const status = await bridge.status()
          if (!active) return
          if (status.state === 'ready') setRunner({tone: 'ok', label: `Runner 已连接${status.workspace ? ` · ${status.workspace}` : ''}`})
          else if (status.state === 'stopped') setRunner({tone: 'down', label: 'Runner 未启动（本地服务已停止）'})
          else setRunner({tone: 'unknown', label: `Runner ${status.state}`})
        } catch { if (active) setRunner({tone: 'down', label: 'Runner 未连接'}) }
        return
      }
      if (isDesktop) return
      try {
        const root = apiBase().replace(/\/api\/v1\/?$/, '')
        const res = await fetch(`${root}/readyz`)
        if (!active) return
        setRunner(res.ok ? {tone: 'ok', label: 'Runner 已连接（服务就绪）'} : {tone: 'down', label: 'Runner 未连接（服务未就绪）'})
      } catch { if (active) setRunner({tone: 'down', label: 'Runner 未连接'}) }
    }
    void check()
    const timer = window.setInterval(check, 30000)
    return () => { active = false; window.clearInterval(timer) }
  }, [isDesktop])
  return runner
}

function ThemeSeg() {
  const {theme, setTheme} = usePrefs()
  return (
    <div className="theme-seg" role="group" aria-label="主题">
      {([['dark', '深色'], ['light', '浅色'], ['system', '跟随系统']] as const).map(([value, label]) => (
        <button key={value} aria-pressed={theme === value} onClick={() => setTheme(value)}>{label}</button>
      ))}
    </div>
  )
}

function WorkspaceSwitcher() {
  const {workspaces, currentId, setCurrentId} = useWorkspace()
  const current = workspaces.find(workspace => workspace.id === currentId) ?? workspaces[0]
  function toggle() {
    const menu = document.getElementById('wsMenu') as HTMLElement & {showPopover?: () => void; hidePopover?: () => void} | null
    if (!menu) return
    let isOpen = false
    try { isOpen = Boolean(document.querySelector('#wsMenu:popover-open')) } catch { isOpen = menu.hasAttribute('open') }
    if (isOpen) menu.hidePopover?.()
    else menu.showPopover?.()
  }
  return (
    <>
      <button className="secondary ws-button" aria-haspopup="menu" onClick={toggle}>
        工作区 <strong>{current ? current.root : '未登记'}</strong> <span aria-hidden="true">▾</span>
      </button>
      <div id="wsMenu" className="ws-popover" role="menu" ref={el => { if (el && !el.hasAttribute('popover')) el.setAttribute('popover', 'auto') }}>
        <h3>切换工作区</h3>
        {workspaces.length === 0 && <p className="small muted" style={{padding: '4px 8px'}}>还没有可访问的工作区。</p>}
        {workspaces.map(workspace => (
          <button key={workspace.id} className="ws-popover__item" role="menuitemradio" aria-checked={current?.id === workspace.id}
            onClick={() => { setCurrentId(workspace.id); (document.getElementById('wsMenu') as HTMLElement & {hidePopover?: () => void})?.hidePopover?.() }}>
            <span>{workspace.root}</span>
            {workspace.is_git && workspace.dirty && <span className="dirty">Git dirty</span>}
          </button>
        ))}
        <h3>管理</h3>
        <Link className="ws-popover__item" href="/workspaces" role="menuitem">登记与共享工作区</Link>
      </div>
    </>
  )
}

function CommandPalette() {
  const router = useRouter()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [taskId, setTaskId] = useState('')
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        const dialog = dialogRef.current
        if (dialog && !dialog.open) { try { dialog.showModal() } catch { dialog.setAttribute('open', '') } }
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'n') {
        event.preventDefault()
        router.push('/tasks/new')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [router])
  function go(event: FormEvent) {
    event.preventDefault()
    const id = taskId.trim()
    dialogRef.current?.close()
    if (id) router.push(`/tasks/${encodeURIComponent(id)}`)
  }
  return (
    <dialog ref={dialogRef} aria-labelledby="palette-title">
      <form onSubmit={go} style={{display: 'grid', gap: 12}}>
        <h2 id="palette-title">命令面板</h2>
        <label>按任务 ID 跳转<input value={taskId} onChange={event => setTaskId(event.target.value)} placeholder="例如 task-41" aria-label="任务 ID" /></label>
        <div className="form-actions">
          <button type="submit" disabled={!taskId.trim()}>打开任务</button>
          <button type="button" className="secondary" onClick={() => { dialogRef.current?.close(); router.push('/tasks/new') }}>新建任务</button>
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>关闭</button>
        </div>
      </form>
    </dialog>
  )
}

function SessionExpiredDialog() {
  const auth = useAuth()
  const router = useRouter()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const wasAuthenticated = useRef(false)
  const [expired, setExpired] = useState(false)
  useEffect(() => { if (auth.status === 'authenticated') wasAuthenticated.current = true }, [auth.status])
  useEffect(() => onUnauthorized(() => { if (wasAuthenticated.current && !auth.isDesktop) setExpired(true) }), [auth.isDesktop])
  useEffect(() => {
    const dialog = dialogRef.current
    if (dialog && expired && !dialog.open) { try { dialog.showModal() } catch { dialog.setAttribute('open', '') } }
  }, [expired])
  if (!expired) return null
  return (
    <dialog ref={dialogRef} aria-labelledby="expired-title">
      <form method="dialog" style={{display: 'grid', gap: 12}}>
        <h2 id="expired-title">会话已过期</h2>
        <p className="muted">会话已过期，请重新登录。当前页面会保留，登录后可返回继续。</p>
        <div className="dlg-actions">
          <button type="button" onClick={() => { setExpired(false); dialogRef.current?.close(); router.push('/login') }}>重新登录</button>
        </div>
      </form>
    </dialog>
  )
}

function ShellFrame({children}: {children: React.ReactNode}) {
  const auth = useAuth()
  const pathname = usePathname()
  const runner = useRunnerState(auth.isDesktop)
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [currentId, setCurrentIdState] = useState('')
  const [pendingCount, setPendingCount] = useState(0)

  const reloadWorkspaces = useCallback(() => {
    listWorkspaces().then(result => {
      setWorkspaces(result)
      setCurrentIdState(current => {
        const stored = window.localStorage.getItem('aicouncil.currentWorkspace') ?? ''
        const next = result.some(item => item.id === current) ? current : result.some(item => item.id === stored) ? stored : result[0]?.id ?? ''
        return next
      })
    }).catch(() => {})
  }, [])
  useEffect(() => { reloadWorkspaces() }, [reloadWorkspaces, pathname])

  const setCurrentId = useCallback((id: string) => {
    setCurrentIdState(id)
    window.localStorage.setItem('aicouncil.currentWorkspace', id)
  }, [])

  useEffect(() => {
    let active = true
    listTasks({state: 'AWAITING_APPROVAL', limit: 200}).then(page => {
      if (active) setPendingCount(page.items.length)
    }).catch(() => {})
    return () => { active = false }
  }, [pathname])

  const canManage = auth.can('admin:users') || auth.can('admin:roles')
  const item = (href: string, label: string, extra?: React.ReactNode) => (
    <Link className="nav-item" href={href} aria-current={pathname === href ? 'page' : undefined}>
      <span>{label}</span>{extra}
    </Link>
  )

  return (
    <WorkspaceContext.Provider value={{workspaces, currentId, setCurrentId, reload: reloadWorkspaces}}>
      <div className="app-shell">
        <header className="topbar">
          <Link className="topbar__brand" href="/" aria-label="AI Council 首页"><strong>AI Council</strong><span>多模型协商与受控执行</span></Link>
          <WorkspaceSwitcher />
          <div className="topbar__spacer" />
          <span className="runner-state" data-state={runner.tone === 'ok' ? 'ok' : runner.tone} role="status">
            <span className="dot" aria-hidden="true" />{runner.label}
          </span>
          <ThemeSeg />
          {auth.status === 'authenticated'
            ? <Link className="btn secondary" href="/account">{auth.identity?.subject}</Link>
            : auth.isDesktop ? <span className="small muted">本地会话</span> : <Link className="btn secondary" href="/login">登录</Link>}
        </header>
        {runner.tone === 'down' && (
          <div className="runner-alert" role="alert">Runner 未连接：执行与工作区操作暂不可用。</div>
        )}
        <div className="shell-body">
          <nav className="sidenav" aria-label="主导航">
            <h3>工作</h3>
            {item('/', '指挥中心')}
            {item('/tasks', '任务', pendingCount > 0 ? <span className="nav-badge" aria-label={`${pendingCount} 个待批准`}>{pendingCount}</span> : undefined)}
            {item('/tasks/new', '新建任务', <span className="nav-key" aria-hidden="true">Ctrl N</span>)}
            {item('/artifacts', '制品')}
            <h3>配置</h3>
            {item('/workspaces', '工作区')}
            {item('/providers', 'Provider')}
            {canManage && <><h3>管理</h3>{item('/admin/users', '用户与权限')}</>}
            <h3>系统</h3>
            {item('/settings', '设置')}
            {item('/account', '账户')}
          </nav>
          <main className="app-main">{children}</main>
        </div>
        <CommandPalette />
        <SessionExpiredDialog />
        <ToastHost />
      </div>
    </WorkspaceContext.Provider>
  )
}

export function AppShell({children}: {children: React.ReactNode}) {
  const pathname = usePathname()
  const bare = AUTH_ROUTES.some(route => pathname === route || pathname.startsWith(`${route}/`))
  return (
    <PrefsProvider>
      <AuthProvider>
        {bare ? <>{children}<ToastHost /></> : <ShellFrame>{children}</ShellFrame>}
      </AuthProvider>
    </PrefsProvider>
  )
}
