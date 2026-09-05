'use client'

import {FormEvent,useCallback,useEffect,useMemo,useState} from 'react'
import {ApiError,createRole,createUser,listPermissions,listRoles,listUsers,updateRole,updateUser} from '@/lib/api'
import {ManagedRole,ManagedUser,Permission} from '@/lib/types'
import {useAuth} from '@/components/auth-session'

function message(reason:unknown,fallback:string){return reason instanceof Error?reason.message:fallback}
function toggle(items:string[],item:string){return items.includes(item)?items.filter(value=>value!==item):[...items,item]}

export default function UserManagementPage(){
 const auth=useAuth()
 const canUsers=auth.can('admin:users')
 const canRoles=auth.can('admin:roles')
 const canPermissions=auth.can('admin:permissions')
 const allowed=canUsers||canRoles
 const [users,setUsers]=useState<ManagedUser[]>([])
 const [roles,setRoles]=useState<ManagedRole[]>([])
 const [permissions,setPermissions]=useState<Permission[]>([])
 const [loading,setLoading]=useState(false)
 const [busy,setBusy]=useState('')
 const [error,setError]=useState('')
 const [notice,setNotice]=useState('')
 const [revoked,setRevoked]=useState(false)
 const [subject,setSubject]=useState('')
 const [password,setPassword]=useState('')
 const [initialRoles,setInitialRoles]=useState<string[]>([])
 const [roleName,setRoleName]=useState('')
 const [rolePermissions,setRolePermissions]=useState<string[]>([])
 const [editingUser,setEditingUser]=useState<ManagedUser|null>(null)
 const [editRoles,setEditRoles]=useState<string[]>([])
 const [editPassword,setEditPassword]=useState('')
 const [editingRole,setEditingRole]=useState<ManagedRole|null>(null)
 const [editPermissions,setEditPermissions]=useState<string[]>([])

 const clearSensitiveEditors=useCallback(()=>{
  setPassword('');setEditPassword('');setEditingUser(null);setEditingRole(null)
 },[])
 const load=useCallback(async()=>{
  if(!allowed)return
  setLoading(true);setError('');setNotice('');setUsers([]);setRoles([]);setPermissions([])
  try{
   const [nextUsers,nextRoles,nextPermissions]=await Promise.all([
    canUsers?listUsers():Promise.resolve([]),canRoles?listRoles():Promise.resolve([]),canPermissions?listPermissions():Promise.resolve([]),
   ])
   setUsers(nextUsers);setRoles(nextRoles);setPermissions(nextPermissions);setRevoked(false)
  }catch(reason){
   setError(message(reason,'管理数据加载失败'))
   if(reason instanceof ApiError&&reason.status===403){setRevoked(true);clearSensitiveEditors();await auth.refresh()}
  }finally{setLoading(false)}
 },[allowed,auth.refresh,canPermissions,canRoles,canUsers,clearSensitiveEditors])

 useEffect(()=>{if(auth.status==='authenticated'&&allowed&&!revoked)void load()},[allowed,auth.status,load,revoked])
 useEffect(()=>{if(auth.status!=='authenticated'||!allowed){setUsers([]);setRoles([]);setPermissions([]);clearSensitiveEditors()}},[allowed,auth.status,clearSensitiveEditors])

 const permissionNames=useMemo(()=>permissions.map(item=>item.name),[permissions])
 async function addUser(event:FormEvent){
  event.preventDefault();setBusy('create-user');setError('');setNotice('')
  try{const user=await createUser({subject:subject.trim(),password,roles:initialRoles});setUsers(current=>[...current,user].sort((a,b)=>a.subject.localeCompare(b.subject)));setSubject('');setPassword('');setInitialRoles([]);setNotice(`用户 ${user.subject} 已创建。`)}
  catch(reason){setError(message(reason,'用户创建失败'))}finally{setBusy('')}
 }
 async function addRole(event:FormEvent){
  event.preventDefault();setBusy('create-role');setError('');setNotice('')
  try{const role=await createRole({name:roleName.trim(),permissions:rolePermissions});setRoles(current=>[...current,role].sort((a,b)=>a.name.localeCompare(b.name)));setRoleName('');setRolePermissions([]);setNotice(`角色 ${role.name} 已创建。`)}
  catch(reason){setError(message(reason,'角色创建失败'))}finally{setBusy('')}
 }
 function openUser(user:ManagedUser){setEditingUser(user);setEditRoles(user.roles);setEditPassword('')}
 function closeUser(){setEditingUser(null);setEditRoles([]);setEditPassword('')}
 async function saveUser(event:FormEvent){
  event.preventDefault();if(!editingUser)return;setBusy('edit-user');setError('');setNotice('')
  try{const input:{disabled:boolean;roles:string[];password?:string}={disabled:editingUser.disabled,roles:editRoles};if(editPassword)input.password=editPassword;const user=await updateUser(editingUser.subject,input);setUsers(current=>current.map(item=>item.subject===user.subject?user:item));closeUser();setNotice(`用户 ${user.subject} 已更新。`)}
  catch(reason){setError(message(reason,'用户更新失败'))}finally{setBusy('')}
 }
 function openRole(role:ManagedRole){setEditingRole(role);setEditPermissions(role.permissions)}
 async function saveRole(event:FormEvent){
  event.preventDefault();if(!editingRole)return;setBusy('edit-role');setError('');setNotice('')
  try{const role=await updateRole(editingRole.name,{permissions:editPermissions});setRoles(current=>current.map(item=>item.name===role.name?role:item));setEditingRole(null);setNotice(`角色 ${role.name} 已更新。`)}
  catch(reason){setError(message(reason,'角色更新失败'))}finally{setBusy('')}
 }

 if(auth.status==='loading')return <p role="status" className="state-panel">正在验证管理权限…</p>
 if(auth.status==='error')return <section className="state-panel" role="alert"><h1>暂时无法验证权限</h1><p>{auth.error?.message??'会话服务不可用'}。请检查网络后重试。</p><button className="secondary" onClick={()=>void auth.refresh()}>重试</button></section>
 if(auth.status==='anonymous')return <section className="state-panel" role="alert"><h1>需要登录</h1><p>登录管理员账户后才能访问用户与角色管理。</p></section>
 if(!allowed)return <section className="state-panel" role="alert"><h1>无法打开管理台</h1><p>当前账户没有管理用户或角色的权限。请联系管理员分配对应权限。</p></section>
 return <section className="management-page" aria-labelledby="management-title">
  <header className="page-heading"><div><h1 id="management-title">用户与角色</h1><p>分配最小必要权限。用户状态与角色变更会由服务端立即生效。</p></div><button className="secondary" onClick={()=>{setRevoked(false);void load()}} disabled={loading||Boolean(busy)}>刷新数据</button></header>
  {loading&&<p role="status" className="state-panel">正在加载用户、角色与权限目录…</p>}
  {error&&<p role="alert">{error} 请检查权限或网络连接后重试。</p>}
  {notice&&<p role="status">{notice}</p>}
  {!loading&&!revoked&&<div className="management-columns">
   {canUsers&&<section className="management-section" aria-labelledby="users-title">
    <div className="section-heading"><div><h2 id="users-title">用户</h2><p>{users.length} 个受管账户</p></div></div>
    <div className="table-wrap"><table className="management-table"><thead><tr><th>账号</th><th>状态</th><th>角色</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{users.length?users.map(user=><tr key={user.subject}><th scope="row">{user.subject}</th><td><span className={`status-chip ${user.disabled?'status-chip--disabled':''}`}>{user.disabled?'已禁用':'有效'}</span></td><td>{user.roles.length?<span className="table-tags">{user.roles.join(' · ')}</span>:<span className="muted">无角色</span>}</td><td><button className="table-action" aria-label={`编辑用户 ${user.subject}`} onClick={()=>openUser(user)}>编辑</button></td></tr>):<tr><td colSpan={4} className="empty-cell">暂无用户</td></tr>}</tbody></table></div>
    <form className="management-form" onSubmit={addUser}><div><h3>创建用户</h3><p>密码只用于本次提交，成功后立即清空。</p></div><label>账号<input value={subject} onChange={event=>setSubject(event.target.value)} required autoComplete="off"/></label><label>初始密码<input value={password} onChange={event=>setPassword(event.target.value)} type="password" required autoComplete="new-password"/></label><fieldset className="permission-fieldset"><legend>初始角色</legend>{roles.length?<div className="permission-grid">{roles.map(role=><label key={role.name}><input aria-label={`分配初始角色 ${role.name}`} type="checkbox" checked={initialRoles.includes(role.name)} onChange={()=>setInitialRoles(toggle(initialRoles,role.name))}/><span>{role.name}</span></label>)}</div>:<p className="empty-copy">当前没有可分配角色</p>}</fieldset><button disabled={busy==='create-user'||!subject.trim()||!password}>{busy==='create-user'?'正在创建…':'创建用户'}</button></form>
   </section>}
   {canRoles&&<section className="management-section" aria-labelledby="roles-title">
    <div className="section-heading"><div><h2 id="roles-title">角色</h2><p>{roles.length} 个权限集合</p></div></div>
    <ul className="role-list">{roles.length?roles.map(role=><li key={role.name}><div><strong>{role.name}</strong><span>{role.permissions.length?role.permissions.join(' · '):'暂无权限'}</span></div>{canPermissions&&<button className="table-action" aria-label={`编辑角色 ${role.name}`} onClick={()=>openRole(role)}>编辑</button>}</li>):<li className="empty-cell">暂无角色</li>}</ul>
    {canPermissions?<form className="management-form" onSubmit={addRole}><div><h3>创建角色</h3><p>从服务端权限目录组合新的职责边界。</p></div><label>角色名称<input value={roleName} onChange={event=>setRoleName(event.target.value)} required/></label><fieldset className="permission-fieldset"><legend>分配权限</legend>{permissionNames.length?<div className="permission-grid">{permissionNames.map(permission=><label key={permission}><input type="checkbox" checked={rolePermissions.includes(permission)} onChange={()=>setRolePermissions(toggle(rolePermissions,permission))}/><code>{permission}</code></label>)}</div>:<p className="empty-copy">暂无可分配权限</p>}</fieldset><button disabled={busy==='create-role'||!roleName.trim()}>{busy==='create-role'?'正在创建…':'创建角色'}</button></form>:<p className="empty-copy">当前账户可以查看角色，但需要权限目录权限才能创建或修改角色。</p>}
   </section>}
  </div>}
  {editingUser&&<section className="editor-panel" aria-labelledby="edit-user-title"><div className="editor-panel__heading"><h2 id="edit-user-title">编辑用户 · {editingUser.subject}</h2><button className="quiet-button" onClick={closeUser}>取消</button></div><form onSubmit={saveUser}><label className="switch-row"><input type="checkbox" checked={!editingUser.disabled} onChange={event=>setEditingUser({...editingUser,disabled:!event.target.checked})}/><span>允许登录</span></label><fieldset className="permission-fieldset"><legend>角色</legend>{roles.length?<div className="permission-grid">{roles.map(role=><label key={role.name}><input aria-label={`用户角色 ${role.name}`} type="checkbox" checked={editRoles.includes(role.name)} onChange={()=>setEditRoles(toggle(editRoles,role.name))}/><span>{role.name}</span></label>)}</div>:<p className="empty-copy">当前没有可分配角色</p>}</fieldset><label>设置新密码<input value={editPassword} onChange={event=>setEditPassword(event.target.value)} type="password" autoComplete="new-password" placeholder="留空则保持不变"/></label><button disabled={busy==='edit-user'}>{busy==='edit-user'?'正在保存…':'保存用户'}</button></form></section>}
  {editingRole&&canPermissions&&<section className="editor-panel" aria-labelledby="edit-role-title"><div className="editor-panel__heading"><h2 id="edit-role-title">编辑角色 · {editingRole.name}</h2><button className="quiet-button" onClick={()=>setEditingRole(null)}>取消</button></div><form onSubmit={saveRole}><fieldset className="permission-fieldset"><legend>有效权限</legend><div className="permission-grid">{permissionNames.map(permission=><label key={permission}><input type="checkbox" checked={editPermissions.includes(permission)} onChange={()=>setEditPermissions(toggle(editPermissions,permission))}/><code>{permission}</code></label>)}</div></fieldset><button disabled={busy==='edit-role'}>{busy==='edit-role'?'正在保存…':'保存角色'}</button></form></section>}
 </section>
}
