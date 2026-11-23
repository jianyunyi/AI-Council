'use client'

import {FormEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState} from 'react'
import {useAuth} from '@/components/auth-session'
import {closeDialog, ConfirmDialog, ConfirmState, openDialog} from '@/components/ui'
import {ApiError, createRole, createUser, listPermissions, listRoles, listUsers, updateRole, updateUser} from '@/lib/api'
import {ManagedRole, ManagedUser, Permission} from '@/lib/types'

function message(reason: unknown, fallback: string) { return reason instanceof Error ? reason.message : fallback }
function toggle(items: string[], item: string) { return items.includes(item) ? items.filter(value => value !== item) : [...items, item] }

/* Native dialog wrapper (§3.4): showModal gives focus containment, inert
   background and focus return for free. */
function AdminDialog({open, title, onClose, children}: {open: boolean; title: string; onClose: () => void; children: ReactNode}) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) openDialog(dialog)
    if (!open && dialog.open) closeDialog(dialog, 'cancel')
  }, [open ])
  if (!open) return null
  return (
    <dialog ref={ref} aria-label={title} onClose={onClose}>
      <div style={{display: 'grid', gap: 12}}>
        <h2>{title}</h2>
        {children}
      </div>
    </dialog>
  )
}

export default function UserManagementPage() {
  const auth = useAuth()
  const canUsers = auth.can('admin:users')
  const canRoles = auth.can('admin:roles')
  const canPermissions = auth.can('admin:permissions')
  const allowed = canUsers || canRoles
  const [users, setUsers] = useState<ManagedUser[]>([])
  const [roles, setRoles] = useState<ManagedRole[]>([])
  const [permissions, setPermissions] = useState<Permission[]>([])
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [revoked, setRevoked] = useState(false)
  const [subject, setSubject] = useState('')
  const [password, setPassword] = useState('')
  const [initialRoles, setInitialRoles] = useState<string[]>([])
  const [roleName, setRoleName] = useState('')
  const [rolePermissions, setRolePermissions] = useState<string[]>([])
  const [editingUser, setEditingUser] = useState<ManagedUser | null>(null)
  const [editRoles, setEditRoles] = useState<string[]>([])
  const [editPassword, setEditPassword] = useState('')
  const [editingRole, setEditingRole] = useState<ManagedRole | null>(null)
  const [editPermissions, setEditPermissions] = useState<string[]>([])
  const [roleConfirmName, setRoleConfirmName] = useState('')
  const [createUserOpen, setCreateUserOpen] = useState(false)
  const [createRoleOpen, setCreateRoleOpen] = useState(false)
  const [confirm, setConfirm] = useState<ConfirmState | null>(null)
  const [pendingDisable, setPendingDisable] = useState<ManagedUser | null>(null)

  const clearSensitiveEditors = useCallback(() => {
    setPassword(''); setEditPassword(''); setEditingUser(null); setEditingRole(null)
  }, [])
  const load = useCallback(async () => {
    if (!allowed) return
    setLoading(true); setError(''); setNotice(''); setUsers([]); setRoles([]); setPermissions([])
    try {
      const [nextUsers, nextRoles, nextPermissions] = await Promise.all([
        canUsers ? listUsers() : Promise.resolve([]), canRoles ? listRoles() : Promise.resolve([]), canPermissions ? listPermissions() : Promise.resolve([]),
      ])
      setUsers(nextUsers); setRoles(nextRoles); setPermissions(nextPermissions); setRevoked(false)
    } catch (reason) {
      setError(message(reason, '管理数据加载失败'))
      if (reason instanceof ApiError && reason.status === 403) { setRevoked(true); clearSensitiveEditors(); await auth.refresh() }
    } finally { setLoading(false) }
  }, [allowed, auth.refresh, canPermissions, canRoles, canUsers, clearSensitiveEditors])

  useEffect(() => { if (auth.status === 'authenticated' && allowed && !revoked) void load() }, [allowed, auth.status, load, revoked])
  useEffect(() => { if (auth.status !== 'authenticated' || !allowed) { setUsers([]); setRoles([]); setPermissions([]); clearSensitiveEditors() } }, [allowed, auth.status, clearSensitiveEditors])

  const permissionNames = useMemo(() => permissions.map(item => item.name), [permissions])
  async function addUser(event: FormEvent) {
    event.preventDefault(); setBusy('create-user'); setError(''); setNotice('')
    try { const user = await createUser({subject: subject.trim(), password, roles: initialRoles}); setUsers(current => [...current, user].sort((a, b) => a.subject.localeCompare(b.subject))); setSubject(''); setPassword(''); setInitialRoles([]); setCreateUserOpen(false); setNotice(`用户 ${user.subject} 已创建。`) }
    catch (reason) { setError(message(reason, '用户创建失败')) } finally { setBusy('') }
  }
  async function addRole(event: FormEvent) {
    event.preventDefault(); setBusy('create-role'); setError(''); setNotice('')
    try { const role = await createRole({name: roleName.trim(), permissions: rolePermissions}); setRoles(current => [...current, role].sort((a, b) => a.name.localeCompare(b.name))); setRoleName(''); setRolePermissions([]); setCreateRoleOpen(false); setNotice(`角色 ${role.name} 已创建。`) }
    catch (reason) { setError(message(reason, '角色创建失败')) } finally { setBusy('') }
  }
  function openUser(user: ManagedUser) { setEditingUser(user); setEditRoles(user.roles); setEditPassword('') }
  function closeUser() { setEditingUser(null); setEditRoles([]); setEditPassword('') }
  async function saveUser(event: FormEvent) {
    event.preventDefault(); if (!editingUser) return; setBusy('edit-user'); setError(''); setNotice('')
    try { const input: {disabled: boolean; roles: string[]; password?: string} = {disabled: editingUser.disabled, roles: editRoles}; if (editPassword) input.password = editPassword; const user = await updateUser(editingUser.subject, input); setUsers(current => current.map(item => item.subject === user.subject ? user : item)); closeUser(); setNotice(`用户 ${user.subject} 已更新。`) }
    catch (reason) { setError(message(reason, '用户更新失败')) } finally { setBusy('') }
  }
  function openRole(role: ManagedRole) { setEditingRole(role); setEditPermissions(role.permissions); setRoleConfirmName('') }
  async function saveRole(event: FormEvent) {
    event.preventDefault(); if (!editingRole) return; setBusy('edit-role'); setError(''); setNotice('')
    try { const role = await updateRole(editingRole.name, {permissions: editPermissions}); setRoles(current => current.map(item => item.name === role.name ? role : item)); setEditingRole(null); setNotice(`角色 ${role.name} 已更新。`) }
    catch (reason) { setError(message(reason, '角色更新失败')) } finally { setBusy('') }
  }
  function askDisable(user: ManagedUser) {
    setPendingDisable(user)
    setConfirm({title: `停用用户 ${user.subject}？`, description: <p>停用后该用户立即无法登录，正在进行的会话会被服务端拒绝。可以随时重新启用。</p>, confirmLabel: '停用用户'})
  }
  async function resolveDisable(confirmed: boolean) {
    setConfirm(null)
    const user = pendingDisable
    setPendingDisable(null)
    if (!confirmed || !user) return
    setBusy('disable-user'); setError('')
    try { const updated = await updateUser(user.subject, {disabled: true, roles: user.roles}); setUsers(current => current.map(item => item.subject === updated.subject ? updated : item)); setNotice(`用户 ${user.subject} 已停用。`) }
    catch (reason) { setError(message(reason, '停用失败')) } finally { setBusy('') }
  }
  async function enableUser(user: ManagedUser) {
    setBusy('enable-user'); setError('')
    try { const updated = await updateUser(user.subject, {disabled: false, roles: user.roles}); setUsers(current => current.map(item => item.subject === updated.subject ? updated : item)); setNotice(`用户 ${user.subject} 已启用。`) }
    catch (reason) { setError(message(reason, '启用失败')) } finally { setBusy('') }
  }

  if (auth.status === 'loading') return <p role="status" className="state-panel">正在验证管理权限…</p>
  if (auth.status === 'error') return <section className="state-panel" role="alert"><h1>暂时无法验证权限</h1><p>{auth.error?.message ?? '会话服务不可用'}。请检查网络后重试。</p><button className="secondary" onClick={() => void auth.refresh()}>重试</button></section>
  if (auth.status === 'anonymous') return <section className="state-panel" role="alert"><h1>需要登录</h1><p>登录管理员账户后才能访问用户与角色管理。</p></section>
  if (!allowed) return <section className="state-panel" role="alert"><h1>无法打开管理台</h1><p>当前账户没有管理用户或角色的权限。请联系管理员分配对应权限。</p></section>
  return <section className="management-page" aria-labelledby="management-title">
    <header className="page-heading">
      <div><h1 id="management-title">用户与权限</h1><p>分配最小必要权限。用户状态与角色变更会由服务端立即生效。</p></div>
      <div className="form-actions">
        {canUsers && <button onClick={() => setCreateUserOpen(true)}>＋ 新建用户</button>}
        {canRoles && canPermissions && <button className="secondary" onClick={() => setCreateRoleOpen(true)}>＋ 新建角色</button>}
        <button className="secondary" onClick={() => { setRevoked(false); void load() }} disabled={loading || Boolean(busy)}>刷新数据</button>
      </div>
    </header>
    {loading && <p role="status" className="state-panel">正在加载用户、角色与权限目录…</p>}
    {error && <p role="alert">{error} 请检查权限或网络连接后重试。</p>}
    {notice && <p role="status">{notice}</p>}
    {!loading && !revoked && <div className="management-columns">
      {canUsers && <section className="management-section" aria-labelledby="users-title">
        <div className="section-heading"><div><h2 id="users-title">用户</h2><p>{users.length} 个受管账户</p></div></div>
        <div className="table-wrap"><table className="management-table"><thead><tr><th>账号</th><th>状态</th><th>角色</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{users.length ? users.map(user => <tr key={user.subject}><th scope="row">{user.subject}</th><td><span className={`status-chip ${user.disabled ? 'status-chip--disabled' : ''}`}>{user.disabled ? '已禁用' : '有效'}</span></td><td>{user.roles.length ? <span className="table-tags">{user.roles.join(' · ')}</span> : <span className="muted">无角色</span>}</td><td><span className="row-actions"><button className="table-action" aria-label={`编辑用户 ${user.subject}`} onClick={() => openUser(user)}>编辑</button>{user.disabled
          ? <button className="table-action" aria-label={`启用用户 ${user.subject}`} disabled={Boolean(busy)} onClick={() => void enableUser(user)}>启用</button>
          : <button className="table-action" aria-label={`停用用户 ${user.subject}`} disabled={Boolean(busy)} onClick={() => askDisable(user)}>停用</button>}</span></td></tr>) : <tr><td colSpan={4} className="empty-cell">暂无用户</td></tr>}</tbody></table></div>
      </section>}
      {canRoles && <section className="management-section" aria-labelledby="roles-title">
        <div className="section-heading"><div><h2 id="roles-title">角色</h2><p>{roles.length} 个权限集合</p></div></div>
        <ul className="role-list">{roles.length ? roles.map(role => <li key={role.name}><div><strong>{role.name}</strong><span>{role.permissions.length ? role.permissions.join(' · ') : '暂无权限'}</span></div>{canPermissions && <button className="table-action" aria-label={`编辑角色 ${role.name}`} onClick={() => openRole(role)}>编辑</button>}</li>) : <li className="empty-cell">暂无角色</li>}</ul>
        {!canPermissions && <p className="empty-copy">当前账户可以查看角色，但需要权限目录权限才能创建或修改角色。</p>}
        {canPermissions && permissionNames.length === 0 && <p className="empty-copy">暂无可分配权限</p>}
      </section>}
    </div>}

    <AdminDialog open={createUserOpen} title="新建用户" onClose={() => setCreateUserOpen(false)}>
      <form onSubmit={addUser} style={{display: 'grid', gap: 12}}>
        <p className="muted">密码只用于本次提交，成功后立即清空。</p>
        <label>账号<input value={subject} onChange={event => setSubject(event.target.value)} required autoComplete="off" /></label>
        <label>初始密码<input value={password} onChange={event => setPassword(event.target.value)} type="password" required autoComplete="new-password" /></label>
        <fieldset className="permission-fieldset"><legend>初始角色</legend>{roles.length ? <div className="permission-grid">{roles.map(role => <label key={role.name}><input aria-label={`分配初始角色 ${role.name}`} type="checkbox" checked={initialRoles.includes(role.name)} onChange={() => setInitialRoles(toggle(initialRoles, role.name))} /><span>{role.name}</span></label>)}</div> : <p className="empty-copy">当前没有可分配角色</p>}</fieldset>
        <div className="dlg-actions">
          <button type="button" className="secondary" onClick={() => setCreateUserOpen(false)}>取消</button>
          <button disabled={busy === 'create-user' || !subject.trim() || !password}>{busy === 'create-user' ? '正在创建…' : '创建用户'}</button>
        </div>
      </form>
    </AdminDialog>

    <AdminDialog open={createRoleOpen} title="新建角色" onClose={() => setCreateRoleOpen(false)}>
      <form onSubmit={addRole} style={{display: 'grid', gap: 12}}>
        <p className="muted">从服务端权限目录组合新的职责边界。</p>
        <label>角色名称<input value={roleName} onChange={event => setRoleName(event.target.value)} required /></label>
        <fieldset className="permission-fieldset"><legend>分配权限</legend>{permissionNames.length ? <div className="permission-grid">{permissionNames.map(permission => <label key={permission}><input type="checkbox" checked={rolePermissions.includes(permission)} onChange={() => setRolePermissions(toggle(rolePermissions, permission))} /><code>{permission}</code></label>)}</div> : <p className="empty-copy">暂无可分配权限</p>}</fieldset>
        <div className="dlg-actions">
          <button type="button" className="secondary" onClick={() => setCreateRoleOpen(false)}>取消</button>
          <button disabled={busy === 'create-role' || !roleName.trim()}>{busy === 'create-role' ? '正在创建…' : '创建角色'}</button>
        </div>
      </form>
    </AdminDialog>

    <AdminDialog open={Boolean(editingUser)} title={editingUser ? `编辑用户 · ${editingUser.subject}` : '编辑用户'} onClose={closeUser}>
      {editingUser && <form onSubmit={saveUser} style={{display: 'grid', gap: 12}}>
        <label className="switch-row"><input type="checkbox" checked={!editingUser.disabled} onChange={event => setEditingUser({...editingUser, disabled: !event.target.checked})} /><span>允许登录</span></label>
        <fieldset className="permission-fieldset"><legend>角色</legend>{roles.length ? <div className="permission-grid">{roles.map(role => <label key={role.name}><input aria-label={`用户角色 ${role.name}`} type="checkbox" checked={editRoles.includes(role.name)} onChange={() => setEditRoles(toggle(editRoles, role.name))} /><span>{role.name}</span></label>)}</div> : <p className="empty-copy">当前没有可分配角色</p>}</fieldset>
        <label>设置新密码<input value={editPassword} onChange={event => setEditPassword(event.target.value)} type="password" autoComplete="new-password" placeholder="留空则保持不变" /></label>
        <div className="dlg-actions">
          <button type="button" className="secondary" onClick={closeUser}>取消</button>
          <button disabled={busy === 'edit-user'}>{busy === 'edit-user' ? '正在保存…' : '保存用户'}</button>
        </div>
      </form>}
    </AdminDialog>

    <AdminDialog open={Boolean(editingRole) && canPermissions} title={editingRole ? `编辑角色 · ${editingRole.name}` : '编辑角色'} onClose={() => setEditingRole(null)}>
      {editingRole && <form onSubmit={saveRole} style={{display: 'grid', gap: 12}}>
        <p className="muted">修改角色权限会影响所有拥有该角色的用户，属于高风险操作。</p>
        <fieldset className="permission-fieldset"><legend>有效权限</legend><div className="permission-grid">{permissionNames.map(permission => <label key={permission}><input type="checkbox" checked={editPermissions.includes(permission)} onChange={() => setEditPermissions(toggle(editPermissions, permission))} /><code>{permission}</code></label>)}</div></fieldset>
        <label>输入角色名「{editingRole.name}」以确认
          <input value={roleConfirmName} onChange={event => setRoleConfirmName(event.target.value)} autoComplete="off" aria-label="确认角色名" />
        </label>
        <div className="dlg-actions">
          <button type="button" className="secondary" onClick={() => setEditingRole(null)}>取消</button>
          <button disabled={busy === 'edit-role' || roleConfirmName !== editingRole.name}>{busy === 'edit-role' ? '正在保存…' : '保存角色'}</button>
        </div>
      </form>}
    </AdminDialog>

    <ConfirmDialog request={confirm} onResolve={resolveDisable} />
  </section>
}
