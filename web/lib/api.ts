import {ArtifactEnvelope,CreateRoleInput,CreateUserInput,Envelope,Identity,ManagedRole,ManagedUser,Permission,RejectionCategory,Task,TaskListPage,TaskRejection,UpdateRoleInput,UpdateUserInput,Workspace,WorkspaceKind,WorkspaceShare} from './types'
import {apiBase,authorizationHeader} from './desktop'

const unauthorizedListeners=new Set<()=>void>()
export function onUnauthorized(listener:()=>void){unauthorizedListeners.add(listener);return()=>{unauthorizedListeners.delete(listener)}}

export class ApiError extends Error {
  constructor(public status:number,public code:string,message:string,public fields?:Record<string,string>,public issues?:{code:string;role?:string;ordinal?:number;message:string}[]){super(message);this.name='ApiError'}
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
  const error=new ApiError(res.status,body?.error?.code??'http_error',(body?.error?.message??raw.trim())||`request failed: ${res.status}`,body?.error?.fields,(body?.error as {issues?:{code:string;role?:string;ordinal?:number;message:string}[]}|undefined)?.issues)
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
export type ProviderKeyProfile={id:string;provider:'openai'|'deepseek'|'anthropic';model:string;masked_key:string;has_key:boolean;created_at:string}
export function listProviderKeys(){return request<ProviderKeyProfile[]>('/providers/keys')}
export function listProviderModels(id:string){return request<{provider_profile_id:string;provider:string;source:string;models:{id:string;verified:boolean}[]}>(`/providers/${encodeURIComponent(id)}/models`)}
export function createProviderKey(input:{provider:ProviderKeyProfile['provider'];model:string;api_key:string}){return request<ProviderKeyProfile>('/providers/keys',{method:'POST',body:JSON.stringify(input)})}
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
export function listWorkspaces(kind?:WorkspaceKind){return request<Workspace[]>(`/workspaces${kind?`?kind=${encodeURIComponent(kind)}`:''}`)}
export function getWorkspace(id:string){return request<Workspace>(`/workspaces/${encodeURIComponent(id)}`)}
export function listWorkspaceShares(id:string){return request<WorkspaceShare[]>(`/workspaces/${encodeURIComponent(id)}/shares`)}
export function searchWorkspaceMembers(id:string,query:string){return request<{subject:string}[]>(`/workspaces/${encodeURIComponent(id)}/members?q=${encodeURIComponent(query)}`)}
export function putWorkspaceShare(id:string,subject:string,access:WorkspaceShare['access']){return request<void>(`/workspaces/${encodeURIComponent(id)}/shares/${encodeURIComponent(subject)}`,{method:'PUT',body:JSON.stringify({access})})}
export function deleteWorkspaceShare(id:string,subject:string){return request<void>(`/workspaces/${encodeURIComponent(id)}/shares/${encodeURIComponent(subject)}`,{method:'DELETE'})}
export function createTask(input:{workspaceId:string;requirement:string;acceptance:string[]}){return request<Task>('/tasks',{method:'POST',body:JSON.stringify({workspace_id:input.workspaceId,requirement:input.requirement,acceptance:input.acceptance})})}
export function getTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}`)}
export type TaskListQuery={workspaceId?:string|string[];state?:string|string[];createdBy?:string;origin?:string|string[];q?:string;updatedAfter?:string;updatedBefore?:string;limit?:number;cursor?:string}
export function listTasks(query:TaskListQuery={}){
 const params=new URLSearchParams()
 const add=(key:string,value?:string|string[])=>{if(value===undefined)return;for(const item of Array.isArray(value)?value:[value])params.append(key,item)}
 add('workspace_id',query.workspaceId);add('state',query.state);add('origin',query.origin)
 if(query.createdBy)params.set('created_by',query.createdBy)
 if(query.q)params.set('q',query.q)
 if(query.updatedAfter)params.set('updated_after',query.updatedAfter)
 if(query.updatedBefore)params.set('updated_before',query.updatedBefore)
 if(query.limit)params.set('limit',String(query.limit))
 if(query.cursor)params.set('cursor',query.cursor)
 const qs=params.toString()
 return request<TaskListPage>(`/tasks${qs?`?${qs}`:''}`)
}
export function startTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/start`,{method:'POST'})}
export function approveTask(id:string,planVersion:number,approvalHash:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/approve`,{method:'POST',body:JSON.stringify({plan_version:planVersion,approval_hash:approvalHash})})}
export function executeTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/execute`,{method:'POST'})}
export function rejectTask(id:string,input:{reason:string;category:RejectionCategory;planVersion?:number}){return request<Task>(`/tasks/${encodeURIComponent(id)}/reject`,{method:'POST',body:JSON.stringify({reason:input.reason,category:input.category,plan_version:input.planVersion})})}
export function listTaskRejections(id:string){return request<TaskRejection[]>(`/tasks/${encodeURIComponent(id)}/rejections`)}
export function cancelTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/cancel`,{method:'POST'})}
export function getArtifact(taskId:string,artifactId:string){return request<ArtifactEnvelope>(`/tasks/${encodeURIComponent(taskId)}/artifacts/${encodeURIComponent(artifactId)}`)}
export type CouncilRole='proposer'|'reviewer'|'judge'|'red_team'
export type CouncilSlotParams={temperature?:number;max_output_tokens?:number}
export type CouncilSlot={role:CouncilRole;ordinal:number;provider_profile_id:string;model:string;params?:CouncilSlotParams;enabled:boolean}
export type CouncilResolvedSlot={role:string;ordinal:number;provider_profile_id:string;provider:string;model:string;params?:CouncilSlotParams}
export type CouncilResolvedPreview={source:string;version:number;proposers:CouncilResolvedSlot[];reviewers:CouncilResolvedSlot[];judge:CouncilResolvedSlot[];red_team:CouncilResolvedSlot[]}
export type CouncilValidationIssue={code:string;role?:string;ordinal?:number;message:string}
export type CouncilRolesView={scope_type:'global'|'workspace';scope_id?:string;version:number;effective_source:'workspace'|'global'|'legacy';roles:CouncilSlot[];resolved_preview:CouncilResolvedPreview;warnings:CouncilValidationIssue[]}
export type CouncilScope={scopeType:'global'|'workspace';scopeId?:string}
function councilScopeQuery(scope:CouncilScope){const params=new URLSearchParams({scope_type:scope.scopeType});if(scope.scopeId)params.set('scope_id',scope.scopeId);return params.toString()}
export function getCouncilRoles(scope:CouncilScope){return request<CouncilRolesView>(`/council/roles?${councilScopeQuery(scope)}`)}
export function putCouncilRoles(scope:CouncilScope,version:number,slots:CouncilSlot[]){return request<CouncilRolesView>('/council/roles',{method:'PUT',headers:{'if-match':String(version)},body:JSON.stringify({scope_type:scope.scopeType,scope_id:scope.scopeId??'',version,slots})})}
export function validateCouncilRoles(scope:CouncilScope,slots:CouncilSlot[]){return request<{valid:boolean;errors:CouncilValidationIssue[];warnings:CouncilValidationIssue[]}>('/council/roles/validate',{method:'POST',body:JSON.stringify({scope_type:scope.scopeType,scope_id:scope.scopeId??'',slots})})}
export function deleteCouncilRoles(scope:CouncilScope){return request<CouncilRolesView>(`/council/roles?${councilScopeQuery(scope)}`,{method:'DELETE'})}
export type AIModelStatus='active'|'disabled'|'degraded'
export type AIModelWorkspaceStatus='provisioning'|'ready'|'error'|'archived'
export type AIModelWorkspace={workspace_id:string;kind:'model_private';status:AIModelWorkspaceStatus;last_error?:string;attempts:number;runner_ready:boolean;updated_at:string}
export type AIModel={id:string;provider_profile_id:string;provider_name:string;provider_type:string;model:string;handle:string;display_name:string;default_params:Record<string,unknown>;status:AIModelStatus;workspace:AIModelWorkspace|null;warnings?:string[];created_at:string;updated_at:string}
export type CreateAIModelInput={provider_profile_id:string;model:string;handle:string;display_name?:string;default_params?:Record<string,unknown>}
export function listAIModels(){return request<AIModel[]>('/ai-models')}
export function createAIModel(input:CreateAIModelInput){return request<AIModel>('/ai-models',{method:'POST',body:JSON.stringify(input)})}
export function updateAIModel(id:string,input:{display_name?:string;default_params?:Record<string,unknown>;status?:AIModelStatus}){return request<AIModel>(`/ai-models/${encodeURIComponent(id)}`,{method:'PATCH',body:JSON.stringify(input)})}
export function disableAIModel(id:string){return request<AIModel>(`/ai-models/${encodeURIComponent(id)}`,{method:'DELETE'})}
export function retryAIModelWorkspace(id:string){return request<AIModel>(`/ai-models/${encodeURIComponent(id)}/workspace/retry`,{method:'POST'})}
