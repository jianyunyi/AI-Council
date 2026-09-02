'use client'
import {Suspense} from 'react'
import {useSearchParams} from 'next/navigation'
import {TaskWorkbench} from '@/components/task/task-workbench'

function TaskView(){const id=useSearchParams().get('id');return id?<TaskWorkbench id={id}/>:<p role="alert">缺少任务编号。</p>}
export default function DesktopTaskPage(){return <Suspense fallback={<p>正在加载任务…</p>}><TaskView/></Suspense>}
