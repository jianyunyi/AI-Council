import {CreateRoleInput,CreateUserInput,Envelope,Identity,ManagedRole,ManagedUser,Permission,Task,UpdateRoleInput,UpdateUserInput,Workspace,WorkspaceShare} from './types'
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
export function register(input:{email:string;password:string}){return request<{status:string}>('/auth/register',{method:'POST',body:JSON.stringify(input)})}
export function verifyEmail(token:string){return request<{status:string}>('/auth/verify-email',{method:'POST',body:JSON.stringify({token})})}
export function requestPasswordReset(email:string){return request<{status:string}>('/auth/password-reset/request',{method:'POST',body:JSON.stringify({email})})}
export function confirmPasswordReset(input:{token:string;password:string}){return request<{status:string}>('/auth/password-reset/confirm',{method:'POST',body:JSON.stringify(input)})}
export function oauthStartURL(provider:'github'|'google'){return `${apiBase()}/auth/oauth/${provider}/start`}
export function listOAuthProviders(){return request<{providers:string[]}>('/auth/oauth/providers')}
export type ProviderKeyProfile={id:string;provider:'openai'|'deepseek'|'anthropic';model:string;base_url?:string;masked_key:string;has_key:boolean;created_at:string}
export function listProviderKeys(){return request<ProviderKeyProfile[]>('/providers/keys')}
export function createProviderKey(input:{provider:ProviderKeyProfile['provider'];model:string;api_key:string;base_url?:string}){return request<ProviderKeyProfile>('/providers/keys',{method:'POST',body:JSON.stringify(input)})}
export function deleteProviderKey(id:string){return request<void>(`/providers/keys/${encodeURIComponent(id)}`,{method:'DELETE'})}
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
export function getWorkspace(id:string){return request<Workspace>(`/workspaces/${encodeURIComponent(id)}`)}
export function listWorkspaceShares(id:string){return request<WorkspaceShare[]>(`/workspaces/${encodeURIComponent(id)}/shares`)}
export function searchWorkspaceMembers(id:string,query:string){return request<{subject:string}[]>(`/workspaces/${encodeURIComponent(id)}/members?q=${encodeURIComponent(query)}`)}
export function putWorkspaceShare(id:string,subject:string,access:WorkspaceShare['access']){return request<void>(`/workspaces/${encodeURIComponent(id)}/shares/${encodeURIComponent(subject)}`,{method:'PUT',body:JSON.stringify({access})})}
export function deleteWorkspaceShare(id:string,subject:string){return request<void>(`/workspaces/${encodeURIComponent(id)}/shares/${encodeURIComponent(subject)}`,{method:'DELETE'})}
export function createTask(input:{workspaceId:string;requirement:string;acceptance:string[]}){return request<Task>('/tasks',{method:'POST',body:JSON.stringify({workspace_id:input.workspaceId,requirement:input.requirement,acceptance:input.acceptance})})}
export function getTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}`)}
export function startTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/start`,{method:'POST'})}
export function approveTask(id:string,planVersion:number,approvalHash:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/approve`,{method:'POST',body:JSON.stringify({plan_version:planVersion,approval_hash:approvalHash})})}
export function executeTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/execute`,{method:'POST'})}
