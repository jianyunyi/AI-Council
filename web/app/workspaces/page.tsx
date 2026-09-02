'use client'
import {useState} from 'react'
import {desktopBridge} from '@/lib/desktop-bridge'

export default function Workspaces(){const [path,setPath]=useState('');const [message,setMessage]=useState('');const [busy,setBusy]=useState(false);const bridge=desktopBridge();async function select(){if(!bridge||!path.trim())return;setBusy(true);setMessage('');try{await bridge.openWorkspace(path);setMessage('工作区已验证。启动服务后可创建任务。')}catch(error){setMessage(error instanceof Error?error.message:'注册工作区失败')}finally{setBusy(false)}}return <><h1>工作区</h1><div className="card"><label>本地路径<input value={path} onChange={event=>setPath(event.target.value)} placeholder="例如 C:\\Projects\\my-app" /></label><p>路径必须是实际目录。更换工作区前请先在首页停止本地服务；执行时仍会检查 Git 状态、路径边界和人工审批。</p>{message&&<p role="status">{message}</p>}<button onClick={select} disabled={!bridge||busy||!path.trim()}>{busy?'正在验证…':'选择工作区'}</button>{!bridge&&<p role="alert">工作区选择只在 AI Council 桌面版中可用。</p>}</div></>}
