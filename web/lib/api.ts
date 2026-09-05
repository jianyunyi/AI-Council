import {CreateRoleInput,CreateUserInput,Envelope,Identity,ManagedRole,ManagedUser,Permission,Task,UpdateRoleInput,UpdateUserInput,Workspace} from './types'
import {apiBase,authorizationHeader} from './desktop'

const unauthorizedListeners=new Set<()=>void>()
export function onUnauthorized(listener:()=>void){unauthorizedListeners.add(listener);return()=>{unauthorizedListeners.delete(listener)}}

export class ApiError extends Error {
  constructor(public status:number,public code:string,message:string,public fields?:Record<string,string>){super(message);this.name='ApiError'}
}

export async function request<T>(path:string,init:RequestInit={}):Promise<T>{
 const headers=new Headers({'content-type':'application/json',...authorizationHeader()})
 new Headers(init.headers).forEach((value,key)=>headers.set(key,value))
 const res=await fetch(apiBase()+path,{...init,credentials:'same-origin',headers})
 if(res.status===204)return undefined as T
 const raw=await res.text()
 let body:Envelope<T>|undefined
 try{body=JSON.parse(raw) as Envelope<T>}catch{}
 if(!res.ok||body?.error){
  const error=new ApiError(res.status,body?.error?.code??'http_error',(body?.error?.message??raw.trim())||`request failed: ${res.status}`,body?.error?.fields)
  if(res.status===401)unauthorizedListeners.forEach(listener=>listener())
  throw error
 }
 if(!body||typeof body!=='object'||!Object.prototype.hasOwnProperty.call(body,'data'))throw new ApiError(res.status,'invalid_response','服务器返回了无法识别的响应')
 return body.data
}

export function login(input:{subject:string;password:string}){return request<Identity>('/auth/login',{method:'POST',body:JSON.stringify(input)})}
export function me(){return request<Identity>('/auth/me')}
export function logout(){return request<void>('/auth/logout',{method:'POST'})}
export function listUsers(){return request<ManagedUser[]>('/admin/users')}
export function createUser(input:CreateUserInput){return request<ManagedUser>('/admin/users',{method:'POST',body:JSON.stringify(input)})}
export function updateUser(subject:string,input:UpdateUserInput){return request<ManagedUser>(`/admin/users/${encodeURIComponent(subject)}`,{method:'PATCH',body:JSON.stringify(input)})}
export function listRoles(){return request<ManagedRole[]>('/admin/roles')}
export function createRole(input:CreateRoleInput){return request<ManagedRole>('/admin/roles',{method:'POST',body:JSON.stringify(input)})}
export function updateRole(name:string,input:UpdateRoleInput){return request<ManagedRole>(`/admin/roles/${encodeURIComponent(name)}`,{method:'PATCH',body:JSON.stringify(input)})}
export function listPermissions(){return request<Permission[]>('/admin/permissions')}
export function createWorkspace(root:string){return request<Workspace>('/workspaces',{method:'POST',body:JSON.stringify({root})})}
export function listWorkspaces(){return request<Workspace[]>('/workspaces')}
export function createTask(input:{workspaceId:string;requirement:string;acceptance:string[]}){return request<Task>('/tasks',{method:'POST',body:JSON.stringify({workspace_id:input.workspaceId,requirement:input.requirement,acceptance:input.acceptance})})}
export function getTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}`)}
export function startTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/start`,{method:'POST'})}
export function approveTask(id:string,planVersion:number,approvalHash:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/approve`,{method:'POST',body:JSON.stringify({plan_version:planVersion,approval_hash:approvalHash})})}
export function executeTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/execute`,{method:'POST'})}
