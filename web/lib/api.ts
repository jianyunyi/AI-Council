import {Task,Envelope} from './types'
import {apiBase,authorizationHeader} from './desktop'
async function request<T>(path:string, init?:RequestInit):Promise<T>{const res=await fetch(apiBase()+path,{headers:{'content-type':'application/json',...authorizationHeader(),...(init?.headers??{})},...init});const body=await res.json() as Envelope<T>;if(!res.ok||body.error)throw new Error(body.error?.message??`request failed: ${res.status}`);return body.data}
export function createTask(input:{workspaceId:string;requirement:string;acceptance:string[]}){return request<Task>('/tasks',{method:'POST',body:JSON.stringify(input)})}
export function getTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}`)}
export function startTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/start`,{method:'POST'})}
export function approveTask(id:string,planVersion:number,approvalHash:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/approve`,{method:'POST',body:JSON.stringify({plan_version:planVersion,approval_hash:approvalHash})})}
export function executeTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}/execute`,{method:'POST'})}
