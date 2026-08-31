import {Task,Envelope} from './types'
const base = process.env.NEXT_PUBLIC_API_BASE ?? 'http://127.0.0.1:18080/api/v1'
async function request<T>(path:string, init?:RequestInit):Promise<T>{const res=await fetch(base+path,{headers:{'content-type':'application/json',...(init?.headers??{})},...init});const body=await res.json() as Envelope<T>;if(!res.ok||body.error)throw new Error(body.error?.message??`request failed: ${res.status}`);return body.data}
export function createTask(input:{workspaceId:string;requirement:string;acceptance:string[]}){return request<Task>('/tasks',{method:'POST',body:JSON.stringify(input)})}
export function getTask(id:string){return request<Task>(`/tasks/${encodeURIComponent(id)}`)}
