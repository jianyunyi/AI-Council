'use client'
import {useEffect,useState} from 'react'
import {getTask} from '@/lib/api'
import {subscribe,CouncilEvent} from '@/lib/events'
import {ApprovalPanel} from './approval-panel'
export function TaskWorkbench({id}:{id:string}){const [task,setTask]=useState<any>(null);const [events,setEvents]=useState<CouncilEvent[]>([]);useEffect(()=>{getTask(id).then(setTask).catch(()=>{});return subscribe(id,e=>setEvents(xs=>xs.some(x=>x.id===e.id)?xs:[...xs,e]))},[id]);if(!task)return <p>加载任务中…</p>;return <><h1>任务 {task.id}</h1><div className="card"><strong>状态：{task.state}</strong><p>{task.requirement}</p><p>已接收 {events.length} 个事件</p></div><div className="card"><h2>事件时间线</h2>{events.map(e=><p key={e.id}>#{e.id} {e.event}</p>)}</div>{task.state==='AWAITING_APPROVAL'&&<ApprovalPanel hash={task.approval_hash} version={task.plan_version} onApprove={()=>{}}/>}</>}
